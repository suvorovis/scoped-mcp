package confluence

import (
	"context"
	"net/url"
	"slices"
)

const (
	commentExpand = "body.storage,history,ancestors"
	depthParam    = "depth"
	depthAll      = "all"
	typeComment   = "comment"
)

type Comment struct {
	ID              string
	ParentCommentID string
	Author          string
	Created         string
	Body            string
}

type Comments interface {
	ListComments(ctx context.Context, pageID string) ([]Comment, error)
}

var _ Comments = (*Client)(nil)

func (c *Client) ListComments(ctx context.Context, pageID string) ([]Comment, error) {
	comments := []Comment{}
	query := url.Values{expandParam: {commentExpand}, depthParam: {depthAll}}
	err := c.forEachListed(ctx, childPath(pageID, childComments), query, func(content contentResponse) {
		comments = append(comments, commentOf(content))
	})
	if err != nil {
		return nil, err
	}
	return comments, nil
}

func commentOf(content contentResponse) Comment {
	return Comment{
		ID:              content.ID,
		ParentCommentID: parentCommentID(content.Ancestors),
		Author:          content.History.CreatedBy.DisplayName,
		Created:         content.History.CreatedDate,
		Body:            content.Body.Storage.Value,
	}
}

func parentCommentID(ancestors []contentRef) string {
	for _, ancestor := range slices.Backward(ancestors) {
		if ancestor.Type == typeComment {
			return ancestor.ID
		}
	}
	return ""
}
