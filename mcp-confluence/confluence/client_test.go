package confluence_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/guard"
	"scoped-mcp/mcp-confluence/guard/mocks"
)

const (
	baseURL = "https://confluence.example.com"
	token   = "pat-secret"
	pageURL = baseURL + "/rest/api/content/101?expand=body.storage%2Cversion%2Cancestors%2Cspace"

	pageBody = `{"id":"101","type":"page","status":"current","title":"Child","space":{"key":"DOCS","name":"Documentation"},` +
		`"history":{"createdBy":{"username":"someone","displayName":"Some One"},"createdDate":"2026-10-01T10:00:00.000+03:00"},` +
		`"version":{"by":{"displayName":"Some One"},"when":"2026-10-02T10:00:00.000+03:00","number":4,"minorEdit":false},` +
		`"ancestors":[{"id":"1","type":"page","title":"Space home"},{"id":"100","type":"page","title":"Root"}],` +
		`"body":{"storage":{"value":"<p>Текст</p>","representation":"storage"}},` +
		`"metadata":{"labels":{"results":[{"name":"secret-label"}]}},"restrictions":{"read":{"restrictions":{"user":{"results":[]}}}},` +
		`"_links":{"webui":"/display/DOCS/Child","self":"https://confluence.example.com/rest/api/content/101","base":"https://confluence.example.com"}}`
	bodyMarker = "SECRET-SPACE"
)

type clientHarness struct {
	transport *mocks.MockRoundTripper
	logs      *bytes.Buffer
	client    *confluence.Client
}

func newHarness(t *testing.T, clientBaseURL string) clientHarness {
	t.Helper()
	transport := mocks.NewMockRoundTripper(gomock.NewController(t))
	logs := &bytes.Buffer{}
	client, err := confluence.New(clientBaseURL, token, transport, log.New(logs, "", 0))
	require.NoError(t, err)
	return clientHarness{transport: transport, logs: logs, client: client}
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{StatusCode: statusCode, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func markedHTMLResponse(statusCode int) *http.Response {
	return &http.Response{StatusCode: statusCode, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("<html>" + bodyMarker + "</html>"))}
}

func emptyResponse(statusCode int) *http.Response {
	return &http.Response{StatusCode: statusCode, Header: http.Header{}, Body: http.NoBody}
}

func unreadableResponse(statusCode int) *http.Response {
	return &http.Response{StatusCode: statusCode, Header: http.Header{}, Body: io.NopCloser(iotest.ErrReader(errors.New("body read failed")))}
}

func redirectResponse(location string) *http.Response {
	response := emptyResponse(http.StatusFound)
	response.Header.Set("Location", location)
	return response
}

func expectGet(t *testing.T, transport *mocks.MockRoundTripper, wantURL string) *gomock.Call {
	t.Helper()
	return transport.EXPECT().RoundTrip(gomock.Any()).Do(func(sent *http.Request) {
		require.Equal(t, http.MethodGet, sent.Method)
		require.Equal(t, wantURL, sent.URL.String())
		require.Empty(t, sent.URL.RawPath)
		require.Equal(t, http.Header{"Authorization": {"Bearer " + token}, "Accept": {"application/json"}}, sent.Header)
		require.True(t, sent.Body == nil || sent.Body == http.NoBody)
	})
}

type listedBatch struct {
	start    int
	response *http.Response
}

func singleBatch(response *http.Response) []listedBatch {
	return []listedBatch{{start: 0, response: response}}
}

func expectJSONWrite(t *testing.T, transport *mocks.MockRoundTripper, wantMethod, wantURL, wantBody string) *gomock.Call {
	t.Helper()
	return transport.EXPECT().RoundTrip(gomock.Any()).Do(func(sent *http.Request) {
		require.Equal(t, wantMethod, sent.Method)
		require.Equal(t, wantURL, sent.URL.String())
		require.Equal(t, http.Header{"Authorization": {"Bearer " + token}, "Accept": {"application/json"}, "Content-Type": {"application/json"}}, sent.Header)
		require.Empty(t, sent.TransferEncoding)
		require.Nil(t, sent.Trailer)
		sentBody, err := io.ReadAll(sent.Body)
		require.NoError(t, err)
		require.Equal(t, int64(len(sentBody)), sent.ContentLength)
		require.JSONEq(t, wantBody, string(sentBody))
	})
}

func requireSentinel(t *testing.T, wantErr, err error) {
	t.Helper()
	if wantErr == nil {
		require.NoError(t, err)
		return
	}
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, wantErr, err)
}

