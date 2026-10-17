package gitlab_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-gitlab/gitlab"
	"scoped-mcp/mcp-gitlab/guard"
	"scoped-mcp/mcp-gitlab/guard/mocks"
)

const (
	gitlabBaseURL   = "https://gitlab.example.com"
	token           = "glpat-secret"
	project         = "group/ci-test"
	escapedProject  = "group%2Fci-test"
	mergeRequestURL = gitlabBaseURL + "/api/v4/projects/" + escapedProject + "/merge_requests/7"
	diffsURL        = mergeRequestURL + "/diffs"
	notesURL        = mergeRequestURL + "/notes"
	discussionsURL  = mergeRequestURL + "/discussions"
	draftNotesURL   = mergeRequestURL + "/draft_notes"
	discussionID    = "6a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3"
	replyURL        = discussionsURL + "/" + discussionID + "/notes"
	webURL          = "https://gitlab.example.com/group/ci-test/-/merge_requests/7"
	mainGoSHA1      = "93310a20c22640d80343161f98be28beb14eb4e9"
	mainGoDiff      = "@@ -1,2 +1,2 @@\n package main\n-import \"os\"\n+import \"fmt\"\n"
	noteText        = "Looks good"
	noteBody        = `{"body":"Looks good"}`

	maxResponseBytes      = 1 << 20
	maxDiffsResponseBytes = 32 << 20
	diffsPerPage          = 20
	maxDiffPages          = 150
)

var (
	ref      = gitlab.MergeRequestRef{Project: project, IID: 7}
	diffRefs = gitlab.DiffRefs{BaseSHA: strings.Repeat("1", 40), StartSHA: strings.Repeat("2", 40), HeadSHA: strings.Repeat("3", 40)}

	mergeRequestJSON = `{"id":1,"iid":7,"web_url":"` + webURL + `","diff_refs":{"base_sha":"` + diffRefs.BaseSHA + `","start_sha":"` + diffRefs.StartSHA + `","head_sha":"` + diffRefs.HeadSHA + `"}}`

	singleLinePosition     = gitlab.Position{DiffRefs: diffRefs, OldPath: "cmd/main.go", NewPath: "cmd/main.go", NewLine: 12}
	singleLinePositionJSON = `{"position_type":"text","base_sha":"` + diffRefs.BaseSHA + `","start_sha":"` + diffRefs.StartSHA + `","head_sha":"` + diffRefs.HeadSHA + `","old_path":"cmd/main.go","new_path":"cmd/main.go","new_line":12}`

	rangePosition = gitlab.Position{
		DiffRefs: diffRefs, OldPath: "cmd/main.go", NewPath: "cmd/main.go", OldLine: 4, NewLine: 3,
		LineRange: &gitlab.LineRange{
			Start: gitlab.LineRangeEnd{LineCode: mainGoSHA1 + "_1_1", Type: "old"},
			End:   gitlab.LineRangeEnd{LineCode: mainGoSHA1 + "_4_3", Type: "new"},
		},
	}
	rangePositionJSON = `{"position_type":"text","base_sha":"` + diffRefs.BaseSHA + `","start_sha":"` + diffRefs.StartSHA + `","head_sha":"` + diffRefs.HeadSHA + `","old_path":"cmd/main.go","new_path":"cmd/main.go","old_line":4,"new_line":3,` +
		`"line_range":{"start":{"line_code":"` + mainGoSHA1 + `_1_1","type":"old"},"end":{"line_code":"` + mainGoSHA1 + `_4_3","type":"new"}}}`
)

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func htmlResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func emptyResponse(statusCode int) *http.Response {
	return &http.Response{StatusCode: statusCode, Header: http.Header{}, Body: http.NoBody}
}

func unreadableResponse(statusCode int) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(iotest.ErrReader(errors.New("body read failed"))),
	}
}

func paddedTo(size int, body string) string {
	return strings.Repeat(" ", size-len(body)) + body
}

func redirectResponse(location string) *http.Response {
	response := emptyResponse(http.StatusFound)
	response.Header.Set("Location", location)
	return response
}

func newClient(t *testing.T, transport http.RoundTripper) *gitlab.Client {
	t.Helper()
	gitlabClient, err := gitlab.New(gitlabBaseURL, token, transport)
	require.NoError(t, err)
	return gitlabClient
}

