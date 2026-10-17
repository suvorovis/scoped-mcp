package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/guard/mocks"
)

const unusedConfigPath = "/nonexistent/mcp-jira/config.yaml"

var serveConfig = config.Config{
	BaseURL:  "https://jira.example.com",
	Tools:    config.Tools{CreateIssue: true},
	Projects: []config.Project{{Key: "PROJ", IssueTypes: []config.IssueType{{Name: "Task", ID: "3"}}}},
}

func withAttachFiles(dir string) config.Config {
	cfg := serveConfig
	cfg.Tools.AttachFiles = true
	cfg.Attachments = &config.Attachments{Dirs: []string{dir}, Extensions: []string{".md"}, MaxBytes: 1024}
	return cfg
}

func withAddComment() config.Config {
	cfg := serveConfig
	cfg.Tools.AddComment = true
	return cfg
}

func withLinkIssues() config.Config {
	cfg := serveConfig
	cfg.Tools.LinkIssues = true
	cfg.IssueLinks = []config.IssueLink{{Relation: "blocks", Type: "Blocks", Direction: config.LinkOutward}}
	return cfg
}

func withUpdateIssueOnly() config.Config {
	cfg := withoutCreateIssue(serveConfig)
	cfg.Tools.UpdateIssue = true
	cfg.Fields = map[string]config.Field{"epic_link": {Jira: "customfield_10201", Kind: config.KindIssueKey}}
	cfg.Projects = []config.Project{{Key: "PROJ", UpdateFields: []string{"epic_link"}}}
	return cfg
}

func withoutCreateIssue(cfg config.Config) config.Config {
	cfg.Tools.CreateIssue = false
	cfg.Projects = []config.Project{{Key: "PROJ"}}
	return cfg
}

func withAttachFilesOnly(dir string) config.Config {
	return withoutCreateIssue(withAttachFiles(dir))
}

func withAddCommentOnly() config.Config {
	return withoutCreateIssue(withAddComment())
}

func withLinkIssuesOnly() config.Config {
	return withoutCreateIssue(withLinkIssues())
}

func connectServer(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func TestNewServerListsTools(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		wantToolNames []string
	}{
		{name: "включён только create_issue", cfg: serveConfig, wantToolNames: []string{"create_issue"}},
		{name: "включены create_issue и attach_files", cfg: withAttachFiles("/home/bot/out"), wantToolNames: []string{"attach_files", "create_issue"}},
		{name: "включён только attach_files", cfg: withAttachFilesOnly("/home/bot/out"), wantToolNames: []string{"attach_files"}},
		{name: "включены create_issue и add_comment", cfg: withAddComment(), wantToolNames: []string{"add_comment", "create_issue"}},
		{name: "включён только add_comment", cfg: withAddCommentOnly(), wantToolNames: []string{"add_comment"}},
		{name: "включены create_issue и link_issues", cfg: withLinkIssues(), wantToolNames: []string{"create_issue", "link_issues"}},
		{name: "включён только link_issues", cfg: withLinkIssuesOnly(), wantToolNames: []string{"link_issues"}},
		{name: "включён только update_issue", cfg: withUpdateIssueOnly(), wantToolNames: []string{"update_issue"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := newServer(tt.cfg, unusedConfigPath, "pat-secret", mocks.NewMockRoundTripper(gomock.NewController(t)))
			require.NoError(t, err)

			listed, err := connectServer(t, server).ListTools(t.Context(), nil)

			require.NoError(t, err)
			toolNames := make([]string, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				toolNames = append(toolNames, tool.Name)
			}
			require.Equal(t, tt.wantToolNames, toolNames)
		})
	}
}

