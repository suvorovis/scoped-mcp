package guard_test

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/guard"
	"scoped-mcp/mcp-jira/guard/mocks"
)

var allowedConfig = config.Config{
	BaseURL: "https://jira.example.com",
	Tools:   config.Tools{CreateIssue: true},
	Fields: map[string]config.Field{
		"priority":  {Jira: "priority", Kind: config.KindName, Required: true},
		"epic_name": {Jira: "customfield_10203", Kind: config.KindText, Required: true},
		"epic_link": {Jira: "customfield_10201", Kind: config.KindIssueKey},
	},
	Projects: []config.Project{
		{Key: "PROJ", AssignToSelf: true, IssueTypes: []config.IssueType{
			{Name: "Epic", ID: "10000", Fields: []string{"priority", "epic_name"}},
			{Name: "Story", ID: "11203", Fields: []string{"priority", "epic_link"}},
		}},
		{Key: "OTHER", IssueTypes: []config.IssueType{{Name: "Task", ID: "3"}}},
	},
}

const (
	issueURL      = "https://jira.example.com/rest/api/2/issue"
	tokenOwnerURL = "https://jira.example.com/rest/api/2/myself"
	epicBody      = `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s","priority":{"name":"High"},"customfield_10203":"Epic name"}}`

	tokenOwnerBody = `{"name":"token.owner","displayName":"Token Owner"}`
)

func withBasePathJira() config.Config {
	cfg := allowedConfig
	cfg.BaseURL = "https://jira.example.com/jira"
	return cfg
}

func withoutAssignToSelf() config.Config {
	cfg := allowedConfig
	cfg.Projects = slices.Clone(cfg.Projects)
	for i := range cfg.Projects {
		cfg.Projects[i].AssignToSelf = false
	}
	return cfg
}

const (
	attachURL             = issueURL + "/PROJ-7790/attachments"
	attachBoundary        = "a1b2c3"
	attachContentType     = "multipart/form-data; boundary=" + attachBoundary
	attachMaxBytes        = 64
	reportDisposition     = `form-data; name="file"; filename="report.md"`
	octetStream           = "application/octet-stream"
	attachDelimiter       = "--" + attachBoundary + "\r\n"
	attachCloseDelimiter  = "\r\n--" + attachBoundary + "--\r\n"
	attachSecondDelimiter = "\r\n--" + attachBoundary + "\r\n"
)

func withAttachFiles() config.Config {
	cfg := allowedConfig
	cfg.Tools.AttachFiles = true
	cfg.Attachments = &config.Attachments{Dirs: []string{"/home/bot/out"}, Extensions: []string{".md", ".PNG"}, MaxBytes: attachMaxBytes}
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

func withAttachFilesButNoSection() config.Config {
	cfg := allowedConfig
	cfg.Tools.AttachFiles = true
	return cfg
}

func withAttachFilesAndBasePathJira() config.Config {
	cfg := withAttachFiles()
	cfg.BaseURL = withBasePathJira().BaseURL
	return cfg
}

func attachmentBody(t *testing.T, fileName, content string) string {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.SetBoundary(attachBoundary))
	part, err := writer.CreateFormFile("file", fileName)
	require.NoError(t, err)
	_, err = io.WriteString(part, content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.String()
}

func partHeaders(disposition, contentType string) string {
	headers := "Content-Disposition: " + disposition + "\r\n"
	if contentType != "" {
		headers += "Content-Type: " + contentType + "\r\n"
	}
	return headers
}

func rawPart(headers, content string) string {
	return headers + "\r\n" + content
}

func rawMultipartBody(parts ...string) string {
	return attachDelimiter + strings.Join(parts, attachSecondDelimiter) + attachCloseDelimiter
}

func attachmentRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, url, bodyReader)
	require.NoError(t, err)
	request.Header = attachmentHeader()
	return request
}

func attachmentHeader() http.Header {
	return http.Header{"Content-Type": {attachContentType}, "X-Atlassian-Token": {"no-check"}}
}

