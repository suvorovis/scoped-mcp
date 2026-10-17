package guard_test

import (
	"errors"
	"io"
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
	baseURL     = "https://confluence.example.com"
	basePathURL = baseURL + "/confluence"
	contentURL  = baseURL + "/rest/api/content"
	pageExpand  = "expand=body.storage,version,ancestors,space"

	attachmentsMaxBytes = 64

	rootBody       = `{"id":"100","type":"page","status":"current","title":"Root","space":{"key":"DOCS"},"ancestors":[{"id":"1"}]}`
	childBody      = `{"id":"101","type":"page","status":"current","title":"Child","space":{"key":"DOCS"},"ancestors":[{"id":"1"},{"id":"100"}]}`
	deepBody       = `{"id":"102","type":"page","status":"current","title":"Deep","space":{"key":"DOCS"},"ancestors":[{"id":"1"},{"id":"100"},{"id":"101"}]}`
	writeRootBody  = `{"id":"200","type":"page","status":"current","title":"Write root","space":{"key":"DOCS"},"ancestors":[{"id":"1"}]}`
	writeChildBody = `{"id":"201","type":"page","status":"current","title":"Write child","space":{"key":"DOCS"},"ancestors":[{"id":"1"},{"id":"200"}]}`
	outsideBody    = `{"id":"300","type":"page","status":"current","title":"Outside","space":{"key":"SECRET"},"ancestors":[{"id":"1"},{"id":"2"}]}`
)

var allowedConfig = config.Config{
	BaseURL:     baseURL,
	ReadRoots:   []int64{100},
	WriteRoots:  []int64{200},
	Tools:       config.Tools{GetPage: true, ListChildren: true, Search: true, ListComments: true, ListAttachments: true, GetAttachment: true, CreatePage: true, UpdatePage: true, UploadAttachment: true},
	Attachments: &config.Attachments{DownloadDir: "/home/bot/out/confluence", UploadDirs: []string{"/home/bot/out"}, Extensions: []string{".md", ".PNG"}, MaxBytes: attachmentsMaxBytes},
	Search:      config.Search{MaxResults: 50},
}

func withBasePath() config.Config {
	cfg := allowedConfig
	cfg.BaseURL = basePathURL
	return cfg
}

func withTools(tools config.Tools) config.Config {
	cfg := allowedConfig
	cfg.Tools = tools
	return cfg
}

func zoneCheckURLAt(base, contentID string) string {
	return base + "/rest/api/content/" + contentID + "?expand=ancestors,space,container"
}

func expectZoneCheck(t *testing.T, nextTransport *mocks.MockRoundTripper, wantURL string, status int, body io.ReadCloser) *gomock.Call {
	t.Helper()
	return nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(sent *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, sent.Method)
		require.Equal(t, wantURL, sent.URL.String())
		require.Equal(t, "Bearer pat-secret", sent.Header.Get("Authorization"))
		require.Equal(t, "application/json", sent.Header.Get("Accept"))
		require.True(t, sent.Body == nil || sent.Body == http.NoBody)
		return &http.Response{StatusCode: status, Body: body}, nil
	})
}

func expectZoneCheckJSON(t *testing.T, nextTransport *mocks.MockRoundTripper, wantURL, body string) *gomock.Call {
	t.Helper()
	return expectZoneCheck(t, nextTransport, wantURL, http.StatusOK, io.NopCloser(strings.NewReader(body)))
}

