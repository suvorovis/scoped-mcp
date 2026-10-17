package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/mcp-gitlab/guard"
	"scoped-mcp/mcp-gitlab/guard/mocks"
)

func serveConfig(baseURL string, tools config.Tools) config.Config {
	return config.Config{BaseURL: baseURL, Tools: tools}
}

var allTools = config.Tools{AddMRNote: true, AddMRLineComment: true, ReplyToDiscussion: true, AddDraftNote: true}

func connectServer(t *testing.T, mcpServer *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := mcpServer.Connect(t.Context(), serverTransport, nil)
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
		tools         config.Tools
		wantToolNames []string
	}{
		{name: "только add_mr_note", tools: config.Tools{AddMRNote: true}, wantToolNames: []string{"add_mr_note"}},
		{name: "только add_draft_note", tools: config.Tools{AddDraftNote: true}, wantToolNames: []string{"add_draft_note"}},
		{name: "add_mr_line_comment и reply_to_discussion", tools: config.Tools{AddMRLineComment: true, ReplyToDiscussion: true}, wantToolNames: []string{"add_mr_line_comment", "reply_to_discussion"}},
		{name: "все четыре — по алфавиту", tools: allTools, wantToolNames: []string{"add_draft_note", "add_mr_line_comment", "add_mr_note", "reply_to_discussion"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mcpServer, err := newServer(serveConfig("https://gitlab.example.com", tt.tools), testToken, mocks.NewMockRoundTripper(gomock.NewController(t)))
			require.NoError(t, err)

			listed, err := connectServer(t, mcpServer).ListTools(t.Context(), nil)

			require.NoError(t, err)
			toolNames := make([]string, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				toolNames = append(toolNames, tool.Name)
			}
			require.Equal(t, tt.wantToolNames, toolNames)
		})
	}
}

const (
	testToken        = "glpat-secret"
	discussionID     = "6a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3"
	mainGoPathSHA1   = "93310a20c22640d80343161f98be28beb14eb4e9"
	mainGoDiff       = "@@ -1,2 +1,2 @@\n package main\n-import \"os\"\n+import \"fmt\"\n"
	baseSHA          = "1111111111111111111111111111111111111111"
	startSHA         = "2222222222222222222222222222222222222222"
	headSHA          = "3333333333333333333333333333333333333333"
	mergeRequestPath = "/api/v4/projects/group%2Fci-test/merge_requests/7"
	firstDiffsQuery  = "page=1&per_page=20"
)

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Token  string
	Body   string
}

type fakeGitLab struct {
	t           *testing.T
	httpsServer *httptest.Server
	mu          sync.Mutex
	requests    []recordedRequest
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	t.Helper()
	fake := &fakeGitLab{t: t}
	fake.httpsServer = httptest.NewTLSServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.httpsServer.Close)
	return fake
}

func (f *fakeGitLab) webURL() string {
	return f.httpsServer.URL + "/group/ci-test/-/merge_requests/7"
}

func (f *fakeGitLab) recordedRequests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *fakeGitLab) record(request recordedRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
}

func (f *fakeGitLab) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read request body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	path := r.URL.EscapedPath()
	f.record(recordedRequest{
		Method: r.Method,
		Path:   path,
		Query:  r.URL.RawQuery,
		Token:  r.Header.Get("PRIVATE-TOKEN"),
		Body:   string(body),
	})
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && path == mergeRequestPath:
		_, _ = io.WriteString(w, `{"iid":7,"web_url":"`+f.webURL()+`","diff_refs":{"base_sha":"`+baseSHA+`","start_sha":"`+startSHA+`","head_sha":"`+headSHA+`"}}`)
	case r.Method == http.MethodGet && path == mergeRequestPath+"/diffs":
		_, _ = io.WriteString(w, `[{"old_path":"cmd/main.go","new_path":"cmd/main.go","diff":`+strconv.Quote(mainGoDiff)+`,"new_file":false,"renamed_file":false,"deleted_file":false}]`)
	case r.Method == http.MethodPost && path == mergeRequestPath+"/notes":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":101}`)
	case r.Method == http.MethodPost && path == mergeRequestPath+"/discussions":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"`+discussionID+`","notes":[{"id":202}]}`)
	case r.Method == http.MethodPost && path == mergeRequestPath+"/discussions/"+discussionID+"/notes":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":303}`)
	case r.Method == http.MethodPost && path == mergeRequestPath+"/draft_notes":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":404}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"404 Not Found"}`)
	}
}

func positionJSON(lineFields string) string {
	return `{"position_type":"text","base_sha":"` + baseSHA + `","start_sha":"` + startSHA + `","head_sha":"` + headSHA + `","old_path":"cmd/main.go","new_path":"cmd/main.go",` + lineFields + `}`
}

