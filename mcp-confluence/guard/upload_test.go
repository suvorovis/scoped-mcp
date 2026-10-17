package guard_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/guard"
	"scoped-mcp/mcp-confluence/guard/mocks"
)

const (
	uploadedAttachmentID      = "501"
	uploadURL                 = contentURL + "/" + writeChildID + "/child/attachment"
	uploadBoundary            = "a1b2c3"
	uploadContentType         = "multipart/form-data; boundary=" + uploadBoundary
	quotedBoundaryContentType = `multipart/form-data; boundary="` + uploadBoundary + `"`
	reportDisposition         = `form-data; name="file"; filename="report.md"`
	filePartContentType       = "application/octet-stream"
	uploadOpeningDelimiter    = "--" + uploadBoundary + "\r\n"
	uploadPartDelimiter       = "\r\n--" + uploadBoundary + "\r\n"
	uploadClosingDelimiter    = "\r\n--" + uploadBoundary + "--\r\n"
	attachmentNotChecked      = ""

	attachmentOfWriteChildBody              = `{"id":"501","type":"attachment","status":"current","title":"report.md","container":{"id":"201","type":"page"}}`
	attachmentOfOtherPageBody               = `{"id":"501","type":"attachment","status":"current","title":"report.md","container":{"id":"300","type":"page"}}`
	attachmentOfBlogpostBody                = `{"id":"501","type":"attachment","status":"current","title":"report.md","container":{"id":"201","type":"blogpost"}}`
	attachmentOfSpaceBody                   = `{"id":"501","type":"attachment","status":"current","title":"report.md","container":{"id":201,"type":"global"}}`
	attachmentOfWriteChildWithNumericIDBody = `{"id":"501","type":"attachment","status":"current","title":"report.md","container":{"id":201,"type":"page"}}`
	trashedAttachmentBody                   = `{"id":"501","type":"attachment","status":"trashed","title":"report.md","container":{"id":"201","type":"page"}}`
	prefixedAttachmentOfWriteChildBody      = `{"id":"att501","type":"attachment","status":"current","title":"report.md","container":{"id":"201","type":"page"}}`
	otherPrefixedAttachmentBody             = `{"id":"att999","type":"attachment","status":"current","title":"report.md","container":{"id":"201","type":"page"}}`
	pageInsteadOfAttachmentBody             = `{"id":"501","type":"page","status":"current","title":"x","space":{"key":"DOCS"},"ancestors":[{"id":"200"}]}`
)

var uploadVersionURL = uploadVersionURLAt(baseURL, writeChildID)

func uploadURLAt(base, pageID string) string {
	return base + "/rest/api/content/" + pageID + "/child/attachment"
}

func uploadVersionURLAt(base, pageID string) string {
	return uploadURLAt(base, pageID) + "/" + uploadedAttachmentID + "/data"
}

func attachmentBody(t *testing.T, fileName, content string) string {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.SetBoundary(uploadBoundary))
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
	return uploadOpeningDelimiter + strings.Join(parts, uploadPartDelimiter) + uploadClosingDelimiter
}

func uploadRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	request := newRequest(t, method, url, bodyReader)
	request.Header.Set("Content-Type", uploadContentType)
	request.Header.Set("X-Atlassian-Token", "no-check")
	return request
}

func withoutAttachmentsSection() config.Config {
	cfg := allowedConfig
	cfg.Attachments = nil
	return cfg
}

func expectPageAndAttachmentZoneChecks(t *testing.T, nextTransport *mocks.MockRoundTripper, base, pageID, pageZoneCheckResponse, attachmentZoneCheckResponse string) []any {
	t.Helper()
	calls := []any{expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(base, pageID), pageZoneCheckResponse)}
	if attachmentZoneCheckResponse == attachmentNotChecked {
		return calls
	}
	return append(calls, expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(base, uploadedAttachmentID), attachmentZoneCheckResponse))
}