func TestNewServerAttachFilesPassesGuard(t *testing.T) {
	outDir := t.TempDir()
	reportPath := filepath.Join(outDir, "отчёт.md")
	require.NoError(t, os.WriteFile(reportPath, []byte("# Отчёт"), 0o600))
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
		require.Equal(t, "https://jira.example.com/rest/api/2/issue/PROJ-1/attachments", sent.URL.String())
		_, params, err := mime.ParseMediaType(sent.Header.Get("Content-Type"))
		require.NoError(t, err)
		part, err := multipart.NewReader(sent.Body, params["boundary"]).NextRawPart()
		require.NoError(t, err)
		require.Equal(t, "отчёт.md", part.FileName())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`[{"id":"10501","filename":"отчёт.md"}]`)),
		}, nil
	})
	server, err := newServer(withAttachFiles(outDir), unusedConfigPath, "pat-secret", nextTransport)
	require.NoError(t, err)

	result, err := connectServer(t, server).CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "attach_files",
		Arguments: map[string]any{"issue_key": "PROJ-1", "paths": []any{reportPath}},
	})

	require.NoError(t, err)
	require.False(t, result.IsError)
	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"issue_key":"PROJ-1","url":"https://jira.example.com/browse/PROJ-1","attached":[{"file":"отчёт.md","id":"10501"}]}`, string(structured))
}

func TestNewServerAddCommentPassesGuard(t *testing.T) {
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, sent.Method)
		require.Equal(t, "https://jira.example.com/rest/api/2/issue/PROJ-1/comment", sent.URL.String())
		sentBody, err := io.ReadAll(sent.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"body":"h3. Итог <ok> & \"done\""}`, string(sentBody))
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"10100","body":"h3. Итог"}`)),
		}, nil
	})
	server, err := newServer(withAddCommentOnly(), unusedConfigPath, "pat-secret", nextTransport)
	require.NoError(t, err)

	result, err := connectServer(t, server).CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "add_comment",
		Arguments: map[string]any{"issue_key": "PROJ-1", "body": `h3. Итог <ok> & "done"`},
	})

	require.NoError(t, err)
	require.False(t, result.IsError)
	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"issue_key":"PROJ-1","id":"10100","url":"https://jira.example.com/browse/PROJ-1?focusedCommentId=10100#comment-10100"}`, string(structured))
}

func TestNewServerLinkIssuesPassesGuard(t *testing.T) {
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, sent.Method)
		require.Equal(t, "https://jira.example.com/rest/api/2/issueLink", sent.URL.String())
		sentBody, err := io.ReadAll(sent.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"type":{"name":"Blocks"},"inwardIssue":{"key":"PROJ-1"},"outwardIssue":{"key":"PROJ-2"}}`, string(sentBody))
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: http.NoBody}, nil
	})
	server, err := newServer(withLinkIssuesOnly(), unusedConfigPath, "pat-secret", nextTransport)
	require.NoError(t, err)

	result, err := connectServer(t, server).CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "link_issues",
		Arguments: map[string]any{"issue_key": "PROJ-1", "relation": "blocks", "target_key": "PROJ-2"},
	})

	require.NoError(t, err)
	require.False(t, result.IsError)
	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"issue_key":"PROJ-1","relation":"blocks","target_key":"PROJ-2","url":"https://jira.example.com/browse/PROJ-1"}`, string(structured))
}

func TestNewServerUpdateIssuePassesGuard(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		wantBody string
	}{
		{name: "перенос в эпик", args: map[string]any{"issue_key": "PROJ-1", "epic_link": "PROJ-2"}, wantBody: `{"fields":{"customfield_10201":"PROJ-2"}}`},
		{name: "удаление из эпика", args: map[string]any{"issue_key": "PROJ-1", "epic_link": nil}, wantBody: `{"fields":{"customfield_10201":null}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPut, sent.Method)
				require.Equal(t, "https://jira.example.com/rest/api/2/issue/PROJ-1", sent.URL.String())
				sentBody, err := io.ReadAll(sent.Body)
				require.NoError(t, err)
				require.JSONEq(t, tt.wantBody, string(sentBody))
				return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: http.NoBody}, nil
			})
			server, err := newServer(withUpdateIssueOnly(), unusedConfigPath, "pat-secret", nextTransport)
			require.NoError(t, err)

			result, err := connectServer(t, server).CallTool(t.Context(), &mcp.CallToolParams{Name: "update_issue", Arguments: tt.args})

			require.NoError(t, err)
			require.False(t, result.IsError)
			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.JSONEq(t, `{"key":"PROJ-1","url":"https://jira.example.com/browse/PROJ-1"}`, string(structured))
		})
	}
}

func TestIgnoreCancellation(t *testing.T) {
	errOther := errors.New("other")
	tests := []struct {
		name    string
		err     error
		wantErr error
	}{
		{name: "без ошибки"},
		{name: "остановка сигналом", err: context.Canceled},
		{name: "обёрнутая остановка сигналом", err: fmt.Errorf("serve: %w", context.Canceled)},
		{name: "прочая ошибка", err: errOther, wantErr: errOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ignoreCancellation(tt.err)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
