package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListCommentsInput struct {
	PageID string `json:"page_id" jsonschema:"Numeric id of the page"`
}

type CommentOutput struct {
	ID              string `json:"id"`
	ParentCommentID string `json:"parent_comment_id,omitempty"`
	Author          string `json:"author"`
	Created         string `json:"created"`
	Body            string `json:"body"`
}

type ListCommentsOutput struct {
	Comments []CommentOutput `json:"comments"`
}

func (t *Toolset) registerListComments(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        ListCommentsToolName,
		Description: "List all comments of a page from the allowed subtrees, including replies (parent_comment_id links a reply to its parent). Bodies are in storage format.",
	}, t.ListComments)
}

func (t *Toolset) ListComments(ctx context.Context, input ListCommentsInput) (ListCommentsOutput, error) {
	if err := checkContentID("page_id", input.PageID); err != nil {
		return ListCommentsOutput{}, err
	}
	comments, err := t.comments.ListComments(ctx, input.PageID)
	if err != nil {
		return ListCommentsOutput{}, err
	}
	output := make([]CommentOutput, 0, len(comments))
	for _, comment := range comments {
		output = append(output, CommentOutput{ID: comment.ID, ParentCommentID: comment.ParentCommentID, Author: comment.Author, Created: comment.Created, Body: comment.Body})
	}
	return ListCommentsOutput{Comments: output}, nil
}