func TestRoundTripAttachTarget(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		host        string
		wantAllowed bool
	}{
		{name: "POST вложения в задачу проекта из конфига", cfg: withAttachFiles(), method: http.MethodPost, url: attachURL, wantAllowed: true},
		{name: "задача второго проекта из конфига", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "/OTHER-1/attachments", wantAllowed: true},
		{name: "base_url с путём", cfg: withAttachFilesAndBasePathJira(), method: http.MethodPost, url: "https://jira.example.com/jira/rest/api/2/issue/PROJ-7790/attachments", wantAllowed: true},
		{name: "заголовок Host совпадает с base_url", cfg: withAttachFiles(), method: http.MethodPost, url: attachURL, host: "jira.example.com", wantAllowed: true},
		{name: "включён только attach_files", cfg: withCreateIssueDisabled(), method: http.MethodPost, url: attachURL, wantAllowed: true},

		{name: "attach_files выключен и раздела attachments нет", cfg: allowedConfig, method: http.MethodPost, url: attachURL},
		{name: "раздел attachments есть, tools.attach_files выключен", cfg: withAttachFilesDisabled(), method: http.MethodPost, url: attachURL},
		{name: "tools.attach_files включён, раздела attachments нет", cfg: withAttachFilesButNoSection(), method: http.MethodPost, url: attachURL},
		{name: "задача проекта не из конфига", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "/SECRET-1/attachments"},
		{name: "ключ строчными", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "/proj-1/attachments"},
		{name: "ключ с нулевым номером", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "/PROJ-0/attachments"},
		{name: "числовой ID задачи", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "/10001/attachments"},
		{name: "пустой ключ", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "//attachments"},
		{name: "лишний сегмент перед attachments", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "/SECRET-1/x/PROJ-1/attachments"},
		{name: "путь конкретного вложения", cfg: withAttachFiles(), method: http.MethodPost, url: attachURL + "/1"},
		{name: "тело вложения на путь комментариев", cfg: withAllTools(), method: http.MethodPost, url: commentAPIURL},
		{name: "путь comment перед attachments", cfg: withAllTools(), method: http.MethodPost, url: issueURL + "/PROJ-7790/comment/attachments"},
		{name: "путь с завершающим слэшем", cfg: withAttachFiles(), method: http.MethodPost, url: attachURL + "/"},
		{name: "путь в процентной кодировке", cfg: withAttachFiles(), method: http.MethodPost, url: issueURL + "/PROJ-7790/%61ttachments"},
		{name: "путь без префикса base_url", cfg: withAttachFilesAndBasePathJira(), method: http.MethodPost, url: attachURL},
		{name: "метод PUT", cfg: withAttachFiles(), method: http.MethodPut, url: attachURL},
		{name: "метод GET", cfg: withAttachFiles(), method: http.MethodGet, url: attachURL},
		{name: "другой хост", cfg: withAttachFiles(), method: http.MethodPost, url: "https://evil.example.com/rest/api/2/issue/PROJ-7790/attachments"},
		{name: "заголовок Host другого хоста", cfg: withAttachFiles(), method: http.MethodPost, url: attachURL, host: "evil.example.com"},
		{name: "схема http", cfg: withAttachFiles(), method: http.MethodPost, url: "http://jira.example.com/rest/api/2/issue/PROJ-7790/attachments"},
		{name: "непустой query", cfg: withAttachFiles(), method: http.MethodPost, url: attachURL + "?x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := attachmentBody(t, "report.md", "# Отчёт")
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.url, body)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request := attachmentRequest(t, tt.method, tt.url, body)
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripAttachBody(t *testing.T) {
	reportBody := attachmentBody(t, "report.md", "# Отчёт")
	tests := []struct {
		name            string
		headerOverrides http.Header
		body            string
		wantAllowed     bool
	}{
		{name: "один файл с разрешённым расширением", body: reportBody, wantAllowed: true},
		{name: "последний суффикс .png против .PNG в конфиге", body: attachmentBody(t, "scheme.drawio.png", "png"), wantAllowed: true},
		{name: "имя файла кириллицей и с кавычками", body: attachmentBody(t, `отчёт "v2".md`, "x"), wantAllowed: true},
		{name: "содержимое ровно max_bytes", body: attachmentBody(t, "report.md", strings.Repeat("x", attachMaxBytes)), wantAllowed: true},
		{name: "пустой файл", body: attachmentBody(t, "report.md", ""), wantAllowed: true},

		{name: "без X-Atlassian-Token", headerOverrides: http.Header{"X-Atlassian-Token": nil}, body: reportBody},
		{name: "X-Atlassian-Token с другим значением", headerOverrides: http.Header{"X-Atlassian-Token": {"nocheck"}}, body: reportBody},
		{name: "X-Atlassian-Token дважды", headerOverrides: http.Header{"X-Atlassian-Token": {"no-check", "no-check"}}, body: reportBody},
		{name: "без Content-Type", headerOverrides: http.Header{"Content-Type": nil}, body: reportBody},
		{name: "Content-Type JSON", headerOverrides: http.Header{"Content-Type": {"application/json"}}, body: reportBody},
		{name: "Content-Type multipart/mixed", headerOverrides: http.Header{"Content-Type": {"multipart/mixed; boundary=" + attachBoundary}}, body: reportBody},
		{name: "Content-Type без boundary", headerOverrides: http.Header{"Content-Type": {"multipart/form-data"}}, body: reportBody},
		{name: "Content-Type с лишним параметром", headerOverrides: http.Header{"Content-Type": {attachContentType + "; charset=utf-8"}}, body: reportBody},
		{name: "Content-Type дважды", headerOverrides: http.Header{"Content-Type": {attachContentType, attachContentType}}, body: reportBody},
		{name: "boundary не совпадает с телом", headerOverrides: http.Header{"Content-Type": {"multipart/form-data; boundary=zzz"}}, body: reportBody},

		{name: "без тела", body: ""},
		{name: "тело не multipart", body: `{"fields":{}}`},
		{name: "содержимое больше max_bytes", body: attachmentBody(t, "report.md", strings.Repeat("x", attachMaxBytes+1))},
		{name: "тело больше max_bytes и 64 KiB оболочки", body: attachmentBody(t, "report.md", strings.Repeat("x", attachMaxBytes+guard.AttachmentEnvelopeBytes))},
		{name: "расширение не из конфига", body: attachmentBody(t, "run.sh", "x")},
		{name: "имя без расширения", body: attachmentBody(t, "README", "x")},
		{name: "пустое имя файла", body: attachmentBody(t, "", "x")},
		{name: "имя файла со слэшем", body: attachmentBody(t, "../report.md", "x")},
		{name: "имя файла с обратным слэшем", body: attachmentBody(t, `..\report.md`, "x")},
		{name: "поле не file", body: rawMultipartBody(rawPart(partHeaders(`form-data; name="upload"; filename="report.md"`, octetStream), "x"))},
		{name: "часть без filename", body: rawMultipartBody(rawPart(partHeaders(`form-data; name="file"`, octetStream), "x"))},
		{name: "filename* с другим расширением рядом с filename", body: rawMultipartBody(rawPart(partHeaders(reportDisposition+"; filename*=UTF-8''run.sh", octetStream), "x"))},
		{name: "filename* с разрешённым расширением рядом с filename", body: rawMultipartBody(rawPart(partHeaders(reportDisposition+"; filename*=UTF-8''other.md", octetStream), "x"))},
		{name: "Content-Disposition attachment", body: rawMultipartBody(rawPart(partHeaders(`attachment; name="file"; filename="report.md"`, octetStream), "x"))},
		{name: "часть с Content-Type text/plain", body: rawMultipartBody(rawPart(partHeaders(reportDisposition, "text/plain"), "x"))},
		{name: "часть без Content-Type", body: rawMultipartBody(rawPart(partHeaders(reportDisposition, ""), "x"))},
		{name: "часть с Content-Transfer-Encoding", body: rawMultipartBody(rawPart(partHeaders(reportDisposition, octetStream)+"Content-Transfer-Encoding: base64\r\n", "eA=="))},
		{name: "вторая часть", body: rawMultipartBody(rawPart(partHeaders(reportDisposition, octetStream), "x"), rawPart(partHeaders(reportDisposition, octetStream), "y"))},
		{name: "преамбула перед первой частью", body: "preamble\r\n" + reportBody},
		{name: "мусор после закрывающего разделителя", body: reportBody + "garbage"},
		{name: "нет закрывающего разделителя", body: attachDelimiter + rawPart(partHeaders(reportDisposition, octetStream), "x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, attachURL, tt.body)
			}
			guardTransport, err := guard.New(withAttachFiles(), nextTransport)
			require.NoError(t, err)
			request := attachmentRequest(t, http.MethodPost, attachURL, tt.body)
			for key, values := range tt.headerOverrides {
				request.Header[key] = values
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

const (
	commentAPIURL = issueURL + "/PROJ-7790/comment"
	commentBody   = `{"body":"h3. Итог\n*готово*"}`
)

func withAddComment() config.Config {
	cfg := allowedConfig
	cfg.Tools.AddComment = true
	return cfg
}

func withOnlyAddCommentEnabled() config.Config {
	cfg := withAddComment()
	cfg.Tools.CreateIssue = false
	return cfg
}

func withAddCommentAndBasePathJira() config.Config {
	cfg := withAddComment()
	cfg.BaseURL = withBasePathJira().BaseURL
	return cfg
}

func withAllTools() config.Config {
	cfg := withAttachFiles()
	cfg.Tools.AddComment = true
	cfg.Tools.LinkIssues = true
	cfg.IssueLinks = withLinkIssues().IssueLinks
	cfg.Tools.UpdateIssue = true
	cfg.Projects = withUpdateIssue().Projects
	return cfg
}

func jsonRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, url, bodyReader)
	require.NoError(t, err)
	request.Header = jsonHeader()
	return request
}

func jsonHeader() http.Header {
	return http.Header{"Content-Type": {"application/json"}}
}

func TestRoundTripCommentTarget(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		host        string
		wantAllowed bool
	}{
		{name: "POST комментария к задаче проекта из конфига", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL, wantAllowed: true},
		{name: "задача второго проекта из конфига", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "/OTHER-1/comment", wantAllowed: true},
		{name: "base_url с путём", cfg: withAddCommentAndBasePathJira(), method: http.MethodPost, url: "https://jira.example.com/jira/rest/api/2/issue/PROJ-7790/comment", wantAllowed: true},
		{name: "заголовок Host совпадает с base_url", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL, host: "jira.example.com", wantAllowed: true},
		{name: "включён только add_comment", cfg: withOnlyAddCommentEnabled(), method: http.MethodPost, url: commentAPIURL, wantAllowed: true},

		{name: "tools.add_comment выключен", cfg: withAttachFiles(), method: http.MethodPost, url: commentAPIURL},
		{name: "задача проекта не из конфига", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "/SECRET-1/comment"},
		{name: "ключ строчными", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "/proj-1/comment"},
		{name: "ключ с нулевым номером", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "/PROJ-0/comment"},
		{name: "числовой ID задачи", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "/10001/comment"},
		{name: "пустой ключ", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "//comment"},
		{name: "лишний сегмент перед comment", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "/SECRET-1/x/PROJ-1/comment"},
		{name: "путь конкретного комментария", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL + "/10100"},
		{name: "путь с завершающим слэшем", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL + "/"},
		{name: "путь attachments перед comment", cfg: withAllTools(), method: http.MethodPost, url: issueURL + "/PROJ-7790/attachments/comment"},
		{name: "путь comments во множественном числе", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL + "s"},
		{name: "путь в процентной кодировке", cfg: withAddComment(), method: http.MethodPost, url: issueURL + "/PROJ-7790/%63omment"},
		{name: "путь без префикса base_url", cfg: withAddCommentAndBasePathJira(), method: http.MethodPost, url: commentAPIURL},
		{name: "метод GET списка комментариев", cfg: withAddComment(), method: http.MethodGet, url: commentAPIURL},
		{name: "метод PUT комментария", cfg: withAddComment(), method: http.MethodPut, url: commentAPIURL + "/10100"},
		{name: "метод DELETE комментария", cfg: withAddComment(), method: http.MethodDelete, url: commentAPIURL + "/10100"},
		{name: "метод PUT", cfg: withAddComment(), method: http.MethodPut, url: commentAPIURL},
		{name: "другой хост", cfg: withAddComment(), method: http.MethodPost, url: "https://evil.example.com/rest/api/2/issue/PROJ-7790/comment"},
		{name: "заголовок Host другого хоста", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL, host: "evil.example.com"},
		{name: "схема http", cfg: withAddComment(), method: http.MethodPost, url: "http://jira.example.com/rest/api/2/issue/PROJ-7790/comment"},
		{name: "непустой query", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL + "?expand=renderedBody"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.url, commentBody)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request := jsonRequest(t, tt.method, tt.url, commentBody)
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripCommentBody(t *testing.T) {
	oversized := `{"body":"` + strings.Repeat("x", guard.MaxBodyBytes) + `"}`
	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{name: "текст в wiki markup", body: commentBody, wantAllowed: true},
		{name: "текст с пробелами по краям", body: `{"body":"  готово  "}`, wantAllowed: true},
		{name: "ключи с пробелами вокруг", body: `{ "body" : "готово" }`, wantAllowed: true},

		{name: "без тела", body: ""},
		{name: "невалидный JSON", body: `{"body":`},
		{name: "тело — массив", body: `[{"body":"x"}]`},
		{name: "тело — строка", body: `"x"`},
		{name: "мусор после JSON", body: commentBody + `{"visibility":{}}`},
		{name: "превышение размера", body: oversized},
		{name: "пустой объект", body: `{}`},
		{name: "visibility рядом с body", body: `{"body":"x","visibility":{"type":"role","value":"Administrators"}}`},
		{name: "properties рядом с body", body: `{"body":"x","properties":[{"key":"k","value":{}}]}`},
		{name: "лишний ключ рядом с body", body: `{"body":"x","author":{"name":"someone.else"}}`},
		{name: "без body", body: `{"visibility":{"type":"role","value":"Administrators"}}`},
		{name: "body повторяется", body: `{"body":"x","body":"y"}`},
		{name: "body числом", body: `{"body":1}`},
		{name: "body null", body: `{"body":null}`},
		{name: "body объектом", body: `{"body":{"text":"x"}}`},
		{name: "body пустой строкой", body: `{"body":""}`},
		{name: "body из пробелов", body: `{"body":" \n\t "}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, commentAPIURL, tt.body)
			}
			guardTransport, err := guard.New(withAddComment(), nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, http.MethodPost, commentAPIURL, tt.body))

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

const (
	issueLinkURL     = "https://jira.example.com/rest/api/2/issueLink"
	blocksTypeRef    = `{"name":"Blocks"}`
	proj7790Ref      = `{"key":"PROJ-7790"}`
	proj7791Ref      = `{"key":"PROJ-7791"}`
	duplicateTypeRef = `{"name":"Duplicate"}`
	secretIssueRef   = `{"key":"SECRET-1"}`
)

var blocksLinkBody = linkBody(blocksTypeRef, proj7790Ref, proj7791Ref)

func withLinkIssues() config.Config {
	cfg := allowedConfig
	cfg.Tools.LinkIssues = true
	cfg.IssueLinks = []config.IssueLink{
		{Relation: "blocks", Type: "Blocks", Direction: config.LinkOutward},
		{Relation: "is blocked by", Type: "Blocks", Direction: config.LinkInward},
		{Relation: "relates to", Type: "Relates", Direction: config.LinkOutward},
	}
	return cfg
}

func withOnlyLinkIssuesEnabled() config.Config {
	cfg := withLinkIssues()
	cfg.Tools.CreateIssue = false
	return cfg
}

func withLinkIssuesAndBasePathJira() config.Config {
	cfg := withLinkIssues()
	cfg.BaseURL = withBasePathJira().BaseURL
	return cfg
}

func withLinkIssuesWithoutIssueLinks() config.Config {
	cfg := allowedConfig
	cfg.Tools.LinkIssues = true
	return cfg
}

func linkBody(linkType, inwardIssue, outwardIssue string) string {
	return jsonObject(linkTypeMember(linkType), inwardIssueMember(inwardIssue), outwardIssueMember(outwardIssue))
}

func jsonObject(members ...string) string {
	return "{" + strings.Join(members, ",") + "}"
}

func linkTypeMember(linkType string) string {
	return `"type":` + linkType
}

func inwardIssueMember(inwardIssue string) string {
	return `"inwardIssue":` + inwardIssue
}

func outwardIssueMember(outwardIssue string) string {
	return `"outwardIssue":` + outwardIssue
}

func TestRoundTripIssueLinkTarget(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		host        string
		wantAllowed bool
	}{
		{name: "POST связи при tools.link_issues", cfg: withLinkIssues(), method: http.MethodPost, url: issueLinkURL, wantAllowed: true},
		{name: "base_url с путём", cfg: withLinkIssuesAndBasePathJira(), method: http.MethodPost, url: "https://jira.example.com/jira/rest/api/2/issueLink", wantAllowed: true},
		{name: "заголовок Host совпадает с base_url", cfg: withLinkIssues(), method: http.MethodPost, url: issueLinkURL, host: "jira.example.com", wantAllowed: true},
		{name: "включён только link_issues", cfg: withOnlyLinkIssuesEnabled(), method: http.MethodPost, url: issueLinkURL, wantAllowed: true},

		{name: "tools.link_issues выключен", cfg: allowedConfig, method: http.MethodPost, url: issueLinkURL},
		{name: "tools.link_issues включён, раздела issue_links нет", cfg: withLinkIssuesWithoutIssueLinks(), method: http.MethodPost, url: issueLinkURL},
		{name: "путь конкретной связи", cfg: withLinkIssues(), method: http.MethodPost, url: issueLinkURL + "/10200"},
		{name: "метод GET связи", cfg: withLinkIssues(), method: http.MethodGet, url: issueLinkURL + "/10200"},
		{name: "метод DELETE связи", cfg: withLinkIssues(), method: http.MethodDelete, url: issueLinkURL + "/10200"},
		{name: "метод GET", cfg: withLinkIssues(), method: http.MethodGet, url: issueLinkURL},
		{name: "метод PUT", cfg: withLinkIssues(), method: http.MethodPut, url: issueLinkURL},
		{name: "путь issueLinkType", cfg: withLinkIssues(), method: http.MethodPost, url: "https://jira.example.com/rest/api/2/issueLinkType"},
		{name: "путь с завершающим слэшем", cfg: withLinkIssues(), method: http.MethodPost, url: issueLinkURL + "/"},
		{name: "путь в процентной кодировке", cfg: withLinkIssues(), method: http.MethodPost, url: "https://jira.example.com/rest/api/2/issue%4Cink"},
		{name: "путь без префикса base_url", cfg: withLinkIssuesAndBasePathJira(), method: http.MethodPost, url: issueLinkURL},
		{name: "тело связи на путь создания задачи", cfg: withLinkIssues(), method: http.MethodPost, url: issueURL},
		{name: "тело связи на путь комментариев", cfg: withAllTools(), method: http.MethodPost, url: commentAPIURL},
		{name: "другой хост", cfg: withLinkIssues(), method: http.MethodPost, url: "https://evil.example.com/rest/api/2/issueLink"},
		{name: "заголовок Host другого хоста", cfg: withLinkIssues(), method: http.MethodPost, url: issueLinkURL, host: "evil.example.com"},
		{name: "схема http", cfg: withLinkIssues(), method: http.MethodPost, url: "http://jira.example.com/rest/api/2/issueLink"},
		{name: "непустой query", cfg: withLinkIssues(), method: http.MethodPost, url: issueLinkURL + "?x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.url, blocksLinkBody)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request := jsonRequest(t, tt.method, tt.url, blocksLinkBody)
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripIssueLinkBody(t *testing.T) {
	oversized := linkBody(`{"name":"`+strings.Repeat("x", guard.MaxBodyBytes)+`"}`, proj7790Ref, proj7791Ref)
	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{name: "связь Blocks между задачами проекта из конфига", body: blocksLinkBody, wantAllowed: true},
		{name: "тип Relates", body: linkBody(`{"name":"Relates"}`, proj7790Ref, proj7791Ref), wantAllowed: true},
		{name: "задачи разных проектов", body: linkBody(blocksTypeRef, `{"key":"OTHER-1"}`, proj7790Ref), wantAllowed: true},
		{name: "задачи поменяны местами", body: linkBody(blocksTypeRef, proj7791Ref, proj7790Ref), wantAllowed: true},
		{name: "пробелы между токенами JSON", body: `{ "type" : ` + blocksTypeRef + ` , "inwardIssue" : ` + proj7790Ref + ` , "outwardIssue" : ` + proj7791Ref + ` }`, wantAllowed: true},

		{name: "без тела", body: ""},
		{name: "невалидный JSON", body: `{"type":`},
		{name: "тело — массив", body: "[" + blocksLinkBody + "]"},
		{name: "мусор после JSON", body: blocksLinkBody + `{"comment":{}}`},
		{name: "превышение размера", body: oversized},
		{name: "пустой объект", body: `{}`},
		{name: "без type", body: jsonObject(inwardIssueMember(proj7790Ref), outwardIssueMember(proj7791Ref))},
		{name: "без inwardIssue", body: jsonObject(linkTypeMember(blocksTypeRef), outwardIssueMember(proj7791Ref))},
		{name: "без outwardIssue", body: jsonObject(linkTypeMember(blocksTypeRef), inwardIssueMember(proj7790Ref))},
		{name: "comment рядом с тремя ключами", body: jsonObject(linkTypeMember(blocksTypeRef), inwardIssueMember(proj7790Ref), outwardIssueMember(proj7791Ref), `"comment":{"body":"x"}`)},
		{name: "лишний ключ", body: jsonObject(linkTypeMember(blocksTypeRef), inwardIssueMember(proj7790Ref), outwardIssueMember(proj7791Ref), `"id":"10200"`)},
		{name: "type повторяется", body: jsonObject(linkTypeMember(duplicateTypeRef), linkTypeMember(blocksTypeRef), inwardIssueMember(proj7790Ref), outwardIssueMember(proj7791Ref))},
		{name: "type строкой", body: linkBody(`"Blocks"`, proj7790Ref, proj7791Ref)},
		{name: "type по id", body: linkBody(`{"id":"10000"}`, proj7790Ref, proj7791Ref)},
		{name: "type с id рядом с name", body: linkBody(`{"name":"Blocks","id":"10000"}`, proj7790Ref, proj7791Ref)},
		{name: "name в type повторяется", body: linkBody(`{"name":"Duplicate","name":"Blocks"}`, proj7790Ref, proj7791Ref)},
		{name: "name в type числом", body: linkBody(`{"name":1}`, proj7790Ref, proj7791Ref)},
		{name: "тип не из конфига", body: linkBody(duplicateTypeRef, proj7790Ref, proj7791Ref)},
		{name: "тип в другом регистре", body: linkBody(`{"name":"blocks"}`, proj7790Ref, proj7791Ref)},
		{name: "тип фразой relation", body: linkBody(`{"name":"is blocked by"}`, proj7790Ref, proj7791Ref)},
		{name: "inwardIssue строкой", body: linkBody(blocksTypeRef, `"PROJ-7790"`, proj7791Ref)},
		{name: "inwardIssue по id", body: linkBody(blocksTypeRef, `{"id":"10001"}`, proj7791Ref)},
		{name: "inwardIssue с id рядом с key", body: linkBody(blocksTypeRef, `{"key":"PROJ-7790","id":"10001"}`, proj7791Ref)},
		{name: "key в inwardIssue повторяется", body: linkBody(blocksTypeRef, `{"key":"SECRET-1","key":"PROJ-7790"}`, proj7791Ref)},
		{name: "key в inwardIssue числом", body: linkBody(blocksTypeRef, `{"key":7790}`, proj7791Ref)},
		{name: "inwardIssue чужого проекта", body: linkBody(blocksTypeRef, secretIssueRef, proj7791Ref)},
		{name: "outwardIssue чужого проекта", body: linkBody(blocksTypeRef, proj7790Ref, secretIssueRef)},
		{name: "ключ строчными", body: linkBody(blocksTypeRef, `{"key":"proj-7790"}`, proj7791Ref)},
		{name: "ключ с нулевым номером", body: linkBody(blocksTypeRef, proj7790Ref, `{"key":"PROJ-0"}`)},
		{name: "числовой ID задачи в outwardIssue", body: linkBody(blocksTypeRef, proj7790Ref, `{"key":"10001"}`)},
		{name: "одна и та же задача с обеих сторон", body: linkBody(blocksTypeRef, proj7790Ref, proj7790Ref)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, issueLinkURL, tt.body)
			}
			guardTransport, err := guard.New(withLinkIssues(), nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, http.MethodPost, issueLinkURL, tt.body))

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

const (
	updateIssueURL    = issueURL + "/PROJ-7790"
	epicLinkBody      = `{"fields":{"customfield_10201":"PROJ-7630"}}`
	clearEpicLinkBody = `{"fields":{"customfield_10201":null}}`
)

func withUpdateIssue() config.Config {
	cfg := allowedConfig
	cfg.Tools.UpdateIssue = true
	cfg.Projects = slices.Clone(cfg.Projects)
	cfg.Projects[0].UpdateFields = []string{"epic_link"}
	return cfg
}

func withOnlyUpdateIssueEnabled() config.Config {
	cfg := withUpdateIssue()
	cfg.Tools.CreateIssue = false
	return cfg
}

func withUpdateIssueAndBasePathJira() config.Config {
	cfg := withUpdateIssue()
	cfg.BaseURL = withBasePathJira().BaseURL
	return cfg
}

func withUpdateIssueWithoutUpdateFields() config.Config {
	cfg := allowedConfig
	cfg.Tools.UpdateIssue = true
	return cfg
}

func TestRoundTripUpdateIssueTarget(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		host        string
		wantAllowed bool
	}{
		{name: "PUT задачи проекта с update_fields", cfg: withUpdateIssue(), method: http.MethodPut, url: updateIssueURL, wantAllowed: true},
		{name: "base_url с путём", cfg: withUpdateIssueAndBasePathJira(), method: http.MethodPut, url: "https://jira.example.com/jira/rest/api/2/issue/PROJ-7790", wantAllowed: true},
		{name: "заголовок Host совпадает с base_url", cfg: withUpdateIssue(), method: http.MethodPut, url: updateIssueURL, host: "jira.example.com", wantAllowed: true},
		{name: "включён только update_issue", cfg: withOnlyUpdateIssueEnabled(), method: http.MethodPut, url: updateIssueURL, wantAllowed: true},
		{name: "включены все инструменты", cfg: withAllTools(), method: http.MethodPut, url: updateIssueURL, wantAllowed: true},

		{name: "tools.update_issue выключен", cfg: allowedConfig, method: http.MethodPut, url: updateIssueURL},
		{name: "tools.update_issue включён, update_fields нет ни у одного проекта", cfg: withUpdateIssueWithoutUpdateFields(), method: http.MethodPut, url: updateIssueURL},
		{name: "задача проекта из конфига без update_fields", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/OTHER-1"},
		{name: "задача проекта не из конфига", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/SECRET-1"},
		{name: "ключ строчными", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/proj-7790"},
		{name: "ключ с нулевым номером", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/PROJ-0"},
		{name: "числовой ID задачи", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/10001"},
		{name: "пустой ключ", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/"},
		{name: "путь без ключа", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL},
		{name: "лишний сегмент перед ключом", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/SECRET-1/PROJ-7790"},
		{name: "путь с завершающим слэшем", cfg: withUpdateIssue(), method: http.MethodPut, url: updateIssueURL + "/"},
		{name: "путь комментариев", cfg: withAllTools(), method: http.MethodPut, url: updateIssueURL + "/comment"},
		{name: "путь вложений", cfg: withAllTools(), method: http.MethodPut, url: updateIssueURL + "/attachments"},
		{name: "путь исполнителя", cfg: withUpdateIssue(), method: http.MethodPut, url: updateIssueURL + "/assignee"},
		{name: "путь переходов", cfg: withUpdateIssue(), method: http.MethodPut, url: updateIssueURL + "/transitions"},
		{name: "путь в процентной кодировке", cfg: withUpdateIssue(), method: http.MethodPut, url: issueURL + "/PROJ%2D7790"},
		{name: "путь без префикса base_url", cfg: withUpdateIssueAndBasePathJira(), method: http.MethodPut, url: updateIssueURL},
		{name: "метод POST", cfg: withUpdateIssue(), method: http.MethodPost, url: updateIssueURL},
		{name: "метод PATCH", cfg: withUpdateIssue(), method: http.MethodPatch, url: updateIssueURL},
		{name: "метод DELETE", cfg: withUpdateIssue(), method: http.MethodDelete, url: updateIssueURL},
		{name: "метод GET", cfg: withUpdateIssue(), method: http.MethodGet, url: updateIssueURL},
		{name: "другой хост", cfg: withUpdateIssue(), method: http.MethodPut, url: "https://evil.example.com/rest/api/2/issue/PROJ-7790"},
		{name: "заголовок Host другого хоста", cfg: withUpdateIssue(), method: http.MethodPut, url: updateIssueURL, host: "evil.example.com"},
		{name: "схема http", cfg: withUpdateIssue(), method: http.MethodPut, url: "http://jira.example.com/rest/api/2/issue/PROJ-7790"},
		{name: "непустой query", cfg: withUpdateIssue(), method: http.MethodPut, url: updateIssueURL + "?notifyUsers=false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.url, epicLinkBody)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request := jsonRequest(t, tt.method, tt.url, epicLinkBody)
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripUpdateIssueBody(t *testing.T) {
	oversized := `{"fields":{"customfield_10201":"` + strings.Repeat("x", guard.MaxBodyBytes) + `"}}`
	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{name: "epic link ключом задачи", body: epicLinkBody, wantAllowed: true},
		{name: "epic link null очищает поле", body: clearEpicLinkBody, wantAllowed: true},
		{name: "значение не проверяется", body: `{"fields":{"customfield_10201":{"any":["thing"]}}}`, wantAllowed: true},
		{name: "пробелы между токенами JSON", body: `{ "fields" : { "customfield_10201" : "PROJ-7630" } }`, wantAllowed: true},

		{name: "без тела", body: ""},
		{name: "невалидный JSON", body: `{"fields":`},
		{name: "тело — массив", body: "[" + epicLinkBody + "]"},
		{name: "мусор после JSON", body: epicLinkBody + `{"update":{}}`},
		{name: "превышение размера", body: oversized},
		{name: "пустой объект", body: `{}`},
		{name: "пустой объект fields", body: `{"fields":{}}`},
		{name: "fields null", body: `{"fields":null}`},
		{name: "fields массивом", body: `{"fields":[]}`},
		{name: "update вместо fields", body: `{"update":{"customfield_10201":[{"set":"PROJ-7630"}]}}`},
		{name: "update рядом с fields", body: `{"fields":{"customfield_10201":"PROJ-7630"},"update":{"labels":[{"add":"x"}]}}`},
		{name: "fields повторяется", body: `{"fields":{"priority":{"name":"High"}},"fields":{"customfield_10201":"PROJ-7630"}}`},
		{name: "поле повторяется", body: `{"fields":{"customfield_10201":"PROJ-7630","customfield_10201":"PROJ-7631"}}`},
		{name: "поле типа задачи вне update_fields", body: `{"fields":{"priority":{"name":"High"}}}`},
		{name: "разрешённое поле рядом с неразрешённым", body: `{"fields":{"customfield_10201":"PROJ-7630","customfield_10203":"x"}}`},
		{name: "summary", body: `{"fields":{"summary":"s"}}`},
		{name: "assignee", body: `{"fields":{"assignee":{"name":"someone.else"}}}`},
		{name: "project", body: `{"fields":{"project":{"key":"SECRET"}}}`},
		{name: "issuetype", body: `{"fields":{"issuetype":{"id":"10000"}}}`},
		{name: "ключ конфига вместо Jira id", body: `{"fields":{"epic_link":"PROJ-7630"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, updateIssueURL, tt.body)
			}
			guardTransport, err := guard.New(withUpdateIssue(), nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, http.MethodPut, updateIssueURL, tt.body))

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripTarget(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		host        string
		wantAllowed bool
	}{
		{name: "POST на создание задачи", cfg: allowedConfig, method: http.MethodPost, url: issueURL, wantAllowed: true},
		{name: "base_url с путём", cfg: withBasePathJira(), method: http.MethodPost, url: "https://jira.example.com/jira/rest/api/2/issue", wantAllowed: true},
		{name: "заголовок Host совпадает с base_url", cfg: allowedConfig, method: http.MethodPost, url: issueURL, host: "jira.example.com", wantAllowed: true},

		{name: "tools.create_issue выключен", cfg: withCreateIssueDisabled(), method: http.MethodPost, url: issueURL},
		{name: "метод PUT", cfg: allowedConfig, method: http.MethodPut, url: issueURL},
		{name: "метод GET", cfg: allowedConfig, method: http.MethodGet, url: issueURL},
		{name: "другой хост", cfg: allowedConfig, method: http.MethodPost, url: "https://evil.example.com/rest/api/2/issue"},
		{name: "заголовок Host другого хоста", cfg: allowedConfig, method: http.MethodPost, url: issueURL, host: "evil.example.com"},
		{name: "схема http", cfg: allowedConfig, method: http.MethodPost, url: "http://jira.example.com/rest/api/2/issue"},
		{name: "путь конкретной задачи", cfg: allowedConfig, method: http.MethodPost, url: issueURL + "/PROJ-1"},
		{name: "тело создания задачи на путь комментариев", cfg: withAddComment(), method: http.MethodPost, url: commentAPIURL},
		{name: "тело создания задачи на путь связей", cfg: withLinkIssues(), method: http.MethodPost, url: issueLinkURL},
		{name: "путь с завершающим слэшем", cfg: allowedConfig, method: http.MethodPost, url: issueURL + "/"},
		{name: "путь search", cfg: allowedConfig, method: http.MethodPost, url: "https://jira.example.com/rest/api/2/search"},
		{name: "путь без префикса base_url", cfg: withBasePathJira(), method: http.MethodPost, url: issueURL},
		{name: "непустой query", cfg: allowedConfig, method: http.MethodPost, url: issueURL + "?updateHistory=true"},
		{name: "пустой query со знаком вопроса", cfg: allowedConfig, method: http.MethodPost, url: issueURL + "?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.url, epicBody)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(t.Context(), tt.method, tt.url, strings.NewReader(epicBody))
			require.NoError(t, err)
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripBody(t *testing.T) {
	oversized := `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"` + strings.Repeat("x", guard.MaxBodyBytes) + `"}}`
	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{name: "Epic со всеми разрешёнными полями", body: epicBody, wantAllowed: true},
		{name: "Story только с summary", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"11203"},"summary":"s"}}`, wantAllowed: true},
		{name: "тип без дополнительных полей принимает summary", body: `{"fields":{"project":{"key":"OTHER"},"issuetype":{"id":"3"},"summary":"s"}}`, wantAllowed: true},

		{name: "без тела", body: ""},
		{name: "невалидный JSON", body: `{"fields":`},
		{name: "тело — массив", body: `[{"fields":{}}]`},
		{name: "мусор после JSON", body: epicBody + `{"update":{}}`},
		{name: "превышение размера", body: oversized},
		{name: "update рядом с fields", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s"},"update":{"labels":[{"add":"x"}]}}`},
		{name: "без fields", body: `{"update":{}}`},
		{name: "fields не объект", body: `{"fields":[]}`},
		{name: "fields null", body: `{"fields":null}`},
		{name: "fields повторяется", body: `{"fields":{"project":{"key":"SECRET"},"issuetype":{"id":"10000"},"summary":"s"},"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "без project", body: `{"fields":{"issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "чужой проект", body: `{"fields":{"project":{"key":"SECRET"},"issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "project повторяется", body: `{"fields":{"project":{"key":"SECRET"},"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "project с лишним ключом", body: `{"fields":{"project":{"key":"PROJ","id":"1"},"issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "key в project повторяется", body: `{"fields":{"project":{"key":"SECRET","key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "project по id", body: `{"fields":{"project":{"id":"1"},"issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "project строкой", body: `{"fields":{"project":"PROJ","issuetype":{"id":"10000"},"summary":"s"}}`},
		{name: "без issuetype", body: `{"fields":{"project":{"key":"PROJ"},"summary":"s"}}`},
		{name: "issuetype OTHER в проекте PROJ", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"3"},"summary":"s"}}`},
		{name: "issuetype по name", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"name":"Epic"},"summary":"s"}}`},
		{name: "issuetype id числом", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":10000},"summary":"s"}}`},
		{name: "id в issuetype повторяется", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"3","id":"10000"},"summary":"s"}}`},
		{name: "поле, не разрешённое типу", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s","customfield_10201":"PROJ-1"}}`},
		{name: "поле по ключу конфига вместо ID Jira", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s","epic_name":"x"}}`},
		{name: "issuelinks не входит в разрешённые поля", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s","issuelinks":[]}}`},
		{name: "описание у типа без полей", body: `{"fields":{"project":{"key":"OTHER"},"issuetype":{"id":"3"},"summary":"s","description":"d"}}`},
		{name: "assignee до запроса владельца токена", body: `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s","assignee":{"name":"token.owner"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, issueURL, tt.body)
			}
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, issueURL, body)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func TestRoundTripTokenOwnerLookupTarget(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		host        string
		body        io.Reader
		wantAllowed bool
	}{
		{name: "GET владельца токена", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL, wantAllowed: true},
		{name: "GET владельца токена с http.NoBody", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL, body: http.NoBody, wantAllowed: true},
		{name: "base_url с путём", cfg: withBasePathJira(), method: http.MethodGet, url: "https://jira.example.com/jira/rest/api/2/myself", wantAllowed: true},
		{name: "заголовок Host совпадает с base_url", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL, host: "jira.example.com", wantAllowed: true},

		{name: "ни у одного проекта нет assign_to_self", cfg: withoutAssignToSelf(), method: http.MethodGet, url: tokenOwnerURL},
		{name: "assign_to_self есть, tools.create_issue выключен", cfg: withCreateIssueDisabled(), method: http.MethodGet, url: tokenOwnerURL},
		{name: "GET с телом", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL, body: strings.NewReader(`{}`)},
		{name: "метод POST", cfg: allowedConfig, method: http.MethodPost, url: tokenOwnerURL},
		{name: "метод HEAD", cfg: allowedConfig, method: http.MethodHead, url: tokenOwnerURL},
		{name: "другой хост", cfg: allowedConfig, method: http.MethodGet, url: "https://evil.example.com/rest/api/2/myself"},
		{name: "заголовок Host другого хоста", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL, host: "evil.example.com"},
		{name: "схема http", cfg: allowedConfig, method: http.MethodGet, url: "http://jira.example.com/rest/api/2/myself"},
		{name: "путь с завершающим слэшем", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL + "/"},
		{name: "путь в процентной кодировке", cfg: allowedConfig, method: http.MethodGet, url: "https://jira.example.com/rest/api/2/my%73elf"},
		{name: "путь без префикса base_url", cfg: withBasePathJira(), method: http.MethodGet, url: tokenOwnerURL},
		{name: "путь user", cfg: allowedConfig, method: http.MethodGet, url: "https://jira.example.com/rest/api/2/user"},
		{name: "непустой query", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL + "?expand=groups"},
		{name: "пустой query со знаком вопроса", cfg: allowedConfig, method: http.MethodGet, url: tokenOwnerURL + "?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextTransport := mocks.NewMockRoundTripper(ctrl)
			if tt.wantAllowed {
				expectTokenOwnerLookup(t, nextTransport, tt.url, http.StatusOK, lookupResponseBody(ctrl, tokenOwnerBody, nil))
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(t.Context(), tt.method, tt.url, tt.body)
			require.NoError(t, err)
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			if !tt.wantAllowed {
				requireForbidden(t, response, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.NoError(t, response.Body.Close())
		})
	}
}

func epicWithAssignee(assigneeJSON string) string {
	return `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s","assignee":` + assigneeJSON + `}}`
}

func TestRoundTripAssigneeAfterTokenOwnerLookup(t *testing.T) {
	errBodyRead := errors.New("connection reset while reading body")
	oversizedTokenOwnerBody := `{"name":"token.owner","displayName":"` + strings.Repeat("x", guard.MaxBodyBytes) + `"}`
	tests := []struct {
		name          string
		lookupStatus  int
		lookupBody    string
		lookupReadErr error
		wantLookupErr error
		createBody    string
		wantAllowed   bool
	}{
		{
			name:         "assignee — запомненный владелец токена",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody:  epicWithAssignee(`{"name":"token.owner"}`),
			wantAllowed: true,
		},
		{
			name:         "без assignee после запроса владельца",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody:  `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"11203"},"summary":"s"}}`,
			wantAllowed: true,
		},

		{
			name:         "assignee — другой пользователь",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody: epicWithAssignee(`{"name":"someone.else"}`),
		},
		{
			name:         "ответ 500 — владелец не запомнен, assignee отклонён",
			lookupStatus: http.StatusInternalServerError, lookupBody: tokenOwnerBody,
			createBody: epicWithAssignee(`{"name":"token.owner"}`),
		},
		{
			name:         "ответ без name — пустой assignee отклонён",
			lookupStatus: http.StatusOK, lookupBody: `{"displayName":"Token Owner"}`,
			createBody: epicWithAssignee(`{"name":""}`),
		},
		{
			name:         "ответ не JSON — владелец не запомнен, assignee отклонён",
			lookupStatus: http.StatusOK, lookupBody: `<html>login</html>`,
			createBody: epicWithAssignee(`{"name":"token.owner"}`),
		},
		{
			name:         "ответ больше 1 MiB — ErrTokenOwnerResponse, assignee отклонён",
			lookupStatus: http.StatusOK, lookupBody: oversizedTokenOwnerBody,
			wantLookupErr: guard.ErrTokenOwnerResponse,
			createBody:    epicWithAssignee(`{"name":"token.owner"}`),
		},
		{
			name:         "ответ не читается — ErrTokenOwnerResponse, assignee отклонён",
			lookupStatus: http.StatusOK, lookupReadErr: errBodyRead,
			wantLookupErr: guard.ErrTokenOwnerResponse,
			createBody:    epicWithAssignee(`{"name":"token.owner"}`),
		},
		{
			name:         "assignee у проекта без assign_to_self",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody: `{"fields":{"project":{"key":"OTHER"},"issuetype":{"id":"3"},"summary":"s","assignee":{"name":"token.owner"}}}`,
		},
		{
			name:         "assignee с лишним ключом",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody: epicWithAssignee(`{"name":"token.owner","key":"x"}`),
		},
		{
			name:         "name в assignee повторяется",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody: epicWithAssignee(`{"name":"someone.else","name":"token.owner"}`),
		},
		{
			name:         "assignee строкой",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody: epicWithAssignee(`"token.owner"`),
		},
		{
			name:         "assignee по accountId",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody: epicWithAssignee(`{"accountId":"token.owner"}`),
		},
		{
			name:         `name числом при владельце "1"`,
			lookupStatus: http.StatusOK, lookupBody: `{"name":"1"}`,
			createBody: epicWithAssignee(`{"name":1}`),
		},
		{
			name:         "assignee null",
			lookupStatus: http.StatusOK, lookupBody: tokenOwnerBody,
			createBody: epicWithAssignee(`null`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextTransport := mocks.NewMockRoundTripper(ctrl)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)
			expectTokenOwnerLookup(t, nextTransport, tokenOwnerURL, tt.lookupStatus, lookupResponseBody(ctrl, tt.lookupBody, tt.lookupReadErr))
			lookupRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tokenOwnerURL, nil)
			require.NoError(t, err)

			lookupResponse, err := guardTransport.RoundTrip(lookupRequest)

			if tt.wantLookupErr != nil {
				require.ErrorIs(t, err, tt.wantLookupErr)
				require.Nil(t, lookupResponse)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.lookupStatus, lookupResponse.StatusCode)
				require.Equal(t, tt.lookupBody, readAll(t, lookupResponse.Body))
				require.NoError(t, lookupResponse.Body.Close())
			}

			if tt.wantAllowed {
				expectForwarded(t, nextTransport, issueURL, tt.createBody)
			}
			createRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, issueURL, strings.NewReader(tt.createBody))
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(createRequest)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func lookupResponseBody(ctrl *gomock.Controller, body string, readErr error) io.ReadCloser {
	responseBody := mocks.NewMockReadCloser(ctrl)
	if readErr != nil {
		responseBody.EXPECT().Read(gomock.Any()).Return(0, readErr).AnyTimes()
	} else {
		responseBody.EXPECT().Read(gomock.Any()).DoAndReturn(strings.NewReader(body).Read).AnyTimes()
	}
	responseBody.EXPECT().Close().Return(nil)
	return responseBody
}

func TestRoundTripReturnsNextTransportError(t *testing.T) {
	errNetwork := errors.New("network down")
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	nextTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, errNetwork)
	guardTransport, err := guard.New(allowedConfig, nextTransport)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, issueURL, strings.NewReader(epicBody))
	require.NoError(t, err)

	response, err := guardTransport.RoundTrip(request)

	require.ErrorIs(t, err, errNetwork)
	require.Nil(t, response)
}

func TestRoundTripForwardedGetBodyIgnoresIncomingGetBody(t *testing.T) {
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	expectForwarded(t, nextTransport, issueURL, epicBody)
	guardTransport, err := guard.New(allowedConfig, nextTransport)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, issueURL, strings.NewReader(epicBody))
	require.NoError(t, err)
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"fields":{"project":{"key":"SECRET"}},"update":{}}`)), nil
	}

	response, err := guardTransport.RoundTrip(request)

	requireOutcome(t, true, response, err)
}

func TestRoundTripClosesRequestBody(t *testing.T) {
	reportBody := attachmentBody(t, "report.md", "# Отчёт")
	tests := []struct {
		name        string
		method      string
		url         string
		header      http.Header
		body        string
		wantAllowed bool
	}{
		{name: "пропущенное создание", method: http.MethodPost, url: issueURL, body: epicBody, wantAllowed: true},
		{name: "создание отклонено по методу", method: http.MethodPut, url: issueURL, body: epicBody},
		{name: "создание отклонено по телу", method: http.MethodPost, url: issueURL, body: `{"update":{}}`},
		{name: "пропущенное вложение", method: http.MethodPost, url: attachURL, header: attachmentHeader(), body: reportBody, wantAllowed: true},
		{name: "вложение отклонено по телу", method: http.MethodPost, url: attachURL, header: attachmentHeader(), body: `{"fields":{}}`},
		{name: "пропущенный комментарий", method: http.MethodPost, url: commentAPIURL, header: jsonHeader(), body: commentBody, wantAllowed: true},
		{name: "комментарий отклонён по телу", method: http.MethodPost, url: commentAPIURL, header: jsonHeader(), body: `{"body":"x","visibility":{}}`},
		{name: "пропущенная связь", method: http.MethodPost, url: issueLinkURL, header: jsonHeader(), body: blocksLinkBody, wantAllowed: true},
		{name: "связь отклонена по телу", method: http.MethodPost, url: issueLinkURL, header: jsonHeader(), body: linkBody(duplicateTypeRef, proj7790Ref, proj7791Ref)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextTransport := mocks.NewMockRoundTripper(ctrl)
			if tt.wantAllowed {
				expectForwarded(t, nextTransport, tt.url, tt.body)
			}
			bodyReader := strings.NewReader(tt.body)
			requestBody := mocks.NewMockReadCloser(ctrl)
			requestBody.EXPECT().Read(gomock.Any()).DoAndReturn(bodyReader.Read).AnyTimes()
			requestBody.EXPECT().Close().Return(nil)
			guardTransport, err := guard.New(withAllTools(), nextTransport)
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(t.Context(), tt.method, tt.url, requestBody)
			require.NoError(t, err)
			if tt.header != nil {
				request.Header = tt.header
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantAllowed, response, err)
		})
	}
}

func expectForwarded(t *testing.T, nextTransport *mocks.MockRoundTripper, wantURL, wantBody string) {
	t.Helper()
	nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(forwarded *http.Request) (*http.Response, error) {
		require.Equal(t, wantURL, forwarded.URL.String())
		require.Equal(t, wantBody, readAll(t, forwarded.Body))
		require.Equal(t, int64(len(wantBody)), forwarded.ContentLength)
		require.NotNil(t, forwarded.GetBody)
		retryBody, err := forwarded.GetBody()
		require.NoError(t, err)
		require.Equal(t, wantBody, readAll(t, retryBody))
		return &http.Response{StatusCode: http.StatusCreated, Body: http.NoBody}, nil
	})
}

func expectTokenOwnerLookup(t *testing.T, nextTransport *mocks.MockRoundTripper, wantURL string, status int, body io.ReadCloser) {
	t.Helper()
	nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(forwarded *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, forwarded.Method)
		require.Equal(t, wantURL, forwarded.URL.String())
		require.True(t, forwarded.Body == nil || forwarded.Body == http.NoBody)
		return &http.Response{StatusCode: status, Body: body}, nil
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
		require.Equal(t, http.StatusCreated, response.StatusCode)
		return
	}
	requireForbidden(t, response, err)
}

func requireForbidden(t *testing.T, response *http.Response, err error) {
	t.Helper()
	require.ErrorIs(t, err, guard.ErrForbidden)
	require.Nil(t, response)
}
