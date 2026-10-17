package tools_test

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/confluence/mocks"
	"scoped-mcp/mcp-confluence/tools"
)

const configPath = "/nonexistent/mcp-confluence/config.yaml"

var toolConfig = config.Config{
	BaseURL:     "https://confluence.example.com",
	ReadRoots:   []int64{100},
	WriteRoots:  []int64{200},
	Tools:       config.Tools{GetPage: true, ListChildren: true, Search: true, ListComments: true, ListAttachments: true, GetAttachment: true, CreatePage: true, UpdatePage: true, UploadAttachment: true},
	Attachments: &config.Attachments{DownloadDir: "/home/bot/out/confluence", UploadDirs: []string{"/home/bot/out"}, Extensions: []string{".md", ".PNG"}, MaxBytes: 1024},
	Search:      config.Search{MaxResults: 50},
}

func withTools(enabled config.Tools) config.Config {
	cfg := toolConfig
	cfg.Tools = enabled
	if !enabled.UsesAttachments() {
		cfg.Attachments = nil
	}
	return cfg
}

type clientMocks struct {
	pages       *mocks.MockPages
	comments    *mocks.MockComments
	attachments *mocks.MockAttachments
}

func newToolset(t *testing.T, cfg config.Config) (*tools.Toolset, clientMocks) {
	t.Helper()
	return newToolsetWithConfigPath(t, cfg, configPath)
}

func newToolsetWithConfigPath(t *testing.T, cfg config.Config, toolsetConfigPath string) (*tools.Toolset, clientMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	pagesMock, commentsMock, attachmentsMock := mocks.NewMockPages(ctrl), mocks.NewMockComments(ctrl), mocks.NewMockAttachments(ctrl)
	return tools.New(pagesMock, commentsMock, attachmentsMock, cfg, toolsetConfigPath), clientMocks{pages: pagesMock, comments: commentsMock, attachments: attachmentsMock}
}

func connectMCP(t *testing.T, toolset *tools.Toolset) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-confluence-test", Version: "test"}, nil)
	toolset.Register(server)
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func TestRegisterListsEnabledTools(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		wantToolNames []string
	}{
		{name: "все инструменты", cfg: toolConfig, wantToolNames: []string{"create_page", "get_attachment", "get_page", "list_attachments", "list_children", "list_comments", "search", "update_page", "upload_attachment"}},
		{name: "только get_page", cfg: withTools(config.Tools{GetPage: true}), wantToolNames: []string{"get_page"}},
		{name: "только запись", cfg: withTools(config.Tools{CreatePage: true, UpdatePage: true}), wantToolNames: []string{"create_page", "update_page"}},
		{name: "только вложения", cfg: withTools(config.Tools{ListAttachments: true, GetAttachment: true, UploadAttachment: true}), wantToolNames: []string{"get_attachment", "list_attachments", "upload_attachment"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, _ := newToolset(t, tt.cfg)

			listed, err := connectMCP(t, toolset).ListTools(t.Context(), nil)

			require.NoError(t, err)
			toolNames := make([]string, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				toolNames = append(toolNames, tool.Name)
			}
			require.Equal(t, tt.wantToolNames, toolNames)
		})
	}
}