func requireNoResponseFragment(t *testing.T, harness clientHarness, err error) {
	t.Helper()
	require.NotContains(t, fmt.Sprint(err), bodyMarker)
	require.NotContains(t, harness.logs.String(), bodyMarker)
}

func TestNewRejectsUnparsableBaseURL(t *testing.T) {
	_, err := confluence.New("https://confluence.example.com/%zz", token, mocks.NewMockRoundTripper(gomock.NewController(t)), log.New(io.Discard, "", 0))

	require.Error(t, err)
}

func TestTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	errMalformedResponse := errors.New(`net/http: malformed HTTP response "` + bodyMarker + `"`)
	tests := []struct {
		name         string
		run          func(ctx context.Context, client *confluence.Client) error
		transportErr error
		wantErr      error
	}{
		{name: "чтение: сетевая ошибка → ErrUnavailable", run: getPage, transportErr: errNetwork, wantErr: confluence.ErrUnavailable},
		{name: "чтение: таймаут → ErrUnavailable", run: getPage, transportErr: context.DeadlineExceeded, wantErr: confluence.ErrUnavailable},
		{name: "чтение: отмена → ErrUnavailable", run: getPage, transportErr: context.Canceled, wantErr: confluence.ErrUnavailable},
		{name: "чтение: текст ошибки с фрагментом ответа → не в логе и не в ошибке", run: getPage, transportErr: errMalformedResponse, wantErr: confluence.ErrUnavailable},
		{name: "чтение: вне зоны → ErrNotFound", run: getPage, transportErr: fmt.Errorf("%w: 101", guard.ErrOutOfZone), wantErr: confluence.ErrNotFound},
		{name: "чтение: сбой проверки зоны → ErrUnavailable", run: getPage, transportErr: fmt.Errorf("%w: status 500", guard.ErrZoneCheckFailed), wantErr: confluence.ErrUnavailable},
		{name: "чтение: 401 на проверке зоны → ErrUnauthorized", run: getPage, transportErr: guard.ErrTokenRejected, wantErr: confluence.ErrUnauthorized},
		{name: "чтение: отказ guard по форме → голый ErrForbidden", run: getPage, transportErr: fmt.Errorf("%w: GET /x", guard.ErrForbidden), wantErr: confluence.ErrForbidden},
		{name: "чтение: комментарии, сетевая ошибка → ErrUnavailable", run: listComments, transportErr: errNetwork, wantErr: confluence.ErrUnavailable},
		{name: "чтение: комментарии вне зоны → ErrNotFound", run: listComments, transportErr: fmt.Errorf("%w: 101", guard.ErrOutOfZone), wantErr: confluence.ErrNotFound},
		{name: "чтение: список вложений, таймаут → ErrUnavailable", run: listAttachments, transportErr: context.DeadlineExceeded, wantErr: confluence.ErrUnavailable},
		{name: "чтение: скачивание, сетевая ошибка → ErrUnavailable", run: downloadAttachment, transportErr: errNetwork, wantErr: confluence.ErrUnavailable},
		{name: "чтение: скачивание вне зоны → ErrNotFound", run: downloadAttachment, transportErr: fmt.Errorf("%w: 201", guard.ErrOutOfZone), wantErr: confluence.ErrNotFound},
		{name: "чтение: скачивание, отказ guard по форме → голый ErrForbidden", run: downloadAttachment, transportErr: fmt.Errorf("%w: GET /x", guard.ErrForbidden), wantErr: confluence.ErrForbidden},
		{name: "создание: сетевая ошибка → ErrMaybeWritten", run: createPageError, transportErr: errNetwork, wantErr: confluence.ErrMaybeWritten},
		{name: "запись: сетевая ошибка → ErrMaybeWritten", run: updatePageError, transportErr: errNetwork, wantErr: confluence.ErrMaybeWritten},
		{name: "запись: таймаут → ErrMaybeWritten", run: updatePageError, transportErr: context.DeadlineExceeded, wantErr: confluence.ErrMaybeWritten},
		{name: "запись: текст ошибки с фрагментом ответа → не в логе и не в ошибке", run: updatePageError, transportErr: errMalformedResponse, wantErr: confluence.ErrMaybeWritten},
		{name: "запись: вне зоны записи → ErrNotFound", run: updatePageError, transportErr: fmt.Errorf("%w: 101", guard.ErrOutOfZone), wantErr: confluence.ErrNotFound},
		{name: "запись: сбой проверки зоны → ErrUnavailable, не ErrMaybeWritten", run: updatePageError, transportErr: fmt.Errorf("%w: status 500", guard.ErrZoneCheckFailed), wantErr: confluence.ErrUnavailable},
		{name: "запись: 401 на проверке зоны → ErrUnauthorized", run: updatePageError, transportErr: guard.ErrTokenRejected, wantErr: confluence.ErrUnauthorized},
		{name: "запись: отказ guard по форме → голый ErrForbidden", run: updatePageError, transportErr: fmt.Errorf("%w: body", guard.ErrForbidden), wantErr: confluence.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			harness.transport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			err := tt.run(t.Context(), harness.client)

			requireSentinel(t, tt.wantErr, err)
			require.NotEmpty(t, harness.logs.String())
			requireNoResponseFragment(t, harness, err)
		})
	}
}

