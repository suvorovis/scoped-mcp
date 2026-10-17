package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-gitlab/diff"
	"scoped-mcp/mcp-gitlab/gitlab"
	"scoped-mcp/mcp-gitlab/guard"
)

const (
	AddMRLineCommentToolName = "add_mr_line_comment"
	AddDraftNoteToolName     = "add_draft_note"
)

var (
	ErrPathInvalid     = errors.New("tools: file path must not contain control characters")
	ErrLineRequired    = errors.New("tools: new_line or old_line is required")
	ErrLineInvalid     = errors.New("tools: line numbers must be positive")
	ErrDraftTarget     = errors.New("tools: in_reply_to_discussion_id and a line position are mutually exclusive")
	ErrDiffRefsMissing = errors.New("tools: the merge request has no diff_refs (no commits yet?), a line position cannot be built; nothing was posted, retry after the merge request has commits")
	ErrDiffUnreadable  = errors.New("tools: the file diff could not be parsed (binary file?), the line cannot be located in it; nothing was posted")
)

type LineInput struct {
	NewPath      string `json:"new_path"`
	OldPath      string `json:"old_path,omitempty"`
	NewLine      int    `json:"new_line,omitempty"`
	OldLine      int    `json:"old_line,omitempty"`
	StartNewLine int    `json:"start_new_line,omitempty"`
	StartOldLine int    `json:"start_old_line,omitempty"`
}

func checkLine(line LineInput) error {
	if line.NewPath == "" {
		return fmt.Errorf("%w: %s", ErrFieldRequired, paramNewPath)
	}
	if !guard.ValidPath(line.NewPath) || !guard.ValidPath(line.oldPath()) {
		return ErrPathInvalid
	}
	if line.NewLine < 0 || line.OldLine < 0 || line.StartNewLine < 0 || line.StartOldLine < 0 {
		return ErrLineInvalid
	}
	if line.NewLine == 0 && line.OldLine == 0 {
		return ErrLineRequired
	}
	return nil
}

func (l LineInput) isMultiLine() bool {
	return l.StartNewLine != 0 || l.StartOldLine != 0
}

func (l LineInput) needsFileDiff() bool {
	return l.isMultiLine() || !l.hasBothLineNumbers()
}

func (l LineInput) hasBothLineNumbers() bool {
	return l.OldLine != 0 && l.NewLine != 0
}

func (l LineInput) oldPath() string {
	if l.OldPath == "" {
		return l.NewPath
	}
	return l.OldPath
}

func (l LineInput) firstLine() diff.Target {
	if !l.isMultiLine() {
		return l.lastLine()
	}
	return diff.Target{OldLine: l.StartOldLine, NewLine: l.StartNewLine}
}

func (l LineInput) lastLine() diff.Target {
	return diff.Target{OldLine: l.OldLine, NewLine: l.NewLine}
}

type AddMRLineCommentInput struct {
	Project string `json:"project"`
	MRIID   int64  `json:"mr_iid"`
	Body    string `json:"body"`
	LineInput
}

type AddMRLineCommentOutput struct {
	DiscussionID string `json:"discussion_id"`
	NoteID       int64  `json:"note_id"`
	URL          string `json:"url"`
}

type AddDraftNoteInput struct {
	Project               string `json:"project"`
	MRIID                 int64  `json:"mr_iid"`
	Note                  string `json:"note"`
	InReplyToDiscussionID string `json:"in_reply_to_discussion_id,omitempty"`
	LineInput
}

func (i AddDraftNoteInput) hasPosition() bool {
	return i.LineInput != LineInput{}
}

type AddDraftNoteOutput struct {
	DraftNoteID int64  `json:"draft_note_id"`
	URL         string `json:"url"`
}

func (t *Toolset) registerAddMRLineComment(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        AddMRLineCommentToolName,
		Description: AddMRLineCommentDescription(t.cfg),
		InputSchema: AddMRLineCommentInputSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AddMRLineCommentInput) (*mcp.CallToolResult, AddMRLineCommentOutput, error) {
		out, err := t.AddMRLineComment(ctx, input)
		return nil, out, err
	})
}

func (t *Toolset) AddMRLineComment(ctx context.Context, input AddMRLineCommentInput) (AddMRLineCommentOutput, error) {
	ref, err := mergeRequestRef(input.Project, input.MRIID)
	if err != nil {
		return AddMRLineCommentOutput{}, err
	}
	if err := checkText(paramBody, input.Body); err != nil {
		return AddMRLineCommentOutput{}, err
	}
	if err := checkLine(input.LineInput); err != nil {
		return AddMRLineCommentOutput{}, err
	}
	mergeRequest, err := t.mergeRequestAPI.MergeRequest(ctx, ref)
	if err != nil {
		return AddMRLineCommentOutput{}, err
	}
	position, err := t.buildPosition(ctx, ref, mergeRequest, input.LineInput)
	if err != nil {
		return AddMRLineCommentOutput{}, err
	}
	discussion, err := t.mergeRequestAPI.AddDiscussion(ctx, ref, input.Body, position)
	if err != nil {
		return AddMRLineCommentOutput{}, err
	}
	return AddMRLineCommentOutput{
		DiscussionID: discussion.ID,
		NoteID:       discussion.NoteID,
		URL:          noteURL(mergeRequest.WebURL, discussion.NoteID),
	}, nil
}

