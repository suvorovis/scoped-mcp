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

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/guard"
)

const (
	downloadDirMode  os.FileMode = 0o700
	downloadFileMode os.FileMode = 0o600
	tempFilePattern              = ".mcp-confluence-download-*"
	dotFilePrefix                = "_"
)

var (
	ErrUnsafeAttachmentTitle         = errors.New(`tools: the attachment title is not a safe file name (empty, . or .., or contains / \ ; " or control characters), it cannot be downloaded by this tool`)
	ErrDownloadWrite                 = errors.New("tools: the attachment could not be saved into attachments.download_dir")
	ErrInvalidAttachmentPath         = errors.New(`tools: path must be a non-empty absolute or ~/ path of an existing readable file whose name is not . or .. and has no / \ ; " or control characters`)
	ErrAttachmentExtensionNotAllowed = errors.New("tools: the file extension is not allowed by attachments.extensions")
	ErrAttachmentFileTooLarge        = errors.New("tools: the file is larger than attachments.max_bytes")
	ErrAttachmentOutsideDirs         = errors.New("tools: the file is outside the directories allowed by attachments.upload_dirs")
	ErrAttachmentThroughHome         = errors.New("tools: the file is reached from its attachments.upload_dirs entry through the home directory or the root: the entry gives access to the home directory")
	ErrAttachmentSecretFile          = errors.New("tools: the file is the config file, the token file or the keychain")
	ErrAttachmentNotRegularFile      = errors.New("tools: the path is not a regular file")
	ErrAttachmentChanged             = errors.New("tools: the file was replaced or changed after the check and was not sent")
)

type ListAttachmentsInput struct {
	PageID string `json:"page_id" jsonschema:"Numeric id of the page"`
}

type AttachmentOutput struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	Version   int    `json:"version"`
}

type ListAttachmentsOutput struct {
	Attachments []AttachmentOutput `json:"attachments"`
}

type GetAttachmentInput struct {
	PageID       string `json:"page_id" jsonschema:"Numeric id of the page"`
	AttachmentID string `json:"attachment_id" jsonschema:"Id of the attachment from list_attachments, with or without the att prefix"`
}

type GetAttachmentOutput struct {
	Path string `json:"path"`
}

type UploadAttachmentInput struct {
	PageID string `json:"page_id" jsonschema:"Numeric id of the page from the allowed writable subtrees"`
	Path   string `json:"path" jsonschema:"Absolute or ~/ path of the file to attach"`
}

type UploadAttachmentOutput struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Version int    `json:"version"`
}

func (t *Toolset) registerListAttachments(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        ListAttachmentsToolName,
		Description: "List attachments (id, title, media type, size, version) of a page from the allowed subtrees.",
	}, t.ListAttachments)
}

func (t *Toolset) ListAttachments(ctx context.Context, input ListAttachmentsInput) (ListAttachmentsOutput, error) {
	if err := checkContentID("page_id", input.PageID); err != nil {
		return ListAttachmentsOutput{}, err
	}
	attachments, err := t.attachments.ListAttachments(ctx, input.PageID)
	if err != nil {
		return ListAttachmentsOutput{}, err
	}
	output := make([]AttachmentOutput, 0, len(attachments))
	for _, attachment := range attachments {
		output = append(output, AttachmentOutput{ID: attachment.ID, Title: attachment.Title, MediaType: attachment.MediaType, Size: attachment.Size, Version: attachment.Version})
	}
	return ListAttachmentsOutput{Attachments: output}, nil
}

func (t *Toolset) registerGetAttachment(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name: GetAttachmentToolName,
		Description: fmt.Sprintf("Download an attachment of a page from the allowed subtrees into %s and return the saved path. At most %d bytes; an existing file with the same name is overwritten; a title starting with a dot is saved with the prefix %s; a title that is not a safe file name is refused.",
			t.cfg.Attachments.DownloadDir, t.cfg.Attachments.MaxBytes, dotFilePrefix),
	}, t.GetAttachment)
}

func (t *Toolset) GetAttachment(ctx context.Context, input GetAttachmentInput) (GetAttachmentOutput, error) {
	if err := checkContentID("page_id", input.PageID); err != nil {
		return GetAttachmentOutput{}, err
	}
	attachmentContentID, err := checkAttachmentID(input.AttachmentID)
	if err != nil {
		return GetAttachmentOutput{}, err
	}
	attachment, err := t.findAttachment(ctx, input.PageID, attachmentContentID)
	if err != nil {
		return GetAttachmentOutput{}, err
	}
	fileName, err := SafeFileName(attachment.Title)
	if err != nil {
		return GetAttachmentOutput{}, err
	}
	content, err := t.attachments.DownloadAttachment(ctx, input.PageID, attachment, t.cfg.Attachments.MaxBytes)
	if err != nil {
		return GetAttachmentOutput{}, err
	}
	path, err := t.saveDownloaded(fileName, content)
	if err != nil {
		return GetAttachmentOutput{}, err
	}
	return GetAttachmentOutput{Path: path}, nil
}

