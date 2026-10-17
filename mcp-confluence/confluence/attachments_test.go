package confluence_test

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/guard"
)

const (
	uploadURL         = baseURL + "/rest/api/content/201/child/attachment"
	uploadVersionURL  = uploadURL + "/501/data"
	reportDownloadURL = baseURL + "/download/attachments/201/report.md?api=v2&version=2"
	reportJSON        = `{"id":"att501","type":"attachment","status":"current","title":"report.md","extensions":{"mediaType":"text/markdown","fileSize":8,"comment":""},"version":{"number":2,"minorEdit":false},"_links":{"download":"/download/attachments/201/report.md?version=2&modificationDate=1&api=v2","webui":"/x"}}`
	schemeJSON        = `{"id":"502","type":"attachment","title":"отчёт v2,final.png","extensions":{"mediaType":"image/png","fileSize":1024},"version":{"number":1}}`
	noAttachmentsJSON = `{"results":[]}`
	reportListJSON    = `{"results":[` + reportJSON + `]}`
)

var (
	reportAttachment = confluence.Attachment{ID: "att501", Title: "report.md", MediaType: "text/markdown", Size: 8, Version: 2}
	schemeAttachment = confluence.Attachment{ID: "502", Title: "отчёт v2,final.png", MediaType: "image/png", Size: 1024, Version: 1}
	reportFile       = confluence.AttachmentFile{Name: "report.md", Content: []byte("# Отчёт\n")}
)

func attachmentsListURL(start int) string {
	return baseURL + "/rest/api/content/201/child/attachment?expand=version&limit=100&start=" + strconv.Itoa(start)
}

func TestListAttachments(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		want     []confluence.Attachment
		wantErr  error
	}{
		{name: "два вложения", response: jsonResponse(http.StatusOK, `{"results":[`+reportJSON+`,`+schemeJSON+`],"_links":{}}`), want: []confluence.Attachment{reportAttachment, schemeAttachment}},
		{name: "без fileSize → size 0", response: jsonResponse(http.StatusOK, `{"results":[{"id":"503","title":"x.md","extensions":{"mediaType":"text/plain"},"version":{"number":1}}]}`), want: []confluence.Attachment{{ID: "503", Title: "x.md", MediaType: "text/plain", Version: 1}}},
		{name: "пусто", response: jsonResponse(http.StatusOK, noAttachmentsJSON), want: []confluence.Attachment{}},
		{name: "404", response: jsonResponse(http.StatusNotFound, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrNotFound},
		{name: "500", response: markedHTMLResponse(http.StatusInternalServerError), wantErr: confluence.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			expectGet(t, harness.transport, attachmentsListURL(0)).Return(tt.response, nil)

			got, err := harness.client.ListAttachments(t.Context(), "201")

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
			requireNoResponseFragment(t, harness, err)
		})
	}
}

func binaryResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestDownloadAttachment(t *testing.T) {
	tests := []struct {
		name       string
		attachment confluence.Attachment
		wantURL    string
		maxBytes   int64
		response   *http.Response
		want       []byte
		wantErr    error
	}{
		{name: "содержимое как есть", attachment: reportAttachment, wantURL: reportDownloadURL, maxBytes: 64, response: binaryResponse("# Отчёт\n"), want: []byte("# Отчёт\n")},
		{name: "имя с пробелом и запятой кодируется стандартно", attachment: schemeAttachment, wantURL: baseURL + "/download/attachments/201/%D0%BE%D1%82%D1%87%D1%91%D1%82%20v2,final.png?api=v2&version=1", maxBytes: 64, response: binaryResponse("png"), want: []byte("png")},
		{name: "имя с ? и % кодируется в пути, не в query", attachment: confluence.Attachment{Title: "a?b%c.md", Version: 1}, wantURL: baseURL + "/download/attachments/201/a%3Fb%25c.md?api=v2&version=1", maxBytes: 64, response: binaryResponse("x"), want: []byte("x")},
		{name: "ровно max_bytes", attachment: reportAttachment, wantURL: reportDownloadURL, maxBytes: 3, response: binaryResponse("abc"), want: []byte("abc")},
		{name: "больше max_bytes → ErrAttachmentTooLarge без данных", attachment: reportAttachment, wantURL: reportDownloadURL, maxBytes: 3, response: binaryResponse("abcd"), wantErr: confluence.ErrAttachmentTooLarge},
		{name: "404", attachment: reportAttachment, wantURL: reportDownloadURL, maxBytes: 64, response: markedHTMLResponse(http.StatusNotFound), wantErr: confluence.ErrNotFound},
		{name: "500", attachment: reportAttachment, wantURL: reportDownloadURL, maxBytes: 64, response: markedHTMLResponse(http.StatusInternalServerError), wantErr: confluence.ErrUnavailable},
		{name: "редирект", attachment: reportAttachment, wantURL: reportDownloadURL, maxBytes: 64, response: redirectResponse("https://login.example.com/"), wantErr: confluence.ErrUnavailable},
		{name: "тело не читается", attachment: reportAttachment, wantURL: reportDownloadURL, maxBytes: 64, response: unreadableResponse(http.StatusOK), wantErr: confluence.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			harness.transport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, sent.Method)
				require.Equal(t, tt.wantURL, sent.URL.String())
				require.Empty(t, sent.URL.RawPath)
				require.Equal(t, http.Header{"Authorization": {"Bearer " + token}, "Accept": {"*/*"}}, sent.Header)
				require.True(t, sent.Body == nil || sent.Body == http.NoBody)
				return tt.response, nil
			})

			got, err := harness.client.DownloadAttachment(t.Context(), "201", tt.attachment, tt.maxBytes)

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
			requireNoResponseFragment(t, harness, err)
		})
	}
}

func expectUpload(t *testing.T, harness clientHarness, wantURL string, wantFile confluence.AttachmentFile) *gomock.Call {
	t.Helper()
	return harness.transport.EXPECT().RoundTrip(gomock.Any()).Do(func(sent *http.Request) {
		require.Equal(t, http.MethodPost, sent.Method)
		require.Equal(t, wantURL, sent.URL.String())
		require.Equal(t, []string{"Bearer " + token}, sent.Header.Values("Authorization"))
		require.Equal(t, []string{"application/json"}, sent.Header.Values("Accept"))
		require.Equal(t, []string{"no-check"}, sent.Header.Values("X-Atlassian-Token"))
		require.Len(t, sent.Header, 4)
		require.Empty(t, sent.TransferEncoding)
		require.Nil(t, sent.Trailer)
		mediaType, params, err := mime.ParseMediaType(sent.Header.Get("Content-Type"))
		require.NoError(t, err)
		require.Equal(t, "multipart/form-data", mediaType)
		sentBody, err := io.ReadAll(sent.Body)
		require.NoError(t, err)
		require.Equal(t, int64(len(sentBody)), sent.ContentLength)
		reader := multipart.NewReader(strings.NewReader(string(sentBody)), params["boundary"])
		part, err := reader.NextRawPart()
		require.NoError(t, err)
		require.Equal(t, "file", part.FormName())
		require.Equal(t, wantFile.Name, part.FileName())
		require.Equal(t, "application/octet-stream", part.Header.Get("Content-Type"))
		content, err := io.ReadAll(part)
		require.NoError(t, err)
		require.Equal(t, wantFile.Content, content)
		_, err = reader.NextRawPart()
		require.ErrorIs(t, err, io.EOF)
	})
}