func expectForwarded(t *testing.T, nextTransport *mocks.MockRoundTripper, wantMethod, wantURL, wantBody string) *gomock.Call {
	t.Helper()
	return nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(forwarded *http.Request) (*http.Response, error) {
		require.Equal(t, wantMethod, forwarded.Method)
		require.Equal(t, wantURL, forwarded.URL.String())
		if wantBody == "" {
			require.True(t, forwarded.Body == nil || forwarded.Body == http.NoBody)
		} else {
			require.Equal(t, wantBody, readAll(t, forwarded.Body))
			require.Equal(t, int64(len(wantBody)), forwarded.ContentLength)
			require.NotNil(t, forwarded.GetBody)
			retryBody, err := forwarded.GetBody()
			require.NoError(t, err)
			require.Equal(t, wantBody, readAll(t, retryBody))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"forwarded":true}`))}, nil
	})
}

func expectZoneCheckThenForward(t *testing.T, nextTransport *mocks.MockRoundTripper, wantZoneCheckURL, zoneCheckResponse string, wantErr error, wantMethod, wantURL, wantBody string) {
	t.Helper()
	calls := []any{}
	if wantZoneCheckURL != "" {
		calls = append(calls, expectZoneCheckJSON(t, nextTransport, wantZoneCheckURL, zoneCheckResponse))
	}
	if wantErr == nil {
		calls = append(calls, expectForwarded(t, nextTransport, wantMethod, wantURL, wantBody))
	}
	gomock.InOrder(calls...)
}

func newRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, body)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer pat-secret")
	request.Header.Set("Accept", "application/json")
	return request
}

func readAll(t *testing.T, body io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	return string(data)
}

func requireOutcome(t *testing.T, wantErr error, response *http.Response, err error) {
	t.Helper()
	if wantErr == nil {
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.NoError(t, response.Body.Close())
		return
	}
	require.ErrorIs(t, err, wantErr)
	require.Nil(t, response)
}

func TestRoundTripRejectsOriginAndRoutes(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.Config
		method string
		url    string
		host   string
	}{
		{name: "другой хост", cfg: allowedConfig, method: http.MethodGet, url: "https://evil.example.com/rest/api/content/100?" + pageExpand},
		{name: "схема http", cfg: allowedConfig, method: http.MethodGet, url: "http://confluence.example.com/rest/api/content/100?" + pageExpand},
		{name: "заголовок Host другого хоста", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/100?" + pageExpand, host: "evil.example.com"},
		{name: "путь в процентной кодировке", cfg: allowedConfig, method: http.MethodGet, url: baseURL + "/rest/api/%63ontent/100?" + pageExpand},
		{name: "путь без префикса base_url", cfg: withBasePath(), method: http.MethodGet, url: contentURL + "/100?" + pageExpand},
		{name: "корень REST", cfg: allowedConfig, method: http.MethodGet, url: baseURL + "/rest/api/"},
		{name: "пространства", cfg: allowedConfig, method: http.MethodGet, url: baseURL + "/rest/api/space"},
		{name: "пользователь", cfg: allowedConfig, method: http.MethodGet, url: baseURL + "/rest/api/user/current"},
		{name: "DELETE страницы", cfg: allowedConfig, method: http.MethodDelete, url: contentURL + "/100"},
		{name: "POST на конкретную страницу", cfg: allowedConfig, method: http.MethodPost, url: contentURL + "/100"},
		{name: "HEAD страницы", cfg: allowedConfig, method: http.MethodHead, url: contentURL + "/100?" + pageExpand},
		{name: "история версий", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/100/history"},
		{name: "метки", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/100/label"},
		{name: "ограничения", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/100/restriction"},
		{name: "дети любого типа", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/100/child?start=0&limit=25"},
		{name: "потомки", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/100/descendant/page?start=0&limit=25"},
		{name: "путь с завершающим слэшем", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/100/?" + pageExpand},
		{name: "id с ведущим нулём", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/0100?" + pageExpand},
		{name: "id нулевой", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/0?" + pageExpand},
		{name: "id не число", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/abc?" + pageExpand},
		{name: "id длиннее 19 цифр", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/" + strings.Repeat("1", 20) + "?" + pageExpand},
		{name: "пустой id", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "//?" + pageExpand},
		{name: "POST на список вложений", cfg: allowedConfig, method: http.MethodPost, url: contentURL + "/100/child/attachment?start=0&limit=100"},
		{name: "query с сырыми пробелами и кавычками", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/search?cql=" + searchCQLBeforeQuery + `x"&limit=25`},
		{name: "скачивание вне префикса", cfg: allowedConfig, method: http.MethodGet, url: baseURL + "/download/export/100.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request := newRequest(t, tt.method, tt.url, nil)
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, guard.ErrForbidden, response, err)
		})
	}
}

