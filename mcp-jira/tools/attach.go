package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/jira"
)

const AttachFilesToolName = "attach_files"

var (
	ErrAttachmentPath           = errors.New("tools: attachment path must be a non-empty absolute or ~/ path of an existing file whose name has no backslash and no control characters, listed once")
	ErrAttachmentOutsideDirs    = errors.New("tools: attachment is outside the directories allowed by attachments.dirs")
	ErrAttachmentForbiddenFile  = errors.New("tools: attachment is the token file, the keychain file or the config file")
	ErrAttachmentNotRegularFile = errors.New("tools: attachment is not a regular file")
	ErrAttachmentExtension      = errors.New("tools: attachment extension is not allowed by attachments.extensions")
	ErrAttachmentTooLarge       = errors.New("tools: attachment is larger than attachments.max_bytes")
	ErrAttachmentChanged        = errors.New("tools: attachment file was replaced after the check and was not sent")
	ErrAttachmentThroughHome    = errors.New("tools: attachment is reached from its attachments.dirs entry through the home directory or the root: the entry gives access to the home directory")
)

type AttachFilesInput struct {
	IssueKey string   `json:"issue_key"`
	Paths    []string `json:"paths"`
}

type AttachedFile struct {
	Name string `json:"file"`
	ID   string `json:"id"`
}

type AttachFilesOutput struct {
	IssueKey string         `json:"issue_key"`
	URL      string         `json:"url"`
	Attached []AttachedFile `json:"attached"`
}

type AttachFilesError struct {
	IssueKey     string
	Attached     []AttachedFile
	StoppedAt    string
	NotAttempted []string
	Err          error
}

func (e *AttachFilesError) Error() string {
	attachedEntries := make([]string, 0, len(e.Attached))
	attachedNames := make([]string, 0, len(e.Attached))
	for _, attached := range e.Attached {
		attachedEntries = append(attachedEntries, fmt.Sprintf("%s (id %s)", attached.Name, attached.ID))
		attachedNames = append(attachedNames, attached.Name)
	}
	var text strings.Builder
	if len(e.Attached) == 0 {
		fmt.Fprintf(&text, "tools: no files were attached to %s before %s", e.IssueKey, e.StoppedAt)
	} else {
		fmt.Fprintf(&text, "tools: attached to %s: %s", e.IssueKey, strings.Join(attachedEntries, ", "))
	}
	if e.mayBeAttached() {
		fmt.Fprintf(&text, "; %s may have been attached, check the issue before retrying it: %v", e.StoppedAt, e.Err)
	} else {
		fmt.Fprintf(&text, "; %s was not attached: %v", e.StoppedAt, e.Err)
	}
	if len(e.NotAttempted) > 0 {
		fmt.Fprintf(&text, "; not attempted after it: %s", strings.Join(e.NotAttempted, ", "))
	}
	if len(e.Attached) > 0 {
		fmt.Fprintf(&text, ". Retrying would attach %s again: Jira does not deduplicate attachments, so %s", strings.Join(attachedNames, ", "), e.retryAdvice())
	}
	return text.String()
}

func (e *AttachFilesError) mayBeAttached() bool {
	return errors.Is(e.Err, jira.ErrAttachUnconfirmed)
}

func (e *AttachFilesError) retryAdvice() string {
	switch {
	case !e.mayBeAttached():
		return "retry only the files that were not attached"
	case len(e.NotAttempted) > 0:
		return fmt.Sprintf("retry only the files after %s, and %s only if it is missing from the issue", e.StoppedAt, e.StoppedAt)
	default:
		return fmt.Sprintf("retry %s only if it is missing from the issue", e.StoppedAt)
	}
}

func (e *AttachFilesError) Unwrap() error {
	return e.Err
}

type checkedFile struct {
	realPath string
	info     os.FileInfo
}

func (f checkedFile) name() string {
	return filepath.Base(f.realPath)
}

func (t *Toolset) registerAttachFiles(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        AttachFilesToolName,
		Description: AttachFilesDescription(t.cfg),
		InputSchema: AttachFilesInputSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AttachFilesInput) (*mcp.CallToolResult, AttachFilesOutput, error) {
		out, err := t.AttachFiles(ctx, input)
		return nil, out, err
	})
}

func (t *Toolset) AttachFiles(ctx context.Context, input AttachFilesInput) (AttachFilesOutput, error) {
	if err := t.checkIssueKey(paramIssueKey, input.IssueKey); err != nil {
		return AttachFilesOutput{}, err
	}
	checkedFiles, err := t.checkPaths(input.Paths)
	if err != nil {
		return AttachFilesOutput{}, err
	}
	uploadedFiles := make([]AttachedFile, 0, len(checkedFiles))
	for i, file := range checkedFiles {
		uploaded, uploadErr := t.uploadCheckedFile(ctx, input.IssueKey, file)
		if uploadErr != nil {
			return AttachFilesOutput{}, &AttachFilesError{
				IssueKey:     input.IssueKey,
				Attached:     uploadedFiles,
				StoppedAt:    file.name(),
				NotAttempted: fileNames(checkedFiles[i+1:]),
				Err:          uploadErr,
			}
		}
		uploadedFiles = append(uploadedFiles, uploaded)
	}
	return AttachFilesOutput{IssueKey: input.IssueKey, URL: jira.IssueURL(t.cfg.BaseURL, input.IssueKey), Attached: uploadedFiles}, nil
}

