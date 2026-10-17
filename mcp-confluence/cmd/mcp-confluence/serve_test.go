package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	guardmocks "scoped-mcp/mcp-confluence/guard/mocks"
)

const (
	unusedConfigPath = "/nonexistent/mcp-confluence/config.yaml"
	callToolTimeout  = 5 * time.Second
)

var errStopAfterForward = errors.New("stop after forward")

var serveConfig = config.Config{
	BaseURL:     "https://confluence.example.com",
	SecretsMode: config.SecretsModeEnv,
	ReadRoots:   []int64{100},
	WriteRoots:  []int64{200},
	Tools:       config.Tools{GetPage: true, UpdatePage: true},
	Search:      config.Search{MaxResults: 25},
}

func withTools(enabled config.Tools) config.Config {
	cfg := serveConfig
	cfg.Tools = enabled
	if enabled.UsesAttachments() {
		cfg.Attachments = &config.Attachments{DownloadDir: "/home/bot/out/confluence", UploadDirs: []string{"/home/bot/out"}, Extensions: []string{".md"}, MaxBytes: 1024}
	}
	return cfg
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
		{name: "get_page и update_page", cfg: serveConfig, wantToolNames: []string{"get_page", "update_page"}},
		{name: "только search", cfg: withTools(config.Tools{Search: true}), wantToolNames: []string{"search"}},
		{name: "вложения", cfg: withTools(config.Tools{ListAttachments: true, GetAttachment: true, UploadAttachment: true}), wantToolNames: []string{"get_attachment", "list_attachments", "upload_attachment"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport, receivedRequests := stubConfluence(t)
			server, err := newServer(tt.cfg, unusedConfigPath, "pat-secret", nextTransport, io.Discard)
			require.NoError(t, err)

			listed, err := connectServer(t, server).ListTools(t.Context(), nil)

			require.NoError(t, err)
			require.Empty(t, receivedRequests())
			toolNames := make([]string, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				toolNames = append(toolNames, tool.Name)
			}
			require.Equal(t, tt.wantToolNames, toolNames)
		})
	}
}

func TestNewServerRejectsConfigPathNotAbsolute(t *testing.T) {
	tests := []struct {
		name       string
		configPath string
	}{
		{name: "пустой путь", configPath: ""},
		{name: "относительный путь", configPath: "config.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newServer(serveConfig, tt.configPath, "pat-secret", guardmocks.NewMockRoundTripper(gomock.NewController(t)), io.Discard)

			require.ErrorIs(t, err, errConfigPathNotAbsolute)
		})
	}
}

func stubConfluence(t *testing.T, responses ...*http.Response) (*guardmocks.MockRoundTripper, func() []*http.Request) {
	t.Helper()
	var mutex sync.Mutex
	var received []*http.Request
	nextTransport := guardmocks.NewMockRoundTripper(gomock.NewController(t))
	nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
		mutex.Lock()
		defer mutex.Unlock()
		received = append(received, sent)
		if len(received) > len(responses) {
			return nil, errStopAfterForward
		}
		return responses[len(received)-1], nil
	}).AnyTimes()
	receivedRequests := func() []*http.Request {
		mutex.Lock()
		defer mutex.Unlock()
		return slices.Clone(received)
	}
	return nextTransport, receivedRequests
}

func requestURLs(requests []*http.Request) []string {
	urls := make([]string, 0, len(requests))
	for _, request := range requests {
		urls = append(urls, request.Method+" "+request.URL.String())
	}
	return urls
}

func callTool(t *testing.T, server *mcp.Server, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callToolTimeout)
	defer cancel()
	result, err := connectServer(t, server).CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	return result
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{StatusCode: statusCode, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestNewServerGetPagePassesGuard(t *testing.T) {
	nextTransport, receivedRequests := stubConfluence(t,
		jsonResponse(http.StatusOK, `{"id":"101","type":"page","status":"current","space":{"key":"DOCS"},"ancestors":[{"id":"1"},{"id":"100"}]}`),
		jsonResponse(http.StatusOK, `{"id":"101","title":"Child","version":{"number":4},"ancestors":[{"id":"1"},{"id":"100"}],"space":{"key":"DOCS"},"body":{"storage":{"value":"<p>x</p>"}},"_links":{"webui":"/display/DOCS/Child"}}`),
	)
	var logs bytes.Buffer
	server, err := newServer(serveConfig, unusedConfigPath, "pat-secret", nextTransport, &logs)
	require.NoError(t, err)

	result := callTool(t, server, "get_page", map[string]any{"page_id": "101"})

	received := receivedRequests()
	require.Equal(t, []string{
		"GET https://confluence.example.com/rest/api/content/101?expand=ancestors,space,container",
		"GET https://confluence.example.com/rest/api/content/101?expand=body.storage%2Cversion%2Cancestors%2Cspace",
	}, requestURLs(received))
	require.Equal(t, "Bearer pat-secret", received[0].Header.Get("Authorization"))
	require.False(t, result.IsError)
	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"101","title":"Child","version":4,"parent_id":"100","webui":"https://confluence.example.com/display/DOCS/Child","body":"<p>x</p>"}`, string(structured))
	require.Empty(t, logs.String())
}

func TestNewServerUpdatePageOutsideZoneIsNotFound(t *testing.T) {
	nextTransport, receivedRequests := stubConfluence(t,
		jsonResponse(http.StatusOK, `{"id":"101","type":"page","status":"current","space":{"key":"DOCS"},"ancestors":[{"id":"1"},{"id":"100"}]}`),
	)
	var logs bytes.Buffer
	server, err := newServer(serveConfig, unusedConfigPath, "pat-secret", nextTransport, &logs)
	require.NoError(t, err)

	result := callTool(t, server, "update_page", map[string]any{"page_id": "101", "version": 4, "title": "Child", "body": "<p>y</p>"})

	require.Equal(t, []string{"GET https://confluence.example.com/rest/api/content/101?expand=ancestors,space,container"}, requestURLs(receivedRequests()))
	require.True(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "page not found", text.Text)
	require.NotContains(t, logs.String(), "DOCS")
}

func TestNewServerUploadSendsOnlyNonConfigFiles(t *testing.T) {
	tests := []struct {
		name                  string
		uploadedFile          string
		wantReachedConfluence bool
	}{
		{name: "файл конфига не загружается и в Confluence не уходит", uploadedFile: "config.yaml", wantReachedConfluence: false},
		{name: "соседний файл из upload_dirs уходит в Confluence", uploadedFile: "notes.yaml", wantReachedConfluence: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uploadDir := t.TempDir()
			configPath := filepath.Join(uploadDir, "config.yaml")
			for _, fileName := range []string{"config.yaml", "notes.yaml"} {
				require.NoError(t, os.WriteFile(filepath.Join(uploadDir, fileName), []byte("base_url: x\n"), 0o600))
			}
			cfg := withTools(config.Tools{UploadAttachment: true})
			cfg.Attachments = &config.Attachments{DownloadDir: t.TempDir(), UploadDirs: []string{uploadDir}, Extensions: []string{".yaml"}, MaxBytes: 1024}
			nextTransport, receivedRequests := stubConfluence(t)
			server, err := newServer(cfg, configPath, "pat-secret", nextTransport, io.Discard)
			require.NoError(t, err)

			result := callTool(t, server, "upload_attachment", map[string]any{"page_id": "201", "path": filepath.Join(uploadDir, tt.uploadedFile)})

			require.True(t, result.IsError)
			reachedConfluence := len(receivedRequests()) > 0
			require.Equal(t, tt.wantReachedConfluence, reachedConfluence)
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
