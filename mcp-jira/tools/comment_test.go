package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-jira/jira"
	"scoped-mcp/mcp-jira/jira/mocks"
	"scoped-mcp/mcp-jira/tools"
)

const (
	commentIssueKey = "PROJ-7790"
	commentText     = "h3. Итог\n*готово*"
	addedCommentID  = "10100"
	addedCommentURL = "https://jira.example.com/browse/PROJ-7790?focusedCommentId=10100#comment-10100"
)

var addedComment = jira.Comment{ID: addedCommentID, URL: addedCommentURL}

func expectComment(issueKey, body string, comment jira.Comment, err error) func(commenter *mocks.MockCommenter) {
	return func(commenter *mocks.MockCommenter) {
		commenter.EXPECT().AddComment(gomock.Any(), issueKey, body).Return(comment, err)
	}
}

func TestAddComment(t *testing.T) {
	tests := []struct {
		name    string
		input   tools.AddCommentInput
		expect  func(commenter *mocks.MockCommenter)
		want    tools.AddCommentOutput
		wantErr error
	}{
		{
			name:   "комментарий к задаче проекта из конфига → id и ссылка",
			input:  tools.AddCommentInput{IssueKey: commentIssueKey, Body: commentText},
			expect: expectComment(commentIssueKey, commentText, addedComment, nil),
			want:   tools.AddCommentOutput{IssueKey: commentIssueKey, ID: addedCommentID, URL: addedCommentURL},
		},
		{
			name:   "задача второго проекта из конфига",
			input:  tools.AddCommentInput{IssueKey: "OTHER-1", Body: "x"},
			expect: expectComment("OTHER-1", "x", jira.Comment{ID: "1", URL: "u"}, nil),
			want:   tools.AddCommentOutput{IssueKey: "OTHER-1", ID: "1", URL: "u"},
		},
		{
			name:   "текст уходит без обрезки пробелов",
			input:  tools.AddCommentInput{IssueKey: commentIssueKey, Body: "  готово\n"},
			expect: expectComment(commentIssueKey, "  готово\n", addedComment, nil),
			want:   tools.AddCommentOutput{IssueKey: commentIssueKey, ID: addedCommentID, URL: addedCommentURL},
		},
		{
			name:    "комментарий не подтверждён Jira → ошибка клиента как есть",
			input:   tools.AddCommentInput{IssueKey: commentIssueKey, Body: commentText},
			expect:  expectComment(commentIssueKey, commentText, jira.Comment{}, jira.ErrCommentUnconfirmed),
			wantErr: jira.ErrCommentUnconfirmed,
		},
		{
			name:    "ошибка клиента пробрасывается",
			input:   tools.AddCommentInput{IssueKey: commentIssueKey, Body: commentText},
			expect:  expectComment(commentIssueKey, commentText, jira.Comment{}, jira.ErrUnauthorized),
			wantErr: jira.ErrUnauthorized,
		},

		{name: "задача проекта не из конфига", input: tools.AddCommentInput{IssueKey: "SECRET-1", Body: "x"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "ключ строчными", input: tools.AddCommentInput{IssueKey: "proj-1", Body: "x"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "ключ с нулевым номером", input: tools.AddCommentInput{IssueKey: "PROJ-0", Body: "x"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "числовой ID задачи", input: tools.AddCommentInput{IssueKey: "10001", Body: "x"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "ключ с путём", input: tools.AddCommentInput{IssueKey: "PROJ-1/comment/1", Body: "x"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "пустой ключ", input: tools.AddCommentInput{Body: "x"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "пустой текст", input: tools.AddCommentInput{IssueKey: commentIssueKey}, wantErr: tools.ErrFieldRequired},
		{name: "текст из пробелов", input: tools.AddCommentInput{IssueKey: commentIssueKey, Body: " \n\t "}, wantErr: tools.ErrFieldRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			commenter := mocks.NewMockCommenter(ctrl)
			if tt.expect != nil {
				tt.expect(commenter)
			}
			clients := mockClients(ctrl)
			clients.Commenter = commenter
			toolset := tools.New(clients, withAddComment(), unusedConfigPath)

			got, err := toolset.AddComment(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAddCommentOverMCP(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]any
		expect      func(commenter *mocks.MockCommenter)
		wantJSON    string
		wantIsError bool
	}{
		{
			name:     "успех → issue_key, id и url",
			args:     map[string]any{"issue_key": commentIssueKey, "body": commentText},
			expect:   expectComment(commentIssueKey, commentText, addedComment, nil),
			wantJSON: `{"issue_key":"PROJ-7790","id":"10100","url":"https://jira.example.com/browse/PROJ-7790?focusedCommentId=10100#comment-10100"}`,
		},
		{name: "без body → IsError", args: map[string]any{"issue_key": commentIssueKey}, wantIsError: true},
		{name: "пустой body → IsError", args: map[string]any{"issue_key": commentIssueKey, "body": ""}, wantIsError: true},
		{name: "body из пробелов → IsError", args: map[string]any{"issue_key": commentIssueKey, "body": "  "}, wantIsError: true},
		{name: "body не строка → IsError", args: map[string]any{"issue_key": commentIssueKey, "body": 1}, wantIsError: true},
		{name: "без issue_key → IsError", args: map[string]any{"body": "x"}, wantIsError: true},
		{name: "issue_key не по шаблону → IsError", args: map[string]any{"issue_key": "10001", "body": "x"}, wantIsError: true},
		{name: "задача проекта не из конфига → IsError", args: map[string]any{"issue_key": "SECRET-1", "body": "x"}, wantIsError: true},
		{name: "лишний параметр visibility → IsError", args: map[string]any{"issue_key": commentIssueKey, "body": "x", "visibility": map[string]any{"type": "role"}}, wantIsError: true},
		{
			name:        "ошибка клиента → IsError",
			args:        map[string]any{"issue_key": commentIssueKey, "body": commentText},
			expect:      expectComment(commentIssueKey, commentText, jira.Comment{}, jira.ErrCommentUnconfirmed),
			wantIsError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			commenter := mocks.NewMockCommenter(ctrl)
			if tt.expect != nil {
				tt.expect(commenter)
			}
			clients := mockClients(ctrl)
			clients.Commenter = commenter
			session := connectMCP(t, tools.New(clients, withAddComment(), unusedConfigPath))

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.AddCommentToolName, Arguments: tt.args})

			require.NoError(t, err)
			require.Equal(t, tt.wantIsError, result.IsError)
			if tt.wantIsError {
				require.NotEmpty(t, result.Content)
				return
			}
			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantJSON, string(structured))
		})
	}
}
