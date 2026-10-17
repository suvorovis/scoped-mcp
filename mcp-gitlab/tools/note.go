package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	AddMRNoteToolName         = "add_mr_note"
	ReplyToDiscussionToolName = "reply_to_discussion"
)

type AddMRNoteInput struct {
	Project string `json:"project"`
	MRIID   int64  `json:"mr_iid"`
	Body    string `json:"body"`
}

type AddMRNoteOutput struct {
	NoteID int64  `json:"note_id"`
	URL    string `json:"url"`
}

type ReplyToDiscussionInput struct {
	Project      string `json:"project"`
	MRIID        int64  `json:"mr_iid"`
	DiscussionID string `json:"discussion_id"`
	Body         string `json:"body"`
}

type ReplyToDiscussionOutput struct {
	NoteID int64  `json:"note_id"`
	URL    string `json:"url"`
}

func (t *Toolset) registerAddMRNote(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        AddMRNoteToolName,
		Description: AddMRNoteDescription(t.cfg),
		InputSchema: AddMRNoteInputSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AddMRNoteInput) (*mcp.CallToolResult, AddMRNoteOutput, error) {
		out, err := t.AddMRNote(ctx, input)
		return nil, out, err
	})
}

func (t *Toolset) AddMRNote(ctx context.Context, input AddMRNoteInput) (AddMRNoteOutput, error) {
	ref, err := mergeRequestRef(input.Project, input.MRIID)
	if err != nil {
		return AddMRNoteOutput{}, err
	}
	if err := checkText(paramBody, input.Body); err != nil {
		return AddMRNoteOutput{}, err
	}
	mergeRequest, err := t.mergeRequestAPI.MergeRequest(ctx, ref)
	if err != nil {
		return AddMRNoteOutput{}, err
	}
	note, err := t.mergeRequestAPI.AddNote(ctx, ref, input.Body)
	if err != nil {
		return AddMRNoteOutput{}, err
	}
	return AddMRNoteOutput{NoteID: note.ID, URL: noteURL(mergeRequest.WebURL, note.ID)}, nil
}

func (t *Toolset) registerReplyToDiscussion(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        ReplyToDiscussionToolName,
		Description: ReplyToDiscussionDescription(t.cfg),
		InputSchema: ReplyToDiscussionInputSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ReplyToDiscussionInput) (*mcp.CallToolResult, ReplyToDiscussionOutput, error) {
		out, err := t.ReplyToDiscussion(ctx, input)
		return nil, out, err
	})
}

func (t *Toolset) ReplyToDiscussion(ctx context.Context, input ReplyToDiscussionInput) (ReplyToDiscussionOutput, error) {
	ref, err := mergeRequestRef(input.Project, input.MRIID)
	if err != nil {
		return ReplyToDiscussionOutput{}, err
	}
	if err := checkDiscussionID(input.DiscussionID); err != nil {
		return ReplyToDiscussionOutput{}, err
	}
	if err := checkText(paramBody, input.Body); err != nil {
		return ReplyToDiscussionOutput{}, err
	}
	mergeRequest, err := t.mergeRequestAPI.MergeRequest(ctx, ref)
	if err != nil {
		return ReplyToDiscussionOutput{}, err
	}
	note, err := t.mergeRequestAPI.AddReply(ctx, ref, input.DiscussionID, input.Body)
	if err != nil {
		return ReplyToDiscussionOutput{}, err
	}
	return ReplyToDiscussionOutput{NoteID: note.ID, URL: noteURL(mergeRequest.WebURL, note.ID)}, nil
}