func TestZoneCheckFailureLogsCause(t *testing.T) {
	harness := newHarness(t, baseURL)
	harness.transport.EXPECT().RoundTrip(gomock.Any()).Return(nil, fmt.Errorf("%w: response for 101 is not content JSON", guard.ErrZoneCheckFailed))

	err := getPage(t.Context(), harness.client)

	require.ErrorIs(t, err, confluence.ErrUnavailable)
	require.Contains(t, harness.logs.String(), "response for 101 is not content JSON")
}

func getPage(ctx context.Context, client *confluence.Client) error {
	_, err := client.GetPage(ctx, "101")
	return err
}

func checkPage(ctx context.Context, client *confluence.Client) error {
	return client.CheckPage(ctx, "101")
}

func createPageError(ctx context.Context, client *confluence.Client) error {
	_, err := createPage(ctx, client)
	return err
}

func updatePageError(ctx context.Context, client *confluence.Client) error {
	_, err := updatePage(ctx, client)
	return err
}

func listComments(ctx context.Context, client *confluence.Client) error {
	_, err := client.ListComments(ctx, "101")
	return err
}

func listAttachments(ctx context.Context, client *confluence.Client) error {
	_, err := client.ListAttachments(ctx, "201")
	return err
}

func downloadAttachment(ctx context.Context, client *confluence.Client) error {
	_, err := client.DownloadAttachment(ctx, "201", confluence.Attachment{ID: "501", Title: "report.md", Version: 2}, 64)
	return err
}

var guardedConfig = config.Config{
	BaseURL:     baseURL,
	ReadRoots:   []int64{100},
	WriteRoots:  []int64{200},
	Tools:       config.Tools{GetPage: true, ListChildren: true, Search: true, ListComments: true, ListAttachments: true, GetAttachment: true, CreatePage: true, UpdatePage: true, UploadAttachment: true},
	Attachments: &config.Attachments{Extensions: []string{".md"}, MaxBytes: 1 << 20},
	Search:      config.Search{MaxResults: 25},
}

var zoneCheckResponses = map[string]string{
	"101": `{"id":"101","type":"page","status":"current","space":{"key":"DOCS"},"ancestors":[{"id":"1"},{"id":"100"}]}`,
	"201": `{"id":"201","type":"page","status":"current","space":{"key":"DOCS"},"ancestors":[{"id":"1"},{"id":"200"}]}`,
	"501": `{"id":"att501","type":"attachment","status":"current","container":{"id":"201","type":"page"}}`,
}

func guardedSearchCQL(t *testing.T) string {
	t.Helper()
	cql, err := guard.SearchCQL(guardedConfig.ReadZoneRoots(), "отчёт \"v2\"")
	require.NoError(t, err)
	return cql
}

