package guard_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/mcp-gitlab/guard"
	"scoped-mcp/mcp-gitlab/guard/mocks"
)

const (
	gitlabBaseURL    = "https://gitlab.example.com"
	apiURL           = gitlabBaseURL + "/api/v4"
	mergeRequestURL  = apiURL + "/projects/42/merge_requests/7"
	pathProjectMRURL = apiURL + "/projects/group%2Fci-test/merge_requests/7"
	diffsURL         = mergeRequestURL + "/diffs"
	notesURL         = mergeRequestURL + "/notes"
	discussionsURL   = mergeRequestURL + "/discussions"
	draftNotesURL    = mergeRequestURL + "/draft_notes"
	discussionID     = "6a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3"
	replyURL         = discussionsURL + "/" + discussionID + "/notes"

	noteBody = `{"body":"Looks good to me"}`

	tokenHeader       = "PRIVATE-TOKEN"
	acceptHeader      = "Accept"
	contentTypeHeader = "Content-Type"
	jsonContentType   = "application/json"
)

var allToolsConfig = config.Config{
	BaseURL: gitlabBaseURL,
	Tools:   config.Tools{AddMRNote: true, AddMRLineComment: true, ReplyToDiscussion: true, AddDraftNote: true},
}

func withTools(tools config.Tools) config.Config {
	cfg := allToolsConfig
	cfg.Tools = tools
	return cfg
}

var (
	onlyNoteConfig        = withTools(config.Tools{AddMRNote: true})
	onlyReplyConfig       = withTools(config.Tools{ReplyToDiscussion: true})
	onlyLineCommentConfig = withTools(config.Tools{AddMRLineComment: true})
	onlyDraftConfig       = withTools(config.Tools{AddDraftNote: true})
	noToolsConfig         = withTools(config.Tools{})
)

const (
	mainGoPath       = "cmd/main.go"
	mainGoPathSHA1   = "93310a20c22640d80343161f98be28beb14eb4e9"
	otherPathSHA1    = "8ec9a00bfd09b3190ac6b22251dbb1aa95a0579d"
	cyrillicPath     = "docs/отчёт v2.md"
	cyrillicPathSHA1 = "afbc273c75a9ef555710673e6bcb0b3892ae5df9"
	discussionText   = "Rename this"
	draftText        = "Draft remark"
	validLinesJSON   = `,"new_line":12`
)

var (
	baseSHA  = strings.Repeat("1", 40)
	startSHA = strings.Repeat("2", 40)
	headSHA  = strings.Repeat("3", 40)

	validPositionJSON = positionWith(func(*positionFields) {})
	validRangeStart   = rangeEndpoint(mainGoPathSHA1+"_1_1", "old")
	validRangeEnd     = rangeEndpoint(mainGoPathSHA1+"_4_3", "new")

	discussionBody = discussionWith(func(*positionFields) {})
	draftBody      = `{"note":` + jsonString(draftText) + `}`
)

type positionFields struct {
	positionType    string
	baseSHA         string
	startSHA        string
	headSHA         string
	oldPath         string
	newPath         string
	extraFieldsJSON string
}

func validPositionFields() positionFields {
	return positionFields{
		positionType:    "text",
		baseSHA:         baseSHA,
		startSHA:        startSHA,
		headSHA:         headSHA,
		oldPath:         mainGoPath,
		newPath:         mainGoPath,
		extraFieldsJSON: validLinesJSON,
	}
}

func (p positionFields) encodeNonEmpty() string {
	var fields []string
	add := func(key, value string) {
		if value != "" {
			fields = append(fields, jsonString(key)+":"+jsonString(value))
		}
	}
	add("position_type", p.positionType)
	add("base_sha", p.baseSHA)
	add("start_sha", p.startSHA)
	add("head_sha", p.headSHA)
	add("old_path", p.oldPath)
	add("new_path", p.newPath)
	return "{" + strings.Join(fields, ",") + p.extraFieldsJSON + "}"
}

func positionWith(mutate func(p *positionFields)) string {
	position := validPositionFields()
	mutate(&position)
	return position.encodeNonEmpty()
}

func discussionWith(mutate func(p *positionFields)) string {
	return `{"body":` + jsonString(discussionText) + `,"position":` + positionWith(mutate) + `}`
}

func draftWith(mutate func(p *positionFields)) string {
	return `{"note":` + jsonString(draftText) + `,"position":` + positionWith(mutate) + `}`
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func lineRangeJSON(start, end string) string {
	return `,"line_range":{"start":` + start + `,"end":` + end + `}`
}

func rangeEndpoint(lineCode, lineType string) string {
	return `{"line_code":"` + lineCode + `","type":"` + lineType + `"}`
}

func newRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, url, bodyReader)
	require.NoError(t, err)
	if body != "" {
		request.Header.Set(contentTypeHeader, jsonContentType)
	}
	return request
}