func TestNewServerToolsSendExpectedRequests(t *testing.T) {
	tests := []struct {
		name         string
		tool         string
		args         map[string]any
		wantRequests []recordedRequest
		wantJSON     string
	}{
		{
			name: "add_mr_note",
			tool: "add_mr_note",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "body": "Looks good <ok> & \"done\""},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/notes", Token: testToken, Body: `{"body":"Looks good \u003cok\u003e \u0026 \"done\""}`},
			},
			wantJSON: `{"note_id":101,"url":"%s#note_101"}`,
		},
		{
			name: "add_mr_line_comment на добавленной строке — через diffs, номер как есть",
			tool: "add_mr_line_comment",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "body": "Rename this", "new_path": "cmd/main.go", "new_line": 2},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodGet, Path: mergeRequestPath + "/diffs", Query: firstDiffsQuery, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/discussions", Token: testToken, Body: `{"body":"Rename this","position":` + positionJSON(`"new_line":2`) + `}`},
			},
			wantJSON: `{"discussion_id":"` + discussionID + `","note_id":202,"url":"%s#note_202"}`,
		},
		{
			name: "add_mr_line_comment на неизменённой строке только с new_line — old_line из diffs",
			tool: "add_mr_line_comment",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "body": "Rename this", "new_path": "cmd/main.go", "new_line": 1},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodGet, Path: mergeRequestPath + "/diffs", Query: firstDiffsQuery, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/discussions", Token: testToken, Body: `{"body":"Rename this","position":` + positionJSON(`"old_line":1,"new_line":1`) + `}`},
			},
			wantJSON: `{"discussion_id":"` + discussionID + `","note_id":202,"url":"%s#note_202"}`,
		},
		{
			name: "add_mr_line_comment многострочный — через diffs",
			tool: "add_mr_line_comment",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "body": "Rename these", "new_path": "cmd/main.go", "start_old_line": 1, "start_new_line": 1, "new_line": 2},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodGet, Path: mergeRequestPath + "/diffs", Query: firstDiffsQuery, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/discussions", Token: testToken, Body: `{"body":"Rename these","position":` + positionJSON(`"new_line":2,"line_range":{"start":{"line_code":"`+mainGoPathSHA1+`_1_1","type":"old"},"end":{"line_code":"`+mainGoPathSHA1+`_3_2","type":"new"}}`) + `}`},
			},
			wantJSON: `{"discussion_id":"` + discussionID + `","note_id":202,"url":"%s#note_202"}`,
		},
		{
			name: "reply_to_discussion",
			tool: "reply_to_discussion",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "discussion_id": discussionID, "body": "Agreed"},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/discussions/" + discussionID + "/notes", Token: testToken, Body: `{"body":"Agreed"}`},
			},
			wantJSON: `{"note_id":303,"url":"%s#note_303"}`,
		},
		{
			name: "add_draft_note к удалённой строке — через diffs, номер как есть",
			tool: "add_draft_note",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "note": "Draft", "new_path": "cmd/main.go", "old_line": 2},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodGet, Path: mergeRequestPath + "/diffs", Query: firstDiffsQuery, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/draft_notes", Token: testToken, Body: `{"note":"Draft","position":` + positionJSON(`"old_line":2`) + `}`},
			},
			wantJSON: `{"draft_note_id":404,"url":"%s"}`,
		},
		{
			name: "add_draft_note общий",
			tool: "add_draft_note",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "note": "General draft"},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/draft_notes", Token: testToken, Body: `{"note":"General draft"}`},
			},
			wantJSON: `{"draft_note_id":404,"url":"%s"}`,
		},
		{
			name: "add_draft_note ответом в дискуссию",
			tool: "add_draft_note",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "note": "Draft reply", "in_reply_to_discussion_id": discussionID},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/draft_notes", Token: testToken, Body: `{"in_reply_to_discussion_id":"` + discussionID + `","note":"Draft reply"}`},
			},
			wantJSON: `{"draft_note_id":404,"url":"%s"}`,
		},
		{
			name: "add_draft_note многострочный — через diffs",
			tool: "add_draft_note",
			args: map[string]any{"project": "group/ci-test", "mr_iid": 7, "note": "Draft range", "new_path": "cmd/main.go", "start_old_line": 1, "start_new_line": 1, "new_line": 2},
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
				{Method: http.MethodGet, Path: mergeRequestPath + "/diffs", Query: firstDiffsQuery, Token: testToken},
				{Method: http.MethodPost, Path: mergeRequestPath + "/draft_notes", Token: testToken, Body: `{"note":"Draft range","position":` + positionJSON(`"new_line":2,"line_range":{"start":{"line_code":"`+mainGoPathSHA1+`_1_1","type":"old"},"end":{"line_code":"`+mainGoPathSHA1+`_3_2","type":"new"}}`) + `}`},
			},
			wantJSON: `{"draft_note_id":404,"url":"%s"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitLab(t)
			mcpServer, err := newServer(serveConfig(fake.httpsServer.URL, allTools), testToken, fake.httpsServer.Client().Transport)
			require.NoError(t, err)

			result, err := connectServer(t, mcpServer).CallTool(t.Context(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})

			require.NoError(t, err)
			require.False(t, result.IsError, result.Content)
			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.JSONEq(t, fmt.Sprintf(tt.wantJSON, fake.webURL()), string(structured))
			require.Equal(t, tt.wantRequests, fake.recordedRequests())
		})
	}
}

func TestNewServerRejectsToolCall(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantRequests []recordedRequest
	}{
		{
			name: "quick action отклоняется инструментом до любого запроса",
			body: "LGTM\n/merge",
		},
		{
			name: "тело больше лимита guard не уходит дальше чтения MR",
			body: strings.Repeat("a", guard.MaxBodyBytes),
			wantRequests: []recordedRequest{
				{Method: http.MethodGet, Path: mergeRequestPath, Token: testToken},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitLab(t)
			mcpServer, err := newServer(serveConfig(fake.httpsServer.URL, allTools), testToken, fake.httpsServer.Client().Transport)
			require.NoError(t, err)

			result, err := connectServer(t, mcpServer).CallTool(t.Context(), &mcp.CallToolParams{
				Name:      "add_mr_note",
				Arguments: map[string]any{"project": "group/ci-test", "mr_iid": 7, "body": tt.body},
			})

			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Equal(t, tt.wantRequests, fake.recordedRequests())
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
