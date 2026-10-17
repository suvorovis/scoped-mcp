package confluence_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-confluence/confluence"
)

var childPage = confluence.Page{
	ID: "101", Title: "Child", Version: 4, ParentID: "100", AncestorIDs: []string{"1", "100"}, SpaceKey: "DOCS",
	WebURL: baseURL + "/display/DOCS/Child", Body: "<p>Текст</p>",
}

func TestGetPage(t *testing.T) {
	tests := []struct {
		name          string
		clientBaseURL string
		wantURL       string
		response      *http.Response
		want          confluence.Page
		wantErr       error
	}{
		{name: "полный ответ → только поля белого списка", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusOK, pageBody), want: childPage},
		{
			name: "страница без предков", clientBaseURL: baseURL, wantURL: pageURL,
			response: jsonResponse(http.StatusOK, `{"id":"101","title":"Root","version":{"number":1},"ancestors":[],"space":{"key":"DOCS"},"body":{"storage":{"value":""}},"_links":{"webui":"/display/DOCS/Root"}}`),
			want:     confluence.Page{ID: "101", Title: "Root", Version: 1, SpaceKey: "DOCS", WebURL: baseURL + "/display/DOCS/Root"},
		},
		{
			name: "base_url с путём → путь в запросе и в ссылке", clientBaseURL: baseURL + "/confluence",
			wantURL:  baseURL + "/confluence/rest/api/content/101?expand=body.storage%2Cversion%2Cancestors%2Cspace",
			response: jsonResponse(http.StatusOK, pageBody),
			want:     withWebURL(childPage, baseURL+"/confluence/display/DOCS/Child"),
		},
		{
			name: "base_url с завершающим слэшем → без двойного слэша", clientBaseURL: baseURL + "/confluence/",
			wantURL:  baseURL + "/confluence/rest/api/content/101?expand=body.storage%2Cversion%2Cancestors%2Cspace",
			response: jsonResponse(http.StatusOK, pageBody),
			want:     withWebURL(childPage, baseURL+"/confluence/display/DOCS/Child"),
		},
		{name: "без webui → пустая ссылка", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusOK, `{"id":"101","title":"x","version":{"number":1}}`), want: confluence.Page{ID: "101", Title: "x", Version: 1}},
		{name: "401", clientBaseURL: baseURL, wantURL: pageURL, response: emptyResponse(http.StatusUnauthorized), wantErr: confluence.ErrUnauthorized},
		{name: "403", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusForbidden, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrNotFound},
		{name: "404", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusNotFound, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrNotFound},
		{name: "400", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusBadRequest, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrRejected},
		{name: "409 на чтение → ErrRejected, не ErrVersionConflict", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusConflict, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrRejected},
		{name: "500", clientBaseURL: baseURL, wantURL: pageURL, response: markedHTMLResponse(http.StatusInternalServerError), wantErr: confluence.ErrUnavailable},
		{name: "редирект → не следуем", clientBaseURL: baseURL, wantURL: pageURL, response: redirectResponse("https://login.example.com/"), wantErr: confluence.ErrUnavailable},
		{name: "200 не JSON", clientBaseURL: baseURL, wantURL: pageURL, response: markedHTMLResponse(http.StatusOK), wantErr: confluence.ErrUnavailable},
		{name: "200 не читается", clientBaseURL: baseURL, wantURL: pageURL, response: unreadableResponse(http.StatusOK), wantErr: confluence.ErrUnavailable},
		{name: "200 больше 8 MiB", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusOK, `{"id":"101","title":"`+bodyMarker+strings.Repeat("x", 8<<20)+`"}`), wantErr: confluence.ErrUnavailable},
		{name: "201 на чтение", clientBaseURL: baseURL, wantURL: pageURL, response: jsonResponse(http.StatusCreated, pageBody), wantErr: confluence.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, tt.clientBaseURL)
			expectGet(t, harness.transport, tt.wantURL).Return(tt.response, nil)

			got, err := harness.client.GetPage(t.Context(), "101")

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
			requireNoResponseFragment(t, harness, err)
		})
	}
}

func withWebURL(page confluence.Page, webURL string) confluence.Page {
	page.WebURL = webURL
	return page
}

func TestCheckPage(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		wantErr  error
	}{
		{name: "200 → в зоне", response: jsonResponse(http.StatusOK, `{"id":"101","version":{"number":4}}`)},
		{name: "404 → ErrNotFound", response: jsonResponse(http.StatusNotFound, `{}`), wantErr: confluence.ErrNotFound},
		{name: "500 → ErrUnavailable", response: emptyResponse(http.StatusInternalServerError), wantErr: confluence.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			expectGet(t, harness.transport, baseURL+"/rest/api/content/101?expand=version").Return(tt.response, nil)

			err := harness.client.CheckPage(t.Context(), "101")

			requireSentinel(t, tt.wantErr, err)
		})
	}
}