func TestRoundTripTarget(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		hostHeader  string
		body        string
		wantAllowed bool
	}{
		{name: "GET MR по числовому id", cfg: allToolsConfig, method: http.MethodGet, url: mergeRequestURL, wantAllowed: true},
		{name: "GET MR по пути проекта", cfg: allToolsConfig, method: http.MethodGet, url: pathProjectMRURL, wantAllowed: true},
		{name: "GET MR, %2f строчными", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group%2fci-test/merge_requests/7", wantAllowed: true},
		{name: "GET MR при одном add_mr_note", cfg: onlyNoteConfig, method: http.MethodGet, url: mergeRequestURL, wantAllowed: true},
		{name: "GET MR при одном reply_to_discussion", cfg: onlyReplyConfig, method: http.MethodGet, url: mergeRequestURL, wantAllowed: true},
		{name: "GET MR, заголовок Host совпадает с base_url", cfg: allToolsConfig, method: http.MethodGet, url: mergeRequestURL, hostHeader: "gitlab.example.com", wantAllowed: true},
		{name: "GET diffs с page и per_page", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL + "?page=2&per_page=20", wantAllowed: true},
		{name: "GET diffs без query", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL, wantAllowed: true},
		{name: "GET diffs при одном add_mr_line_comment", cfg: onlyLineCommentConfig, method: http.MethodGet, url: diffsURL + "?page=1&per_page=20", wantAllowed: true},
		{name: "GET diffs при одном add_draft_note", cfg: onlyDraftConfig, method: http.MethodGet, url: diffsURL + "?page=1&per_page=20", wantAllowed: true},
		{name: "POST note", cfg: allToolsConfig, method: http.MethodPost, url: notesURL, body: noteBody, wantAllowed: true},
		{name: "POST note по пути проекта", cfg: allToolsConfig, method: http.MethodPost, url: pathProjectMRURL + "/notes", body: noteBody, wantAllowed: true},
		{name: "POST note при одном add_mr_note", cfg: onlyNoteConfig, method: http.MethodPost, url: notesURL, body: noteBody, wantAllowed: true},
		{name: "POST reply", cfg: allToolsConfig, method: http.MethodPost, url: replyURL, body: noteBody, wantAllowed: true},
		{name: "POST reply при одном reply_to_discussion", cfg: onlyReplyConfig, method: http.MethodPost, url: replyURL, body: noteBody, wantAllowed: true},
		{name: "POST discussion", cfg: allToolsConfig, method: http.MethodPost, url: discussionsURL, body: discussionBody, wantAllowed: true},
		{name: "POST discussion по пути проекта", cfg: allToolsConfig, method: http.MethodPost, url: pathProjectMRURL + "/discussions", body: discussionBody, wantAllowed: true},
		{name: "POST discussion при одном add_mr_line_comment", cfg: onlyLineCommentConfig, method: http.MethodPost, url: discussionsURL, body: discussionBody, wantAllowed: true},
		{name: "POST draft note", cfg: allToolsConfig, method: http.MethodPost, url: draftNotesURL, body: draftBody, wantAllowed: true},
		{name: "POST draft note при одном add_draft_note", cfg: onlyDraftConfig, method: http.MethodPost, url: draftNotesURL, body: draftBody, wantAllowed: true},

		{name: "GET MR без единого флага tools", cfg: noToolsConfig, method: http.MethodGet, url: mergeRequestURL},
		{name: "GET MR с телом", cfg: allToolsConfig, method: http.MethodGet, url: mergeRequestURL, body: `{}`},
		{name: "GET MR с query", cfg: allToolsConfig, method: http.MethodGet, url: mergeRequestURL + "?include_diverged_commits_count=true"},
		{name: "GET MR с пустым query и знаком вопроса", cfg: allToolsConfig, method: http.MethodGet, url: mergeRequestURL + "?"},
		{name: "GET diffs при одном add_mr_note", cfg: onlyNoteConfig, method: http.MethodGet, url: diffsURL},
		{name: "GET diffs при одном reply_to_discussion", cfg: onlyReplyConfig, method: http.MethodGet, url: diffsURL},
		{name: "GET diffs с unidiff", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL + "?page=1&unidiff=true"},
		{name: "GET diffs с page=0", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL + "?page=0"},
		{name: "GET diffs с нечисловым page", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL + "?page=abc"},
		{name: "GET diffs с отрицательным per_page", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL + "?per_page=-1"},
		{name: "GET diffs с повтором page", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL + "?page=1&page=2"},
		{name: "GET diffs с page без значения", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL + "?page"},
		{name: "GET diffs с телом", cfg: allToolsConfig, method: http.MethodGet, url: diffsURL, body: `{}`},
		{name: "POST на MR", cfg: allToolsConfig, method: http.MethodPost, url: mergeRequestURL, body: noteBody},
		{name: "PUT MR", cfg: allToolsConfig, method: http.MethodPut, url: mergeRequestURL, body: `{"title":"x"}`},
		{name: "DELETE MR", cfg: allToolsConfig, method: http.MethodDelete, url: mergeRequestURL},
		{name: "GET списка notes", cfg: allToolsConfig, method: http.MethodGet, url: notesURL},
		{name: "POST на конкретную note", cfg: allToolsConfig, method: http.MethodPost, url: notesURL + "/5", body: noteBody},
		{name: "PUT конкретной note", cfg: allToolsConfig, method: http.MethodPut, url: notesURL + "/5", body: noteBody},
		{name: "DELETE конкретной note", cfg: allToolsConfig, method: http.MethodDelete, url: notesURL + "/5"},
		{name: "POST note при выключенном add_mr_note", cfg: onlyReplyConfig, method: http.MethodPost, url: notesURL, body: noteBody},
		{name: "POST note с query", cfg: allToolsConfig, method: http.MethodPost, url: notesURL + "?internal=true", body: noteBody},
		{name: "POST note с завершающим слэшем", cfg: allToolsConfig, method: http.MethodPost, url: notesURL + "/", body: noteBody},
		{name: "POST reply при выключенном reply_to_discussion", cfg: onlyNoteConfig, method: http.MethodPost, url: replyURL, body: noteBody},
		{name: "GET reply", cfg: allToolsConfig, method: http.MethodGet, url: replyURL},
		{name: "POST reply с discussion_id в верхнем регистре", cfg: allToolsConfig, method: http.MethodPost, url: discussionsURL + "/" + strings.ToUpper(discussionID) + "/notes", body: noteBody},
		{name: "POST reply с discussion_id из 39 символов", cfg: allToolsConfig, method: http.MethodPost, url: discussionsURL + "/" + discussionID[:39] + "/notes", body: noteBody},
		{name: "PUT на дискуссию (resolve)", cfg: allToolsConfig, method: http.MethodPut, url: discussionsURL + "/" + discussionID + "?resolved=true"},
		{name: "POST bulk_publish", cfg: allToolsConfig, method: http.MethodPost, url: draftNotesURL + "/bulk_publish"},
		{name: "PUT publish одного черновика", cfg: allToolsConfig, method: http.MethodPut, url: draftNotesURL + "/5/publish"},
		{name: "DELETE черновика", cfg: allToolsConfig, method: http.MethodDelete, url: draftNotesURL + "/5"},
		{name: "GET черновиков", cfg: allToolsConfig, method: http.MethodGet, url: draftNotesURL},
		{name: "POST approve", cfg: allToolsConfig, method: http.MethodPost, url: mergeRequestURL + "/approve"},
		{name: "PUT merge", cfg: allToolsConfig, method: http.MethodPut, url: mergeRequestURL + "/merge"},
		{name: "POST award_emoji", cfg: allToolsConfig, method: http.MethodPost, url: mergeRequestURL + "/award_emoji", body: `{"name":"thumbsup"}`},
		{name: "POST graphql", cfg: allToolsConfig, method: http.MethodPost, url: gitlabBaseURL + "/api/graphql", body: `{"query":"{ currentUser { id } }"}`},
		{name: "GET проекта", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42"},
		{name: "GET списка MR", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42/merge_requests"},
		{name: "GET MR без iid", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42/merge_requests/"},
		{name: "iid 0", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42/merge_requests/0"},
		{name: "iid с ведущим нулём", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42/merge_requests/07"},
		{name: "iid не число", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42/merge_requests/abc"},
		{name: "iid отрицательный", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42/merge_requests/-1"},
		{name: "проект из одного сегмента", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group/merge_requests/7"},
		{name: "проект с сегментом .", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group%2F.%2Fci-test/merge_requests/7"},
		{name: "проект с сегментом ..", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group%2F..%2Fci-test/merge_requests/7"},
		{name: "проект с завершающим %2F", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group%2Fci-test%2F/merge_requests/7"},
		{name: "проект с литеральным / вместо %2F", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group/ci-test/merge_requests/7"},
		{name: "проект с двойным кодированием", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group%252Fci-test/merge_requests/7"},
		{name: "проект с сегментом, начинающимся с дефиса", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group%2F-ci-test/merge_requests/7"},
		{name: "проект с пробелом", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/group%2Fci%20test/merge_requests/7"},
		{name: "пустой проект", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects//merge_requests/7"},
		{name: "сегмент .. после iid", cfg: allToolsConfig, method: http.MethodPost, url: mergeRequestURL + "/../8/notes", body: noteBody},
		{name: "другой префикс API", cfg: allToolsConfig, method: http.MethodGet, url: gitlabBaseURL + "/api/v3/projects/42/merge_requests/7"},
		{name: "другой хост", cfg: allToolsConfig, method: http.MethodGet, url: "https://evil.example.com/api/v4/projects/42/merge_requests/7"},
		{name: "заголовок Host другого хоста", cfg: allToolsConfig, method: http.MethodGet, url: mergeRequestURL, hostHeader: "evil.example.com"},
		{name: "схема http", cfg: allToolsConfig, method: http.MethodGet, url: "http://gitlab.example.com/api/v4/projects/42/merge_requests/7"},
		{name: "userinfo в URL", cfg: allToolsConfig, method: http.MethodGet, url: "https://user:secret@gitlab.example.com/api/v4/projects/42/merge_requests/7"},
		{name: "сегмент notes в percent-кодировке", cfg: allToolsConfig, method: http.MethodPost, url: mergeRequestURL + "/%6Eotes", body: noteBody},
		{name: "merge_requests с закодированным подчёркиванием", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/42/merge%5Frequests/7"},
		{name: "числовой id проекта в percent-кодировке", cfg: allToolsConfig, method: http.MethodGet, url: apiURL + "/projects/%342/merge_requests/7"},
		{name: "метод post строчными", cfg: allToolsConfig, method: "post", url: notesURL, body: noteBody},
		{name: "POST note с пустым query и знаком вопроса", cfg: allToolsConfig, method: http.MethodPost, url: notesURL + "?", body: noteBody},
		{name: "POST discussion при выключенном add_mr_line_comment", cfg: onlyDraftConfig, method: http.MethodPost, url: discussionsURL, body: discussionBody},
		{name: "POST draft note при выключенном add_draft_note", cfg: onlyLineCommentConfig, method: http.MethodPost, url: draftNotesURL, body: draftBody},
		{name: "GET discussions", cfg: allToolsConfig, method: http.MethodGet, url: discussionsURL},
		{name: "POST на конкретную дискуссию", cfg: allToolsConfig, method: http.MethodPost, url: discussionsURL + "/" + discussionID, body: discussionBody},
		{name: "POST discussion с query", cfg: allToolsConfig, method: http.MethodPost, url: discussionsURL + "?x=1", body: discussionBody},
		{name: "POST draft note с query", cfg: allToolsConfig, method: http.MethodPost, url: draftNotesURL + "?x=1", body: draftBody},
		{name: "тело note на маршруте discussions", cfg: allToolsConfig, method: http.MethodPost, url: discussionsURL, body: noteBody},
		{name: "тело discussion на маршруте notes", cfg: allToolsConfig, method: http.MethodPost, url: notesURL, body: discussionBody},
		{name: "тело note на маршруте draft_notes", cfg: allToolsConfig, method: http.MethodPost, url: draftNotesURL, body: noteBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.method, tt.url, tt.body)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request := newRequest(t, tt.method, tt.url, tt.body)
			if tt.hostHeader != "" {
				request.Host = tt.hostHeader
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripRejectsRawPathOrOpaqueDivergingFromPath(t *testing.T) {
	tests := []struct {
		name    string
		rewrite func(requestURL *url.URL)
	}{
		{name: "RawPath не кодирует Path", rewrite: func(requestURL *url.URL) {
			requestURL.RawPath = "/api/v4/projects/99/merge_requests/7"
		}},
		{name: "Opaque с другим путём", rewrite: func(requestURL *url.URL) {
			requestURL.Opaque = "//gitlab.example.com/api/v4/projects/42/merge_requests/7/approve"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			guardTransport, err := guard.New(allToolsConfig, nextTransport)
			require.NoError(t, err)
			request := newRequest(t, http.MethodGet, mergeRequestURL, "")
			tt.rewrite(request.URL)

			response, err := guardTransport.RoundTrip(request)

			requireForbidden(t, response, err)
		})
	}
}

func TestRoundTripRejectsTrailerOrTransferEncoding(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		url     string
		body    string
		rewrite func(request *http.Request)
	}{
		{name: "GET с трейлером", method: http.MethodGet, url: mergeRequestURL, rewrite: func(request *http.Request) {
			request.Trailer = http.Header{"Sudo": {"root"}}
		}},
		{name: "POST с трейлером", method: http.MethodPost, url: notesURL, body: noteBody, rewrite: func(request *http.Request) {
			request.Trailer = http.Header{"X-Http-Method-Override": {http.MethodPut}}
		}},
		{name: "GET с Transfer-Encoding chunked", method: http.MethodGet, url: mergeRequestURL, rewrite: func(request *http.Request) {
			request.TransferEncoding = []string{"chunked"}
		}},
		{name: "POST с Transfer-Encoding chunked", method: http.MethodPost, url: notesURL, body: noteBody, rewrite: func(request *http.Request) {
			request.TransferEncoding = []string{"chunked"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			guardTransport, err := guard.New(allToolsConfig, nextTransport)
			require.NoError(t, err)
			request := newRequest(t, tt.method, tt.url, tt.body)
			tt.rewrite(request)

			response, err := guardTransport.RoundTrip(request)

			requireForbidden(t, response, err)
		})
	}
}

func TestRoundTripHeaders(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		url         string
		body        string
		header      http.Header
		wantAllowed bool
	}{
		{name: "POST с application/json", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}}, wantAllowed: true},
		{name: "POST с charset=utf-8", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {"application/json; charset=utf-8"}}, wantAllowed: true},
		{name: "POST с charset=UTF-8 и типом в другом регистре", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {"Application/JSON; Charset=UTF-8"}}, wantAllowed: true},
		{name: "GET без заголовков", method: http.MethodGet, url: mergeRequestURL, header: http.Header{}, wantAllowed: true},
		{name: "GET с заголовками клиента", method: http.MethodGet, url: mergeRequestURL, header: http.Header{tokenHeader: {"glpat-secret"}, acceptHeader: {jsonContentType}}, wantAllowed: true},
		{name: "POST с заголовками клиента", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{tokenHeader: {"glpat-secret"}, acceptHeader: {jsonContentType}, contentTypeHeader: {jsonContentType}}, wantAllowed: true},
		{name: "POST с заголовками клиента строчными ключами", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{"private-token": {"glpat-secret"}, "accept": {jsonContentType}, "content-type": {jsonContentType}}, wantAllowed: true},

		{name: "POST с application/json без тела", method: http.MethodPost, url: notesURL, header: http.Header{contentTypeHeader: {jsonContentType}}},
		{name: "POST без Content-Type", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{}},
		{name: "POST с form Content-Type", method: http.MethodPost, url: notesURL, body: `{"body":"x&body=%2Fmerge&y="}`, header: http.Header{contentTypeHeader: {"application/x-www-form-urlencoded"}}},
		{name: "POST с text/plain", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {"text/plain"}}},
		{name: "POST с application/json и другим параметром", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {"application/json; boundary=x"}}},
		{name: "POST с application/json и charset=utf-16", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {"application/json; charset=utf-16"}}},
		{name: "POST с неразборчивым Content-Type", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {"application/json; charset"}}},
		{name: "POST с двумя Content-Type", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType, jsonContentType}}},
		{name: "POST с Content-Type под ключами в разном регистре", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "content-type": {"application/x-www-form-urlencoded"}}},
		{name: "GET с X-HTTP-Method-Override", method: http.MethodGet, url: mergeRequestURL, header: http.Header{"X-Http-Method-Override": {http.MethodPut}}},
		{name: "GET с X-HTTP-Method", method: http.MethodGet, url: mergeRequestURL, header: http.Header{"X-Http-Method": {http.MethodPut}}},
		{name: "GET с X-Method-Override", method: http.MethodGet, url: mergeRequestURL, header: http.Header{"X-Method-Override": {http.MethodPut}}},
		{name: "POST с X-HTTP-Method-Override", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "X-Http-Method-Override": {http.MethodPut}}},
		{name: "POST с X-HTTP-Method", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "X-Http-Method": {http.MethodPut}}},
		{name: "POST с X-Method-Override", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "X-Method-Override": {http.MethodPut}}},
		{name: "POST с x-http-method-override строчным ключом", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "x-http-method-override": {http.MethodPut}}},
		{name: "GET с Sudo", method: http.MethodGet, url: mergeRequestURL, header: http.Header{"Sudo": {"root"}}},
		{name: "POST с Sudo", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "Sudo": {"root"}}},
		{name: "POST с sudo строчным ключом", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "sudo": {"root"}}},
		{name: "POST с Content-Encoding", method: http.MethodPost, url: notesURL, body: noteBody, header: http.Header{contentTypeHeader: {jsonContentType}, "Content-Encoding": {"gzip"}}},
		{name: "GET с X-Forwarded-For", method: http.MethodGet, url: mergeRequestURL, header: http.Header{"X-Forwarded-For": {"127.0.0.1"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.method, tt.url, tt.body)
			}
			guardTransport, err := guard.New(allToolsConfig, nextTransport)
			require.NoError(t, err)
			request := newRequest(t, tt.method, tt.url, tt.body)
			request.Header = tt.header

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripTextBody(t *testing.T) {
	oversized := `{"body":"` + strings.Repeat("x", guard.MaxBodyBytes) + `"}`
	tests := []struct {
		name        string
		url         string
		body        string
		wantAllowed bool
	}{
		{name: "текст в Markdown", url: notesURL, body: noteBody, wantAllowed: true},
		{name: "многострочный текст со списком", url: notesURL, body: `{"body":"Summary:\n- rename x\n- drop y"}`, wantAllowed: true},
		{name: "слэш в середине строки проходит", url: notesURL, body: `{"body":"see cmd/main.go and a/b"}`, wantAllowed: true},
		{name: "команда в обратных кавычках проходит", url: notesURL, body: "{\"body\":\"run `/merge` manually\"}", wantAllowed: true},
		{name: "пробелы по краям текста проходят", url: notesURL, body: `{"body":"  ok  "}`, wantAllowed: true},
		{name: "CRLF без quick action проходит", url: notesURL, body: `{"body":"line one\r\nline two"}`, wantAllowed: true},
		{name: "ключи с пробелами вокруг", url: notesURL, body: `{ "body" : "ok" }`, wantAllowed: true},
		{name: "ответ в дискуссию с тем же правилом", url: replyURL, body: noteBody, wantAllowed: true},

		{name: "без тела", url: notesURL},
		{name: "невалидный JSON", url: notesURL, body: `{"body":`},
		{name: "тело — массив", url: notesURL, body: `[{"body":"x"}]`},
		{name: "тело — строка", url: notesURL, body: `"x"`},
		{name: "мусор после JSON", url: notesURL, body: noteBody + `{"internal":true}`},
		{name: "превышение размера", url: notesURL, body: oversized},
		{name: "пустой объект", url: notesURL, body: `{}`},
		{name: "internal рядом с body", url: notesURL, body: `{"body":"x","internal":true}`},
		{name: "resolve_discussion рядом с body", url: notesURL, body: `{"body":"x","resolve_discussion":true}`},
		{name: "merge_request_diff_head_sha рядом с body", url: notesURL, body: `{"body":"x","merge_request_diff_head_sha":"` + strings.Repeat("a", 40) + `"}`},
		{name: "commit_id рядом с body", url: notesURL, body: `{"body":"x","commit_id":"` + strings.Repeat("a", 40) + `"}`},
		{name: "created_at рядом с body", url: notesURL, body: `{"body":"x","created_at":"2026-01-01T00:00:00Z"}`},
		{name: "position рядом с body на маршруте notes", url: notesURL, body: `{"body":"x","position":{}}`},
		{name: "note вместо body", url: notesURL, body: `{"note":"x"}`},
		{name: "без body", url: notesURL, body: `{"internal":true}`},
		{name: "body повторяется", url: notesURL, body: `{"body":"x","body":"y"}`},
		{name: "body числом", url: notesURL, body: `{"body":1}`},
		{name: "body null", url: notesURL, body: `{"body":null}`},
		{name: "body объектом", url: notesURL, body: `{"body":{"text":"x"}}`},
		{name: "body пустой строкой", url: notesURL, body: `{"body":""}`},
		{name: "body из пробелов", url: notesURL, body: `{"body":" \n\t "}`},
		{name: "quick action в первой строке", url: notesURL, body: `{"body":"/merge"}`},
		{name: "quick action в середине", url: notesURL, body: `{"body":"LGTM\n/approve\nthanks"}`},
		{name: "quick action в последней строке", url: notesURL, body: `{"body":"LGTM\n/close"}`},
		{name: "quick action после пробелов", url: notesURL, body: `{"body":"   /approve"}`},
		{name: "quick action после таба", url: notesURL, body: `{"body":"\t/merge"}`},
		{name: "quick action после \r\n", url: notesURL, body: `{"body":"LGTM\r\n/merge"}`},
		{name: "quick action через escape \\/", url: notesURL, body: `{"body":"\/merge"}`},
		{name: "путь в начале строки", url: notesURL, body: `{"body":"/etc/fstab is read here"}`},
		{name: "quick action в блоке кода", url: notesURL, body: "{\"body\":\"```\\n/usr/bin/env\\n```\"}"},
		{name: "quick action в ответе в дискуссию", url: replyURL, body: `{"body":"/unresolve"}`},
		{name: "лишний ключ в ответе в дискуссию", url: replyURL, body: `{"body":"x","internal":true}`},
		{name: "невалидный UTF-8 в body", url: notesURL, body: "{\"body\":\"ok \xff\"}"},
		{name: "невалидный UTF-8 в ответе в дискуссию", url: replyURL, body: "{\"body\":\"ok \xc3\"}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, http.MethodPost, tt.url, tt.body)
			}
			guardTransport, err := guard.New(allToolsConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(newRequest(t, http.MethodPost, tt.url, tt.body))

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripDiscussionBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{name: "позиция с new_line", body: discussionBody, wantAllowed: true},
		{name: "позиция с old_line", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"old_line":3` }), wantAllowed: true},
		{name: "позиция с old_line и new_line", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"old_line":3,"new_line":4` }), wantAllowed: true},
		{name: "переименование: old_path отличается", body: discussionWith(func(p *positionFields) { p.oldPath = "cmd/old_main.go" }), wantAllowed: true},
		{name: "путь с пробелом и кириллицей", body: discussionWith(func(p *positionFields) { p.oldPath = cyrillicPath; p.newPath = cyrillicPath }), wantAllowed: true},
		{name: "line_range с концами old и new", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(validRangeStart, validRangeEnd)
		}), wantAllowed: true},

		{name: "line_range при кириллическом new_path", body: discussionWith(func(p *positionFields) {
			p.oldPath = cyrillicPath
			p.newPath = cyrillicPath
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(
				rangeEndpoint(cyrillicPathSHA1+"_1_1", "old"),
				rangeEndpoint(cyrillicPathSHA1+"_4_3", "new"),
			)
		}), wantAllowed: true},

		{name: "невалидный JSON", body: `{"body":"x","position":`},
		{name: "только body без position", body: `{"body":"x"}`},
		{name: "position null", body: `{"body":"x","position":null}`},
		{name: "position строкой", body: `{"body":"x","position":"text"}`},
		{name: "position пустой объект", body: `{"body":"x","position":{}}`},
		{name: "лишний ключ рядом с body и position", body: `{"body":"x","position":` + validPositionJSON + `,"internal":true}`},
		{name: "merge_request_diff_head_sha рядом с body и position", body: `{"body":"x","position":` + validPositionJSON + `,"merge_request_diff_head_sha":"` + headSHA + `"}`},
		{name: "note вместо body", body: `{"note":"x","position":` + validPositionJSON + `}`},
		{name: "body с quick action при валидной позиции", body: `{"body":"/approve","position":` + validPositionJSON + `}`},
		{name: "body пустой при валидной позиции", body: `{"body":"","position":` + validPositionJSON + `}`},
		{name: "position с лишним ключом width", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON += `,"width":100` })},
		{name: "position с line_code на верхнем уровне", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON += `,"line_code":"` + mainGoPathSHA1 + `_1_1"` })},
		{name: "position_type image", body: discussionWith(func(p *positionFields) { p.positionType = "image" })},
		{name: "position_type file", body: discussionWith(func(p *positionFields) { p.positionType = "file" })},
		{name: "без position_type", body: discussionWith(func(p *positionFields) { p.positionType = "" })},
		{name: "без base_sha", body: discussionWith(func(p *positionFields) { p.baseSHA = "" })},
		{name: "base_sha в верхнем регистре", body: discussionWith(func(p *positionFields) { p.baseSHA = strings.Repeat("A", 40) })},
		{name: "start_sha из 39 символов", body: discussionWith(func(p *positionFields) { p.startSHA = startSHA[:39] })},
		{name: "head_sha из 41 символа", body: discussionWith(func(p *positionFields) { p.headSHA = headSHA + "3" })},
		{name: "head_sha числом", body: discussionWith(func(p *positionFields) { p.headSHA = ""; p.extraFieldsJSON += `,"head_sha":123` })},
		{name: "old_path пустая строка", body: discussionWith(func(p *positionFields) { p.oldPath = ""; p.extraFieldsJSON += `,"old_path":""` })},
		{name: "без old_path", body: discussionWith(func(p *positionFields) { p.oldPath = "" })},
		{name: "new_path с переводом строки", body: discussionWith(func(p *positionFields) { p.newPath = "cmd/main.go\n" })},
		{name: "new_path с нулевым байтом", body: discussionWith(func(p *positionFields) { p.newPath = "cmd/main.go\x00" })},
		{name: "new_path числом", body: discussionWith(func(p *positionFields) { p.newPath = ""; p.extraFieldsJSON += `,"new_path":1` })},
		{name: "без old_line и new_line", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = "" })},
		{name: "new_line 0", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":0` })},
		{name: "new_line отрицательный", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":-3` })},
		{name: "new_line дробный", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":1.5` })},
		{name: "new_line 1.0", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":1.0` })},
		{name: "new_line 1e0", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":1e0` })},
		{name: "new_line строкой", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":"12"` })},
		{name: "new_line true", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":true` })},
		{name: "old_line null при валидном new_line", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"old_line":null,"new_line":12` })},
		{name: "new_line повторяется", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = `,"new_line":12,"new_line":13` })},
		{name: "line_range не объект", body: discussionWith(func(p *positionFields) { p.extraFieldsJSON = validLinesJSON + `,"line_range":[]` })},
		{name: "line_range с лишним ключом", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + `,"line_range":{"start":` + validRangeStart + `,"end":` + validRangeEnd + `,"middle":{}}`
		})},
		{name: "line_code от SHA1 другого пути при кириллическом new_path", body: discussionWith(func(p *positionFields) {
			p.oldPath = cyrillicPath
			p.newPath = cyrillicPath
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(otherPathSHA1+"_1_1", "old"), rangeEndpoint(otherPathSHA1+"_2_2", "old"))
		})},
		{name: "line_range без end", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + `,"line_range":{"start":` + validRangeStart + `}`
		})},
		{name: "line_range без start", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + `,"line_range":{"end":` + validRangeEnd + `}`
		})},
		{name: "start без line_code", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(`{"type":"old"}`, validRangeEnd)
		})},
		{name: "start без type", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(`{"line_code":"`+mainGoPathSHA1+`_1_1"}`, validRangeEnd)
		})},
		{name: "type context", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(mainGoPathSHA1+"_1_1", "context"), validRangeEnd)
		})},
		{name: "type в верхнем регистре", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(mainGoPathSHA1+"_1_1", "OLD"), validRangeEnd)
		})},
		{name: "line_code без номеров", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(mainGoPathSHA1, "old"), validRangeEnd)
		})},
		{name: "line_code с одним номером", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(mainGoPathSHA1+"_1", "old"), validRangeEnd)
		})},
		{name: "line_code с SHA1 другого пути", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(otherPathSHA1+"_1_1", "old"), validRangeEnd)
		})},
		{name: "line_code с SHA1 в верхнем регистре", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(strings.ToUpper(mainGoPathSHA1)+"_1_1", "old"), validRangeEnd)
		})},
		{name: "start с old_line", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(`{"line_code":"`+mainGoPathSHA1+`_1_1","type":"old","old_line":1}`, validRangeEnd)
		})},
		{name: "end с new_line", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(validRangeStart, `{"line_code":"`+mainGoPathSHA1+`_4_3","type":"new","new_line":3}`)
		})},
		{name: "end с повтором type", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(validRangeStart, `{"line_code":"`+mainGoPathSHA1+`_4_3","type":"new","type":"old"}`)
		})},
		{name: "end с лишним ключом", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(validRangeStart, `{"line_code":"`+mainGoPathSHA1+`_4_3","type":"new","text":"x"}`)
		})},
		{name: "line_range без верхних old_line и new_line", body: discussionWith(func(p *positionFields) {
			p.extraFieldsJSON = lineRangeJSON(validRangeStart, validRangeEnd)
		})},
		{name: "невалидный UTF-8 в body", body: `{"body":"ok ` + "\xff" + `","position":` + validPositionJSON + `}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, http.MethodPost, discussionsURL, tt.body)
			}
			guardTransport, err := guard.New(allToolsConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(newRequest(t, http.MethodPost, discussionsURL, tt.body))

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripDraftBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{name: "только note", body: draftBody, wantAllowed: true},
		{name: "note с position", body: draftWith(func(*positionFields) {}), wantAllowed: true},
		{name: "note с position и line_range", body: draftWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(validRangeStart, validRangeEnd)
		}), wantAllowed: true},
		{name: "note с in_reply_to_discussion_id", body: `{"note":` + jsonString(draftText) + `,"in_reply_to_discussion_id":"` + discussionID + `"}`, wantAllowed: true},

		{name: "position и in_reply_to_discussion_id вместе", body: `{"note":"x","in_reply_to_discussion_id":"` + discussionID + `","position":` + validPositionJSON + `}`},
		{name: "body вместо note", body: `{"body":"x"}`},
		{name: "note и body", body: `{"note":"x","body":"x"}`},
		{name: "без note", body: `{"in_reply_to_discussion_id":"` + discussionID + `"}`},
		{name: "note пустой", body: `{"note":""}`},
		{name: "note из пробелов", body: `{"note":"  \n"}`},
		{name: "note с quick action", body: `{"note":"LGTM\n/approve"}`},
		{name: "note повторяется", body: `{"note":"x","note":"/approve"}`},
		{name: "note null", body: `{"note":null}`},
		{name: "note числом", body: `{"note":1}`},
		{name: "commit_id рядом с note", body: `{"note":"x","commit_id":"` + strings.Repeat("a", 40) + `"}`},
		{name: "merge_request_diff_head_sha рядом с note", body: `{"note":"x","merge_request_diff_head_sha":"` + headSHA + `"}`},
		{name: "resolve_discussion рядом с note", body: `{"note":"x","resolve_discussion":true}`},
		{name: "in_reply_to_discussion_id в верхнем регистре", body: `{"note":"x","in_reply_to_discussion_id":"` + strings.ToUpper(discussionID) + `"}`},
		{name: "in_reply_to_discussion_id из 39 символов", body: `{"note":"x","in_reply_to_discussion_id":"` + discussionID[:39] + `"}`},
		{name: "in_reply_to_discussion_id числом", body: `{"note":"x","in_reply_to_discussion_id":12}`},
		{name: "in_reply_to_discussion_id null", body: `{"note":"x","in_reply_to_discussion_id":null}`},
		{name: "position с position_type image", body: draftWith(func(p *positionFields) { p.positionType = "image" })},
		{name: "position без строк", body: draftWith(func(p *positionFields) { p.extraFieldsJSON = "" })},
		{name: "position с line_code чужого пути", body: draftWith(func(p *positionFields) {
			p.extraFieldsJSON = validLinesJSON + lineRangeJSON(rangeEndpoint(otherPathSHA1+"_1_1", "old"), validRangeEnd)
		})},
		{name: "невалидный UTF-8 в note", body: "{\"note\":\"ok \xff\"}"},
		{name: "мусор после JSON", body: draftBody + `{}`},
		{name: "превышение размера", body: `{"note":"` + strings.Repeat("x", guard.MaxBodyBytes) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, http.MethodPost, draftNotesURL, tt.body)
			}
			guardTransport, err := guard.New(allToolsConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(newRequest(t, http.MethodPost, draftNotesURL, tt.body))

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripReturnsNextTransportError(t *testing.T) {
	errNetwork := errors.New("network down")
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	nextTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, errNetwork)
	guardTransport, err := guard.New(allToolsConfig, nextTransport)
	require.NoError(t, err)

	response, err := guardTransport.RoundTrip(newRequest(t, http.MethodPost, notesURL, noteBody))

	require.ErrorIs(t, err, errNetwork)
	require.Nil(t, response)
}

func TestRoundTripForwardedGetBodyIgnoresIncomingGetBody(t *testing.T) {
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	expectForwarded(t, nextTransport, http.MethodPost, notesURL, noteBody)
	guardTransport, err := guard.New(allToolsConfig, nextTransport)
	require.NoError(t, err)
	request := newRequest(t, http.MethodPost, notesURL, noteBody)
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"body":"/merge"}`)), nil
	}

	response, err := guardTransport.RoundTrip(request)

	requireOutcome(t, true, response, err)
}

func TestRoundTripClosesRequestBody(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		url         string
		body        string
		wantAllowed bool
	}{
		{name: "note пропущена дальше", method: http.MethodPost, url: notesURL, body: noteBody, wantAllowed: true},
		{name: "note отклонена по методу", method: http.MethodPut, url: notesURL, body: noteBody},
		{name: "note отклонена по телу", method: http.MethodPost, url: notesURL, body: `{"body":"/merge"}`},
		{name: "GET MR отклонён из-за тела", method: http.MethodGet, url: mergeRequestURL, body: `{}`},
		{name: "discussion пропущена дальше", method: http.MethodPost, url: discussionsURL, body: discussionBody, wantAllowed: true},
		{name: "discussion отклонена по телу", method: http.MethodPost, url: discussionsURL, body: `{"body":"x"}`},
		{name: "draft note пропущена дальше", method: http.MethodPost, url: draftNotesURL, body: draftBody, wantAllowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextTransport := mocks.NewMockRoundTripper(ctrl)
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.method, tt.url, tt.body)
			}
			bodyReader := strings.NewReader(tt.body)
			requestBody := mocks.NewMockReadCloser(ctrl)
			requestBody.EXPECT().Read(gomock.Any()).DoAndReturn(bodyReader.Read).AnyTimes()
			requestBody.EXPECT().Close().Return(nil)
			guardTransport, err := guard.New(allToolsConfig, nextTransport)
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(t.Context(), tt.method, tt.url, requestBody)
			require.NoError(t, err)
			request.Header.Set(contentTypeHeader, jsonContentType)

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestHasQuickAction(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "обычный текст", text: "LGTM"},
		{name: "слэш в середине", text: "see a/b"},
		{name: "команда в обратных кавычках", text: "run `/merge`"},
		{name: "пустой текст", text: ""},
		{name: "первая строка", text: "/merge", want: true},
		{name: "вторая строка", text: "ok\n/approve", want: true},
		{name: "после пробелов", text: "   /approve", want: true},
		{name: "после таба", text: "\t/merge", want: true},
		{name: "после CRLF", text: "ok\r\n/merge", want: true},
		{name: "после неразрывного пробела", text: "\u00a0/merge", want: true},
		{name: "путь", text: "/etc/fstab", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, guard.HasQuickAction(tt.text))
		})
	}
}

func TestValidProject(t *testing.T) {
	tests := []struct {
		project string
		want    bool
	}{
		{project: "42", want: true},
		{project: "group/ci-test", want: true},
		{project: "group/ci_test.v2", want: true},
		{project: "a1/b2", want: true},
		{project: "0"},
		{project: "007"},
		{project: "-1"},
		{project: "group"},
		{project: ""},
		{project: "/group/ci-test"},
		{project: "group/ci-test/"},
		{project: "group//ci-test"},
		{project: "group/./ci-test"},
		{project: "group/../ci-test"},
		{project: "group/-ci-test"},
		{project: "group/ci test"},
		{project: "group%2Fci-test"},
		{project: "group/ci-test?x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			require.Equal(t, tt.want, guard.ValidProject(tt.project))
		})
	}
}

func TestValidDiscussionID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{id: discussionID, want: true},
		{id: strings.Repeat("0", 40), want: true},
		{id: strings.ToUpper(discussionID)},
		{id: discussionID[:39]},
		{id: discussionID + "a"},
		{id: ""},
		{id: "123"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			require.Equal(t, tt.want, guard.ValidDiscussionID(tt.id))
		})
	}
}

func TestValidPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "обычный путь", path: mainGoPath, want: true},
		{name: "пробел и кириллица", path: cyrillicPath, want: true},
		{name: "пустой путь", path: ""},
		{name: "перевод строки", path: mainGoPath + "\n"},
		{name: "таб", path: "cmd/\tmain.go"},
		{name: "нулевой байт", path: "cmd/\x00main.go"},
		{name: "DEL", path: "cmd/\x7fmain.go"},
		{name: "управляющий символ C1", path: "cmd/\u0085main.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, guard.ValidPath(tt.path))
		})
	}
}

func expectForwarded(t *testing.T, nextTransport *mocks.MockRoundTripper, wantMethod, wantURL, wantBody string) {
	t.Helper()
	nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(forwarded *http.Request) (*http.Response, error) {
		require.Equal(t, wantMethod, forwarded.Method)
		require.Equal(t, wantURL, forwarded.URL.String())
		if wantBody == "" {
			require.True(t, forwarded.Body == nil || forwarded.Body == http.NoBody)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}
		require.Equal(t, wantBody, readAll(t, forwarded.Body))
		require.Equal(t, int64(len(wantBody)), forwarded.ContentLength)
		require.NotNil(t, forwarded.GetBody)
		retryBody, err := forwarded.GetBody()
		require.NoError(t, err)
		require.Equal(t, wantBody, readAll(t, retryBody))
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
}

func readAll(t *testing.T, body io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	return string(data)
}

func requireOutcome(t *testing.T, wantAllowed bool, response *http.Response, err error) {
	t.Helper()
	if wantAllowed {
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		return
	}
	requireForbidden(t, response, err)
}

func requireForbidden(t *testing.T, response *http.Response, err error) {
	t.Helper()
	require.ErrorIs(t, err, guard.ErrForbidden)
	require.Nil(t, response)
}