func (t *Toolset) findAttachment(ctx context.Context, pageID, attachmentContentID string) (confluence.Attachment, error) {
	attachments, err := t.attachments.ListAttachments(ctx, pageID)
	if err != nil {
		return confluence.Attachment{}, err
	}
	index := slices.IndexFunc(attachments, func(attachment confluence.Attachment) bool {
		listedContentID, ok := guard.AttachmentContentID(attachment.ID)
		return ok && listedContentID == attachmentContentID
	})
	if index < 0 {
		return confluence.Attachment{}, confluence.ErrNotFound
	}
	return attachments[index], nil
}

func SafeFileName(title string) (string, error) {
	if !guard.IsAttachmentFileName(title) {
		return "", fmt.Errorf("%w: %q", ErrUnsafeAttachmentTitle, title)
	}
	if strings.HasPrefix(title, ".") {
		return dotFilePrefix + title, nil
	}
	return title, nil
}

func (t *Toolset) saveDownloaded(fileName string, content []byte) (string, error) {
	downloadDir := t.cfg.Attachments.DownloadDir
	if err := os.MkdirAll(downloadDir, downloadDirMode); err != nil {
		return "", fmt.Errorf("%w: %w", ErrDownloadWrite, err)
	}
	path := filepath.Join(downloadDir, fileName)
	if err := t.refuseSecretFile(path); err != nil {
		return "", err
	}
	tempPath, err := writeTempFile(downloadDir, content)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrDownloadWrite, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("%w: %w", ErrDownloadWrite, err)
	}
	return path, nil
}

func (t *Toolset) refuseSecretFile(path string) error {
	if t.isSecretDownloadTarget(path) {
		return fmt.Errorf("%w: %q", ErrAttachmentSecretFile, path)
	}
	return nil
}

func (t *Toolset) isSecretDownloadTarget(path string) bool {
	secretPaths := t.secretFilePaths()
	if slices.ContainsFunc(secretPaths, func(secretPath string) bool { return secretPath != "" && filepath.Clean(secretPath) == path }) {
		return true
	}
	targetInfo, err := os.Lstat(path)
	if err != nil {
		return false
	}
	secretFileInfos := slices.Concat(existingFileInfos(secretPaths, os.Stat), existingFileInfos(secretPaths, os.Lstat))
	return containsSameFile(secretFileInfos, targetInfo)
}

func writeTempFile(downloadDir string, content []byte) (string, error) {
	tempFile, err := os.CreateTemp(downloadDir, tempFilePattern)
	if err != nil {
		return "", err
	}
	_, writeErr := tempFile.Write(content)
	if err := errors.Join(writeErr, tempFile.Chmod(downloadFileMode), tempFile.Close()); err != nil {
		_ = os.Remove(tempFile.Name())
		return "", err
	}
	return tempFile.Name(), nil
}

func (t *Toolset) registerUploadAttachment(server *mcp.Server) {
	attachments := t.cfg.Attachments
	addTool(server, &mcp.Tool{
		Name: UploadAttachmentToolName,
		Description: fmt.Sprintf(`Attach a local file to a page from the allowed writable subtrees. path: absolute or ~/ path of a regular file inside %s (symlinks are resolved, the attachment name is the resolved file name, which must not contain / \ ; " or control characters); extensions: %s (any letter case); at most %d bytes. A file with the same name already attached gets a new version.`,
			strings.Join(attachments.UploadDirs, " or "), strings.Join(attachments.Extensions, ", "), attachments.MaxBytes),
	}, t.UploadAttachment)
}

func (t *Toolset) UploadAttachment(ctx context.Context, input UploadAttachmentInput) (UploadAttachmentOutput, error) {
	if err := checkContentID("page_id", input.PageID); err != nil {
		return UploadAttachmentOutput{}, err
	}
	file, err := t.resolveUploadFile(input.Path)
	if err != nil {
		return UploadAttachmentOutput{}, err
	}
	content, err := readCheckedFile(file, t.cfg.Attachments.MaxBytes)
	if err != nil {
		return UploadAttachmentOutput{}, err
	}
	uploaded, err := t.attachments.UploadAttachment(ctx, input.PageID, confluence.AttachmentFile{Name: file.name(), Content: content})
	if err != nil {
		return UploadAttachmentOutput{}, err
	}
	return UploadAttachmentOutput{ID: uploaded.ID, Title: uploaded.Title, Version: uploaded.Version}, nil
}

