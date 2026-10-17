package tools_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/confluence/mocks"
	"scoped-mcp/mcp-confluence/tools"
)

func TestListComments(t *testing.T) {
	comments := []confluence.Comment{
		{ID: "901", Author: "User One", Created: "2026-10-03T12:00:00.000+03:00", Body: "<p>root</p>"},
		{ID: "902", ParentCommentID: "901", Author: "User Two", Created: "2026-10-03T13:00:00.000+03:00", Body: "<p>reply</p>"},
	}
	tests := []struct {
		name    string
		pageID  string
		expect  func(commentsClient *mocks.MockComments)
		want    tools.ListCommentsOutput
		wantErr error
	}{
		{
			name: "комментарии всех уровней", pageID: "101",
			expect: func(commentsClient *mocks.MockComments) {
				commentsClient.EXPECT().ListComments(gomock.Any(), "101").Return(comments, nil)
			},
			want: tools.ListCommentsOutput{Comments: []tools.CommentOutput{
				{ID: "901", Author: "User One", Created: "2026-10-03T12:00:00.000+03:00", Body: "<p>root</p>"},
				{ID: "902", ParentCommentID: "901", Author: "User Two", Created: "2026-10-03T13:00:00.000+03:00", Body: "<p>reply</p>"},
			}},
		},
		{
			name: "без комментариев", pageID: "101",
			expect: func(commentsClient *mocks.MockComments) {
				commentsClient.EXPECT().ListComments(gomock.Any(), "101").Return([]confluence.Comment{}, nil)
			},
			want: tools.ListCommentsOutput{Comments: []tools.CommentOutput{}},
		},
		{
			name: "ошибка клиента", pageID: "300",
			expect: func(commentsClient *mocks.MockComments) {
				commentsClient.EXPECT().ListComments(gomock.Any(), "300").Return(nil, confluence.ErrNotFound)
			},
			wantErr: confluence.ErrNotFound,
		},
		{name: "page_id не число", pageID: "x", wantErr: tools.ErrInvalidContentID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clients := newToolset(t, toolConfig)
			if tt.expect != nil {
				tt.expect(clients.comments)
			}

			got, err := toolset.ListComments(t.Context(), tools.ListCommentsInput{PageID: tt.pageID})

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}