func fileNames(files []checkedFile) []string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.name())
	}
	return names
}

func (t *Toolset) checkPaths(paths []string) ([]checkedFile, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: paths is empty", ErrAttachmentPath)
	}
	allowedDirs := realDirs(t.cfg.Attachments.Dirs)
	homeGuard := config.NewHomeGuard()
	forbiddenFiles := statExistingFiles(t.cfg.TokenFilePath(), t.cfg.KeychainPath, t.configPath)
	checkedFiles := make([]checkedFile, 0, len(paths))
	for _, path := range paths {
		file, err := t.checkPath(path, allowedDirs, homeGuard, forbiddenFiles)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(checkedFiles, func(previous checkedFile) bool { return previous.realPath == file.realPath }) {
			return nil, fmt.Errorf("%w: %q is listed twice", ErrAttachmentPath, file.realPath)
		}
		checkedFiles = append(checkedFiles, file)
	}
	return checkedFiles, nil
}

func (t *Toolset) checkPath(path string, allowedDirs []string, homeGuard config.HomeGuard, forbiddenFiles []os.FileInfo) (checkedFile, error) {
	if path == "" {
		return checkedFile{}, fmt.Errorf("%w: empty path", ErrAttachmentPath)
	}
	expanded, err := config.ExpandHome(path)
	if err != nil {
		return checkedFile{}, fmt.Errorf("%w: %q: %w", ErrAttachmentPath, path, err)
	}
	if !filepath.IsAbs(expanded) {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrAttachmentPath, path)
	}
	realPath, err := filepath.EvalSymlinks(expanded)
	if err != nil {
		return checkedFile{}, fmt.Errorf("%w: %w", ErrAttachmentPath, err)
	}
	if strings.ContainsFunc(filepath.Base(realPath), isForbiddenNameRune) {
		return checkedFile{}, fmt.Errorf("%w: %q has a backslash or a control character in its name", ErrAttachmentPath, realPath)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return checkedFile{}, fmt.Errorf("%w: %w", ErrAttachmentPath, err)
	}
	if !slices.ContainsFunc(allowedDirs, func(dir string) bool { return config.IsInsideDir(realPath, dir) }) {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrAttachmentOutsideDirs, realPath)
	}
	if !slices.ContainsFunc(allowedDirs, func(dir string) bool { return homeGuard.IsInsideDirAvoidingHomeAndRoot(realPath, dir) }) {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrAttachmentThroughHome, realPath)
	}
	if slices.ContainsFunc(forbiddenFiles, func(forbidden os.FileInfo) bool { return os.SameFile(forbidden, info) }) {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrAttachmentForbiddenFile, realPath)
	}
	if !info.Mode().IsRegular() {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrAttachmentNotRegularFile, realPath)
	}
	if !t.cfg.Attachments.AllowsExtension(realPath) {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrAttachmentExtension, realPath)
	}
	if info.Size() > t.cfg.Attachments.MaxBytes {
		return checkedFile{}, fmt.Errorf("%w: %q has %d bytes", ErrAttachmentTooLarge, realPath, info.Size())
	}
	if err := checkReadable(realPath); err != nil {
		return checkedFile{}, err
	}
	return checkedFile{realPath: realPath, info: info}, nil
}

func isForbiddenNameRune(r rune) bool {
	return r == '\\' || r < 0x20 || r == 0x7f
}

func checkReadable(path string) error {
	opened, err := openNonBlocking(path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAttachmentPath, err)
	}
	_ = opened.Close()
	return nil
}

func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

func realDirs(dirs []string) []string {
	resolved := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		resolved = append(resolved, realDir)
	}
	return resolved
}

func statExistingFiles(paths ...string) []os.FileInfo {
	infos := make([]os.FileInfo, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

func (t *Toolset) uploadCheckedFile(ctx context.Context, issueKey string, file checkedFile) (AttachedFile, error) {
	content, err := readCheckedFile(file, t.cfg.Attachments.MaxBytes)
	if err != nil {
		return AttachedFile{}, err
	}
	attachment, err := t.attacher.AttachFile(ctx, issueKey, jira.AttachmentFile{Name: file.name(), Content: content})
	if err != nil {
		return AttachedFile{}, err
	}
	return AttachedFile{Name: file.name(), ID: attachment.ID}, nil
}

func readCheckedFile(file checkedFile, maxBytes int64) ([]byte, error) {
	opened, err := openNonBlocking(file.realPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %w", ErrAttachmentChanged, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAttachmentPath, err)
	}
	defer func() { _ = opened.Close() }()
	if err := recheckOpenedFile(opened, file, maxBytes); err != nil {
		return nil, err
	}
	content, err := io.ReadAll(io.LimitReader(opened, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", file.realPath, err)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("%w: %q grew after the check", ErrAttachmentTooLarge, file.realPath)
	}
	return content, nil
}

func recheckOpenedFile(opened *os.File, checked checkedFile, maxBytes int64) error {
	info, err := opened.Stat()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAttachmentChanged, err)
	}
	if !os.SameFile(info, checked.info) {
		return fmt.Errorf("%w: %q", ErrAttachmentChanged, checked.realPath)
	}
	if info.Size() > maxBytes {
		return fmt.Errorf("%w: %q grew after the check", ErrAttachmentTooLarge, checked.realPath)
	}
	return nil
}
