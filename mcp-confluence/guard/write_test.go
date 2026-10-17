package guard_test

import (
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
	writeChildID = "201"
	storageBody  = `{"storage":{"value":"<p>Текст &amp; \"кавычки\"</p>","representation":"storage"}}`
	createBody   = `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"` + writeChildID + `"}],"body":` + storageBody + `}`
)

var updateBody = updateBodyFor(writeChildID)

func jsonRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	request := newRequest(t, method, url, bodyReader)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func updateBodyFor(pageID string) string {
	return `{"id":"` + pageID + `","type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`
}

func TestRoundTripCreatePageTarget(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		method  string
		url     string
		wantErr error
	}{
		{name: "POST на коллекцию", cfg: allowedConfig, method: http.MethodPost, url: contentURL},
		{name: "base_url с путём", cfg: withBasePath(), method: http.MethodPost, url: basePathURL + "/rest/api/content"},
		{name: "включён только create_page", cfg: withTools(config.Tools{CreatePage: true}), method: http.MethodPost, url: contentURL},

		{name: "create_page выключен", cfg: withTools(config.Tools{UpdatePage: true}), method: http.MethodPost, url: contentURL, wantErr: guard.ErrForbidden},
		{name: "PUT на коллекцию", cfg: allowedConfig, method: http.MethodPut, url: contentURL, wantErr: guard.ErrForbidden},
		{name: "GET коллекции", cfg: allowedConfig, method: http.MethodGet, url: contentURL, wantErr: guard.ErrForbidden},
		{name: "query status", cfg: allowedConfig, method: http.MethodPost, url: contentURL + "?status=draft", wantErr: guard.ErrForbidden},
		{name: "query expand", cfg: allowedConfig, method: http.MethodPost, url: contentURL + "?expand=body.view", wantErr: guard.ErrForbidden},
		{name: "пустой query со знаком вопроса", cfg: allowedConfig, method: http.MethodPost, url: contentURL + "?", wantErr: guard.ErrForbidden},
		{name: "query из одного &", cfg: allowedConfig, method: http.MethodPost, url: contentURL + "?&", wantErr: guard.ErrForbidden},
		{name: "путь с завершающим слэшем", cfg: allowedConfig, method: http.MethodPost, url: contentURL + "/", wantErr: guard.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantErr == nil {
				gomock.InOrder(
					expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(tt.cfg.BaseURL, writeChildID), writeChildBody),
					expectForwarded(t, nextTransport, http.MethodPost, tt.url, createBody),
				)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, tt.method, tt.url, createBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripCreatePageZone(t *testing.T) {
	writeChildWithoutSpace := `{"id":"201","type":"page","status":"current","title":"Write child","ancestors":[{"id":"1"},{"id":"200"}]}`
	tests := []struct {
		name              string
		requestBody       string
		wantZoneCheckURL  string
		zoneCheckResponse string
		wantErr           error
	}{
		{name: "родитель — потомок корня записи", requestBody: createBody, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildBody},
		{name: "родитель — корень записи", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"200"}],"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, "200"), zoneCheckResponse: writeRootBody},
		{name: "ключи в другом порядке", requestBody: `{"body":` + storageBody + `,"ancestors":[{"id":"201"}],"space":{"key":"DOCS"},"title":"Новая","type":"page"}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildBody},
		{name: "пустое тело страницы", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":{"storage":{"value":"","representation":"storage"}}}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildBody},

		{name: "родитель — корень чтения", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"100"}],"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody, wantErr: guard.ErrOutOfZone},
		{name: "родитель — потомок корня чтения", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"101"}],"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, "101"), zoneCheckResponse: childBody, wantErr: guard.ErrOutOfZone},
		{name: "родитель вне зоны", requestBody: `{"type":"page","title":"Новая","space":{"key":"SECRET"},"ancestors":[{"id":"300"}],"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, "300"), zoneCheckResponse: outsideBody, wantErr: guard.ErrOutOfZone},
		{name: "чужой space.key при родителе в зоне", requestBody: `{"type":"page","title":"Новая","space":{"key":"SECRET"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildBody, wantErr: guard.ErrForbidden},
		{name: "пустой space.key при родителе без space в ответе", requestBody: `{"type":"page","title":"Новая","space":{"key":""},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`, wantZoneCheckURL: zoneCheckURLAt(baseURL, writeChildID), zoneCheckResponse: writeChildWithoutSpace, wantErr: guard.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectZoneCheckThenForward(t, nextTransport, tt.wantZoneCheckURL, tt.zoneCheckResponse, tt.wantErr, http.MethodPost, contentURL, tt.requestBody)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, http.MethodPost, contentURL, tt.requestBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripRejectsCreatePageBody(t *testing.T) {
	oversized := `{"type":"page","title":"` + strings.Repeat("x", guard.MaxBodyBytes) + `","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`
	tests := []struct {
		name        string
		requestBody string
	}{
		{name: "без тела", requestBody: ""},
		{name: "невалидный JSON", requestBody: `{"type":`},
		{name: "тело — массив", requestBody: `[` + createBody + `]`},
		{name: "мусор после JSON", requestBody: createBody + `{}`},
		{name: "превышение размера", requestBody: oversized},
		{name: "без ancestors", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"body":` + storageBody + `}`},
		{name: "два ancestors", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"200"},{"id":"201"}],"body":` + storageBody + `}`},
		{name: "ancestors пуст", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[],"body":` + storageBody + `}`},
		{name: "ancestors null", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":null,"body":` + storageBody + `}`},
		{name: "ancestor с лишним ключом type", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201","type":"page"}],"body":` + storageBody + `}`},
		{name: "ancestor с повтором id", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"300","id":"201"}],"body":` + storageBody + `}`},
		{name: "ancestor id числом", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":201}],"body":` + storageBody + `}`},
		{name: "ancestor id не число", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"abc"}],"body":` + storageBody + `}`},
		{name: "ancestor id с ведущим нулём", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"0201"}],"body":` + storageBody + `}`},
		{name: "ancestors повторяется", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"300"}],"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "без space", requestBody: `{"type":"page","title":"Новая","ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "space с лишним ключом", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS","name":"x"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "space по id", requestBody: `{"type":"page","title":"Новая","space":{"id":1},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "space.key null", requestBody: `{"type":"page","title":"Новая","space":{"key":null},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "type blogpost", requestBody: `{"type":"blogpost","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "без type", requestBody: `{"title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "пустой title", requestBody: `{"type":"page","title":"","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "title из пробелов", requestBody: `{"type":"page","title":"  ","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "title числом", requestBody: `{"type":"page","title":1,"space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "title null", requestBody: `{"type":"page","title":null,"space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "status рядом", requestBody: `{"type":"page","title":"Новая","status":"current","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "metadata рядом", requestBody: `{"type":"page","title":"Новая","metadata":{"labels":[]},"space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "id в теле создания", requestBody: `{"id":"5","type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "без body", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}]}`},
		{name: "body с view рядом со storage", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":{"storage":{"value":"x","representation":"storage"},"view":{"value":"x","representation":"view"}}}`},
		{name: "representation wiki", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":{"storage":{"value":"x","representation":"wiki"}}}`},
		{name: "storage без representation", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":{"storage":{"value":"x"}}}`},
		{name: "storage value не строка", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":{"storage":{"value":1,"representation":"storage"}}}`},
		{name: "ключ ancestors в другом регистре", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"Ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
		{name: "повтор ключа ancestors через unicode-экранирование", requestBody: `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"\u0061ncestors":[{"id":"300"}],"body":` + storageBody + `}`},
		{name: "BOM перед объектом", requestBody: "\ufeff" + createBody},
		{name: "комментарий в JSON", requestBody: `{/* x */"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":` + storageBody + `}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, http.MethodPost, contentURL, tt.requestBody))

			requireOutcome(t, guard.ErrForbidden, response, err)
		})
	}
}

func TestRoundTripUpdatePageTarget(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		method  string
		url     string
		wantErr error
	}{
		{name: "PUT страницы в зоне записи", cfg: allowedConfig, method: http.MethodPut, url: contentURL + "/" + writeChildID},
		{name: "base_url с путём", cfg: withBasePath(), method: http.MethodPut, url: basePathURL + "/rest/api/content/" + writeChildID},
		{name: "включён только update_page", cfg: withTools(config.Tools{UpdatePage: true}), method: http.MethodPut, url: contentURL + "/" + writeChildID},

		{name: "update_page выключен", cfg: withTools(config.Tools{CreatePage: true}), method: http.MethodPut, url: contentURL + "/" + writeChildID, wantErr: guard.ErrForbidden},
		{name: "PATCH", cfg: allowedConfig, method: http.MethodPatch, url: contentURL + "/" + writeChildID, wantErr: guard.ErrForbidden},
		{name: "query status", cfg: allowedConfig, method: http.MethodPut, url: contentURL + "/" + writeChildID + "?status=draft", wantErr: guard.ErrForbidden},
		{name: "query conflictPolicy", cfg: allowedConfig, method: http.MethodPut, url: contentURL + "/" + writeChildID + "?conflictPolicy=abort", wantErr: guard.ErrForbidden},
		{name: "пустой query со знаком вопроса", cfg: allowedConfig, method: http.MethodPut, url: contentURL + "/" + writeChildID + "?", wantErr: guard.ErrForbidden},
		{name: "query из одного &", cfg: allowedConfig, method: http.MethodPut, url: contentURL + "/" + writeChildID + "?&", wantErr: guard.ErrForbidden},
		{name: "PUT на дочерний путь", cfg: allowedConfig, method: http.MethodPut, url: contentURL + "/" + writeChildID + "/child/page", wantErr: guard.ErrForbidden},
		{name: "путь с завершающим слэшем", cfg: allowedConfig, method: http.MethodPut, url: contentURL + "/" + writeChildID + "/", wantErr: guard.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantErr == nil {
				gomock.InOrder(
					expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(tt.cfg.BaseURL, writeChildID), writeChildBody),
					expectForwarded(t, nextTransport, http.MethodPut, tt.url, updateBody),
				)
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, tt.method, tt.url, updateBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripUpdatePageZone(t *testing.T) {
	tests := []struct {
		name              string
		pageID            string
		requestBody       string
		zoneCheckResponse string
		wantErr           error
	}{
		{name: "корень записи", pageID: "200", requestBody: updateBodyFor("200"), zoneCheckResponse: writeRootBody},
		{name: "потомок корня записи", pageID: writeChildID, requestBody: updateBody, zoneCheckResponse: writeChildBody},
		{name: "ключи в другом порядке", pageID: writeChildID, requestBody: `{"body":` + storageBody + `,"version":{"number":3},"title":"Новая","type":"page","id":"201"}`, zoneCheckResponse: writeChildBody},

		{name: "корень чтения — вне зоны записи", pageID: "100", requestBody: updateBodyFor("100"), zoneCheckResponse: rootBody, wantErr: guard.ErrOutOfZone},
		{name: "потомок корня чтения — вне зоны записи", pageID: "101", requestBody: updateBodyFor("101"), zoneCheckResponse: childBody, wantErr: guard.ErrOutOfZone},
		{name: "страница вне зоны", pageID: "300", requestBody: updateBodyFor("300"), zoneCheckResponse: outsideBody, wantErr: guard.ErrOutOfZone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageURL := contentURL + "/" + tt.pageID
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectZoneCheckThenForward(t, nextTransport, zoneCheckURLAt(baseURL, tt.pageID), tt.zoneCheckResponse, tt.wantErr, http.MethodPut, pageURL, tt.requestBody)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, http.MethodPut, pageURL, tt.requestBody))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}

func TestRoundTripRejectsUpdatePageBody(t *testing.T) {
	tests := []struct {
		name        string
		requestBody string
	}{
		{name: "без тела", requestBody: ""},
		{name: "id не совпадает с путём", requestBody: `{"id":"200","type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`},
		{name: "id числом", requestBody: `{"id":201,"type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`},
		{name: "без id", requestBody: `{"type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`},
		{name: "id повторяется", requestBody: `{"id":"300","id":"201","type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`},
		{name: "ancestors в теле", requestBody: `{"id":"201","type":"page","title":"Новая","ancestors":[{"id":"300"}],"version":{"number":3},"body":` + storageBody + `}`},
		{name: "status в теле", requestBody: `{"id":"201","type":"page","title":"Новая","status":"current","version":{"number":3},"body":` + storageBody + `}`},
		{name: "space в теле", requestBody: `{"id":"201","type":"page","title":"Новая","space":{"key":"DOCS"},"version":{"number":3},"body":` + storageBody + `}`},
		{name: "без version", requestBody: `{"id":"201","type":"page","title":"Новая","body":` + storageBody + `}`},
		{name: "version числом", requestBody: `{"id":"201","type":"page","title":"Новая","version":3,"body":` + storageBody + `}`},
		{name: "version number строкой", requestBody: `{"id":"201","type":"page","title":"Новая","version":{"number":"3"},"body":` + storageBody + `}`},
		{name: "version number ноль", requestBody: `{"id":"201","type":"page","title":"Новая","version":{"number":0},"body":` + storageBody + `}`},
		{name: "version number дробный", requestBody: `{"id":"201","type":"page","title":"Новая","version":{"number":3.5},"body":` + storageBody + `}`},
		{name: "version number отрицательный", requestBody: `{"id":"201","type":"page","title":"Новая","version":{"number":-3},"body":` + storageBody + `}`},
		{name: "version number в экспоненте", requestBody: `{"id":"201","type":"page","title":"Новая","version":{"number":3e0},"body":` + storageBody + `}`},
		{name: "version с minorEdit", requestBody: `{"id":"201","type":"page","title":"Новая","version":{"number":3,"minorEdit":true},"body":` + storageBody + `}`},
		{name: "type blogpost", requestBody: `{"id":"201","type":"blogpost","title":"Новая","version":{"number":3},"body":` + storageBody + `}`},
		{name: "пустой title", requestBody: `{"id":"201","type":"page","title":"","version":{"number":3},"body":` + storageBody + `}`},
		{name: "representation wiki", requestBody: `{"id":"201","type":"page","title":"Новая","version":{"number":3},"body":{"storage":{"value":"x","representation":"wiki"}}}`},
		{name: "мусор после JSON", requestBody: updateBody + `{}`},
		{name: "ключ version в другом регистре", requestBody: `{"id":"201","type":"page","title":"Новая","Version":{"number":3},"body":` + storageBody + `}`},
		{name: "повтор ключа id через unicode-экранирование", requestBody: `{"id":"201","\u0069d":"300","type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`},
		{name: "BOM перед объектом", requestBody: "\ufeff" + updateBody},
		{name: "комментарий в JSON", requestBody: `{"id":"201",// x
"type":"page","title":"Новая","version":{"number":3},"body":` + storageBody + `}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(jsonRequest(t, http.MethodPut, contentURL+"/"+writeChildID, tt.requestBody))

			requireOutcome(t, guard.ErrForbidden, response, err)
		})
	}
}

func TestRoundTripForwardsBodyAsPlainJSON(t *testing.T) {
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
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			gomock.InOrder(
				expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(baseURL, writeChildID), writeChildBody),
				nextTransport.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(forwarded *http.Request) (*http.Response, error) {
					require.Equal(t, []string{"application/json"}, forwarded.Header.Values("Content-Type"))
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				}),
			)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)
			request := newRequest(t, tt.method, tt.url, strings.NewReader(tt.requestBody))
			request.Header.Set("Content-Type", "text/plain; charset=windows-1251")
			request.Header.Add("Content-Type", "application/xml")

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, nil, response, err)
		})
	}
}
