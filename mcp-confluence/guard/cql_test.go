package guard_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/guard"
	"scoped-mcp/mcp-confluence/guard/mocks"
)

const searchCQLBeforeQuery = `(id = 100 or ancestor = 100 or id = 200 or ancestor = 200) and type = page and text ~ "`

func TestSearchCQL(t *testing.T) {
	tests := []struct {
		name    string
		roots   []string
		query   string
		want    string
		wantErr error
	}{
		{name: "один корень, простой запрос", roots: []string{"100"}, query: "release", want: `(id = 100 or ancestor = 100) and type = page and text ~ "release"`},
		{name: "два корня в порядке списка", roots: []string{"100", "200"}, query: "disk io", want: searchCQLBeforeQuery + `disk io"`},
		{name: "кавычка экранируется", roots: []string{"100", "200"}, query: `" or space = SECRET`, want: searchCQLBeforeQuery + `\" or space = SECRET"`},
		{name: "обратный слэш экранируется", roots: []string{"100", "200"}, query: `a\b`, want: searchCQLBeforeQuery + `a\\b"`},
		{name: "обратный слэш перед кавычкой", roots: []string{"100", "200"}, query: `a\"b`, want: searchCQLBeforeQuery + `a\\\"b"`},
		{name: "кириллица и звёздочка как есть", roots: []string{"100", "200"}, query: "диск*", want: searchCQLBeforeQuery + `диск*"`},
		{name: "пустой запрос", roots: []string{"100"}, query: "", wantErr: guard.ErrInvalidSearchQuery},
		{name: "перевод строки", roots: []string{"100"}, query: "a\nb", wantErr: guard.ErrInvalidSearchQuery},
		{name: "табуляция", roots: []string{"100"}, query: "a\tb", wantErr: guard.ErrInvalidSearchQuery},
		{name: "нулевой байт", roots: []string{"100"}, query: "a\x00b", wantErr: guard.ErrInvalidSearchQuery},
		{name: "DEL", roots: []string{"100"}, query: "a\x7fb", wantErr: guard.ErrInvalidSearchQuery},
		{name: "управляющий символ C1", roots: []string{"100"}, query: "a\u0085b", wantErr: guard.ErrInvalidSearchQuery},
		{name: "разделитель строк U+2028", roots: []string{"100"}, query: "a\u2028b", wantErr: guard.ErrInvalidSearchQuery},
		{name: "разделитель абзацев U+2029", roots: []string{"100"}, query: "a\u2029b", wantErr: guard.ErrInvalidSearchQuery},
		{name: "некорректный UTF-8", roots: []string{"100"}, query: "a\xc0\xa2", wantErr: guard.ErrInvalidSearchQuery},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := guard.SearchCQL(tt.roots, tt.query)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func searchURL(cql string, limit string) string {
	return contentURL + "/search?" + url.Values{"cql": {cql}, "limit": {limit}}.Encode()
}

func TestRoundTripSearch(t *testing.T) {
	cqlFromSearchCQL, err := guard.SearchCQL([]string{"100", "200"}, `disk "io" a\b`)
	require.NoError(t, err)
	tests := []struct {
		name        string
		cfg         config.Config
		method      string
		url         string
		requestBody string
		wantErr     error
	}{
		{name: "CQL от SearchCQL", cfg: allowedConfig, method: http.MethodGet, url: searchURL(cqlFromSearchCQL, "25")},
		{name: "экранированная кавычка в литерале", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`\" or space = X"`, "25")},
		{name: "экранированный обратный слэш в конце литерала", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`a\\"`, "25")},
		{name: "limit равен max_results", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "50")},
		{name: "включён только search", cfg: withTools(config.Tools{Search: true}), method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "25")},
		{name: "ключ cql в процентной кодировке", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/search?c%71l=" + url.QueryEscape(searchCQLBeforeQuery+`x"`) + "&limit=25"},

		{name: "search выключен", cfg: withTools(config.Tools{GetPage: true}), method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "неэкранированная кавычка в литерале", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x" or space = SECRET and text ~ "y"`, "25"), wantErr: guard.ErrForbidden},
		{name: "одиночный обратный слэш перед обычным символом", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`a\b"`, "25"), wantErr: guard.ErrForbidden},
		{name: "незавершённое экранирование", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`a\"`, "25"), wantErr: guard.ErrForbidden},
		{name: "перевод строки в литерале", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+"a\nb\"", "25"), wantErr: guard.ErrForbidden},
		{name: "пустой литерал", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`"`, "25"), wantErr: guard.ErrForbidden},
		{name: "без закрывающей кавычки", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x`, "25"), wantErr: guard.ErrForbidden},
		{name: "хвост после закрывающей кавычки", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x" or space = SECRET`, "25"), wantErr: guard.ErrForbidden},
		{name: "лишний корень в префиксе", cfg: allowedConfig, method: http.MethodGet, url: searchURL(`(id = 100 or ancestor = 100 or id = 200 or ancestor = 200 or ancestor = 300) and type = page and text ~ "x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "корни переставлены", cfg: allowedConfig, method: http.MethodGet, url: searchURL(`(id = 200 or ancestor = 200 or id = 100 or ancestor = 100) and type = page and text ~ "x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "только один корень из двух", cfg: allowedConfig, method: http.MethodGet, url: searchURL(`(id = 100 or ancestor = 100) and type = page and text ~ "x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "без type = page", cfg: allowedConfig, method: http.MethodGet, url: searchURL(`(id = 100 or ancestor = 100 or id = 200 or ancestor = 200) and text ~ "x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "title вместо text", cfg: allowedConfig, method: http.MethodGet, url: searchURL(`(id = 100 or ancestor = 100 or id = 200 or ancestor = 200) and type = page and title ~ "x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "ID в верхнем регистре", cfg: allowedConfig, method: http.MethodGet, url: searchURL(`(ID = 100 or ancestor = 100 or id = 200 or ancestor = 200) and type = page and text ~ "x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "OR в верхнем регистре", cfg: allowedConfig, method: http.MethodGet, url: searchURL(`(id = 100 OR ancestor = 100 or id = 200 or ancestor = 200) and type = page and text ~ "x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "CQL пустой", cfg: allowedConfig, method: http.MethodGet, url: searchURL("", "25"), wantErr: guard.ErrForbidden},
		{name: "без cql", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/search?limit=25", wantErr: guard.ErrForbidden},
		{name: "без limit", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/search?" + url.Values{"cql": {searchCQLBeforeQuery + `x"`}}.Encode(), wantErr: guard.ErrForbidden},
		{name: "limit больше max_results", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "51"), wantErr: guard.ErrForbidden},
		{name: "limit 0", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "0"), wantErr: guard.ErrForbidden},
		{name: "с start", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "25") + "&start=0", wantErr: guard.ErrForbidden},
		{name: "с expand", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "25") + "&expand=body.storage", wantErr: guard.ErrForbidden},
		{name: "с cqlcontext", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "25") + "&cqlcontext=%7B%7D", wantErr: guard.ErrForbidden},
		{name: "метод POST", cfg: allowedConfig, method: http.MethodPost, url: searchURL(searchCQLBeforeQuery+`x"`, "25"), wantErr: guard.ErrForbidden},
		{name: "search с лишним сегментом", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/search/x?" + url.Values{"cql": {searchCQLBeforeQuery + `x"`}, "limit": {"25"}}.Encode(), wantErr: guard.ErrForbidden},
		{name: "разделитель строк U+2028 в литерале", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+"a\u2028b\"", "25"), wantErr: guard.ErrForbidden},
		{name: "некорректный UTF-8 в литерале", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+"a\xc0\xa2\"", "25"), wantErr: guard.ErrForbidden},
		{name: "cql дважды", cfg: allowedConfig, method: http.MethodGet, url: contentURL + "/search?" + url.Values{"cql": {searchCQLBeforeQuery + `x"`, searchCQLBeforeQuery + `y"`}, "limit": {"25"}}.Encode(), wantErr: guard.ErrForbidden},
		{name: "GET поиска с телом", cfg: allowedConfig, method: http.MethodGet, url: searchURL(searchCQLBeforeQuery+`x"`, "25"), requestBody: "with-body", wantErr: guard.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			if tt.wantErr == nil {
				expectForwarded(t, nextTransport, tt.method, tt.url, "")
			}
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(newRequest(t, tt.method, tt.url, strings.NewReader(tt.requestBody)))

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}
