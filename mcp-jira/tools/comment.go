package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const AddCommentToolName = "add_comment"

type AddCommentInput struct {
	IssueKey string `json:"issue_key"`
	Body     string `json:"body"`
}

type AddCommentOutput struct {
	IssueKey string `json:"issue_key"`
	ID       string `json:"id"`
	URL      string `json:"url"`
}

func (t *Toolset) registerAddComment(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        AddCommentToolName,
		Description: AddCommentDescription(t.cfg),
		InputSchema: AddCommentInputSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input AddCommentInput) (*mcp.CallToolResult, AddCommentOutput, error) {
		out, err := t.AddComment(ctx, input)
		return nil, out, err
	})
}

func (t *Toolset) AddComment(ctx context.Context, input AddCommentInput) (AddCommentOutput, error) {
	if err := t.checkIssueKey(paramIssueKey, input.IssueKey); err != nil {
		return AddCommentOutput{}, err
	}
	if isBlank(input.Body) {
		return AddCommentOutput{}, fmt.Errorf("%w: %s", ErrFieldRequired, paramBody)
	}
	comment, err := t.commenter.AddComment(ctx, input.IssueKey, input.Body)
	if err != nil {
		return AddCommentOutput{}, err
	}
	return AddCommentOutput{IssueKey: input.IssueKey, ID: comment.ID, URL: comment.URL}, nil
}
