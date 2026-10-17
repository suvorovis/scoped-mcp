package tools_test

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/jira"
	"scoped-mcp/mcp-jira/jira/mocks"
	"scoped-mcp/mcp-jira/tools"
)

const unusedConfigPath = "/nonexistent/mcp-jira/config.yaml"

var createdIssue = jira.Issue{Key: "PROJ-7790", URL: "https://jira.example.com/browse/PROJ-7790"}

func epicArgs() map[string]any {
	return map[string]any{
		"project": "PROJ", "issue_type": "Epic", "summary": "Прогрев образов",
		"priority": "High", "epic_name": "Прогрев", "labels": []any{"backend"}, "components": []any{"core", "api"},
	}
}

func storyArgs() map[string]any {
	return map[string]any{"project": "PROJ", "issue_type": "История", "summary": "s", "priority": "Low", "epic_link": "OTHER-5"}
}

func taskArgs() map[string]any {
	return map[string]any{"project": "OTHER", "issue_type": "Task", "summary": "s"}
}

func expectCreate(request jira.CreateRequest, err error) func(creator *mocks.MockCreator) {
	return func(creator *mocks.MockCreator) {
		creator.EXPECT().CreateIssue(gomock.Any(), request).Return(createdIssue, err)
	}
}

func TestCreateIssue(t *testing.T) {
	epicRequest := jira.CreateRequest{
		ProjectKey: "PROJ", IssueTypeID: "10000", Summary: "Прогрев образов", AssignToSelf: true,
		Fields: map[string]jira.FieldValue{
			"priority":          {Kind: config.KindName, Text: "High"},
			"customfield_10203": {Kind: config.KindText, Text: "Прогрев"},
			"labels":            {Kind: config.KindLabels, Items: []string{"backend"}},
			"components":        {Kind: config.KindNames, Items: []string{"core", "api"}},
		},
	}
	storyRequest := jira.CreateRequest{
		ProjectKey: "PROJ", IssueTypeID: "11203", Summary: "s", AssignToSelf: true,
		Fields: map[string]jira.FieldValue{
			"priority":          {Kind: config.KindName, Text: "Low"},
			"customfield_10201": {Kind: config.KindIssueKey, Text: "OTHER-5"},
		},
	}
	taskRequest := jira.CreateRequest{ProjectKey: "OTHER", IssueTypeID: "3", Summary: "s"}
	created := tools.CreateIssueOutput{Key: createdIssue.Key, URL: createdIssue.URL}
	tests := []struct {
		name    string
		args    map[string]any
		expect  func(creator *mocks.MockCreator)
		want    tools.CreateIssueOutput
		wantErr error
	}{
		{name: "Epic со всеми полями", args: epicArgs(), expect: expectCreate(epicRequest, nil), want: created},
		{name: "История с epic_link другого разрешённого проекта", args: storyArgs(), expect: expectCreate(storyRequest, nil), want: created},
		{name: "проект без assign_to_self — без назначения", args: taskArgs(), expect: expectCreate(taskRequest, nil), want: created},
		{name: "ошибка клиента пробрасывается", args: epicArgs(), expect: expectCreate(epicRequest, jira.ErrUnauthorized), wantErr: jira.ErrUnauthorized},

		{name: "чужой проект", args: withArg(epicArgs(), "project", "SECRET"), wantErr: tools.ErrProjectNotAllowed},
		{name: "без project", args: withoutArg(epicArgs(), "project"), wantErr: tools.ErrFieldRequired},
		{name: "project не строка", args: withArg(epicArgs(), "project", 1), wantErr: tools.ErrFieldValue},
		{name: "тип другого проекта", args: withArg(epicArgs(), "issue_type", "Task"), wantErr: tools.ErrIssueTypeNotAllowed},
		{name: "неизвестный тип", args: withArg(epicArgs(), "issue_type", "Bug"), wantErr: tools.ErrIssueTypeNotAllowed},
		{name: "issue_type не строка", args: withArg(epicArgs(), "issue_type", 1), wantErr: tools.ErrFieldValue},
		{name: "пустой summary", args: withArg(epicArgs(), "summary", ""), wantErr: tools.ErrFieldRequired},
		{name: "summary из пробелов", args: withArg(epicArgs(), "summary", "  "), wantErr: tools.ErrFieldRequired},
		{name: "без summary", args: withoutArg(epicArgs(), "summary"), wantErr: tools.ErrFieldRequired},
		{name: "summary не строка", args: withArg(epicArgs(), "summary", 1), wantErr: tools.ErrFieldValue},
		{name: "поле, не разрешённое типу", args: withArg(epicArgs(), "epic_link", "PROJ-1"), wantErr: tools.ErrFieldNotAllowed},
		{name: "неизвестное поле", args: withArg(epicArgs(), "sprint", "x"), wantErr: tools.ErrFieldNotAllowed},
		{name: "неразрешённое поле проверяется раньше формы значений", args: withArg(withArg(epicArgs(), "labels", "backend"), "epic_link", "PROJ-1"), wantErr: tools.ErrFieldNotAllowed},
		{name: "значения проверяются в порядке полей типа", args: withArg(withArg(storyArgs(), "labels", "backend"), "epic_link", "epic"), wantErr: tools.ErrFieldValue},
		{name: "нет обязательного priority", args: withoutArg(epicArgs(), "priority"), wantErr: tools.ErrFieldRequired},
		{name: "обязательное поле пустой строкой", args: withArg(epicArgs(), "epic_name", ""), wantErr: tools.ErrFieldRequired},
		{name: "обязательный список пустой", args: withArg(epicArgs(), "components", []any{}), wantErr: tools.ErrFieldRequired},
		{name: "обязательный список из пустой строки", args: withArg(epicArgs(), "components", []any{""}), wantErr: tools.ErrFieldRequired},
		{name: "обязательный список из пробелов", args: withArg(epicArgs(), "components", []any{"  "}), wantErr: tools.ErrFieldRequired},
		{name: "labels строкой", args: withArg(epicArgs(), "labels", "backend"), wantErr: tools.ErrFieldValue},
		{name: "число внутри labels", args: withArg(epicArgs(), "labels", []any{"a", 1}), wantErr: tools.ErrFieldValue},
		{name: "priority массивом", args: withArg(epicArgs(), "priority", []any{"High"}), wantErr: tools.ErrFieldValue},
		{name: "epic_link не ключ задачи", args: withArg(storyArgs(), "epic_link", "epic"), wantErr: tools.ErrIssueKeyInvalid},
		{name: "epic_link чужого проекта", args: withArg(storyArgs(), "epic_link", "SECRET-1"), wantErr: tools.ErrIssueKeyInvalid},
		{name: "epic_link строчными", args: withArg(storyArgs(), "epic_link", "proj-1"), wantErr: tools.ErrIssueKeyInvalid},
		{name: "epic_link с нулевым номером", args: withArg(storyArgs(), "epic_link", "PROJ-0"), wantErr: tools.ErrIssueKeyInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			creator := mocks.NewMockCreator(ctrl)
			if tt.expect != nil {
				tt.expect(creator)
			}

			clients := mockClients(ctrl)
			clients.Creator = creator

			got, err := tools.New(clients, toolConfig, unusedConfigPath).CreateIssue(t.Context(), tt.args)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func mockClients(ctrl *gomock.Controller) tools.Clients {
	return tools.Clients{
		Creator:   mocks.NewMockCreator(ctrl),
		Attacher:  mocks.NewMockAttacher(ctrl),
		Commenter: mocks.NewMockCommenter(ctrl),
		Linker:    mocks.NewMockLinker(ctrl),
		Updater:   mocks.NewMockUpdater(ctrl),
	}
}

func withArg(args map[string]any, key string, value any) map[string]any {
	changed := maps.Clone(args)
	changed[key] = value
	return changed
}

func withoutArg(args map[string]any, key string) map[string]any {
	changed := maps.Clone(args)
	delete(changed, key)
	return changed
}

func connectMCP(t *testing.T, toolset *tools.Toolset) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-jira-test", Version: "test"}, nil)
	toolset.Register(server)
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func allowedAttachments() *config.Attachments {
	return &config.Attachments{Dirs: []string{"/home/bot/out", "/var/reports"}, Extensions: []string{".md", ".PNG"}, MaxBytes: 10485760}
}

func withAttachFiles() config.Config {
	cfg := toolConfig
	cfg.Tools.AttachFiles = true
	cfg.Attachments = allowedAttachments()
	return cfg
}

func withAddComment() config.Config {
	cfg := toolConfig
	cfg.Tools.AddComment = true
	return cfg
}

func withCreateIssueDisabled() config.Config {
	cfg := withAttachFiles()
	cfg.Tools.CreateIssue = false
	return cfg
}

func withAttachFilesDisabled() config.Config {
	cfg := withAttachFiles()
	cfg.Tools.AttachFiles = false
	return cfg
}

func withOnlyAddCommentEnabled() config.Config {
	cfg := withAddComment()
	cfg.Tools.CreateIssue = false
	return cfg
}

var allowedIssueLinks = []config.IssueLink{
	{Relation: "blocks", Type: "Blocks", Direction: config.LinkOutward},
	{Relation: "is blocked by", Type: "Blocks", Direction: config.LinkInward},
	{Relation: "relates to", Type: "Relates", Direction: config.LinkOutward},
}

func withLinkIssues() config.Config {
	cfg := toolConfig
	cfg.Tools.LinkIssues = true
	cfg.IssueLinks = allowedIssueLinks
	return cfg
}

func withOnlyLinkIssuesEnabled() config.Config {
	cfg := withLinkIssues()
	cfg.Tools.CreateIssue = false
	return cfg
}

func withUpdateIssue() config.Config {
	cfg := toolConfig
	cfg.Tools.UpdateIssue = true
	cfg.Projects = slices.Clone(toolConfig.Projects)
	cfg.Projects[0].UpdateFields = []string{"epic_link", "labels"}
	cfg.Projects[1].UpdateFields = []string{"priority"}
	return cfg
}

func withOnlyUpdateIssueEnabled() config.Config {
	cfg := withUpdateIssue()
	cfg.Tools.CreateIssue = false
	return cfg
}

func withAllTools() config.Config {
	cfg := withUpdateIssue()
	cfg.Tools.AttachFiles = true
	cfg.Attachments = allowedAttachments()
	cfg.Tools.AddComment = true
	cfg.Tools.LinkIssues = true
	cfg.IssueLinks = allowedIssueLinks
	return cfg
}

func TestRegisterListsToolsWithConfigSchemas(t *testing.T) {
	createIssueTool := &mcp.Tool{
		Name:        tools.CreateIssueToolName,
		Description: tools.CreateIssueDescription(toolConfig),
		InputSchema: tools.CreateIssueInputSchema(toolConfig),
	}
	attachFilesTool := &mcp.Tool{
		Name:        tools.AttachFilesToolName,
		Description: tools.AttachFilesDescription(withAttachFiles()),
		InputSchema: tools.AttachFilesInputSchema(),
	}
	addCommentTool := &mcp.Tool{
		Name:        tools.AddCommentToolName,
		Description: tools.AddCommentDescription(withAddComment()),
		InputSchema: tools.AddCommentInputSchema(),
	}
	linkIssuesTool := &mcp.Tool{
		Name:        tools.LinkIssuesToolName,
		Description: tools.LinkIssuesDescription(withLinkIssues()),
		InputSchema: tools.LinkIssuesInputSchema(withLinkIssues()),
	}
	updateIssueTool := &mcp.Tool{
		Name:        tools.UpdateIssueToolName,
		Description: tools.UpdateIssueDescription(withUpdateIssue()),
		InputSchema: tools.UpdateIssueInputSchema(withUpdateIssue()),
	}
	tests := []struct {
		name      string
		cfg       config.Config
		wantTools []*mcp.Tool
	}{
		{name: "включён только create_issue", cfg: toolConfig, wantTools: []*mcp.Tool{createIssueTool}},
		{name: "включены create_issue и attach_files", cfg: withAttachFiles(), wantTools: []*mcp.Tool{attachFilesTool, createIssueTool}},
		{name: "включён только attach_files", cfg: withCreateIssueDisabled(), wantTools: []*mcp.Tool{attachFilesTool}},
		{name: "раздел attachments без tools.attach_files — только create_issue", cfg: withAttachFilesDisabled(), wantTools: []*mcp.Tool{createIssueTool}},
		{name: "включены create_issue и add_comment", cfg: withAddComment(), wantTools: []*mcp.Tool{addCommentTool, createIssueTool}},
		{name: "включён только add_comment", cfg: withOnlyAddCommentEnabled(), wantTools: []*mcp.Tool{addCommentTool}},
		{name: "включены create_issue и link_issues", cfg: withLinkIssues(), wantTools: []*mcp.Tool{createIssueTool, linkIssuesTool}},
		{name: "включён только link_issues", cfg: withOnlyLinkIssuesEnabled(), wantTools: []*mcp.Tool{linkIssuesTool}},
		{name: "включены create_issue и update_issue", cfg: withUpdateIssue(), wantTools: []*mcp.Tool{createIssueTool, updateIssueTool}},
		{name: "включён только update_issue", cfg: withOnlyUpdateIssueEnabled(), wantTools: []*mcp.Tool{updateIssueTool}},
		{name: "включены все пять инструментов", cfg: withAllTools(), wantTools: []*mcp.Tool{addCommentTool, attachFilesTool, createIssueTool, linkIssuesTool, updateIssueTool}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			session := connectMCP(t, tools.New(mockClients(ctrl), tt.cfg, unusedConfigPath))

			listed, err := session.ListTools(t.Context(), nil)
			require.NoError(t, err)

			require.Len(t, listed.Tools, len(tt.wantTools))
			for i, wantTool := range tt.wantTools {
				tool := listed.Tools[i]
				require.Equal(t, wantTool.Name, tool.Name)
				require.Equal(t, wantTool.Description, tool.Description)
				gotSchema, err := json.Marshal(tool.InputSchema)
				require.NoError(t, err)
				wantSchema, err := json.Marshal(wantTool.InputSchema)
				require.NoError(t, err)
				require.JSONEq(t, string(wantSchema), string(gotSchema))
			}
		})
	}
}

func TestCreateIssueOverMCP(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]any
		expect      func(creator *mocks.MockCreator)
		wantJSON    string
		wantIsError bool
	}{
		{
			name: "успех → key и url",
			args: epicArgs(),
			expect: func(creator *mocks.MockCreator) {
				creator.EXPECT().CreateIssue(gomock.Any(), gomock.Any()).Return(createdIssue, nil)
			},
			wantJSON: `{"key":"PROJ-7790","url":"https://jira.example.com/browse/PROJ-7790"}`,
		},
		{name: "проект вне enum → IsError", args: withArg(epicArgs(), "project", "SECRET"), wantIsError: true},
		{name: "лишний параметр → IsError", args: withArg(epicArgs(), "sprint", "x"), wantIsError: true},
		{name: "labels строкой → IsError", args: withArg(epicArgs(), "labels", "backend"), wantIsError: true},
		{name: "без summary → IsError", args: withoutArg(epicArgs(), "summary"), wantIsError: true},
		{name: "тип другого проекта → IsError", args: withArg(epicArgs(), "issue_type", "Task"), wantIsError: true},
		{
			name: "ошибка клиента → IsError",
			args: epicArgs(),
			expect: func(creator *mocks.MockCreator) {
				creator.EXPECT().CreateIssue(gomock.Any(), gomock.Any()).Return(jira.Issue{}, jira.ErrUnauthorized)
			},
			wantIsError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			creator := mocks.NewMockCreator(ctrl)
			if tt.expect != nil {
				tt.expect(creator)
			}
			clients := mockClients(ctrl)
			clients.Creator = creator
			session := connectMCP(t, tools.New(clients, toolConfig, unusedConfigPath))

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.CreateIssueToolName, Arguments: tt.args})

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