func TestRoundTripRequestHeaders(t *testing.T) {
	tests := []struct {
		name              string
		method            string
		url               string
		requestBody       string
		header            http.Header
		transferEncoding  []string
		trailer           http.Header
		wantZoneCheckURL  string
		zoneCheckResponse string
		wantErr           error
	}{
		{name: "User-Agent у чтения", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"User-Agent": {"mcp-confluence"}}, wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "User-Agent у создания", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}, "User-Agent": {"mcp-confluence"}}, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildBody},

		{name: "неканонический content-type", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"content-type": {"application/json"}}, wantErr: guard.ErrForbidden},
		{name: "неканонический x-atlassian-token", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"x-atlassian-token": {"no-check"}}, wantErr: guard.ErrForbidden},
		{name: "неканонический content-length", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"content-length": {"10"}}, wantErr: guard.ErrForbidden},
		{name: "неканонический authorization рядом с каноническим", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"authorization": {"Bearer other"}}, wantErr: guard.ErrForbidden},
		{name: "неканонический content-type у создания", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}, "content-type": {"text/plain"}}, wantErr: guard.ErrForbidden},
		{name: "X-HTTP-Method-Override", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"X-Http-Method-Override": {"DELETE"}}, wantErr: guard.ErrForbidden},
		{name: "Content-Encoding", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, wantErr: guard.ErrForbidden},
		{name: "Content-Length в заголовках", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}, "Content-Length": {"1"}}, wantErr: guard.ErrForbidden},
		{name: "Transfer-Encoding в заголовках", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}, "Transfer-Encoding": {"chunked"}}, wantErr: guard.ErrForbidden},
		{name: "Authorization дважды", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"Authorization": {"Bearer pat-secret", "Bearer other"}}, wantErr: guard.ErrForbidden},
		{name: "Transfer-Encoding у чтения", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, transferEncoding: []string{"chunked"}, wantErr: guard.ErrForbidden},
		{name: "трейлер у чтения", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, trailer: http.Header{"X-Trail": {"v"}}, wantErr: guard.ErrForbidden},
		{name: "Transfer-Encoding у создания", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}}, transferEncoding: []string{"chunked"}, wantErr: guard.ErrForbidden},
		{name: "трейлер у создания", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}}, trailer: http.Header{"X-Checksum": nil}, wantErr: guard.ErrForbidden},
		{name: "Cookie", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"Cookie": {"JSESSIONID=1"}}, wantErr: guard.ErrForbidden},
		{name: "Content-Type у чтения", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"Content-Type": {"application/json"}}, wantErr: guard.ErrForbidden},
		{name: "X-Atlassian-Token у чтения", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, header: http.Header{"X-Atlassian-Token": {"no-check"}}, wantErr: guard.ErrForbidden},
		{name: "Content-Type у поиска", method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`release"`, "25"), header: http.Header{"Content-Type": {"application/json"}}, wantErr: guard.ErrForbidden},
		{name: "X-Atlassian-Token у создания", method: http.MethodPost, url: contentURL, requestBody: createBody, header: http.Header{"Content-Type": {"application/json"}, "X-Atlassian-Token": {"no-check"}}, wantErr: guard.ErrForbidden},
		{name: "X-Atlassian-Token у правки", method: http.MethodPut, url: contentURL + "/" + writeChildID, requestBody: updateBody, header: http.Header{"Content-Type": {"application/json"}, "X-Atlassian-Token": {"no-check"}}, wantErr: guard.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectZoneCheckThenForward(t, nextTransport, tt.wantZoneCheckURL, tt.zoneCheckResponse, tt.wantErr, tt.method, tt.url, tt.requestBody)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)
			var bodyReader io.Reader
			if tt.requestBody != "" {
				bodyReader = strings.NewReader(tt.requestBody)
			}
			request := newRequest(t, tt.method, tt.url, bodyReader)
			for key, values := range tt.header {
				request.Header[key] = values
			}
			request.TransferEncoding = tt.transferEncoding
			request.Trailer = tt.trailer

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripReturnsNextTransportError(t *testing.T) {
	errNetwork := errors.New("network down")
	nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	gomock.InOrder(
		expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(baseURL, "100"), rootBody),
		nextTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, errNetwork),
	)
	guardTransport, err := guard.New(allowedConfig, nextTransport)
	require.NoError(t, err)

	response, err := guardTransport.RoundTrip(newRequest(t, http.MethodGet, contentURL+"/100?"+pageExpand, nil))

	require.ErrorIs(t, err, errNetwork)
	require.Nil(t, response)
}