func TestListChildren(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		want     confluence.ChildrenBatch
		wantErr  error
	}{
		{
			name:     "есть следующая страница",
			response: jsonResponse(http.StatusOK, `{"results":[{"id":"102","type":"page","title":"A","space":{"key":"DOCS"}},{"id":"103","title":"B"}],"start":0,"limit":2,"size":2,"_links":{"next":"/rest/api/content/101/child/page?limit=2&start=2","self":"x"}}`),
			want:     confluence.ChildrenBatch{Children: []confluence.PageSummary{{ID: "102", Title: "A"}, {ID: "103", Title: "B"}}, HasMore: true},
		},
		{
			name:     "последняя страница",
			response: jsonResponse(http.StatusOK, `{"results":[{"id":"104","title":"C"}],"start":2,"limit":2,"size":1,"_links":{"self":"x"}}`),
			want:     confluence.ChildrenBatch{Children: []confluence.PageSummary{{ID: "104", Title: "C"}}},
		},
		{name: "пусто", response: jsonResponse(http.StatusOK, `{"results":[],"size":0,"_links":{}}`), want: confluence.ChildrenBatch{Children: []confluence.PageSummary{}}},
		{name: "404", response: jsonResponse(http.StatusNotFound, `{}`), wantErr: confluence.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			expectGet(t, harness.transport, baseURL+"/rest/api/content/101/child/page?limit=2&start=0").Return(tt.response, nil)

			got, err := harness.client.ListChildren(t.Context(), "101", 0, 2)

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
		})
	}
}

func TestSearch(t *testing.T) {
	cql := `(id = 100 or ancestor = 100) and type = page and text ~ "disk \"io\""`
	wantURL := baseURL + "/rest/api/content/search?cql=%28id+%3D+100+or+ancestor+%3D+100%29+and+type+%3D+page+and+text+~+%22disk+%5C%22io%5C%22%22&limit=25"
	tests := []struct {
		name     string
		response *http.Response
		want     []confluence.PageSummary
		wantErr  error
	}{
		{name: "результаты", response: jsonResponse(http.StatusOK, `{"results":[{"id":"102","type":"page","title":"A","excerpt":"…"},{"id":"300","title":"Outside"}],"_links":{}}`), want: []confluence.PageSummary{{ID: "102", Title: "A"}, {ID: "300", Title: "Outside"}}},
		{name: "пусто", response: jsonResponse(http.StatusOK, `{"results":[]}`), want: []confluence.PageSummary{}},
		{name: "400 на CQL → ErrRejected", response: jsonResponse(http.StatusBadRequest, `{"message":"Could not parse cql"}`), wantErr: confluence.ErrRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			expectGet(t, harness.transport, wantURL).Return(tt.response, nil)

			got, err := harness.client.Search(t.Context(), cql, 25)

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
		})
	}
}

const (
	createURL           = baseURL + "/rest/api/content"
	createRequestBody   = `{"type":"page","title":"Новая","space":{"key":"DOCS"},"ancestors":[{"id":"201"}],"body":{"storage":{"value":"<p>a &amp; b</p>","representation":"storage"}}}`
	updateURL           = baseURL + "/rest/api/content/201"
	updateRequestBody   = `{"id":"201","type":"page","title":"Новая","version":{"number":4},"body":{"storage":{"value":"<p>a &amp; b</p>","representation":"storage"}}}`
	createdResponseBody = `{"id":"202","type":"page","status":"current","title":"Новая","space":{"key":"DOCS"},"version":{"number":1},"_links":{"webui":"/display/DOCS/%D0%9D%D0%BE%D0%B2%D0%B0%D1%8F"}}`
)

var draft = confluence.PageDraft{Title: "Новая", Body: "<p>a &amp; b</p>"}

func createPage(ctx context.Context, client *confluence.Client) (confluence.WrittenPage, error) {
	return client.CreatePage(ctx, "201", "DOCS", draft)
}

func updatePage(ctx context.Context, client *confluence.Client) (confluence.WrittenPage, error) {
	return client.UpdatePage(ctx, "201", 4, draft)
}

type writeOperation struct {
	run        func(ctx context.Context, client *confluence.Client) (confluence.WrittenPage, error)
	wantMethod string
	wantURL    string
	wantBody   string
}

var (
	createOperation = writeOperation{run: createPage, wantMethod: http.MethodPost, wantURL: createURL, wantBody: createRequestBody}
	updateOperation = writeOperation{run: updatePage, wantMethod: http.MethodPut, wantURL: updateURL, wantBody: updateRequestBody}
)