func expectRequest(t *testing.T, mockTransport *mocks.MockRoundTripper, wantMethod, wantURL, wantBody string) *gomock.Call {
	t.Helper()
	return mockTransport.EXPECT().RoundTrip(gomock.Any()).Do(func(sent *http.Request) {
		require.Equal(t, wantMethod, sent.Method)
		require.Equal(t, wantURL, sent.URL.String())
		require.Equal(t, sent.URL.EscapedPath(), sent.URL.RawPath)
		require.Equal(t, token, sent.Header.Get("PRIVATE-TOKEN"))
		require.Equal(t, "application/json", sent.Header.Get("Accept"))
		require.Empty(t, sent.Header.Get("Authorization"))
		if wantBody == "" {
			require.True(t, sent.Body == nil || sent.Body == http.NoBody)
			require.Empty(t, sent.Header.Get("Content-Type"))
			return
		}
		require.Equal(t, "application/json", sent.Header.Get("Content-Type"))
		sentBody, err := io.ReadAll(sent.Body)
		require.NoError(t, err)
		require.JSONEq(t, wantBody, string(sentBody))
	})
}

func requireErrorMatchesAll(t *testing.T, wantErr, err error) {
	t.Helper()
	if wantJoined, ok := wantErr.(interface{ Unwrap() []error }); ok {
		for _, wantPart := range wantJoined.Unwrap() {
			requireErrorMatchesAll(t, wantPart, err)
		}
		return
	}
	var wantStatusErr *gitlab.StatusError
	if !errors.As(wantErr, &wantStatusErr) {
		require.ErrorIs(t, err, wantErr)
		return
	}
	var gotStatusErr *gitlab.StatusError
	require.ErrorAs(t, err, &gotStatusErr)
	require.Equal(t, wantStatusErr, gotStatusErr)
}

