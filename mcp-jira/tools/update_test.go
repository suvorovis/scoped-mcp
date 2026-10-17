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
	updateIssueKey = "PROJ-7790"
	updateIssueURL = "https://jira.example.com/browse/PROJ-7790"
)

var (
	moveToEpicRequest = jira.UpdateRequest{
		IssueKey:       updateIssueKey,
		ValuesByJiraID: map[string]jira.FieldValue{"customfield_10201": {Kind: config.KindIssueKey, Text: "PROJ-7630"}},
	}
	clearEpicRequest = jira.UpdateRequest{IssueKey: updateIssueKey, ClearedJiraIDs: []string{"customfield_10201"}}
	updatedIssue     = tools.UpdateIssueOutput{Key: updateIssueKey, URL: updateIssueURL}
)

func moveToEpicArgs() map[string]any {
	return map[string]any{"issue_key": updateIssueKey, "epic_link": "PROJ-7630"}
}

func expectUpdate(request jira.UpdateRequest, err error) func(updater *mocks.MockUpdater) {
	return func(updater *mocks.MockUpdater) {
		updater.EXPECT().UpdateIssue(gomock.Any(), request).Return(err)
	}
}

func TestUpdateIssue(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		expect  func(updater *mocks.MockUpdater)
		want    tools.UpdateIssueOutput
		wantErr error
	}{
		{name: "epic_link → значение поля, ответ с ключом и ссылкой", args: moveToEpicArgs(), expect: expectUpdate(moveToEpicRequest, nil), want: updatedIssue},
		{
			name:   "epic_link null → очистка поля",
			args:   map[string]any{"issue_key": updateIssueKey, "epic_link": nil},
			expect: expectUpdate(clearEpicRequest, nil),
			want:   updatedIssue,
		},
		{
			name: "значение и очистка в одном вызове",
			args: map[string]any{"issue_key": updateIssueKey, "epic_link": nil, "labels": []any{"backend"}},
			expect: expectUpdate(jira.UpdateRequest{
				IssueKey:       updateIssueKey,
				ValuesByJiraID: map[string]jira.FieldValue{"labels": {Kind: config.KindLabels, Items: []string{"backend"}}},
				ClearedJiraIDs: []string{"customfield_10201"},
			}, nil),
			want: updatedIssue,
		},
		{
			name:   "задача другого проекта — поля из её update_fields",
			args:   map[string]any{"issue_key": "OTHER-1", "priority": "High"},
			expect: expectUpdate(jira.UpdateRequest{IssueKey: "OTHER-1", ValuesByJiraID: map[string]jira.FieldValue{"priority": {Kind: config.KindName, Text: "High"}}}, nil),
			want:   tools.UpdateIssueOutput{Key: "OTHER-1", URL: "https://jira.example.com/browse/OTHER-1"},
		},
		{
			name:   "epic_link другого разрешённого проекта",
			args:   withArg(moveToEpicArgs(), "epic_link", "OTHER-5"),
			expect: expectUpdate(jira.UpdateRequest{IssueKey: updateIssueKey, ValuesByJiraID: map[string]jira.FieldValue{"customfield_10201": {Kind: config.KindIssueKey, Text: "OTHER-5"}}}, nil),
			want:   updatedIssue,
		},
		{name: "ErrUpdateUnconfirmed клиента → возвращается как есть", args: moveToEpicArgs(), expect: expectUpdate(moveToEpicRequest, jira.ErrUpdateUnconfirmed), wantErr: jira.ErrUpdateUnconfirmed},
		{name: "ErrUnauthorized клиента → возвращается как есть", args: moveToEpicArgs(), expect: expectUpdate(moveToEpicRequest, jira.ErrUnauthorized), wantErr: jira.ErrUnauthorized},

		{name: "без issue_key", args: withoutArg(moveToEpicArgs(), "issue_key"), wantErr: tools.ErrFieldRequired},
		{name: "issue_key не строка", args: withArg(moveToEpicArgs(), "issue_key", 1), wantErr: tools.ErrFieldValue},
		{name: "issue_key чужого проекта", args: withArg(moveToEpicArgs(), "issue_key", "SECRET-1"), wantErr: tools.ErrIssueKeyInvalid},
		{name: "числовой ID в issue_key", args: withArg(moveToEpicArgs(), "issue_key", "10001"), wantErr: tools.ErrIssueKeyInvalid},
		{name: "только issue_key", args: map[string]any{"issue_key": updateIssueKey}, wantErr: tools.ErrNoFieldsToUpdate},
		{name: "поле не из update_fields проекта задачи", args: map[string]any{"issue_key": "OTHER-1", "epic_link": "PROJ-7630"}, wantErr: tools.ErrFieldNotAllowed},
		{name: "поле из fields, но не из update_fields", args: withArg(moveToEpicArgs(), "description", "x"), wantErr: tools.ErrFieldNotAllowed},
		{name: "неизвестное поле", args: withArg(moveToEpicArgs(), "sprint", "x"), wantErr: tools.ErrFieldNotAllowed},
		{name: "неразрешённое поле проверяется раньше формы значений", args: withArg(withArg(moveToEpicArgs(), "epic_link", 1), "sprint", "x"), wantErr: tools.ErrFieldNotAllowed},
		{name: "epic_link пустой строкой — не очистка", args: withArg(moveToEpicArgs(), "epic_link", ""), wantErr: tools.ErrIssueKeyInvalid},
		{name: "epic_link чужого проекта", args: withArg(moveToEpicArgs(), "epic_link", "SECRET-1"), wantErr: tools.ErrIssueKeyInvalid},
		{name: "epic_link не строка", args: withArg(moveToEpicArgs(), "epic_link", []any{"PROJ-7630"}), wantErr: tools.ErrFieldValue},
		{name: "labels строкой", args: withArg(moveToEpicArgs(), "labels", "backend"), wantErr: tools.ErrFieldValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			updater := mocks.NewMockUpdater(ctrl)
			if tt.expect != nil {
				tt.expect(updater)
			}
			clients := mockClients(ctrl)
			clients.Updater = updater

			got, err := tools.New(clients, withUpdateIssue(), unusedConfigPath).UpdateIssue(t.Context(), tt.args)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestUpdateIssueOverMCP(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]any
		expect      func(updater *mocks.MockUpdater)
		wantJSON    string
		wantIsError bool
	}{
		{
			name:     "успех → key и url",
			args:     moveToEpicArgs(),
			expect:   expectUpdate(moveToEpicRequest, nil),
			wantJSON: `{"key":"PROJ-7790","url":"https://jira.example.com/browse/PROJ-7790"}`,
		},
		{
			name:     "null проходит схему и очищает поле",
			args:     map[string]any{"issue_key": updateIssueKey, "epic_link": nil},
			expect:   expectUpdate(clearEpicRequest, nil),
			wantJSON: `{"key":"PROJ-7790","url":"https://jira.example.com/browse/PROJ-7790"}`,
		},
		{name: "без issue_key → IsError", args: map[string]any{"epic_link": "PROJ-7630"}, wantIsError: true},
		{name: "issue_key не по шаблону → IsError", args: withArg(moveToEpicArgs(), "issue_key", "10001"), wantIsError: true},
		{name: "только issue_key → IsError", args: map[string]any{"issue_key": updateIssueKey}, wantIsError: true},
		{name: "поле вне схемы → IsError", args: withArg(moveToEpicArgs(), "summary", "x"), wantIsError: true},
		{name: "epic_link числом → IsError", args: withArg(moveToEpicArgs(), "epic_link", 1), wantIsError: true},
		{name: "labels строкой → IsError", args: withArg(moveToEpicArgs(), "labels", "backend"), wantIsError: true},
		{name: "ошибка клиента → IsError", args: moveToEpicArgs(), expect: expectUpdate(moveToEpicRequest, jira.ErrUpdateUnconfirmed), wantIsError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			updater := mocks.NewMockUpdater(ctrl)
			if tt.expect != nil {
				tt.expect(updater)
			}
			clients := mockClients(ctrl)
			clients.Updater = updater
			session := connectMCP(t, tools.New(clients, withUpdateIssue(), unusedConfigPath))

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.UpdateIssueToolName, Arguments: tt.args})

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