func TestRoundTripUploadAttachmentTarget(t *testing.T) {
	tests := []struct {
		name                        string
		cfg                         config.Config
		method                      string
		url                         string
		attachmentZoneCheckResponse string
		wantErr                     error
	}{
		{name: "новое вложение к потомку корня записи", cfg: allowedConfig, method: http.MethodPost, url: uploadURL},
		{name: "base_url с путём", cfg: withBasePath(), method: http.MethodPost, url: uploadURLAt(basePathURL, writeChildID)},
		{name: "включён только upload_attachment", cfg: withTools(config.Tools{UploadAttachment: true}), method: http.MethodPost, url: uploadURL},
		{name: "новая версия вложения", cfg: allowedConfig, method: http.MethodPost, url: uploadVersionURL, attachmentZoneCheckResponse: attachmentOfWriteChildBody},
		{name: "новая версия вложения, base_url с путём", cfg: withBasePath(), method: http.MethodPost, url: uploadVersionURLAt(basePathURL, writeChildID), attachmentZoneCheckResponse: attachmentOfWriteChildBody},

		{name: "upload_attachment выключен", cfg: withTools(config.Tools{ListAttachments: true}), method: http.MethodPost, url: uploadURL, wantErr: guard.ErrForbidden},
		{name: "upload_attachment выключен для новой версии", cfg: withTools(config.Tools{ListAttachments: true}), method: http.MethodPost, url: uploadVersionURL, wantErr: guard.ErrForbidden},
		{name: "нет секции attachments", cfg: withoutAttachmentsSection(), method: http.MethodPost, url: uploadURL, wantErr: guard.ErrForbidden},
		{name: "нет секции attachments для новой версии", cfg: withoutAttachmentsSection(), method: http.MethodPost, url: uploadVersionURL, wantErr: guard.ErrForbidden},
		{name: "PUT вложения", cfg: allowedConfig, method: http.MethodPut, url: uploadURL + "/" + uploadedAttachmentID, wantErr: guard.ErrForbidden},
		{name: "DELETE вложения", cfg: allowedConfig, method: http.MethodDelete, url: uploadURL + "/" + uploadedAttachmentID, wantErr: guard.ErrForbidden},
		{name: "POST на конкретное вложение без data", cfg: allowedConfig, method: http.MethodPost, url: uploadURL + "/" + uploadedAttachmentID, wantErr: guard.ErrForbidden},
		{name: "id вложения не число", cfg: allowedConfig, method: http.MethodPost, url: uploadURL + "/abc/data", wantErr: guard.ErrForbidden},
		{name: "id вложения с префиксом att в пути", cfg: allowedConfig, method: http.MethodPost, url: uploadURL + "/att" + uploadedAttachmentID + "/data", wantErr: guard.ErrForbidden},
		{name: "лишний сегмент после data", cfg: allowedConfig, method: http.MethodPost, url: uploadVersionURL + "/x", wantErr: guard.ErrForbidden},
		{name: "query status у нового вложения", cfg: allowedConfig, method: http.MethodPost, url: uploadURL + "?status=draft", wantErr: guard.ErrForbidden},
		{name: "query minorEdit у новой версии", cfg: allowedConfig, method: http.MethodPost, url: uploadVersionURL + "?minorEdit=true", wantErr: guard.ErrForbidden},
		{name: "пустой query со знаком вопроса", cfg: allowedConfig, method: http.MethodPost, url: uploadURL + "?", wantErr: guard.ErrForbidden},
		{name: "путь с завершающим слэшем", cfg: allowedConfig, method: http.MethodPost, url: uploadURL + "/", wantErr: guard.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestBody := attachmentBody(t, "report.md", "# Отчёт")
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantErr == nil {
				calls := expectPageAndAttachmentZoneChecks(t, nextTransport, tt.cfg.BaseURL, writeChildID, writeChildBody, tt.attachmentZoneCheckResponse)
				gomock.InOrder(append(calls, expectForwarded(t, nextTransport, http.MethodPost, tt.url, requestBody))...)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(uploadRequest(t, tt.method, tt.url, requestBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripUploadAttachmentZone(t *testing.T) {
	tests := []struct {
		name              string
		pageID            string
		zoneCheckResponse string
		wantErr           error
	}{
		{name: "потомок корня записи", pageID: writeChildID, zoneCheckResponse: writeChildBody},
		{name: "корень записи", pageID: "200", zoneCheckResponse: writeRootBody},

		{name: "корень чтения — вне зоны записи", pageID: "100", zoneCheckResponse: rootBody, wantErr: guard.ErrOutOfZone},
		{name: "потомок корня чтения — вне зоны записи", pageID: "101", zoneCheckResponse: childBody, wantErr: guard.ErrOutOfZone},
		{name: "страница вне зоны", pageID: "300", zoneCheckResponse: outsideBody, wantErr: guard.ErrOutOfZone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageUploadURL := uploadURLAt(baseURL, tt.pageID)
			requestBody := attachmentBody(t, "report.md", "# Отчёт")
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectZoneCheckThenForward(t, nextTransport, zoneCheckURLAt(baseURL, tt.pageID), tt.zoneCheckResponse, tt.wantErr, http.MethodPost, pageUploadURL, requestBody)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(uploadRequest(t, http.MethodPost, pageUploadURL, requestBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripUploadAttachmentVersionZone(t *testing.T) {
	tests := []struct {
		name                        string
		pageID                      string
		pageZoneCheckResponse       string
		attachmentZoneCheckResponse string
		wantErr                     error
	}{
		{name: "вложение той же страницы", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: attachmentOfWriteChildBody},
		{name: "вложение той же страницы, id в ответе с префиксом att", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: prefixedAttachmentOfWriteChildBody},
		{name: "вложение той же страницы, id контейнера числом", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: attachmentOfWriteChildWithNumericIDBody},

		{name: "вложение чужой страницы", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: attachmentOfOtherPageBody, wantErr: guard.ErrOutOfZone},
		{name: "вложение блога с тем же id контейнера", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: attachmentOfBlogpostBody, wantErr: guard.ErrOutOfZone},
		{name: "вложение пространства с числовым id, равным id страницы", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: attachmentOfSpaceBody, wantErr: guard.ErrOutOfZone},
		{name: "удалённое вложение", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: trashedAttachmentBody, wantErr: guard.ErrOutOfZone},
		{name: "id страницы вместо вложения", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: pageInsteadOfAttachmentBody, wantErr: guard.ErrOutOfZone},
		{name: "в ответе id другого вложения с префиксом att", pageID: writeChildID, pageZoneCheckResponse: writeChildBody, attachmentZoneCheckResponse: otherPrefixedAttachmentBody, wantErr: guard.ErrZoneCheckFailed},
		{name: "страница вне зоны записи — вложение не проверяется", pageID: "101", pageZoneCheckResponse: childBody, attachmentZoneCheckResponse: attachmentNotChecked, wantErr: guard.ErrOutOfZone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			versionURL := uploadVersionURLAt(baseURL, tt.pageID)
			requestBody := attachmentBody(t, "report.md", "# Отчёт")
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			calls := expectPageAndAttachmentZoneChecks(t, nextTransport, baseURL, tt.pageID, tt.pageZoneCheckResponse, tt.attachmentZoneCheckResponse)
			if tt.wantErr == nil {
				calls = append(calls, expectForwarded(t, nextTransport, http.MethodPost, versionURL, requestBody))
			}
			gomock.InOrder(calls...)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(uploadRequest(t, http.MethodPost, versionURL, requestBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripUploadAttachmentBody(t *testing.T) {
	tests := []struct {
		name        string
		requestBody string
	}{
		{name: "один файл с разрешённым расширением", requestBody: attachmentBody(t, "report.md", "# Отчёт")},
		{name: "последний суффикс .png против .PNG в конфиге", requestBody: attachmentBody(t, "scheme.drawio.png", "png")},
		{name: "имя файла кириллицей с пробелом", requestBody: attachmentBody(t, "отчёт v2.md", "x")},
		{name: "содержимое ровно max_bytes", requestBody: attachmentBody(t, "report.md", strings.Repeat("x", attachmentsMaxBytes))},
		{name: "пустой файл", requestBody: attachmentBody(t, "report.md", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectZoneCheckThenForward(t, nextTransport, zoneCheckURLAt(baseURL, writeChildID), writeChildBody, nil, http.MethodPost, uploadURL, tt.requestBody)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(uploadRequest(t, http.MethodPost, uploadURL, tt.requestBody))

			requireOutcome(t, nil, response, err)
		})
	}
}

func TestRoundTripRejectsUploadAttachmentBody(t *testing.T) {
	reportBody := attachmentBody(t, "report.md", "# Отчёт")
	longBoundary := strings.Repeat("b", 71)
	tests := []struct {
		name            string
		headerOverrides http.Header
		requestBody     string
	}{
		{name: "без X-Atlassian-Token", headerOverrides: http.Header{"X-Atlassian-Token": nil}, requestBody: reportBody},
		{name: "X-Atlassian-Token nocheck", headerOverrides: http.Header{"X-Atlassian-Token": {"nocheck"}}, requestBody: reportBody},
		{name: "X-Atlassian-Token дважды", headerOverrides: http.Header{"X-Atlassian-Token": {"no-check", "no-check"}}, requestBody: reportBody},
		{name: "без Content-Type", headerOverrides: http.Header{"Content-Type": nil}, requestBody: reportBody},
		{name: "Content-Type JSON", headerOverrides: http.Header{"Content-Type": {"application/json"}}, requestBody: reportBody},
		{name: "Content-Type multipart/mixed", headerOverrides: http.Header{"Content-Type": {"multipart/mixed; boundary=" + uploadBoundary}}, requestBody: reportBody},
		{name: "Content-Type без boundary", headerOverrides: http.Header{"Content-Type": {"multipart/form-data"}}, requestBody: reportBody},
		{name: "Content-Type с лишним параметром", headerOverrides: http.Header{"Content-Type": {uploadContentType + "; charset=utf-8"}}, requestBody: reportBody},
		{name: "Content-Type дважды", headerOverrides: http.Header{"Content-Type": {uploadContentType, uploadContentType}}, requestBody: reportBody},
		{name: "неканонический content-type с другим boundary", headerOverrides: http.Header{"content-type": {"multipart/form-data; boundary=zzz"}}, requestBody: reportBody},
		{name: "boundary не совпадает с телом", headerOverrides: http.Header{"Content-Type": {"multipart/form-data; boundary=zzz"}}, requestBody: reportBody},
		{name: "параметр boundary дважды с разными значениями", headerOverrides: http.Header{"Content-Type": {uploadContentType + "; boundary=zzz"}}, requestBody: reportBody},
		{name: "boundary длиннее 70 символов", headerOverrides: http.Header{"Content-Type": {"multipart/form-data; boundary=" + longBoundary}}, requestBody: strings.ReplaceAll(reportBody, uploadBoundary, longBoundary)},
		{name: "boundary с пробелом в конце", headerOverrides: http.Header{"Content-Type": {`multipart/form-data; boundary="` + uploadBoundary + ` "`}}, requestBody: strings.ReplaceAll(reportBody, uploadBoundary, uploadBoundary+" ")},

		{name: "без тела", requestBody: ""},
		{name: "тело не multipart", requestBody: `{"type":"page"}`},
		{name: "содержимое больше max_bytes", requestBody: attachmentBody(t, "report.md", strings.Repeat("x", attachmentsMaxBytes+1))},
		{name: "тело больше max_bytes и 64 KiB оболочки", requestBody: attachmentBody(t, "report.md", strings.Repeat("x", attachmentsMaxBytes+guard.AttachmentEnvelopeBytes))},
		{name: "расширение не из конфига", requestBody: attachmentBody(t, "run.sh", "x")},
		{name: "имя без расширения", requestBody: attachmentBody(t, "README", "x")},
		{name: "пустое имя файла", requestBody: attachmentBody(t, "", "x")},
		{name: "имя файла со слэшем", requestBody: attachmentBody(t, "../report.md", "x")},
		{name: "имя файла с обратным слэшем", requestBody: attachmentBody(t, `..\report.md`, "x")},
		{name: "имя файла кириллицей и с кавычками", requestBody: attachmentBody(t, `отчёт "v2".md`, "x")},
		{name: "имя файла с ;", requestBody: attachmentBody(t, "report;v2.md", "x")},
		{name: "имя файла с CR", requestBody: rawMultipartBody(rawPart(partHeaders("form-data; name=\"file\"; filename=\"report\rv2.md\"", filePartContentType), "x"))},
		{name: "имя файла с TAB", requestBody: attachmentBody(t, "report\tv2.md", "x")},
		{name: "имя файла с управляющим символом C1", requestBody: attachmentBody(t, "report\u0085v2.md", "x")},
		{name: "имя файла с U+202E", requestBody: attachmentBody(t, "report\u202egnp.md", "x")},
		{name: "поле не file", requestBody: rawMultipartBody(rawPart(partHeaders(`form-data; name="upload"; filename="report.md"`, filePartContentType), "x"))},
		{name: "часть без filename", requestBody: rawMultipartBody(rawPart(partHeaders(`form-data; name="file"`, filePartContentType), "x"))},
		{name: "поле comment второй частью", requestBody: rawMultipartBody(rawPart(partHeaders(reportDisposition, filePartContentType), "x"), rawPart(partHeaders(`form-data; name="comment"`, ""), "hi"))},
		{name: "поле minorEdit второй частью", requestBody: rawMultipartBody(rawPart(partHeaders(reportDisposition, filePartContentType), "x"), rawPart(partHeaders(`form-data; name="minorEdit"`, ""), "true"))},
		{name: "filename* с другим расширением рядом с filename", requestBody: rawMultipartBody(rawPart(partHeaders(reportDisposition+"; filename*=UTF-8''run.sh", filePartContentType), "x"))},
		{name: "Content-Disposition attachment", requestBody: rawMultipartBody(rawPart(partHeaders(`attachment; name="file"; filename="report.md"`, filePartContentType), "x"))},
		{name: "часть с Content-Type text/plain", requestBody: rawMultipartBody(rawPart(partHeaders(reportDisposition, "text/plain"), "x"))},
		{name: "часть без Content-Type", requestBody: rawMultipartBody(rawPart(partHeaders(reportDisposition, ""), "x"))},
		{name: "часть с Content-Transfer-Encoding", requestBody: rawMultipartBody(rawPart(partHeaders(reportDisposition, filePartContentType)+"Content-Transfer-Encoding: base64\r\n", "eA=="))},
		{name: "вторая часть file", requestBody: rawMultipartBody(rawPart(partHeaders(reportDisposition, filePartContentType), "x"), rawPart(partHeaders(reportDisposition, filePartContentType), "y"))},
		{name: "преамбула перед первой частью", requestBody: "preamble\r\n" + reportBody},
		{name: "мусор после закрывающего разделителя", requestBody: reportBody + "garbage"},
		{name: "нет закрывающего разделителя", requestBody: uploadOpeningDelimiter + rawPart(partHeaders(reportDisposition, filePartContentType), "x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)
			request := uploadRequest(t, http.MethodPost, uploadURL, tt.requestBody)
			for key, values := range tt.headerOverrides {
				request.Header[key] = values
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, guard.ErrForbidden, response, err)
		})
	}
}

func TestRoundTripForwardsCanonicalMultipart(t *testing.T) {
	tests := []struct {
		name                        string
		url                         string
		attachmentZoneCheckResponse string
	}{
		{name: "новое вложение", url: uploadURL, attachmentZoneCheckResponse: attachmentNotChecked},
		{name: "новая версия вложения", url: uploadVersionURL, attachmentZoneCheckResponse: attachmentOfWriteChildBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestBody := attachmentBody(t, "report.md", "# Отчёт")
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			calls := expectPageAndAttachmentZoneChecks(t, nextTransport, baseURL, writeChildID, writeChildBody, tt.attachmentZoneCheckResponse)
			calls = append(calls, nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(forwarded *http.Request) (*http.Response, error) {
				require.Equal(t, []string{uploadContentType}, forwarded.Header.Values("Content-Type"))
				require.Equal(t, []string{"no-check"}, forwarded.Header.Values("X-Atlassian-Token"))
				require.Equal(t, requestBody, readAll(t, forwarded.Body))
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			}))
			gomock.InOrder(calls...)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)
			request := uploadRequest(t, http.MethodPost, tt.url, requestBody)
			request.Header.Set("Content-Type", quotedBoundaryContentType)

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, nil, response, err)
		})
	}
}
