package guard_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/guard"
	"scoped-mcp/mcp-confluence/guard/mocks"
)

const responseFragment = "SECRET-SPACE"

func zoneResponseBody(ctrl *gomock.Controller, body string, bodyReadErr error) io.ReadCloser {
	responseBody := mocks.NewMockReadCloser(ctrl)
	if bodyReadErr != nil {
		responseBody.EXPECT().Read(gomock.Any()).Return(0, bodyReadErr).AnyTimes()
	} else {
		responseBody.EXPECT().Read(gomock.Any()).DoAndReturn(strings.NewReader(body).Read).AnyTimes()
	}
	responseBody.EXPECT().Close().Return(nil)
	return responseBody
}

func TestRoundTripZoneCheckResponse(t *testing.T) {
	errBodyRead := errors.New(`unexpected EOF after "` + responseFragment + `"`)
	errNetwork := errors.New(`net/http: malformed HTTP response "` + responseFragment + `"`)
	oversizedBody := `{"id":"100","type":"page","status":"current","title":"` + strings.Repeat("x", guard.MaxBodyBytes) + `"}`
	tests := []struct {
		name              string
		checkStatus       int
		checkBody         string
		checkBodyReadErr  error
		checkTransportErr error
		wantErr           error
	}{
		{name: "проверка вернула 403", checkStatus: http.StatusForbidden, checkBody: `{"message":"No permission"}`, wantErr: guard.ErrOutOfZone},
		{name: "проверка вернула 404", checkStatus: http.StatusNotFound, checkBody: `{"message":"No content found with id: 100"}`, wantErr: guard.ErrOutOfZone},
		{name: "проверка вернула 401", checkStatus: http.StatusUnauthorized, checkBody: ``, wantErr: guard.ErrTokenRejected},
		{name: "проверка вернула 500", checkStatus: http.StatusInternalServerError, checkBody: `<html>oops</html>`, wantErr: guard.ErrZoneCheckFailed},
		{name: "проверка вернула 302", checkStatus: http.StatusFound, checkBody: ``, wantErr: guard.ErrZoneCheckFailed},
		{name: "ответ не JSON", checkStatus: http.StatusOK, checkBody: `<html>login</html>`, wantErr: guard.ErrZoneCheckFailed},
		{name: "ответ больше 8 MiB", checkStatus: http.StatusOK, checkBody: oversizedBody, wantErr: guard.ErrZoneCheckFailed},
		{name: "ответ не читается", checkStatus: http.StatusOK, checkBodyReadErr: errBodyRead, wantErr: guard.ErrZoneCheckFailed},
		{name: "сетевая ошибка проверки", checkTransportErr: errNetwork, wantErr: guard.ErrZoneCheckFailed},
		{name: "не page", checkStatus: http.StatusOK, checkBody: `{"id":"100","type":"blogpost","status":"current","ancestors":[]}`, wantErr: guard.ErrOutOfZone},
		{name: "комментарий под корнем", checkStatus: http.StatusOK, checkBody: `{"id":"100","type":"comment","status":"current","ancestors":[{"id":"100"}]}`, wantErr: guard.ErrOutOfZone},
		{name: "не current", checkStatus: http.StatusOK, checkBody: `{"id":"100","type":"page","status":"trashed","ancestors":[{"id":"1"}]}`, wantErr: guard.ErrOutOfZone},
		{name: "страница с id в ответе с префиксом att", checkStatus: http.StatusOK, checkBody: `{"id":"att100","type":"page","status":"current","ancestors":[{"id":"1"}]}`, wantErr: guard.ErrZoneCheckFailed},
		{name: "ответ с другим id", checkStatus: http.StatusOK, checkBody: `{"id":"999","type":"page","status":"current","ancestors":[{"id":"100"}]}`, wantErr: guard.ErrZoneCheckFailed},
		{name: "id — корень, предки вне корней", checkStatus: http.StatusOK, checkBody: `{"id":"100","type":"page","status":"current","ancestors":[{"id":"1"},{"id":"2"}]}`, wantErr: nil},
		{name: "id — корень, container — пространство с числовым id", checkStatus: http.StatusOK, checkBody: `{"id":"100","type":"page","status":"current","space":{"id":98305,"key":"DOCS"},"container":{"id":98305,"key":"DOCS","type":"global"},"ancestors":[{"id":"1"}]}`, wantErr: nil},
		{name: "id — корень без предков в ответе", checkStatus: http.StatusOK, checkBody: `{"id":"100","type":"page","status":"current"}`, wantErr: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextTransport := mocks.NewMockRoundTripper(ctrl)
			calls := []any{}
			if tt.checkTransportErr != nil {
				calls = append(calls, nextTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.checkTransportErr))
			} else {
				calls = append(calls, expectZoneCheck(t, nextTransport, zoneCheckURLAt(baseURL, "100"), tt.checkStatus, zoneResponseBody(ctrl, tt.checkBody, tt.checkBodyReadErr)))
			}
			if tt.wantErr == nil {
				calls = append(calls, expectForwarded(t, nextTransport, http.MethodGet, contentURL+"/100?"+pageExpand, ""))
			}
			gomock.InOrder(calls...)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(newRequest(t, http.MethodGet, contentURL+"/100?"+pageExpand, nil))

			requireOutcome(t, tt.wantErr, response, err)
			require.NotErrorIs(t, err, guard.ErrForbidden)
			require.NotContains(t, fmt.Sprint(err), responseFragment)
		})
	}
}

func TestRoundTripZoneCheckErrorOmitsResponseBody(t *testing.T) {
	tests := []struct {
		name         string
		checkBody    string
		bodyFragment string
	}{
		{name: "ответ не JSON", checkBody: `<html>login</html>`, bodyFragment: "<"},
		{name: "ответ с другим id", checkBody: `{"id":"98765","type":"page","status":"current","ancestors":[{"id":"100"}]}`, bodyFragment: "98765"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectZoneCheckJSON(t, nextTransport, zoneCheckURLAt(baseURL, "100"), tt.checkBody)
			guardTransport, err := guard.New(allowedConfig, nextTransport)
			require.NoError(t, err)

			response, err := guardTransport.RoundTrip(newRequest(t, http.MethodGet, contentURL+"/100?"+pageExpand, nil))

			require.ErrorIs(t, err, guard.ErrZoneCheckFailed)
			require.Nil(t, response)
			require.NotContains(t, err.Error(), tt.bodyFragment)
		})
	}
}