func (t *Toolset) registerAddDraftNote(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        AddDraftNoteToolName,
		Description: AddDraftNoteDescription(t.cfg),
		InputSchema: AddDraftNoteInputSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AddDraftNoteInput) (*mcp.CallToolResult, AddDraftNoteOutput, error) {
		out, err := t.AddDraftNote(ctx, input)
		return nil, out, err
	})
}

func (t *Toolset) AddDraftNote(ctx context.Context, input AddDraftNoteInput) (AddDraftNoteOutput, error) {
	ref, err := mergeRequestRef(input.Project, input.MRIID)
	if err != nil {
		return AddDraftNoteOutput{}, err
	}
	if err := checkText(paramNote, input.Note); err != nil {
		return AddDraftNoteOutput{}, err
	}
	if err := checkDraftTarget(input); err != nil {
		return AddDraftNoteOutput{}, err
	}
	mergeRequest, err := t.mergeRequestAPI.MergeRequest(ctx, ref)
	if err != nil {
		return AddDraftNoteOutput{}, err
	}
	request := gitlab.DraftNoteRequest{Note: input.Note, InReplyToDiscussionID: input.InReplyToDiscussionID}
	if input.hasPosition() {
		position, err := t.buildPosition(ctx, ref, mergeRequest, input.LineInput)
		if err != nil {
			return AddDraftNoteOutput{}, err
		}
		request.Position = &position
	}
	draft, err := t.mergeRequestAPI.AddDraftNote(ctx, ref, request)
	if err != nil {
		return AddDraftNoteOutput{}, err
	}
	return AddDraftNoteOutput{DraftNoteID: draft.ID, URL: mergeRequest.WebURL}, nil
}

func checkDraftTarget(input AddDraftNoteInput) error {
	isReply := input.InReplyToDiscussionID != ""
	switch {
	case isReply && input.hasPosition():
		return ErrDraftTarget
	case isReply:
		return checkDiscussionID(input.InReplyToDiscussionID)
	case input.hasPosition():
		return checkLine(input.LineInput)
	default:
		return nil
	}
}

func (t *Toolset) buildPosition(ctx context.Context, ref gitlab.MergeRequestRef, mergeRequest gitlab.MergeRequest, line LineInput) (gitlab.Position, error) {
	if !mergeRequest.DiffRefs.Complete() {
		return gitlab.Position{}, ErrDiffRefsMissing
	}
	position := gitlab.Position{
		DiffRefs: mergeRequest.DiffRefs,
		OldPath:  line.oldPath(),
		NewPath:  line.NewPath,
		OldLine:  line.OldLine,
		NewLine:  line.NewLine,
	}
	if !line.needsFileDiff() {
		return position, nil
	}
	fileDiff, err := t.mergeRequestAPI.FileDiff(ctx, ref, line.NewPath)
	if err != nil {
		return gitlab.Position{}, err
	}
	commentedLines, err := findCommentedLines(fileDiff, line)
	if err != nil {
		return gitlab.Position{}, err
	}
	fillUnchangedLineNumbers(&position, commentedLines.Last)
	if line.isMultiLine() {
		position.LineRange = &gitlab.LineRange{
			Start: lineRangeEndpoint(line.NewPath, commentedLines.First),
			End:   lineRangeEndpoint(line.NewPath, commentedLines.Last),
		}
	}
	return position, nil
}

func findCommentedLines(fileDiff string, line LineInput) (diff.Range, error) {
	lines, err := diff.ParseHunks(fileDiff)
	if err != nil {
		return diff.Range{}, fmt.Errorf("%w: %w", ErrDiffUnreadable, err)
	}
	return diff.FindRange(lines, line.firstLine(), line.lastLine())
}

func fillUnchangedLineNumbers(position *gitlab.Position, lastLine diff.Line) {
	if lastLine.Kind == diff.Context {
		position.OldLine, position.NewLine = lastLine.OldPos, lastLine.NewPos
	}
}

func lineRangeEndpoint(newPath string, diffLine diff.Line) gitlab.LineRangeEnd {
	return gitlab.LineRangeEnd{
		LineCode: diff.LineCode(newPath, diffLine),
		Type:     diffLine.LineRangeType(),
	}
}
