package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/jira"
	"scoped-mcp/mcp-jira/jira/mocks"
	"scoped-mcp/mcp-jira/tools"
)

const (
	linkIssueKey  = "PROJ-7790"
	linkTargetKey = "PROJ-7791"
	linkIssueURL  = "https://jira.example.com/browse/PROJ-7790"
)

var blocksRequest = jira.LinkRequest{TypeName: "Blocks", IssueKey: linkIssueKey, TargetKey: linkTargetKey, Direction: config.LinkOutward}

func expectLink(request jira.LinkRequest, err error) func(linker *mocks.MockLinker) {
	return func(linker *mocks.MockLinker) {
		linker.EXPECT().LinkIssues(gomock.Any(), request).Return(err)
	}
}

func TestLinkIssues(t *testing.T) {
	blocksInput := tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "blocks", TargetKey: linkTargetKey}
	tests := []struct {
		name    string
		input   tools.LinkIssuesInput
		expect  func(linker *mocks.MockLinker)
		want    tools.LinkIssuesOutput
		wantErr error
	}{
		{
			name:   "blocks → запрос с типом Blocks и направлением outward, ответ со ссылкой на issue_key",
			input:  blocksInput,
			expect: expectLink(blocksRequest, nil),
			want:   tools.LinkIssuesOutput{IssueKey: linkIssueKey, Relation: "blocks", TargetKey: linkTargetKey, URL: linkIssueURL},
		},
		{
			name:   "is blocked by → тот же тип, направление inward",
			input:  tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "is blocked by", TargetKey: linkTargetKey},
			expect: expectLink(jira.LinkRequest{TypeName: "Blocks", IssueKey: linkIssueKey, TargetKey: linkTargetKey, Direction: config.LinkInward}, nil),
			want:   tools.LinkIssuesOutput{IssueKey: linkIssueKey, Relation: "is blocked by", TargetKey: linkTargetKey, URL: linkIssueURL},
		},
		{
			name:   "relates to → тип Relates",
			input:  tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "relates to", TargetKey: linkTargetKey},
			expect: expectLink(jira.LinkRequest{TypeName: "Relates", IssueKey: linkIssueKey, TargetKey: linkTargetKey, Direction: config.LinkOutward}, nil),
			want:   tools.LinkIssuesOutput{IssueKey: linkIssueKey, Relation: "relates to", TargetKey: linkTargetKey, URL: linkIssueURL},
		},
		{
			name:   "задачи разных проектов",
			input:  tools.LinkIssuesInput{IssueKey: "OTHER-1", Relation: "blocks", TargetKey: linkIssueKey},
			expect: expectLink(jira.LinkRequest{TypeName: "Blocks", IssueKey: "OTHER-1", TargetKey: linkIssueKey, Direction: config.LinkOutward}, nil),
			want:   tools.LinkIssuesOutput{IssueKey: "OTHER-1", Relation: "blocks", TargetKey: linkIssueKey, URL: "https://jira.example.com/browse/OTHER-1"},
		},
		{
			name:    "ErrLinkUnconfirmed клиента → возвращается как есть",
			input:   blocksInput,
			expect:  expectLink(blocksRequest, jira.ErrLinkUnconfirmed),
			wantErr: jira.ErrLinkUnconfirmed,
		},
		{
			name:    "ErrUnauthorized клиента → возвращается как есть",
			input:   blocksInput,
			expect:  expectLink(blocksRequest, jira.ErrUnauthorized),
			wantErr: jira.ErrUnauthorized,
		},

		{name: "issue_key чужого проекта", input: tools.LinkIssuesInput{IssueKey: "SECRET-1", Relation: "blocks", TargetKey: linkTargetKey}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "target_key чужого проекта", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "blocks", TargetKey: "SECRET-1"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "issue_key строчными", input: tools.LinkIssuesInput{IssueKey: "proj-1", Relation: "blocks", TargetKey: linkTargetKey}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "target_key с нулевым номером", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "blocks", TargetKey: "PROJ-0"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "числовой ID в issue_key", input: tools.LinkIssuesInput{IssueKey: "10001", Relation: "blocks", TargetKey: linkTargetKey}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "пустой target_key", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "blocks"}, wantErr: tools.ErrIssueKeyInvalid},
		{name: "фраза не из конфига", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "duplicates", TargetKey: linkTargetKey}, wantErr: tools.ErrRelationNotAllowed},
		{name: "фраза в другом регистре", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "Blocks", TargetKey: linkTargetKey}, wantErr: tools.ErrRelationNotAllowed},
		{name: "фраза с пробелом по краю", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: " blocks", TargetKey: linkTargetKey}, wantErr: tools.ErrRelationNotAllowed},
		{name: "имя типа вместо фразы", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "Relates", TargetKey: linkTargetKey}, wantErr: tools.ErrRelationNotAllowed},
		{name: "пустая фраза", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, TargetKey: linkTargetKey}, wantErr: tools.ErrRelationNotAllowed},
		{name: "одна и та же задача", input: tools.LinkIssuesInput{IssueKey: linkIssueKey, Relation: "blocks", TargetKey: linkIssueKey}, wantErr: tools.ErrLinkToItself},
		{name: "ключи проверяются раньше фразы", input: tools.LinkIssuesInput{IssueKey: "SECRET-1", Relation: "duplicates", TargetKey: linkTargetKey}, wantErr: tools.ErrIssueKeyInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			linker := mocks.NewMockLinker(ctrl)
			if tt.expect != nil {
				tt.expect(linker)
			}
			clients := mockClients(ctrl)
			clients.Linker = linker
			toolset := tools.New(clients, withLinkIssues(), unusedConfigPath)

			got, err := toolset.LinkIssues(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLinkIssuesOverMCP(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]any
		expect      func(linker *mocks.MockLinker)
		wantJSON    string
		wantIsError bool
	}{
		{
			name:     "успех → issue_key, relation, target_key и url",
			args:     map[string]any{"issue_key": linkIssueKey, "relation": "blocks", "target_key": linkTargetKey},
			expect:   expectLink(blocksRequest, nil),
			wantJSON: `{"issue_key":"PROJ-7790","relation":"blocks","target_key":"PROJ-7791","url":"https://jira.example.com/browse/PROJ-7790"}`,
		},
		{name: "без relation → IsError", args: map[string]any{"issue_key": linkIssueKey, "target_key": linkTargetKey}, wantIsError: true},
		{name: "relation вне enum → IsError", args: map[string]any{"issue_key": linkIssueKey, "relation": "duplicates", "target_key": linkTargetKey}, wantIsError: true},
		{name: "relation не строка → IsError", args: map[string]any{"issue_key": linkIssueKey, "relation": 1, "target_key": linkTargetKey}, wantIsError: true},
		{name: "без target_key → IsError", args: map[string]any{"issue_key": linkIssueKey, "relation": "blocks"}, wantIsError: true},
		{name: "target_key не по шаблону → IsError", args: map[string]any{"issue_key": linkIssueKey, "relation": "blocks", "target_key": "10001"}, wantIsError: true},
		{name: "issue_key чужого проекта → IsError", args: map[string]any{"issue_key": "SECRET-1", "relation": "blocks", "target_key": linkTargetKey}, wantIsError: true},
		{name: "одинаковые ключи → IsError", args: map[string]any{"issue_key": linkIssueKey, "relation": "blocks", "target_key": linkIssueKey}, wantIsError: true},
		{name: "лишний параметр comment → IsError", args: map[string]any{"issue_key": linkIssueKey, "relation": "blocks", "target_key": linkTargetKey, "comment": "x"}, wantIsError: true},
		{
			name:        "ошибка клиента → IsError",
			args:        map[string]any{"issue_key": linkIssueKey, "relation": "blocks", "target_key": linkTargetKey},
			expect:      expectLink(blocksRequest, jira.ErrLinkUnconfirmed),
			wantIsError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			linker := mocks.NewMockLinker(ctrl)
			if tt.expect != nil {
				tt.expect(linker)
			}
			clients := mockClients(ctrl)
			clients.Linker = linker
			session := connectMCP(t, tools.New(clients, withLinkIssues(), unusedConfigPath))

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.LinkIssuesToolName, Arguments: tt.args})

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