func TestMergeRequest(t *testing.T) {
	numericRef := gitlab.MergeRequestRef{Project: "42", IID: 7}
	tests := []struct {
		name     string
		ref      gitlab.MergeRequestRef
		wantURL  string
		response *http.Response
		want     gitlab.MergeRequest
		wantErr  error
	}{
		{name: "200 → web_url и diff_refs", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusOK, mergeRequestJSON), want: gitlab.MergeRequest{WebURL: webURL, DiffRefs: diffRefs}},
		{name: "числовой id проекта в пути", ref: numericRef, wantURL: gitlabBaseURL + "/api/v4/projects/42/merge_requests/7", response: jsonResponse(http.StatusOK, mergeRequestJSON), want: gitlab.MergeRequest{WebURL: webURL, DiffRefs: diffRefs}},
		{name: "200 без diff_refs → пустые DiffRefs", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusOK, `{"web_url":"`+webURL+`"}`), want: gitlab.MergeRequest{WebURL: webURL}},
		{name: "200 с diff_refs null → пустые DiffRefs", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusOK, `{"web_url":"`+webURL+`","diff_refs":null}`), want: gitlab.MergeRequest{WebURL: webURL}},

		{name: "200 ровно 1 MiB → читается", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusOK, paddedTo(maxResponseBytes, mergeRequestJSON)), want: gitlab.MergeRequest{WebURL: webURL, DiffRefs: diffRefs}},

		{name: "200 больше 1 MiB → ErrMergeRequestUnavailable и ErrResponseTooLarge", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusOK, paddedTo(maxResponseBytes+1, mergeRequestJSON)), wantErr: errors.Join(gitlab.ErrMergeRequestUnavailable, gitlab.ErrResponseTooLarge)},
		{name: "200 без web_url → ErrMergeRequestUnavailable", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusOK, `{"iid":7}`), wantErr: gitlab.ErrMergeRequestUnavailable},
		{name: "200 не JSON → ErrMergeRequestUnavailable", ref: ref, wantURL: mergeRequestURL, response: htmlResponse(http.StatusOK, `<html>login</html>`), wantErr: gitlab.ErrMergeRequestUnavailable},
		{name: "200, тело не читается → ErrMergeRequestUnavailable", ref: ref, wantURL: mergeRequestURL, response: unreadableResponse(http.StatusOK), wantErr: gitlab.ErrMergeRequestUnavailable},
		{name: "401 → ErrMergeRequestUnavailable и ErrUnauthorized", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusUnauthorized, `{"message":"401 Unauthorized"}`), wantErr: errors.Join(gitlab.ErrMergeRequestUnavailable, gitlab.ErrUnauthorized)},
		{name: "404 с message → ErrMergeRequestUnavailable и StatusError", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusNotFound, `{"message":"404 Project Not Found"}`), wantErr: errors.Join(gitlab.ErrMergeRequestUnavailable, &gitlab.StatusError{StatusCode: http.StatusNotFound, Message: "404 Project Not Found"})},
		{name: "403 с error → StatusError с текстом error", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusForbidden, `{"error":"insufficient_scope"}`), wantErr: errors.Join(gitlab.ErrMergeRequestUnavailable, &gitlab.StatusError{StatusCode: http.StatusForbidden, Message: "insufficient_scope"})},
		{name: "400 с message-объектом → сырой JSON в Message", ref: ref, wantURL: mergeRequestURL, response: jsonResponse(http.StatusBadRequest, `{"message":{"base":["is invalid"]}}`), wantErr: errors.Join(gitlab.ErrMergeRequestUnavailable, &gitlab.StatusError{StatusCode: http.StatusBadRequest, Message: `{"base":["is invalid"]}`})},
		{name: "500 с HTML → StatusError только со статусом", ref: ref, wantURL: mergeRequestURL, response: htmlResponse(http.StatusInternalServerError, `<html>oops</html>`), wantErr: errors.Join(gitlab.ErrMergeRequestUnavailable, &gitlab.StatusError{StatusCode: http.StatusInternalServerError})},
		{name: "редирект → StatusError 302, не следуем", ref: ref, wantURL: mergeRequestURL, response: redirectResponse("https://sso.example.com/"), wantErr: errors.Join(gitlab.ErrMergeRequestUnavailable, &gitlab.StatusError{StatusCode: http.StatusFound})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectRequest(t, mockTransport, http.MethodGet, tt.wantURL, "").Return(tt.response, nil)

			got, err := newClient(t, mockTransport).MergeRequest(t.Context(), tt.ref)

			require.Equal(t, tt.want, got)
			requireErrorMatchesAll(t, tt.wantErr, err)
			require.NotErrorIs(t, err, gitlab.ErrCommentUnconfirmed)
		})
	}
}

func TestMergeRequestTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{name: "сетевая ошибка → ErrMergeRequestUnavailable с причиной", transportErr: errNetwork, wantErr: gitlab.ErrMergeRequestUnavailable, wantNotErr: guard.ErrForbidden},
		{name: "таймаут → ErrMergeRequestUnavailable с причиной", transportErr: context.DeadlineExceeded, wantErr: gitlab.ErrMergeRequestUnavailable, wantNotErr: guard.ErrForbidden},
		{name: "отказ guard → проходит как есть", transportErr: guard.ErrForbidden, wantErr: guard.ErrForbidden, wantNotErr: gitlab.ErrMergeRequestUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			got, err := newClient(t, mockTransport).MergeRequest(t.Context(), ref)

			require.Equal(t, gitlab.MergeRequest{}, got)
			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
		})
	}
}