func TestRoundTripClosesRequestBody(t *testing.T) {
	tests := []struct {
		name              string
		method            string
		url               string
		requestBody       string
		wantZoneCheckURL  string
		zoneCheckResponse string
		wantErr           error
	}{
		{name: "пересланное создание", method: http.MethodPost, url: contentURL, requestBody: createBody, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildBody},
		{name: "пересланная правка", method: http.MethodPut, url: contentURL + "/" + writeChildID, requestBody: updateBody, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildBody},

		{name: "маршрута нет", method: http.MethodDelete, url: contentURL + "/100", requestBody: "{}", wantErr: guard.ErrForbidden},
		{name: "тело у GET страницы", method: http.MethodGet, url: contentURL + "/100?" + pageExpand, requestBody: "{}", wantErr: guard.ErrForbidden},
		{name: "создание отклонено по телу", method: http.MethodPost, url: contentURL, requestBody: `{"type":"page"}`, wantErr: guard.ErrForbidden},
		{name: "правка отклонена по телу", method: http.MethodPut, url: contentURL + "/" + writeChildID, requestBody: `{"id":"201"}`, wantErr: guard.ErrForbidden},
		{name: "создание отклонено по зоне", method: http.MethodPost, url: contentURL, requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"101"}],"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, "101"), zoneCheckResponse: childBody, wantErr: guard.ErrOutOfZone},
		{name: "правка отклонена по зоне", method: http.MethodPut, url: contentURL + "/101", requestBody: `{"id":"101","type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, "101"), zoneCheckResponse: childBody, wantErr: guard.ErrOutOfZone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextTransport := mocks.NewMockRoundTripper(ctrl)
			expectZoneCheckThenForward(t, nextTransport, tt.wantZoneCheckURL, tt.zoneCheckResponse, tt.wantErr, tt.method, tt.url, tt.requestBody)
			requestBody := mocks.NewMockReadCloser(ctrl)
			requestBody.EXPECT().Read(gomock.Any()).DoAndReturn(strings.NewReader(tt.requestBody).Read).AnyTimes()
			requestBody.EXPECT().Close().Return(nil)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(newRequest(t, tt.method, tt.url, requestBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripLeavesForwardedBodyOpen(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		url         string
		requestBody string
	}{
		{name: "создание", method: http.MethodPost, url: contentURL, requestBody: createBody},
		{name: "правка", method: http.MethodPut, url: contentURL + "/" + writeChildID, requestBody: updateBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextTransport := mocks.NewMockRoundTripper(ctrl)
			bodyOwnedByNextTransport := mocks.NewMockReadCloser(ctrl)
			gomock.InOrder(
				expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(baseURL, writeChildID), writeChildBody),
				nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(forwarded *http.Request) (*http.Response, error) {
					forwarded.Body = bodyOwnedByNextTransport
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				}),
			)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, tt.method, tt.url, tt.requestBody))

			requireOutcome(t, nil, response, err)
		})
	}
}

func TestIsContentID(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "однозначный", value: "1", want: true},
		{name: "19 цифр", value: "1234567890123456789", want: true},
		{name: "20 цифр", value: "12345678901234567890", want: false},
		{name: "пустой", value: "", want: false},
		{name: "ноль", value: "0", want: false},
		{name: "ведущий ноль", value: "0101", want: false},
		{name: "отрицательный", value: "-1", want: false},
		{name: "не число", value: "abc", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, guard.IsContentID(tt.value))
		})
	}
}

func TestAttachmentContentID(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		wantContentID string
		wantOK        bool
	}{
		{name: "число", value: "501", wantContentID: "501", wantOK: true},
		{name: "с префиксом att", value: "att501", wantContentID: "501", wantOK: true},
		{name: "att и 19 цифр", value: "att1234567890123456789", wantContentID: "1234567890123456789", wantOK: true},
		{name: "att и 20 цифр", value: "att12345678901234567890"},
		{name: "только att", value: "att"},
		{name: "att и ноль", value: "att0"},
		{name: "att и ведущий ноль", value: "att0501"},
		{name: "ATT в верхнем регистре", value: "ATT501"},
		{name: "префикс attachment", value: "attachment501"},
		{name: "att дважды", value: "attatt501"},
		{name: "att в конце", value: "501att"},
		{name: "att и отрицательное", value: "att-1"},
		{name: "пустой", value: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contentID, ok := guard.AttachmentContentID(tt.value)

			require.Equal(t, tt.wantContentID, contentID)
			require.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestIsAttachmentFileName(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "обычное имя", value: "report.md", want: true},
		{name: "кириллица, пробел и запятая", value: "отчёт v2,final.png", want: true},
		{name: "dot-файл", value: ".zshrc", want: true},
		{name: "пустое", value: "", want: false},
		{name: "точка", value: ".", want: false},
		{name: "две точки", value: "..", want: false},
		{name: "слэш", value: "a/b.md", want: false},
		{name: "обратный слэш", value: `a\b.md`, want: false},
		{name: "точка с запятой", value: "a;b.md", want: false},
		{name: "кавычка", value: `a"b.md`, want: false},
		{name: "нулевой байт", value: "a\x00b.md", want: false},
		{name: "перевод строки", value: "a\nb.md", want: false},
		{name: "DEL", value: "a\x7fb.md", want: false},
		{name: "символ формата Cf", value: "a\u200bb.md", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, guard.IsAttachmentFileName(tt.value))
		})
	}
}