func TestWritePage(t *testing.T) {
	titleTaken := `{"statusCode":400,"data":{"authorized":false,"valid":true,"errors":[],"successful":false},"message":"A page with this title already exists: A page already exists with the title Новая in the space with key ` + bodyMarker + `"}`
	tests := []struct {
		name      string
		operation writeOperation
		response  *http.Response
		want      confluence.WrittenPage
		wantErr   error
	}{
		{name: "создание: 200 → id, title, version, webui", operation: createOperation, response: jsonResponse(http.StatusOK, createdResponseBody), want: confluence.WrittenPage{ID: "202", Title: "Новая", Version: 1, WebURL: baseURL + "/display/DOCS/%D0%9D%D0%BE%D0%B2%D0%B0%D1%8F"}},
		{name: "правка: 200 → новая версия", operation: updateOperation, response: jsonResponse(http.StatusOK, `{"id":"201","title":"Новая","version":{"number":4},"_links":{"webui":"/x"}}`), want: confluence.WrittenPage{ID: "201", Title: "Новая", Version: 4, WebURL: baseURL + "/x"}},
		{name: "создание: 400 с занятым заголовком", operation: createOperation, response: jsonResponse(http.StatusBadRequest, titleTaken), wantErr: confluence.ErrTitleTaken},
		{name: "правка: 400 с занятым заголовком", operation: updateOperation, response: jsonResponse(http.StatusBadRequest, titleTaken), wantErr: confluence.ErrTitleTaken},
		{name: "создание: 400 без message", operation: createOperation, response: jsonResponse(http.StatusBadRequest, `{"statusCode":400}`), wantErr: confluence.ErrRejected},
		{name: "создание: 400 не JSON", operation: createOperation, response: markedHTMLResponse(http.StatusBadRequest), wantErr: confluence.ErrRejected},
		{name: "создание: 400 с другим message", operation: createOperation, response: jsonResponse(http.StatusBadRequest, `{"message":"Content body cannot be converted to new editor format `+bodyMarker+`"}`), wantErr: confluence.ErrRejected},
		{name: "создание: 409 → ErrRejected", operation: createOperation, response: jsonResponse(http.StatusConflict, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrRejected},
		{name: "правка: 409 → ErrVersionConflict", operation: updateOperation, response: jsonResponse(http.StatusConflict, `{"message":"Version must be incremented on update. Current version is: 5 `+bodyMarker+`"}`), wantErr: confluence.ErrVersionConflict},
		{name: "правка: 401", operation: updateOperation, response: emptyResponse(http.StatusUnauthorized), wantErr: confluence.ErrUnauthorized},
		{name: "правка: 403", operation: updateOperation, response: jsonResponse(http.StatusForbidden, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrNotFound},
		{name: "правка: 404", operation: updateOperation, response: jsonResponse(http.StatusNotFound, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrNotFound},
		{name: "правка: 413 → ErrRejected", operation: updateOperation, response: emptyResponse(http.StatusRequestEntityTooLarge), wantErr: confluence.ErrRejected},
		{name: "создание: 500 → ErrMaybeWritten", operation: createOperation, response: markedHTMLResponse(http.StatusInternalServerError), wantErr: confluence.ErrMaybeWritten},
		{name: "правка: 503 → ErrMaybeWritten", operation: updateOperation, response: jsonResponse(http.StatusServiceUnavailable, `{"message":"`+bodyMarker+`"}`), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: редирект → ErrMaybeWritten", operation: createOperation, response: redirectResponse("https://login.example.com/"), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: 201 вместо 200 → ErrMaybeWritten", operation: createOperation, response: jsonResponse(http.StatusCreated, createdResponseBody), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: 200 не JSON → ErrMaybeWritten", operation: createOperation, response: markedHTMLResponse(http.StatusOK), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: 200 без id → ErrMaybeWritten", operation: createOperation, response: jsonResponse(http.StatusOK, `{"title":"`+bodyMarker+`"}`), wantErr: confluence.ErrMaybeWritten},
		{name: "создание: 200 не читается → ErrMaybeWritten", operation: createOperation, response: unreadableResponse(http.StatusOK), wantErr: confluence.ErrMaybeWritten},
		{name: "запись: 200 больше 8 MiB → ErrMaybeWritten", operation: createOperation, response: jsonResponse(http.StatusOK, `{"id":"202","title":"`+bodyMarker+strings.Repeat("x", 8<<20)+`"}`), wantErr: confluence.ErrMaybeWritten},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			expectJSONWrite(t, harness.transport, tt.operation.wantMethod, tt.operation.wantURL, tt.operation.wantBody).Return(tt.response, nil)

			got, err := tt.operation.run(t.Context(), harness.client)

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
			requireNoResponseFragment(t, harness, err)
		})
	}
}