type diffEntryJSON struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	Diff        string `json:"diff"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
	Collapsed   bool   `json:"collapsed,omitempty"`
	TooLarge    bool   `json:"too_large,omitempty"`
}

var mainGoEntry = diffEntryJSON{OldPath: "cmd/main.go", NewPath: "cmd/main.go", Diff: mainGoDiff}

func fillerEntries(count int) []diffEntryJSON {
	entries := make([]diffEntryJSON, 0, count)
	for i := range count {
		path := fmt.Sprintf("pkg/file%d.go", i)
		entries = append(entries, diffEntryJSON{OldPath: path, NewPath: path, Diff: "@@ -1 +1 @@\n-a\n+b\n"})
	}
	return entries
}

func pageResponse(t *testing.T, entries []diffEntryJSON) *http.Response {
	t.Helper()
	body, err := json.Marshal(entries)
	require.NoError(t, err)
	return jsonResponse(http.StatusOK, string(body))
}

func fullPage(t *testing.T) *http.Response {
	t.Helper()
	return pageResponse(t, fillerEntries(diffsPerPage))
}

func diffsPageURL(page int) string {
	return fmt.Sprintf("%s?page=%d&per_page=%d", diffsURL, page, diffsPerPage)
}

func TestFileDiff(t *testing.T) {
	collapsedEntry := mainGoEntry
	collapsedEntry.Collapsed = true
	collapsedEntry.Diff = ""
	tooLargeEntry := mainGoEntry
	tooLargeEntry.TooLarge = true
	tooLargeEntry.Diff = ""
	renamedEntry := diffEntryJSON{OldPath: "cmd/main.go", NewPath: "cmd/app.go", Diff: mainGoDiff, RenamedFile: true}
	tests := []struct {
		name    string
		pages   []*http.Response
		want    string
		wantErr error
	}{
		{name: "файл на первой короткой странице", pages: []*http.Response{pageResponse(t, append(fillerEntries(2), mainGoEntry))}, want: mainGoDiff},
		{name: "файл на второй странице", pages: []*http.Response{fullPage(t), pageResponse(t, []diffEntryJSON{mainGoEntry})}, want: mainGoDiff},
		{name: "файл с пустым дифом (только режим)", pages: []*http.Response{pageResponse(t, []diffEntryJSON{{OldPath: "cmd/main.go", NewPath: "cmd/main.go"}})}, want: ""},

		{name: "файл не найден — короткая страница", pages: []*http.Response{pageResponse(t, fillerEntries(3))}, wantErr: gitlab.ErrDiffUnavailable},
		{name: "файл не найден — пустая страница после полной", pages: []*http.Response{fullPage(t), pageResponse(t, nil)}, wantErr: gitlab.ErrDiffUnavailable},
		{name: "совпадает только old_path → не найден", pages: []*http.Response{pageResponse(t, []diffEntryJSON{renamedEntry})}, wantErr: gitlab.ErrDiffUnavailable},
		{name: "collapsed", pages: []*http.Response{pageResponse(t, []diffEntryJSON{collapsedEntry})}, wantErr: gitlab.ErrDiffUnavailable},
		{name: "too_large", pages: []*http.Response{pageResponse(t, []diffEntryJSON{tooLargeEntry})}, wantErr: gitlab.ErrDiffUnavailable},
		{name: "401 → ErrDiffUnavailable и ErrUnauthorized", pages: []*http.Response{emptyResponse(http.StatusUnauthorized)}, wantErr: errors.Join(gitlab.ErrDiffUnavailable, gitlab.ErrUnauthorized)},
		{name: "404 → ErrDiffUnavailable и StatusError", pages: []*http.Response{jsonResponse(http.StatusNotFound, `{"message":"404 Not found"}`)}, wantErr: errors.Join(gitlab.ErrDiffUnavailable, &gitlab.StatusError{StatusCode: http.StatusNotFound, Message: "404 Not found"})},
		{name: "500 на второй странице → ErrDiffUnavailable и StatusError", pages: []*http.Response{fullPage(t), htmlResponse(http.StatusInternalServerError, `<html>oops</html>`)}, wantErr: errors.Join(gitlab.ErrDiffUnavailable, &gitlab.StatusError{StatusCode: http.StatusInternalServerError})},
		{name: "редирект → ErrDiffUnavailable и StatusError 302", pages: []*http.Response{redirectResponse("https://sso.example.com/")}, wantErr: errors.Join(gitlab.ErrDiffUnavailable, &gitlab.StatusError{StatusCode: http.StatusFound})},
		{name: "не JSON", pages: []*http.Response{htmlResponse(http.StatusOK, `<html>login</html>`)}, wantErr: gitlab.ErrDiffUnavailable},
		{name: "объект вместо массива", pages: []*http.Response{jsonResponse(http.StatusOK, `{"diff":"x"}`)}, wantErr: gitlab.ErrDiffUnavailable},
		{name: "страница больше 32 MiB → ErrDiffUnavailable и ErrResponseTooLarge", pages: []*http.Response{jsonResponse(http.StatusOK, paddedTo(maxDiffsResponseBytes+1, `[]`))}, wantErr: errors.Join(gitlab.ErrDiffUnavailable, gitlab.ErrResponseTooLarge)},
		{name: "тело не читается", pages: []*http.Response{unreadableResponse(http.StatusOK)}, wantErr: gitlab.ErrDiffUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			calls := make([]any, 0, len(tt.pages))
			for i, page := range tt.pages {
				calls = append(calls, expectRequest(t, mockTransport, http.MethodGet, diffsPageURL(i+1), "").Return(page, nil))
			}
			gomock.InOrder(calls...)

			got, err := newClient(t, mockTransport).FileDiff(t.Context(), ref, "cmd/main.go")

			require.Equal(t, tt.want, got)
			requireErrorMatchesAll(t, tt.wantErr, err)
			require.NotErrorIs(t, err, gitlab.ErrMergeRequestUnavailable)
			require.NotErrorIs(t, err, gitlab.ErrCommentUnconfirmed)
		})
	}
}

func TestFileDiffStopsAfterMaxPages(t *testing.T) {
	mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	page := 0
	mockTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
		page++
		require.Equal(t, diffsPageURL(page), sent.URL.String())
		return fullPage(t), nil
	}).Times(maxDiffPages)

	got, err := newClient(t, mockTransport).FileDiff(t.Context(), ref, "cmd/main.go")

	require.Empty(t, got)
	require.ErrorIs(t, err, gitlab.ErrDiffUnavailable)
}

func TestFileDiffTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{name: "сетевая ошибка → ErrDiffUnavailable с причиной", transportErr: errNetwork, wantErr: gitlab.ErrDiffUnavailable, wantNotErr: guard.ErrForbidden},
		{name: "отказ guard → проходит как есть", transportErr: guard.ErrForbidden, wantErr: guard.ErrForbidden, wantNotErr: gitlab.ErrDiffUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			got, err := newClient(t, mockTransport).FileDiff(t.Context(), ref, "cmd/main.go")

			require.Empty(t, got)
			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
		})
	}
}

func TestAddNote(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		want     gitlab.Note
		wantErr  error
	}{
		{name: "201 с id → Note", response: jsonResponse(http.StatusCreated, `{"id":101,"body":"Looks good","system":false}`), want: gitlab.Note{ID: 101}},
		{name: "200 с id → Note", response: jsonResponse(http.StatusOK, `{"id":102}`), want: gitlab.Note{ID: 102}},

		{name: "201 без id → ErrCommentUnconfirmed", response: jsonResponse(http.StatusCreated, `{"body":"Looks good"}`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "201 с id строкой → ErrCommentUnconfirmed", response: jsonResponse(http.StatusCreated, `{"id":"101"}`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "201 больше 1 MiB → ErrCommentUnconfirmed и ErrResponseTooLarge", response: jsonResponse(http.StatusCreated, paddedTo(maxResponseBytes+1, `{"id":101}`)), wantErr: errors.Join(gitlab.ErrCommentUnconfirmed, gitlab.ErrResponseTooLarge)},
		{name: "201 не JSON → ErrCommentUnconfirmed", response: htmlResponse(http.StatusCreated, `<html>created?</html>`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "201, тело не читается → ErrCommentUnconfirmed", response: unreadableResponse(http.StatusCreated), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "204 без тела → ErrCommentUnconfirmed", response: emptyResponse(http.StatusNoContent), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "401 → ErrUnauthorized", response: jsonResponse(http.StatusUnauthorized, `{"message":"401 Unauthorized"}`), wantErr: gitlab.ErrUnauthorized},
		{name: "400 с message → StatusError", response: jsonResponse(http.StatusBadRequest, `{"message":"400 Bad request - Note {:line_code=>[\"can't be blank\"]}"}`), wantErr: &gitlab.StatusError{StatusCode: http.StatusBadRequest, Message: `400 Bad request - Note {:line_code=>["can't be blank"]}`}},
		{name: "404 → StatusError", response: jsonResponse(http.StatusNotFound, `{"message":"404 Not found"}`), wantErr: &gitlab.StatusError{StatusCode: http.StatusNotFound, Message: "404 Not found"}},
		{name: "403 с error → StatusError", response: jsonResponse(http.StatusForbidden, `{"error":"insufficient_scope"}`), wantErr: &gitlab.StatusError{StatusCode: http.StatusForbidden, Message: "insufficient_scope"}},
		{name: "403 без тела → StatusError только со статусом", response: emptyResponse(http.StatusForbidden), wantErr: &gitlab.StatusError{StatusCode: http.StatusForbidden}},
		{name: "редирект → StatusError 302, не следуем", response: redirectResponse("https://sso.example.com/"), wantErr: &gitlab.StatusError{StatusCode: http.StatusFound}},
		{name: "500 с message → ErrCommentUnconfirmed и StatusError", response: jsonResponse(http.StatusInternalServerError, `{"message":"500 Internal Server Error"}`), wantErr: errors.Join(gitlab.ErrCommentUnconfirmed, &gitlab.StatusError{StatusCode: http.StatusInternalServerError, Message: "500 Internal Server Error"})},
		{name: "503 с HTML → ErrCommentUnconfirmed и StatusError", response: htmlResponse(http.StatusServiceUnavailable, `<html>maintenance</html>`), wantErr: errors.Join(gitlab.ErrCommentUnconfirmed, &gitlab.StatusError{StatusCode: http.StatusServiceUnavailable})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectRequest(t, mockTransport, http.MethodPost, notesURL, noteBody).Return(tt.response, nil)

			got, err := newClient(t, mockTransport).AddNote(t.Context(), ref, noteText)

			require.Equal(t, tt.want, got)
			requireErrorMatchesAll(t, tt.wantErr, err)
			require.NotErrorIs(t, err, gitlab.ErrMergeRequestUnavailable)
		})
	}
}

func TestAddReply(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		want     gitlab.Note
		wantErr  error
	}{
		{name: "201 с id → Note", response: jsonResponse(http.StatusCreated, `{"id":303}`), want: gitlab.Note{ID: 303}},
		{name: "201 без id → ErrCommentUnconfirmed", response: jsonResponse(http.StatusCreated, `{}`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "404 дискуссии нет → StatusError", response: jsonResponse(http.StatusNotFound, `{"message":"404 Discussion Not Found"}`), wantErr: &gitlab.StatusError{StatusCode: http.StatusNotFound, Message: "404 Discussion Not Found"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectRequest(t, mockTransport, http.MethodPost, replyURL, noteBody).Return(tt.response, nil)

			got, err := newClient(t, mockTransport).AddReply(t.Context(), ref, discussionID, noteText)

			require.Equal(t, tt.want, got)
			requireErrorMatchesAll(t, tt.wantErr, err)
		})
	}
}

func TestAddDiscussion(t *testing.T) {
	createdJSON := `{"id":"` + discussionID + `","individual_note":false,"notes":[{"id":202,"body":"Rename this"}]}`
	singleLineDiscussionBody := `{"body":"Rename this","position":` + singleLinePositionJSON + `}`
	rangeDiscussionBody := `{"body":"Rename this","position":` + rangePositionJSON + `}`
	tests := []struct {
		name     string
		position gitlab.Position
		wantBody string
		response *http.Response
		want     gitlab.Discussion
		wantErr  error
	}{
		{name: "одна строка → position без line_range", position: singleLinePosition, wantBody: singleLineDiscussionBody, response: jsonResponse(http.StatusCreated, createdJSON), want: gitlab.Discussion{ID: discussionID, NoteID: 202}},
		{name: "диапазон → position с line_range", position: rangePosition, wantBody: rangeDiscussionBody, response: jsonResponse(http.StatusCreated, createdJSON), want: gitlab.Discussion{ID: discussionID, NoteID: 202}},
		{name: "переименование → old_path как передан", position: gitlab.Position{DiffRefs: diffRefs, OldPath: "cmd/old.go", NewPath: "cmd/main.go", OldLine: 5}, wantBody: `{"body":"Rename this","position":{"position_type":"text","base_sha":"` + diffRefs.BaseSHA + `","start_sha":"` + diffRefs.StartSHA + `","head_sha":"` + diffRefs.HeadSHA + `","old_path":"cmd/old.go","new_path":"cmd/main.go","old_line":5}}`, response: jsonResponse(http.StatusCreated, createdJSON), want: gitlab.Discussion{ID: discussionID, NoteID: 202}},

		{name: "201 без notes → ErrCommentUnconfirmed", position: singleLinePosition, wantBody: singleLineDiscussionBody, response: jsonResponse(http.StatusCreated, `{"id":"`+discussionID+`","notes":[]}`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "201 без id дискуссии → ErrCommentUnconfirmed", position: singleLinePosition, wantBody: singleLineDiscussionBody, response: jsonResponse(http.StatusCreated, `{"notes":[{"id":202}]}`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "201 без id заметки → ErrCommentUnconfirmed", position: singleLinePosition, wantBody: singleLineDiscussionBody, response: jsonResponse(http.StatusCreated, `{"id":"`+discussionID+`","notes":[{"body":"x"}]}`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "400 строки нет в дифе → StatusError", position: singleLinePosition, wantBody: singleLineDiscussionBody, response: jsonResponse(http.StatusBadRequest, `{"message":"400 Bad request - Note {:line_code=>[\"can't be blank\", \"must be a valid line code\"]}"}`), wantErr: &gitlab.StatusError{StatusCode: http.StatusBadRequest, Message: `400 Bad request - Note {:line_code=>["can't be blank", "must be a valid line code"]}`}},
		{name: "500 → ErrCommentUnconfirmed и StatusError", position: singleLinePosition, wantBody: singleLineDiscussionBody, response: htmlResponse(http.StatusInternalServerError, `<html>oops</html>`), wantErr: errors.Join(gitlab.ErrCommentUnconfirmed, &gitlab.StatusError{StatusCode: http.StatusInternalServerError})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectRequest(t, mockTransport, http.MethodPost, discussionsURL, tt.wantBody).Return(tt.response, nil)

			got, err := newClient(t, mockTransport).AddDiscussion(t.Context(), ref, "Rename this", tt.position)

			require.Equal(t, tt.want, got)
			requireErrorMatchesAll(t, tt.wantErr, err)
		})
	}
}

func TestAddDraftNote(t *testing.T) {
	noteOnlyRequest := gitlab.DraftNoteRequest{Note: "Draft remark"}
	noteOnlyBody := `{"note":"Draft remark"}`
	tests := []struct {
		name     string
		request  gitlab.DraftNoteRequest
		wantBody string
		response *http.Response
		want     gitlab.Draft
		wantErr  error
	}{
		{name: "только note", request: noteOnlyRequest, wantBody: noteOnlyBody, response: jsonResponse(http.StatusCreated, `{"id":404,"note":"Draft remark"}`), want: gitlab.Draft{ID: 404}},
		{name: "note с position", request: gitlab.DraftNoteRequest{Note: "Draft remark", Position: &rangePosition}, wantBody: `{"note":"Draft remark","position":` + rangePositionJSON + `}`, response: jsonResponse(http.StatusCreated, `{"id":405}`), want: gitlab.Draft{ID: 405}},
		{name: "note с in_reply_to_discussion_id", request: gitlab.DraftNoteRequest{Note: "Draft remark", InReplyToDiscussionID: discussionID}, wantBody: `{"note":"Draft remark","in_reply_to_discussion_id":"` + discussionID + `"}`, response: jsonResponse(http.StatusCreated, `{"id":406}`), want: gitlab.Draft{ID: 406}},

		{name: "201 без id → ErrCommentUnconfirmed", request: noteOnlyRequest, wantBody: noteOnlyBody, response: jsonResponse(http.StatusCreated, `{"note":"Draft remark"}`), wantErr: gitlab.ErrCommentUnconfirmed},
		{name: "401 → ErrUnauthorized", request: noteOnlyRequest, wantBody: noteOnlyBody, response: emptyResponse(http.StatusUnauthorized), wantErr: gitlab.ErrUnauthorized},
		{name: "400 → StatusError", request: noteOnlyRequest, wantBody: noteOnlyBody, response: jsonResponse(http.StatusBadRequest, `{"message":"400 Bad request"}`), wantErr: &gitlab.StatusError{StatusCode: http.StatusBadRequest, Message: "400 Bad request"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectRequest(t, mockTransport, http.MethodPost, draftNotesURL, tt.wantBody).Return(tt.response, nil)

			got, err := newClient(t, mockTransport).AddDraftNote(t.Context(), ref, tt.request)

			require.Equal(t, tt.want, got)
			requireErrorMatchesAll(t, tt.wantErr, err)
		})
	}
}

func addNote(ctx context.Context, gitlabClient *gitlab.Client) error {
	_, err := gitlabClient.AddNote(ctx, ref, noteText)
	return err
}

func addDiscussion(ctx context.Context, gitlabClient *gitlab.Client) error {
	_, err := gitlabClient.AddDiscussion(ctx, ref, noteText, singleLinePosition)
	return err
}

func addReply(ctx context.Context, gitlabClient *gitlab.Client) error {
	_, err := gitlabClient.AddReply(ctx, ref, discussionID, noteText)
	return err
}

func addDraftNote(ctx context.Context, gitlabClient *gitlab.Client) error {
	_, err := gitlabClient.AddDraftNote(ctx, ref, gitlab.DraftNoteRequest{Note: noteText})
	return err
}

func TestWriteTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		send         func(ctx context.Context, gitlabClient *gitlab.Client) error
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{name: "AddNote: сетевая ошибка → ErrCommentUnconfirmed", send: addNote, transportErr: errNetwork, wantErr: gitlab.ErrCommentUnconfirmed, wantNotErr: guard.ErrForbidden},
		{name: "AddNote: таймаут → ErrCommentUnconfirmed", send: addNote, transportErr: context.DeadlineExceeded, wantErr: gitlab.ErrCommentUnconfirmed, wantNotErr: guard.ErrForbidden},
		{name: "AddNote: отказ guard → как есть", send: addNote, transportErr: guard.ErrForbidden, wantErr: guard.ErrForbidden, wantNotErr: gitlab.ErrCommentUnconfirmed},
		{name: "AddDiscussion: сетевая ошибка → ErrCommentUnconfirmed", send: addDiscussion, transportErr: errNetwork, wantErr: gitlab.ErrCommentUnconfirmed, wantNotErr: guard.ErrForbidden},
		{name: "AddDiscussion: отказ guard → как есть", send: addDiscussion, transportErr: guard.ErrForbidden, wantErr: guard.ErrForbidden, wantNotErr: gitlab.ErrCommentUnconfirmed},
		{name: "AddReply: сетевая ошибка → ErrCommentUnconfirmed", send: addReply, transportErr: errNetwork, wantErr: gitlab.ErrCommentUnconfirmed, wantNotErr: guard.ErrForbidden},
		{name: "AddDraftNote: сетевая ошибка → ErrCommentUnconfirmed", send: addDraftNote, transportErr: errNetwork, wantErr: gitlab.ErrCommentUnconfirmed, wantNotErr: guard.ErrForbidden},
		{name: "AddDraftNote: отказ guard → как есть", send: addDraftNote, transportErr: guard.ErrForbidden, wantErr: guard.ErrForbidden, wantNotErr: gitlab.ErrCommentUnconfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			err := tt.send(t.Context(), newClient(t, mockTransport))

			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
		})
	}
}

func TestDiffRefsComplete(t *testing.T) {
	tests := []struct {
		name     string
		diffRefs gitlab.DiffRefs
		want     bool
	}{
		{name: "все три SHA", diffRefs: diffRefs, want: true},
		{name: "пустые", diffRefs: gitlab.DiffRefs{}},
		{name: "без head_sha", diffRefs: gitlab.DiffRefs{BaseSHA: diffRefs.BaseSHA, StartSHA: diffRefs.StartSHA}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.diffRefs.Complete())
		})
	}
}

func TestStatusErrorText(t *testing.T) {
	tests := []struct {
		name string
		err  gitlab.StatusError
		want string
	}{
		{name: "статус и сообщение", err: gitlab.StatusError{StatusCode: http.StatusNotFound, Message: "404 Project Not Found"}, want: "gitlab: 404 Not Found: 404 Project Not Found"},
		{name: "только статус", err: gitlab.StatusError{StatusCode: http.StatusFound}, want: "gitlab: 302 Found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.err.Error())
		})
	}
}

func TestNewRejectsUnparsableBaseURL(t *testing.T) {
	_, err := gitlab.New("https://gitlab.example.com/%zz", token, mocks.NewMockRoundTripper(gomock.NewController(t)))

	var urlErr *url.Error
	require.ErrorAs(t, err, &urlErr)
}