func TestUploadAttachment(t *testing.T) {
	unprefixedReportListJSON := strings.Replace(reportListJSON, `"id":"att501"`, `"id":"501"`, 1)
	malformedIDReportListJSON := strings.Replace(reportListJSON, `"id":"att501"`, `"id":"file501"`, 1)
	tests := []struct {
		name           string
		listBatches    []listedBatch
		wantUploadURL  string
		uploadResponse *http.Response
		uploadErr      error
		want           confluence.Attachment
		wantErr        error
	}{
		{name: "имя свободно → создание, ответ results[0]", listBatches: singleBatch(jsonResponse(http.StatusOK, `{"results":[`+schemeJSON+`]}`)), wantUploadURL: uploadURL, uploadResponse: jsonResponse(http.StatusOK, `{"results":[`+reportJSON+`],"size":1}`), want: reportAttachment},
		{name: "имя занято → новая версия через /data, ответ объектом", listBatches: singleBatch(jsonResponse(http.StatusOK, reportListJSON)), wantUploadURL: uploadVersionURL, uploadResponse: jsonResponse(http.StatusOK, strings.Replace(reportJSON, `"number":2`, `"number":3`, 1)), want: confluence.Attachment{ID: "att501", Title: "report.md", MediaType: "text/markdown", Size: 8, Version: 3}},
		{name: "имя занято, id в списке без префикса att → /data с тем же числом", listBatches: singleBatch(jsonResponse(http.StatusOK, unprefixedReportListJSON)), wantUploadURL: uploadVersionURL, uploadResponse: jsonResponse(http.StatusOK, reportJSON), want: reportAttachment},
		{name: "имя занято, id в списке не по шаблону → ErrUnavailable, загрузки нет", listBatches: singleBatch(jsonResponse(http.StatusOK, malformedIDReportListJSON)), wantErr: confluence.ErrUnavailable},
		{
			name: "имя занято во второй пачке списка → новая версия",
			listBatches: []listedBatch{
				{start: 0, response: jsonResponse(http.StatusOK, `{"results":[`+schemeJSON+`],"_links":{"next":"/x"}}`)},
				{start: 1, response: jsonResponse(http.StatusOK, reportListJSON)},
			},
			wantUploadURL: uploadVersionURL, uploadResponse: jsonResponse(http.StatusOK, reportJSON), want: reportAttachment,
		},
		{name: "создание: 200 с пустым results → ErrMaybeWritten", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadResponse: jsonResponse(http.StatusOK, noAttachmentsJSON), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: 200 без id → ErrMaybeWritten", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadResponse: jsonResponse(http.StatusOK, `{"results":[{"title":"`+bodyMarker+`"}]}`), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: 200 не JSON → ErrMaybeWritten", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadResponse: markedHTMLResponse(http.StatusOK), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: 403 → ErrNotFound", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadResponse: jsonResponse(http.StatusForbidden, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrNotFound},
		{name: "создание: 413 → ErrRejected", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadResponse: markedHTMLResponse(http.StatusRequestEntityTooLarge), wantErr: confluence.ErrRejected},
		{name: "создание: 500 → ErrMaybeWritten", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadResponse: markedHTMLResponse(http.StatusInternalServerError), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: сетевая ошибка → ErrMaybeWritten", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadErr: fmt.Errorf("connection reset %s", bodyMarker), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: отказ guard по форме → ErrForbidden", listBatches: singleBatch(jsonResponse(http.StatusOK, noAttachmentsJSON)), wantUploadURL: uploadURL, uploadErr: fmt.Errorf("%w: extension", guard.ErrForbidden), wantErr: confluence.ErrForbidden},
		{name: "новая версия: сбой проверки зоны → ErrUnavailable, не ErrMaybeWritten", listBatches: singleBatch(jsonResponse(http.StatusOK, reportListJSON)), wantUploadURL: uploadVersionURL, uploadErr: fmt.Errorf("%w: status 500", guard.ErrZoneCheckFailed), wantErr: confluence.ErrUnavailable},
		{name: "новая версия: 200 без id → ErrMaybeWritten", listBatches: singleBatch(jsonResponse(http.StatusOK, reportListJSON)), wantUploadURL: uploadVersionURL, uploadResponse: jsonResponse(http.StatusOK, `{"title":"`+bodyMarker+`"}`), wantErr: confluence.ErrMaybeWritten},
		{name: "список вложений недоступен → ошибка чтения, загрузки нет", listBatches: singleBatch(markedHTMLResponse(http.StatusBadGateway)), wantErr: confluence.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			calls := make([]any, 0, len(tt.listBatches)+1)
			for _, batch := range tt.listBatches {
				calls = append(calls, expectGet(t, harness.transport, attachmentsListURL(batch.start)).Return(batch.response, nil))
			}
			if tt.wantUploadURL != "" {
				calls = append(calls, expectUpload(t, harness, tt.wantUploadURL, reportFile).Return(tt.uploadResponse, tt.uploadErr))
			}
			gomock.InOrder(calls...)

			got, err := harness.client.UploadAttachment(t.Context(), "201", reportFile)

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
			requireNoResponseFragment(t, harness, err)
		})
	}
}