func TestClientRequestsPassGuard(t *testing.T) {
	searchQuery := url.Values{"cql": {guardedSearchCQL(t)}, "limit": {"25"}}
	tests := []struct {
		name           string
		run            func(ctx context.Context, client *confluence.Client) error
		responses      []string
		wantZoneChecks []string
		wantForwarded  []string
	}{
		{name: "чтение страницы", run: getPage, responses: []string{pageBody}, wantZoneChecks: []string{"101"}, wantForwarded: []string{"GET " + pageURL}},
		{name: "проверка страницы", run: checkPage, responses: []string{`{"id":"101","version":{"number":4}}`}, wantZoneChecks: []string{"101"}, wantForwarded: []string{"GET " + baseURL + "/rest/api/content/101?expand=version"}},
		{name: "дети страницы", run: listChildren, responses: []string{`{"results":[]}`}, wantZoneChecks: []string{"101"}, wantForwarded: []string{"GET " + baseURL + "/rest/api/content/101/child/page?limit=25&start=0"}},
		{
			name: "поиск", run: func(ctx context.Context, client *confluence.Client) error {
				_, err := client.Search(ctx, searchQuery.Get("cql"), 25)
				return err
			},
			responses: []string{`{"results":[]}`}, wantForwarded: []string{"GET " + baseURL + "/rest/api/content/search?" + searchQuery.Encode()},
		},
		{name: "комментарии", run: listComments, responses: []string{`{"results":[]}`}, wantZoneChecks: []string{"101"}, wantForwarded: []string{"GET " + commentsURL(0)}},
		{name: "список вложений", run: listAttachments, responses: []string{`{"results":[]}`}, wantZoneChecks: []string{"201"}, wantForwarded: []string{"GET " + attachmentsListURL(0)}},
		{name: "скачивание вложения", run: downloadAttachment, responses: []string{"# Отчёт"}, wantZoneChecks: []string{"201"}, wantForwarded: []string{"GET " + reportDownloadURL}},
		{name: "создание страницы", run: createPageError, responses: []string{createdResponseBody}, wantZoneChecks: []string{"201"}, wantForwarded: []string{"POST " + createURL}},
		{name: "правка страницы", run: updatePageError, responses: []string{`{"id":"201","title":"Новая","version":{"number":5}}`}, wantZoneChecks: []string{"201"}, wantForwarded: []string{"PUT " + updateURL}},
		{name: "загрузка нового вложения", run: uploadReport, responses: []string{noAttachmentsJSON, reportListJSON}, wantZoneChecks: []string{"201", "201"}, wantForwarded: []string{"GET " + attachmentsListURL(0), "POST " + uploadURL}},
		{name: "загрузка новой версии вложения", run: uploadReport, responses: []string{reportListJSON, reportJSON}, wantZoneChecks: []string{"201", "201", "501"}, wantForwarded: []string{"GET " + attachmentsListURL(0), "POST " + uploadVersionURL}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			guardTransport, err := guard.New(guardedConfig, nextTransport)
			require.NoError(t, err)
			client, err := confluence.New(baseURL, token, guardTransport, log.New(io.Discard, "", 0))
			require.NoError(t, err)
			isZoneCheck := gomock.Cond(func(sent *http.Request) bool { return sent.URL.Query().Get("expand") == "ancestors,space,container" })
			var zoneChecked, forwarded []string
			nextTransport.EXPECT().RoundTrip(isZoneCheck).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
				contentID := path.Base(sent.URL.Path)
				zoneChecked = append(zoneChecked, contentID)
				return jsonResponse(http.StatusOK, zoneCheckResponses[contentID]), nil
			}).Times(len(tt.wantZoneChecks))
			nextTransport.EXPECT().RoundTrip(gomock.Not(isZoneCheck)).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
				forwarded = append(forwarded, sent.Method+" "+sent.URL.String())
				return jsonResponse(http.StatusOK, tt.responses[len(forwarded)-1]), nil
			}).Times(len(tt.wantForwarded))

			err = tt.run(t.Context(), client)

			require.NoError(t, err)
			require.Equal(t, tt.wantZoneChecks, zoneChecked)
			require.Equal(t, tt.wantForwarded, forwarded)
		})
	}
}

func listChildren(ctx context.Context, client *confluence.Client) error {
	_, err := client.ListChildren(ctx, "101", 0, 25)
	return err
}

func uploadReport(ctx context.Context, client *confluence.Client) error {
	_, err := client.UploadAttachment(ctx, "201", reportFile)
	return err
}