type checkedFile struct {
	realPath string
	info     os.FileInfo
}

func (f checkedFile) name() string {
	return filepath.Base(f.realPath)
}

func (t *Toolset) resolveUploadFile(path string) (checkedFile, error) {
	file, err := resolveUploadPath(path)
	if err != nil {
		return checkedFile{}, err
	}
	if err := t.checkUploadPolicy(file); err != nil {
		return checkedFile{}, err
	}
	return file, nil
}

func resolveUploadPath(path string) (checkedFile, error) {
	if path == "" {
		return checkedFile{}, fmt.Errorf("%w: empty path", ErrInvalidAttachmentPath)
	}
	expanded, err := config.ExpandHome(path)
	if err != nil {
		return checkedFile{}, fmt.Errorf("%w: %q: %w", ErrInvalidAttachmentPath, path, err)
	}
	if !filepath.IsAbs(expanded) {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrInvalidAttachmentPath, path)
	}
	realPath, err := filepath.EvalSymlinks(expanded)
	if err != nil {
		return checkedFile{}, fmt.Errorf("%w: %w", ErrInvalidAttachmentPath, err)
	}
	if !guard.IsAttachmentFileName(filepath.Base(realPath)) {
		return checkedFile{}, fmt.Errorf("%w: %q", ErrInvalidAttachmentPath, realPath)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return checkedFile{}, fmt.Errorf("%w: %w", ErrInvalidAttachmentPath, err)
	}
	return checkedFile{realPath: realPath, info: info}, nil
}

func (t *Toolset) checkUploadPolicy(file checkedFile) error {
	allowedDirs := realDirs(t.cfg.Attachments.UploadDirs)
	if !slices.ContainsFunc(allowedDirs, func(dir string) bool { return config.IsInsideDir(file.realPath, dir) }) {
		return fmt.Errorf("%w: %q", ErrAttachmentOutsideDirs, file.realPath)
	}
	homeGuard := config.NewHomeGuard()
	if !slices.ContainsFunc(allowedDirs, func(dir string) bool { return homeGuard.IsInsideDirWithoutCrossingHomeOrRoot(file.realPath, dir) }) {
		return fmt.Errorf("%w: %q", ErrAttachmentThroughHome, file.realPath)
	}
	if t.isSecretFile(file.info) {
		return fmt.Errorf("%w: %q", ErrAttachmentSecretFile, file.realPath)
	}
	if !file.info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q", ErrAttachmentNotRegularFile, file.realPath)
	}
	if !t.cfg.Attachments.AllowsExtension(file.realPath) {
		return fmt.Errorf("%w: %q", ErrAttachmentExtensionNotAllowed, file.realPath)
	}
	if file.info.Size() > t.cfg.Attachments.MaxBytes {
		return fmt.Errorf("%w: %q has %d bytes", ErrAttachmentFileTooLarge, file.realPath, file.info.Size())
	}
	return nil
}

func (t *Toolset) isSecretFile(info os.FileInfo) bool {
	return containsSameFile(existingFileInfos(t.secretFilePaths(), os.Stat), info)
}

func containsSameFile(infos []os.FileInfo, info os.FileInfo) bool {
	return slices.ContainsFunc(infos, func(candidate os.FileInfo) bool { return os.SameFile(candidate, info) })
}

func (t *Toolset) secretFilePaths() []string {
	paths := []string{t.configPath, t.cfg.KeychainPath}
	if t.cfg.SecretsMode != config.SecretsModeFile {
		return paths
	}
	tokenStore, err := t.cfg.TokenStore()
	if err != nil {
		return paths
	}
	return append(paths, tokenStore.Location)
}

func openWithoutBlockingOnFIFO(path string) (*os.File, error) {
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

func existingFileInfos(paths []string, stat func(path string) (os.FileInfo, error)) []os.FileInfo {
	infos := make([]os.FileInfo, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		info, err := stat(path)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

func readCheckedFile(file checkedFile, maxBytes int64) ([]byte, error) {
	opened, err := openWithoutBlockingOnFIFO(file.realPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %w", ErrAttachmentChanged, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAttachmentPath, err)
	}
	defer func() { _ = opened.Close() }()
	if err := recheckOpenedFile(opened, file); err != nil {
		return nil, err
	}
	content, err := io.ReadAll(io.LimitReader(opened, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", file.realPath, err)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("%w: %q grew after the check", ErrAttachmentChanged, file.realPath)
	}
	return content, nil
}

func recheckOpenedFile(opened *os.File, file checkedFile) error {
	info, err := opened.Stat()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAttachmentChanged, err)
	}
	if !os.SameFile(info, file.info) {
		return fmt.Errorf("%w: %q", ErrAttachmentChanged, file.realPath)
	}
	return nil
}
