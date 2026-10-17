package guard_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/guard"
	"scoped-mcp/mcp-confluence/guard/mocks"
)

const commentQuery = "expand=body.storage,history,ancestors&depth=all&start=0&limit=100"

func TestRoundTripReadRoutes(t *testing.T) {
	const maxLengthID = "1234567890123456789"
	tests := []struct {
		name              string
		cfg               config.Config
		url               string
		host              string
		requestBody       string
		wantZoneCheckURL  string
		zoneCheckResponse string
		wantErr           error
	}{
		{name: "корень чтения", cfg: allowedConfig, url: contentURL + "/100?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "потомок корня чтения", cfg: allowedConfig, url: contentURL + "/101?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, "101"), zoneCheckResponse: childBody},
		{name: "глубокий потомок", cfg: allowedConfig, url: contentURL + "/102?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, "102"), zoneCheckResponse: deepBody},
		{name: "корень записи читается", cfg: allowedConfig, url: contentURL + "/200?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, "200"), zoneCheckResponse: writeRootBody},
		{name: "потомок корня записи читается", cfg: allowedConfig, url: contentURL + "/201?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, "201"), zoneCheckResponse: writeChildBody},
		{name: "id из 19 цифр", cfg: allowedConfig, url: contentURL + "/" + maxLengthID + "?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, maxLengthID), zoneCheckResponse: `{"id":"` + maxLengthID + `","type":"page","status":"current","ancestors":[{"id":"100"}]}`},
		{name: "expand только version", cfg: allowedConfig, url: contentURL + "/100?expand=version", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "expand только space", cfg: allowedConfig, url: contentURL + "/200?expand=space", wantZoneCheckURL: zoneCheckURLAt(baseURL, "200"), zoneCheckResponse: writeRootBody},
		{name: "base_url с путём", cfg: withBasePath(), url: basePathURL + "/rest/api/content/100?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(basePathURL, "100"), zoneCheckResponse: rootBody},
		{name: "заголовок Host совпадает с base_url", cfg: allowedConfig, url: contentURL + "/100?" + pageExpand, host: "confluence.example.com", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "включён только search", cfg: withTools(config.Tools{Search: true}), url: contentURL + "/100?expand=version", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "включён только create_page", cfg: withTools(config.Tools{CreatePage: true}), url: contentURL + "/200?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, "200"), zoneCheckResponse: writeRootBody},
		{name: "дети корня чтения", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=25", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "дети потомка корня записи", cfg: allowedConfig, url: contentURL + "/201/child/page?start=50&limit=100", wantZoneCheckURL: zoneCheckURLAt(baseURL, "201"), zoneCheckResponse: writeChildBody},
		{name: "комментарии", cfg: allowedConfig, url: contentURL + "/101/child/comment?" + commentQuery, wantZoneCheckURL: zoneCheckURLAt(baseURL, "101"), zoneCheckResponse: childBody},
		{name: "комментарии с expand только body.storage", cfg: allowedConfig, url: contentURL + "/101/child/comment?expand=body.storage&depth=all&start=0&limit=100", wantZoneCheckURL: zoneCheckURLAt(baseURL, "101"), zoneCheckResponse: childBody},
		{name: "список вложений", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "список вложений при одном upload_attachment", cfg: withTools(config.Tools{UploadAttachment: true}), url: contentURL + "/200/child/attachment?start=0&limit=100", wantZoneCheckURL: zoneCheckURLAt(baseURL, "200"), zoneCheckResponse: writeRootBody},
		{name: "список вложений с expand=version", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&expand=version", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "список вложений при одном get_attachment", cfg: withTools(config.Tools{GetAttachment: true}), url: contentURL + "/100/child/attachment?start=0&limit=100", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "скачивание", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report.md?version=2&api=v2", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},
		{name: "скачивание с именем в процентной кодировке (кириллица, пробел, запятая)", cfg: allowedConfig, url: baseURL + "/download/attachments/100/%D0%BE%D1%82%D1%87%D1%91%D1%82%20v2,final.png?version=1&api=v2", wantZoneCheckURL: zoneCheckURLAt(baseURL, "100"), zoneCheckResponse: rootBody},

		{name: "страница вне зоны", cfg: allowedConfig, url: contentURL + "/300?" + pageExpand, wantZoneCheckURL: zoneCheckURLAt(baseURL, "300"), zoneCheckResponse: outsideBody, wantErr: guard.ErrOutOfZone},
		{name: "включён только list_children", cfg: withTools(config.Tools{ListChildren: true}), url: contentURL + "/100?" + pageExpand, wantErr: guard.ErrForbidden},
		{name: "включён только update_page", cfg: withTools(config.Tools{UpdatePage: true}), url: contentURL + "/200?" + pageExpand, wantErr: guard.ErrForbidden},
		{name: "без expand", cfg: allowedConfig, url: contentURL + "/100", wantErr: guard.ErrForbidden},
		{name: "пустой query со знаком вопроса", cfg: allowedConfig, url: contentURL + "/100?", wantErr: guard.ErrForbidden},
		{name: "expand пустой", cfg: allowedConfig, url: contentURL + "/100?expand=", wantErr: guard.ErrForbidden},
		{name: "expand вне списка", cfg: allowedConfig, url: contentURL + "/100?expand=body.view", wantErr: guard.ErrForbidden},
		{name: "expand с history", cfg: allowedConfig, url: contentURL + "/100?expand=body.storage,history", wantErr: guard.ErrForbidden},
		{name: "expand с metadata.labels", cfg: allowedConfig, url: contentURL + "/100?expand=metadata.labels", wantErr: guard.ErrForbidden},
		{name: "expand с restrictions", cfg: allowedConfig, url: contentURL + "/100?expand=restrictions.read.restrictions.user", wantErr: guard.ErrForbidden},
		{name: "expand дважды", cfg: allowedConfig, url: contentURL + "/100?expand=version&expand=space", wantErr: guard.ErrForbidden},
		{name: "expand с повтором значения", cfg: allowedConfig, url: contentURL + "/100?expand=version,version", wantErr: guard.ErrForbidden},
		{name: "параметр status", cfg: allowedConfig, url: contentURL + "/100?" + pageExpand + "&status=trashed", wantErr: guard.ErrForbidden},
		{name: "параметр version", cfg: allowedConfig, url: contentURL + "/100?" + pageExpand + "&version=1", wantErr: guard.ErrForbidden},
		{name: "query с точкой с запятой", cfg: allowedConfig, url: contentURL + "/100?expand=version;status=draft", wantErr: guard.ErrForbidden},
		{name: "GET с телом", cfg: allowedConfig, url: contentURL + "/100?" + pageExpand, requestBody: "with-body", wantErr: guard.ErrForbidden},
		{name: "дети страницы вне зоны", cfg: allowedConfig, url: contentURL + "/300/child/page?start=0&limit=25", wantZoneCheckURL: zoneCheckURLAt(baseURL, "300"), zoneCheckResponse: outsideBody, wantErr: guard.ErrOutOfZone},
		{name: "дети: list_children выключен", cfg: withTools(config.Tools{GetPage: true}), url: contentURL + "/100/child/page?start=0&limit=25", wantErr: guard.ErrForbidden},
		{name: "дети без start", cfg: allowedConfig, url: contentURL + "/100/child/page?limit=25", wantErr: guard.ErrForbidden},
		{name: "дети без limit", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0", wantErr: guard.ErrForbidden},
		{name: "дети с limit 101", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=101", wantErr: guard.ErrForbidden},
		{name: "дети с limit 0", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=0", wantErr: guard.ErrForbidden},
		{name: "дети с отрицательным start", cfg: allowedConfig, url: contentURL + "/100/child/page?start=-1&limit=25", wantErr: guard.ErrForbidden},
		{name: "дети с expand", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=25&expand=body.storage", wantErr: guard.ErrForbidden},
		{name: "дети с expand=version", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=25&expand=version", wantErr: guard.ErrForbidden},
		{name: "дети с parentVersion", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=25&parentVersion=1", wantErr: guard.ErrForbidden},
		{name: "дети типа blogpost", cfg: allowedConfig, url: contentURL + "/100/child/blogpost?start=0&limit=25", wantErr: guard.ErrForbidden},
		{name: "комментарии: list_comments выключен", cfg: withTools(config.Tools{GetPage: true}), url: contentURL + "/101/child/comment?" + commentQuery, wantErr: guard.ErrForbidden},
		{name: "комментарии без depth", cfg: allowedConfig, url: contentURL + "/101/child/comment?expand=body.storage,history,ancestors&start=0&limit=100", wantErr: guard.ErrForbidden},
		{name: "комментарии с depth пустым", cfg: allowedConfig, url: contentURL + "/101/child/comment?expand=body.storage&depth=&start=0&limit=100", wantErr: guard.ErrForbidden},
		{name: "комментарии с location", cfg: allowedConfig, url: contentURL + "/101/child/comment?" + commentQuery + "&location=inline", wantErr: guard.ErrForbidden},
		{name: "комментарии с expand body.view", cfg: allowedConfig, url: contentURL + "/101/child/comment?expand=body.view&depth=all&start=0&limit=100", wantErr: guard.ErrForbidden},
		{name: "комментарии с expand space", cfg: allowedConfig, url: contentURL + "/101/child/comment?expand=space&depth=all&start=0&limit=100", wantErr: guard.ErrForbidden},
		{name: "комментарии без expand", cfg: allowedConfig, url: contentURL + "/101/child/comment?depth=all&start=0&limit=100", wantErr: guard.ErrForbidden},
		{name: "список вложений: list/get/upload_attachment выключены", cfg: withTools(config.Tools{GetPage: true}), url: contentURL + "/100/child/attachment?start=0&limit=100", wantErr: guard.ErrForbidden},
		{name: "список вложений с filename", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&filename=x.md", wantErr: guard.ErrForbidden},
		{name: "список вложений с expand=container", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&expand=container", wantErr: guard.ErrForbidden},
		{name: "список вложений с expand=body.storage", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&expand=body.storage", wantErr: guard.ErrForbidden},
		{name: "список вложений с expand=version,container", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&expand=version,container", wantErr: guard.ErrForbidden},
		{name: "список вложений с повтором version в expand", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&expand=version,version", wantErr: guard.ErrForbidden},
		{name: "список вложений с пустым expand", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&expand=", wantErr: guard.ErrForbidden},
		{name: "конкретное вложение", cfg: allowedConfig, url: contentURL + "/100/child/attachment/501", wantErr: guard.ErrForbidden},
		{name: "скачивание страницы вне зоны", cfg: allowedConfig, url: baseURL + "/download/attachments/300/report.md?version=1&api=v2", wantZoneCheckURL: zoneCheckURLAt(baseURL, "300"), zoneCheckResponse: outsideBody, wantErr: guard.ErrOutOfZone},
		{name: "скачивание: get_attachment выключен", cfg: withTools(config.Tools{ListAttachments: true}), url: baseURL + "/download/attachments/100/report.md?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание без version", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report.md?api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание без api", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report.md?version=1", wantErr: guard.ErrForbidden},
		{name: "скачивание с api=v1", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report.md?version=1&api=v1", wantErr: guard.ErrForbidden},
		{name: "скачивание с version 0", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report.md?version=0&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с modificationDate", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report.md?version=1&modificationDate=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание без имени", cfg: allowedConfig, url: baseURL + "/download/attachments/100/?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с лишним сегментом", cfg: allowedConfig, url: baseURL + "/download/attachments/100/sub/report.md?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с id не числом", cfg: allowedConfig, url: baseURL + "/download/attachments/abc/report.md?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с %2F в имени", cfg: allowedConfig, url: baseURL + "/download/attachments/100/..%2Freport.md?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с именем ..", cfg: allowedConfig, url: baseURL + "/download/attachments/100/..?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с именем .", cfg: allowedConfig, url: baseURL + "/download/attachments/100/.?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с %5C в имени", cfg: allowedConfig, url: baseURL + "/download/attachments/100/..%5C300%5Cx.png?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с ; в имени", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report.md;jsessionid=1?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с кавычкой в имени", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report%22v2.md?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с U+202E в имени", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report%E2%80%AEdm.sh?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "скачивание с %00 в имени", cfg: allowedConfig, url: baseURL + "/download/attachments/100/report%00.md?version=1&api=v2", wantErr: guard.ErrForbidden},
		{name: "дети с limit -1", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=-1", wantErr: guard.ErrForbidden},
		{name: "дети с limit не числом", cfg: allowedConfig, url: contentURL + "/100/child/page?start=0&limit=abc", wantErr: guard.ErrForbidden},
		{name: "дети со start не числом", cfg: allowedConfig, url: contentURL + "/100/child/page?start=abc&limit=25", wantErr: guard.ErrForbidden},
		{name: "комментарии с depth=root", cfg: allowedConfig, url: contentURL + "/101/child/comment?expand=body.storage&depth=root&start=0&limit=100", wantErr: guard.ErrForbidden},
		{name: "список вложений с mediaType", cfg: allowedConfig, url: contentURL + "/100/child/attachment?start=0&limit=100&mediaType=image/png", wantErr: guard.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectZoneCheckThenForward(t, nextTransport, tt.wantZoneCheckURL, tt.zoneCheckResponse, tt.wantErr, http.MethodGet, tt.url, "")
			guardTransport, err := guard.New(tt.cfg, nextTransport)
			require.NoError(t, err)
			request := newRequest(t, http.MethodGet, tt.url, strings.NewReader(tt.requestBody))
			if tt.host != "" {
				request.Host = tt.host
			}

			response, err := guardTransport.RoundTrip(request)

			requireOutcome(t, tt.wantErr, response, err)
		})
	}
}
