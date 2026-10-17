package jira_test

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/guard"
	"scoped-mcp/mcp-jira/guard/mocks"
	"scoped-mcp/mcp-jira/jira"
)

const (
	jiraBaseURL   = "https://jira.example.com"
	issueURL      = jiraBaseURL + "/rest/api/2/issue"
	tokenOwnerURL = jiraBaseURL + "/rest/api/2/myself"
	token         = "pat-secret"
)

var epicRequest = jira.CreateRequest{
	ProjectKey:  "PROJ",
	IssueTypeID: "10000",
	Summary:     "Прогрев образов",
	Fields: map[string]jira.FieldValue{
		"description":       {Kind: config.KindText, Text: "Описание"},
		"priority":          {Kind: config.KindName, Text: "High"},
		"components":        {Kind: config.KindNames, Items: []string{"core", "api"}},
		"customfield_10300": {Kind: config.KindOption, Text: "Backend"},
		"customfield_10301": {Kind: config.KindOptions, Items: []string{"A", "B"}},
		"labels":            {Kind: config.KindLabels, Items: []string{"backend"}},
		"customfield_10201": {Kind: config.KindIssueKey, Text: "PROJ-7"},
	},
}

const epicBody = `{"fields":{
  "project":{"key":"PROJ"},
  "issuetype":{"id":"10000"},
  "summary":"Прогрев образов",
  "description":"Описание",
  "priority":{"name":"High"},
  "components":[{"name":"core"},{"name":"api"}],
  "customfield_10300":{"value":"Backend"},
  "customfield_10301":[{"value":"A"},{"value":"B"}],
  "labels":["backend"],
  "customfield_10201":"PROJ-7"
}}`

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func htmlResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func emptyResponse(statusCode int) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{},
		Body:       http.NoBody,
	}
}

func unreadableResponse(statusCode int) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(iotest.ErrReader(errors.New("body read failed"))),
	}
}

func redirectResponse(location string) *http.Response {
	response := emptyResponse(http.StatusFound)
	response.Header.Set("Location", location)
	return response
}

func TestCreateIssue(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		request  jira.CreateRequest
		wantURL  string
		wantBody string
		response *http.Response
		want     jira.Issue
		wantErr  error
	}{
		{
			name:     "все виды полей → тело запроса и ключ из ответа",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: jsonResponse(http.StatusCreated, `{"id":"1","key":"PROJ-7790","self":"x"}`),
			want:     jira.Issue{Key: "PROJ-7790", URL: jiraBaseURL + "/browse/PROJ-7790"},
		},
		{
			name:     "только обязательные → тело без доп. полей",
			baseURL:  jiraBaseURL,
			request:  jira.CreateRequest{ProjectKey: "OTHER", IssueTypeID: "3", Summary: "s"},
			wantURL:  issueURL,
			wantBody: `{"fields":{"project":{"key":"OTHER"},"issuetype":{"id":"3"},"summary":"s"}}`,
			response: jsonResponse(http.StatusCreated, `{"key":"OTHER-1"}`),
			want:     jira.Issue{Key: "OTHER-1", URL: jiraBaseURL + "/browse/OTHER-1"},
		},
		{
			name:    "списки без значений → пустые массивы",
			baseURL: jiraBaseURL,
			request: jira.CreateRequest{
				ProjectKey:  "OTHER",
				IssueTypeID: "3",
				Summary:     "s",
				Fields: map[string]jira.FieldValue{
					"labels":            {Kind: config.KindLabels},
					"components":        {Kind: config.KindNames},
					"customfield_10301": {Kind: config.KindOptions},
				},
			},
			wantURL: issueURL,
			wantBody: `{"fields":{"project":{"key":"OTHER"},"issuetype":{"id":"3"},"summary":"s",
				"labels":[],"components":[],"customfield_10301":[]}}`,
			response: jsonResponse(http.StatusCreated, `{"key":"OTHER-2"}`),
			want:     jira.Issue{Key: "OTHER-2", URL: jiraBaseURL + "/browse/OTHER-2"},
		},
		{
			name:     "base_url с путём → путь в запросе и в ссылке",
			baseURL:  jiraBaseURL + "/jira",
			request:  epicRequest,
			wantURL:  jiraBaseURL + "/jira/rest/api/2/issue",
			wantBody: epicBody,
			response: jsonResponse(http.StatusCreated, `{"key":"PROJ-1"}`),
			want:     jira.Issue{Key: "PROJ-1", URL: jiraBaseURL + "/jira/browse/PROJ-1"},
		},
		{
			name:     "401 → ErrUnauthorized",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: emptyResponse(http.StatusUnauthorized),
			wantErr:  jira.ErrUnauthorized,
		},
		{
			name:     "401, тело не читается → ErrUnauthorized",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: unreadableResponse(http.StatusUnauthorized),
			wantErr:  jira.ErrUnauthorized,
		},
		{
			name:     "400 с errorMessages и errors → StatusError с сообщениями и полями",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: jsonResponse(
				http.StatusBadRequest,
				`{"errorMessages":["Field 'customfield_10203' cannot be set."],"errors":{"priority":"Priority is required."}}`,
			),
			wantErr: &jira.StatusError{
				StatusCode:  http.StatusBadRequest,
				Messages:    []string{"Field 'customfield_10203' cannot be set."},
				FieldErrors: map[string]string{"priority": "Priority is required."},
			},
		},
		{
			name:     "400, тело не читается → StatusError только со статусом",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: unreadableResponse(http.StatusBadRequest),
			wantErr:  &jira.StatusError{StatusCode: http.StatusBadRequest},
		},
		{
			name:     "403 без тела → StatusError только со статусом",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: emptyResponse(http.StatusForbidden),
			wantErr:  &jira.StatusError{StatusCode: http.StatusForbidden},
		},
		{
			name:     "500 с HTML → ErrMaybeCreated и StatusError, тело игнорируется",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: htmlResponse(http.StatusInternalServerError, `<html>oops</html>`),
			wantErr:  errors.Join(jira.ErrMaybeCreated, &jira.StatusError{StatusCode: http.StatusInternalServerError}),
		},
		{
			name:     "503 с errorMessages → ErrMaybeCreated и StatusError с сообщениями",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: jsonResponse(http.StatusServiceUnavailable, `{"errorMessages":["Jira is restarting."]}`),
			wantErr: errors.Join(jira.ErrMaybeCreated, &jira.StatusError{
				StatusCode: http.StatusServiceUnavailable,
				Messages:   []string{"Jira is restarting."},
			}),
		},
		{
			name:     "редирект → StatusError 302, не следуем",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: redirectResponse("https://login.example.com/"),
			wantErr:  &jira.StatusError{StatusCode: http.StatusFound},
		},
		{
			name:     "201 без key → ErrUnexpectedResponse",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: jsonResponse(http.StatusCreated, `{"id":"1"}`),
			wantErr:  jira.ErrUnexpectedResponse,
		},
		{
			name:     "201 не JSON → ErrUnexpectedResponse",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: htmlResponse(http.StatusCreated, `<html>created?</html>`),
			wantErr:  jira.ErrUnexpectedResponse,
		},
		{
			name:     "201, тело не читается → ErrUnexpectedResponse",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: unreadableResponse(http.StatusCreated),
			wantErr:  jira.ErrUnexpectedResponse,
		},
		{
			name:     "200 с телом → ErrMaybeCreated",
			baseURL:  jiraBaseURL,
			request:  epicRequest,
			wantURL:  issueURL,
			wantBody: epicBody,
			response: jsonResponse(http.StatusOK, `{"key":"PROJ-1"}`),
			wantErr:  jira.ErrMaybeCreated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectJSONPost(t, mockTransport, tt.wantURL, tt.wantBody).Return(tt.response, nil)
			jiraClient := jira.New(tt.baseURL, token, mockTransport)

			got, err := jiraClient.CreateIssue(t.Context(), tt.request)

			require.Equal(t, tt.want, got)
			requireMatchingError(t, tt.wantErr, err)
		})
	}
}

func requireMatchingError(t *testing.T, wantErr, err error) {
	t.Helper()
	if wantJoined, ok := wantErr.(interface{ Unwrap() []error }); ok {
		for _, wantPart := range wantJoined.Unwrap() {
			requireMatchingError(t, wantPart, err)
		}
		return
	}
	var wantStatusErr *jira.StatusError
	if !errors.As(wantErr, &wantStatusErr) {
		require.ErrorIs(t, err, wantErr)
		return
	}
	var gotStatusErr *jira.StatusError
	require.ErrorAs(t, err, &gotStatusErr)
	require.Equal(t, wantStatusErr, gotStatusErr)
}

func TestCreateIssueTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{
			name:         "сетевая ошибка → ErrMaybeCreated с причиной",
			transportErr: errNetwork,
			wantErr:      jira.ErrMaybeCreated,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "отказ guard → проходит как есть",
			transportErr: guard.ErrForbidden,
			wantErr:      guard.ErrForbidden,
			wantNotErr:   jira.ErrMaybeCreated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			got, err := jira.New(jiraBaseURL, token, mockTransport).CreateIssue(t.Context(), epicRequest)

			require.Equal(t, jira.Issue{}, got)
			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
		})
	}
}

var selfAssignedRequest = jira.CreateRequest{ProjectKey: "PROJ", IssueTypeID: "10000", Summary: "s", AssignToSelf: true}

const selfAssignedBody = `{"fields":{"project":{"key":"PROJ"},"issuetype":{"id":"10000"},"summary":"s","assignee":{"name":"token.owner"}}}`

func TestCreateIssueAssignToSelf(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name               string
		tokenOwnerResponse *http.Response
		tokenOwnerErr      error
		wantCreateSent     bool
		createResponse     *http.Response
		want               jira.Issue
		wantErr            error
		wantNotErr         error
	}{
		{
			name:               "логин из запроса владельца токена → assignee в теле создания",
			tokenOwnerResponse: jsonResponse(http.StatusOK, `{"name":"token.owner","displayName":"Token Owner"}`),
			wantCreateSent:     true,
			createResponse:     jsonResponse(http.StatusCreated, `{"key":"PROJ-1"}`),
			want:               jira.Issue{Key: "PROJ-1", URL: jiraBaseURL + "/browse/PROJ-1"},
			wantNotErr:         jira.ErrTokenOwnerUnknown,
		},
		{
			name:               "запрос владельца токена: 401 → ErrUnauthorized",
			tokenOwnerResponse: emptyResponse(http.StatusUnauthorized),
			wantErr:            jira.ErrUnauthorized,
			wantNotErr:         jira.ErrTokenOwnerUnknown,
		},
		{
			name:               "запрос владельца токена: 403 → ErrTokenOwnerUnknown и StatusError",
			tokenOwnerResponse: jsonResponse(http.StatusForbidden, `{"errorMessages":["No access."]}`),
			wantErr:            errors.Join(jira.ErrTokenOwnerUnknown, &jira.StatusError{StatusCode: http.StatusForbidden, Messages: []string{"No access."}}),
			wantNotErr:         jira.ErrUnauthorized,
		},
		{
			name:               "запрос владельца токена: 500 → ErrTokenOwnerUnknown и StatusError",
			tokenOwnerResponse: htmlResponse(http.StatusInternalServerError, `<html>oops</html>`),
			wantErr:            errors.Join(jira.ErrTokenOwnerUnknown, &jira.StatusError{StatusCode: http.StatusInternalServerError}),
			wantNotErr:         jira.ErrUnauthorized,
		},
		{
			name:               "запрос владельца токена: редирект → ErrTokenOwnerUnknown и StatusError 302, не следуем",
			tokenOwnerResponse: redirectResponse("https://login.example.com/"),
			wantErr:            errors.Join(jira.ErrTokenOwnerUnknown, &jira.StatusError{StatusCode: http.StatusFound}),
			wantNotErr:         jira.ErrUnauthorized,
		},
		{
			name:               "запрос владельца токена: 200 без name → ErrTokenOwnerUnknown",
			tokenOwnerResponse: jsonResponse(http.StatusOK, `{"displayName":"Token Owner"}`),
			wantErr:            jira.ErrTokenOwnerUnknown,
			wantNotErr:         jira.ErrUnauthorized,
		},
		{
			name:               "запрос владельца токена: 200 не JSON → ErrTokenOwnerUnknown",
			tokenOwnerResponse: htmlResponse(http.StatusOK, `<html>login</html>`),
			wantErr:            jira.ErrTokenOwnerUnknown,
			wantNotErr:         jira.ErrUnauthorized,
		},
		{
			name:               "запрос владельца токена: 200, тело не читается → ErrTokenOwnerUnknown",
			tokenOwnerResponse: unreadableResponse(http.StatusOK),
			wantErr:            jira.ErrTokenOwnerUnknown,
			wantNotErr:         jira.ErrUnauthorized,
		},
		{
			name:          "запрос владельца токена: сетевая ошибка → ErrTokenOwnerUnknown с причиной",
			tokenOwnerErr: errNetwork,
			wantErr:       errors.Join(jira.ErrTokenOwnerUnknown, errNetwork),
			wantNotErr:    guard.ErrForbidden,
		},
		{
			name:          "запрос владельца токена: ответ не прочитан guard → ErrTokenOwnerUnknown с причиной",
			tokenOwnerErr: guard.ErrTokenOwnerResponse,
			wantErr:       errors.Join(jira.ErrTokenOwnerUnknown, guard.ErrTokenOwnerResponse),
			wantNotErr:    guard.ErrForbidden,
		},
		{
			name:          "запрос владельца токена: отказ guard → проходит как есть",
			tokenOwnerErr: guard.ErrForbidden,
			wantErr:       guard.ErrForbidden,
			wantNotErr:    jira.ErrTokenOwnerUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			calls := []any{expectTokenOwnerLookup(t, mockTransport).Return(tt.tokenOwnerResponse, tt.tokenOwnerErr)}
			if tt.wantCreateSent {
				calls = append(calls, expectJSONPost(t, mockTransport, issueURL, selfAssignedBody).Return(tt.createResponse, nil))
			}
			gomock.InOrder(calls...)

			got, err := jira.New(jiraBaseURL, token, mockTransport).CreateIssue(t.Context(), selfAssignedRequest)

			require.Equal(t, tt.want, got)
			requireMatchingError(t, tt.wantErr, err)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
			require.NotErrorIs(t, err, tt.wantNotErr)
		})
	}
}

func TestCreateIssueRemembersTokenOwnerOnlyAfterSuccess(t *testing.T) {
	mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
	gomock.InOrder(
		expectTokenOwnerLookup(t, mockTransport).Return(emptyResponse(http.StatusServiceUnavailable), nil),
		expectTokenOwnerLookup(t, mockTransport).Return(jsonResponse(http.StatusOK, `{"name":"token.owner"}`), nil),
		expectJSONPost(t, mockTransport, issueURL, selfAssignedBody).Return(jsonResponse(http.StatusCreated, `{"key":"PROJ-1"}`), nil),
		expectJSONPost(t, mockTransport, issueURL, selfAssignedBody).Return(jsonResponse(http.StatusCreated, `{"key":"PROJ-2"}`), nil),
	)
	jiraClient := jira.New(jiraBaseURL, token, mockTransport)

	_, err := jiraClient.CreateIssue(t.Context(), selfAssignedRequest)
	require.ErrorIs(t, err, jira.ErrTokenOwnerUnknown)

	first, err := jiraClient.CreateIssue(t.Context(), selfAssignedRequest)
	require.NoError(t, err)
	require.Equal(t, "PROJ-1", first.Key)

	second, err := jiraClient.CreateIssue(t.Context(), selfAssignedRequest)
	require.NoError(t, err)
	require.Equal(t, "PROJ-2", second.Key)
}

func expectTokenOwnerLookup(t *testing.T, mockTransport *mocks.MockRoundTripper) *gomock.Call {
	t.Helper()
	return mockTransport.EXPECT().RoundTrip(gomock.Any()).Do(func(sent *http.Request) {
		require.Equal(t, http.MethodGet, sent.Method)
		require.Equal(t, tokenOwnerURL, sent.URL.String())
		require.Equal(t, "Bearer "+token, sent.Header.Get("Authorization"))
		require.Equal(t, "application/json", sent.Header.Get("Accept"))
		require.True(t, sent.Body == nil || sent.Body == http.NoBody)
	})
}

func expectJSONPost(t *testing.T, mockTransport *mocks.MockRoundTripper, wantURL, wantBody string) *gomock.Call {
	t.Helper()
	return expectJSONRequest(t, mockTransport, http.MethodPost, wantURL, wantBody)
}

func expectJSONPut(t *testing.T, mockTransport *mocks.MockRoundTripper, wantURL, wantBody string) *gomock.Call {
	t.Helper()
	return expectJSONRequest(t, mockTransport, http.MethodPut, wantURL, wantBody)
}

func expectJSONRequest(t *testing.T, mockTransport *mocks.MockRoundTripper, wantMethod, wantURL, wantBody string) *gomock.Call {
	t.Helper()
	return mockTransport.EXPECT().RoundTrip(gomock.Any()).Do(func(sent *http.Request) {
		require.Equal(t, wantMethod, sent.Method)
		require.Equal(t, wantURL, sent.URL.String())
		require.Equal(t, "Bearer "+token, sent.Header.Get("Authorization"))
		require.Equal(t, "application/json", sent.Header.Get("Content-Type"))
		require.Equal(t, "application/json", sent.Header.Get("Accept"))
		sentBody, err := io.ReadAll(sent.Body)
		require.NoError(t, err)
		require.JSONEq(t, wantBody, string(sentBody))
	})
}

var reportFile = jira.AttachmentFile{Name: "отчёт \"v2\".md", Content: []byte("# Отчёт\n")}

const attachURL = issueURL + "/PROJ-7790/attachments"

func TestAttachFile(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		wantURL  string
		response *http.Response
		want     jira.Attachment
		wantErr  error
	}{
		{
			name:     "200 → id первого вложения",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: jsonResponse(http.StatusOK, `[{"self":"x","id":"10501","filename":"отчёт \"v2\".md","size":8}]`),
			want:     jira.Attachment{ID: "10501"},
		},
		{
			name:     "base_url с путём → путь в запросе",
			baseURL:  jiraBaseURL + "/jira",
			wantURL:  jiraBaseURL + "/jira/rest/api/2/issue/PROJ-7790/attachments",
			response: jsonResponse(http.StatusOK, `[{"id":"10502","filename":"a.md"}]`),
			want:     jira.Attachment{ID: "10502"},
		},
		{
			name:     "200 с пустым массивом → ErrAttachUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: jsonResponse(http.StatusOK, `[]`),
			wantErr:  jira.ErrAttachUnconfirmed,
		},
		{
			name:     "200 без id → ErrAttachUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: jsonResponse(http.StatusOK, `[{"filename":"a.md"}]`),
			wantErr:  jira.ErrAttachUnconfirmed,
		},
		{
			name:     "200 объектом вместо массива → ErrAttachUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: jsonResponse(http.StatusOK, `{"id":"10501"}`),
			wantErr:  jira.ErrAttachUnconfirmed,
		},
		{
			name:     "200 не JSON → ErrAttachUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: htmlResponse(http.StatusOK, `<html>login</html>`),
			wantErr:  jira.ErrAttachUnconfirmed,
		},
		{
			name:     "200, тело не читается → ErrAttachUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: unreadableResponse(http.StatusOK),
			wantErr:  jira.ErrAttachUnconfirmed,
		},
		{
			name:     "201 → ErrAttachUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: jsonResponse(http.StatusCreated, `[{"id":"10501"}]`),
			wantErr:  jira.ErrAttachUnconfirmed,
		},
		{
			name:     "401 → ErrUnauthorized",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: emptyResponse(http.StatusUnauthorized),
			wantErr:  jira.ErrUnauthorized,
		},
		{
			name:     "404 с errorMessages → StatusError",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: jsonResponse(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`),
			wantErr: &jira.StatusError{
				StatusCode: http.StatusNotFound,
				Messages:   []string{"Issue does not exist or you do not have permission to see it."},
			},
		},
		{
			name:     "413 → StatusError",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: htmlResponse(http.StatusRequestEntityTooLarge, `<html>too large</html>`),
			wantErr:  &jira.StatusError{StatusCode: http.StatusRequestEntityTooLarge},
		},
		{
			name:     "редирект → StatusError 302, не следуем",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: redirectResponse("https://jira.example.com/rest/api/2/issue/OTHER-1/attachments"),
			wantErr:  &jira.StatusError{StatusCode: http.StatusFound},
		},
		{
			name:     "500 → ErrAttachUnconfirmed и StatusError",
			baseURL:  jiraBaseURL,
			wantURL:  attachURL,
			response: jsonResponse(http.StatusInternalServerError, `{"errorMessages":["Disk full."]}`),
			wantErr: errors.Join(jira.ErrAttachUnconfirmed, &jira.StatusError{
				StatusCode: http.StatusInternalServerError,
				Messages:   []string{"Disk full."},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectAttach(t, mockTransport, tt.wantURL, reportFile).Return(tt.response, nil)

			got, err := jira.New(tt.baseURL, token, mockTransport).AttachFile(t.Context(), "PROJ-7790", reportFile)

			require.Equal(t, tt.want, got)
			requireMatchingError(t, tt.wantErr, err)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

func TestAttachFileTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{
			name:         "сетевая ошибка → ErrAttachUnconfirmed с причиной",
			transportErr: errNetwork,
			wantErr:      jira.ErrAttachUnconfirmed,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "отказ guard → проходит как есть",
			transportErr: guard.ErrForbidden,
			wantErr:      guard.ErrForbidden,
			wantNotErr:   jira.ErrAttachUnconfirmed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			got, err := jira.New(jiraBaseURL, token, mockTransport).AttachFile(t.Context(), "PROJ-7790", reportFile)

			require.Equal(t, jira.Attachment{}, got)
			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

func expectAttach(t *testing.T, mockTransport *mocks.MockRoundTripper, wantURL string, wantFile jira.AttachmentFile) *gomock.Call {
	t.Helper()
	return mockTransport.EXPECT().RoundTrip(gomock.Any()).Do(func(sent *http.Request) {
		require.Equal(t, http.MethodPost, sent.Method)
		require.Equal(t, wantURL, sent.URL.String())
		require.Equal(t, "Bearer "+token, sent.Header.Get("Authorization"))
		require.Equal(t, "application/json", sent.Header.Get("Accept"))
		require.Equal(t, "no-check", sent.Header.Get("X-Atlassian-Token"))
		mediaType, params, err := mime.ParseMediaType(sent.Header.Get("Content-Type"))
		require.NoError(t, err)
		require.Equal(t, "multipart/form-data", mediaType)

		reader := multipart.NewReader(sent.Body, params["boundary"])
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

const (
	commentText     = "h3. Итог\n*готово*, см. \"v2\""
	commentBody     = `{"body":"h3. Итог\n*готово*, см. \"v2\""}`
	commentAPIURL   = issueURL + "/PROJ-7790/comment"
	commentIssueKey = "PROJ-7790"
)

func TestAddComment(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		wantURL  string
		response *http.Response
		want     jira.Comment
		wantErr  error
	}{
		{
			name:     "201 → id и ссылка на комментарий",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusCreated, `{"self":"x","id":"10100","body":"h3. Итог"}`),
			want:     jira.Comment{ID: "10100", URL: jiraBaseURL + "/browse/PROJ-7790?focusedCommentId=10100#comment-10100"},
		},
		{
			name:     "base_url с путём → путь в запросе и в ссылке",
			baseURL:  jiraBaseURL + "/jira",
			wantURL:  jiraBaseURL + "/jira/rest/api/2/issue/PROJ-7790/comment",
			response: jsonResponse(http.StatusCreated, `{"id":"10101"}`),
			want:     jira.Comment{ID: "10101", URL: jiraBaseURL + "/jira/browse/PROJ-7790?focusedCommentId=10101#comment-10101"},
		},
		{
			name:     "201 с нечисловым id → id экранируется в query и во фрагменте ссылки",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusCreated, `{"id":"a b&c#d"}`),
			want:     jira.Comment{ID: "a b&c#d", URL: jiraBaseURL + "/browse/PROJ-7790?focusedCommentId=a+b%26c%23d#comment-a%20b&c%23d"},
		},
		{
			name:     "201 без id → ErrCommentUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusCreated, `{"body":"h3. Итог"}`),
			wantErr:  jira.ErrCommentUnconfirmed,
		},
		{
			name:     "201 с пустым id → ErrCommentUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusCreated, `{"id":""}`),
			wantErr:  jira.ErrCommentUnconfirmed,
		},
		{
			name:     "201 не JSON → ErrCommentUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: htmlResponse(http.StatusCreated, `<html>created?</html>`),
			wantErr:  jira.ErrCommentUnconfirmed,
		},
		{
			name:     "201, тело не читается → ErrCommentUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: unreadableResponse(http.StatusCreated),
			wantErr:  jira.ErrCommentUnconfirmed,
		},
		{
			name:     "200 → ErrCommentUnconfirmed",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusOK, `{"id":"10100"}`),
			wantErr:  jira.ErrCommentUnconfirmed,
		},
		{
			name:     "401 → ErrUnauthorized",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: emptyResponse(http.StatusUnauthorized),
			wantErr:  jira.ErrUnauthorized,
		},
		{
			name:     "400 с errors → StatusError",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusBadRequest, `{"errorMessages":[],"errors":{"comment":"Comment body can not be empty!"}}`),
			wantErr: &jira.StatusError{
				StatusCode:  http.StatusBadRequest,
				Messages:    []string{},
				FieldErrors: map[string]string{"comment": "Comment body can not be empty!"},
			},
		},
		{
			name:     "404 с errorMessages → StatusError",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`),
			wantErr: &jira.StatusError{
				StatusCode: http.StatusNotFound,
				Messages:   []string{"Issue does not exist or you do not have permission to see it."},
			},
		},
		{
			name:     "редирект → StatusError 302, не следуем",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: redirectResponse("https://login.example.com/"),
			wantErr:  &jira.StatusError{StatusCode: http.StatusFound},
		},
		{
			name:     "500 → ErrCommentUnconfirmed и StatusError",
			baseURL:  jiraBaseURL,
			wantURL:  commentAPIURL,
			response: jsonResponse(http.StatusInternalServerError, `{"errorMessages":["Database is unavailable."]}`),
			wantErr: errors.Join(jira.ErrCommentUnconfirmed, &jira.StatusError{
				StatusCode: http.StatusInternalServerError,
				Messages:   []string{"Database is unavailable."},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectJSONPost(t, mockTransport, tt.wantURL, commentBody).Return(tt.response, nil)

			got, err := jira.New(tt.baseURL, token, mockTransport).AddComment(t.Context(), commentIssueKey, commentText)

			require.Equal(t, tt.want, got)
			requireMatchingError(t, tt.wantErr, err)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

func TestAddCommentTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{
			name:         "сетевая ошибка → ErrCommentUnconfirmed с причиной",
			transportErr: errNetwork,
			wantErr:      jira.ErrCommentUnconfirmed,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "таймаут → ErrCommentUnconfirmed с причиной",
			transportErr: context.DeadlineExceeded,
			wantErr:      jira.ErrCommentUnconfirmed,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "отказ guard → проходит как есть",
			transportErr: guard.ErrForbidden,
			wantErr:      guard.ErrForbidden,
			wantNotErr:   jira.ErrCommentUnconfirmed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			got, err := jira.New(jiraBaseURL, token, mockTransport).AddComment(t.Context(), commentIssueKey, commentText)

			require.Equal(t, jira.Comment{}, got)
			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

const (
	issueLinkURL   = jiraBaseURL + "/rest/api/2/issueLink"
	blocksLinkBody = `{"type":{"name":"Blocks"},"inwardIssue":{"key":"PROJ-7790"},"outwardIssue":{"key":"PROJ-7791"}}`
)

var blocksRequest = jira.LinkRequest{TypeName: "Blocks", IssueKey: "PROJ-7790", TargetKey: "PROJ-7791", Direction: config.LinkOutward}

func TestLinkIssues(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		request  jira.LinkRequest
		wantURL  string
		wantBody string
		response *http.Response
		wantErr  error
	}{
		{
			name:     "outward: issue_key в inwardIssue, target_key в outwardIssue",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: emptyResponse(http.StatusCreated),
		},
		{
			name:     "inward: issue_key в outwardIssue, target_key в inwardIssue",
			baseURL:  jiraBaseURL,
			request:  jira.LinkRequest{TypeName: "Blocks", IssueKey: "PROJ-7790", TargetKey: "PROJ-7791", Direction: config.LinkInward},
			wantURL:  issueLinkURL,
			wantBody: `{"type":{"name":"Blocks"},"inwardIssue":{"key":"PROJ-7791"},"outwardIssue":{"key":"PROJ-7790"}}`,
			response: emptyResponse(http.StatusCreated),
		},
		{
			name:     "задачи разных проектов → ключи как есть",
			baseURL:  jiraBaseURL,
			request:  jira.LinkRequest{TypeName: "Blocks", IssueKey: "OTHER-1", TargetKey: "PROJ-7790", Direction: config.LinkOutward},
			wantURL:  issueLinkURL,
			wantBody: `{"type":{"name":"Blocks"},"inwardIssue":{"key":"OTHER-1"},"outwardIssue":{"key":"PROJ-7790"}}`,
			response: emptyResponse(http.StatusCreated),
		},
		{
			name:     "201 с телом → успех, тело игнорируется",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: jsonResponse(http.StatusCreated, `{"id":"10200"}`),
		},
		{
			name:     "base_url с путём → путь в запросе",
			baseURL:  jiraBaseURL + "/jira",
			request:  blocksRequest,
			wantURL:  jiraBaseURL + "/jira/rest/api/2/issueLink",
			wantBody: blocksLinkBody,
			response: emptyResponse(http.StatusCreated),
		},
		{
			name:     "200 → ErrLinkUnconfirmed",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: emptyResponse(http.StatusOK),
			wantErr:  jira.ErrLinkUnconfirmed,
		},
		{
			name:     "204 → ErrLinkUnconfirmed",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: emptyResponse(http.StatusNoContent),
			wantErr:  jira.ErrLinkUnconfirmed,
		},
		{
			name:     "401 → ErrUnauthorized",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: emptyResponse(http.StatusUnauthorized),
			wantErr:  jira.ErrUnauthorized,
		},
		{
			name:     "400 с errorMessages → StatusError",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: jsonResponse(http.StatusBadRequest, `{"errorMessages":["Issue linking is not allowed."]}`),
			wantErr: &jira.StatusError{
				StatusCode: http.StatusBadRequest,
				Messages:   []string{"Issue linking is not allowed."},
			},
		},
		{
			name:     "404 с errorMessages → StatusError",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: jsonResponse(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`),
			wantErr: &jira.StatusError{
				StatusCode: http.StatusNotFound,
				Messages:   []string{"Issue does not exist or you do not have permission to see it."},
			},
		},
		{
			name:     "редирект → StatusError 302, не следуем",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: redirectResponse("https://login.example.com/"),
			wantErr:  &jira.StatusError{StatusCode: http.StatusFound},
		},
		{
			name:     "500 → ErrLinkUnconfirmed и StatusError",
			baseURL:  jiraBaseURL,
			request:  blocksRequest,
			wantURL:  issueLinkURL,
			wantBody: blocksLinkBody,
			response: jsonResponse(http.StatusInternalServerError, `{"errorMessages":["Database is unavailable."]}`),
			wantErr: errors.Join(jira.ErrLinkUnconfirmed, &jira.StatusError{
				StatusCode: http.StatusInternalServerError,
				Messages:   []string{"Database is unavailable."},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectJSONPost(t, mockTransport, tt.wantURL, tt.wantBody).Return(tt.response, nil)

			err := jira.New(tt.baseURL, token, mockTransport).LinkIssues(t.Context(), tt.request)

			requireMatchingError(t, tt.wantErr, err)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

func TestLinkIssuesTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{
			name:         "сетевая ошибка → ErrLinkUnconfirmed с причиной",
			transportErr: errNetwork,
			wantErr:      jira.ErrLinkUnconfirmed,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "таймаут → ErrLinkUnconfirmed с причиной",
			transportErr: context.DeadlineExceeded,
			wantErr:      jira.ErrLinkUnconfirmed,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "отказ guard → проходит как есть",
			transportErr: guard.ErrForbidden,
			wantErr:      guard.ErrForbidden,
			wantNotErr:   jira.ErrLinkUnconfirmed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			err := jira.New(jiraBaseURL, token, mockTransport).LinkIssues(t.Context(), blocksRequest)

			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

const (
	updateIssueURL  = issueURL + "/PROJ-7790"
	epicLinkSetBody = `{"fields":{"customfield_10201":"PROJ-7630"}}`
)

var epicLinkSetRequest = jira.UpdateRequest{
	IssueKey:       "PROJ-7790",
	ValuesByJiraID: map[string]jira.FieldValue{"customfield_10201": {Kind: config.KindIssueKey, Text: "PROJ-7630"}},
}

func TestUpdateIssue(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		request  jira.UpdateRequest
		wantURL  string
		wantBody string
		response *http.Response
		wantErr  error
	}{
		{
			name:     "значение поля → PUT с fields, 204 — успех",
			baseURL:  jiraBaseURL,
			request:  epicLinkSetRequest,
			wantURL:  updateIssueURL,
			wantBody: epicLinkSetBody,
			response: emptyResponse(http.StatusNoContent),
		},
		{
			name:     "очистка поля → null",
			baseURL:  jiraBaseURL,
			request:  jira.UpdateRequest{IssueKey: "PROJ-7790", ClearedJiraIDs: []string{"customfield_10201"}},
			wantURL:  updateIssueURL,
			wantBody: `{"fields":{"customfield_10201":null}}`,
			response: emptyResponse(http.StatusNoContent),
		},
		{
			name:    "значения и очистка вместе, значения по форме kind",
			baseURL: jiraBaseURL,
			request: jira.UpdateRequest{
				IssueKey: "PROJ-7790",
				ValuesByJiraID: map[string]jira.FieldValue{
					"priority": {Kind: config.KindName, Text: "High"},
					"labels":   {Kind: config.KindLabels, Items: []string{"backend"}},
				},
				ClearedJiraIDs: []string{"customfield_10201", "components"},
			},
			wantURL:  updateIssueURL,
			wantBody: `{"fields":{"priority":{"name":"High"},"labels":["backend"],"customfield_10201":null,"components":null}}`,
			response: emptyResponse(http.StatusNoContent),
		},
		{
			name:     "base_url с путём → путь в запросе",
			baseURL:  jiraBaseURL + "/jira",
			request:  epicLinkSetRequest,
			wantURL:  jiraBaseURL + "/jira/rest/api/2/issue/PROJ-7790",
			wantBody: epicLinkSetBody,
			response: emptyResponse(http.StatusNoContent),
		},
		{
			name:     "200 → ErrUpdateUnconfirmed",
			baseURL:  jiraBaseURL,
			request:  epicLinkSetRequest,
			wantURL:  updateIssueURL,
			wantBody: epicLinkSetBody,
			response: emptyResponse(http.StatusOK),
			wantErr:  jira.ErrUpdateUnconfirmed,
		},
		{
			name:     "401 → ErrUnauthorized",
			baseURL:  jiraBaseURL,
			request:  epicLinkSetRequest,
			wantURL:  updateIssueURL,
			wantBody: epicLinkSetBody,
			response: emptyResponse(http.StatusUnauthorized),
			wantErr:  jira.ErrUnauthorized,
		},
		{
			name:     "400 с ошибкой поля → StatusError",
			baseURL:  jiraBaseURL,
			request:  epicLinkSetRequest,
			wantURL:  updateIssueURL,
			wantBody: epicLinkSetBody,
			response: jsonResponse(http.StatusBadRequest, `{"errors":{"customfield_10201":"Issue PROJ-7630 is not an epic."}}`),
			wantErr: &jira.StatusError{
				StatusCode:  http.StatusBadRequest,
				FieldErrors: map[string]string{"customfield_10201": "Issue PROJ-7630 is not an epic."},
			},
		},
		{
			name:     "404 с errorMessages → StatusError",
			baseURL:  jiraBaseURL,
			request:  epicLinkSetRequest,
			wantURL:  updateIssueURL,
			wantBody: epicLinkSetBody,
			response: jsonResponse(http.StatusNotFound, `{"errorMessages":["Issue Does Not Exist"]}`),
			wantErr:  &jira.StatusError{StatusCode: http.StatusNotFound, Messages: []string{"Issue Does Not Exist"}},
		},
		{
			name:     "редирект → StatusError 302, не следуем",
			baseURL:  jiraBaseURL,
			request:  epicLinkSetRequest,
			wantURL:  updateIssueURL,
			wantBody: epicLinkSetBody,
			response: redirectResponse("https://login.example.com/"),
			wantErr:  &jira.StatusError{StatusCode: http.StatusFound},
		},
		{
			name:     "500 → ErrUpdateUnconfirmed и StatusError",
			baseURL:  jiraBaseURL,
			request:  epicLinkSetRequest,
			wantURL:  updateIssueURL,
			wantBody: epicLinkSetBody,
			response: jsonResponse(http.StatusInternalServerError, `{"errorMessages":["Database is unavailable."]}`),
			wantErr: errors.Join(jira.ErrUpdateUnconfirmed, &jira.StatusError{
				StatusCode: http.StatusInternalServerError,
				Messages:   []string{"Database is unavailable."},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			expectJSONPut(t, mockTransport, tt.wantURL, tt.wantBody).Return(tt.response, nil)

			err := jira.New(tt.baseURL, token, mockTransport).UpdateIssue(t.Context(), tt.request)

			requireMatchingError(t, tt.wantErr, err)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

func TestUpdateIssueTransportError(t *testing.T) {
	errNetwork := errors.New("connection reset")
	tests := []struct {
		name         string
		transportErr error
		wantErr      error
		wantNotErr   error
	}{
		{
			name:         "сетевая ошибка → ErrUpdateUnconfirmed с причиной",
			transportErr: errNetwork,
			wantErr:      jira.ErrUpdateUnconfirmed,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "таймаут → ErrUpdateUnconfirmed с причиной",
			transportErr: context.DeadlineExceeded,
			wantErr:      jira.ErrUpdateUnconfirmed,
			wantNotErr:   guard.ErrForbidden,
		},
		{
			name:         "отказ guard → проходит как есть",
			transportErr: guard.ErrForbidden,
			wantErr:      guard.ErrForbidden,
			wantNotErr:   jira.ErrUpdateUnconfirmed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTransport := mocks.NewMockRoundTripper(gomock.NewController(t))
			mockTransport.EXPECT().RoundTrip(gomock.Any()).Return(nil, tt.transportErr)

			err := jira.New(jiraBaseURL, token, mockTransport).UpdateIssue(t.Context(), epicLinkSetRequest)

			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorIs(t, err, tt.transportErr)
			require.NotErrorIs(t, err, tt.wantNotErr)
			require.NotErrorIs(t, err, jira.ErrMaybeCreated)
		})
	}
}

func TestStatusErrorText(t *testing.T) {
	tests := []struct {
		name string
		err  jira.StatusError
		want string
	}{
		{
			name: "сообщения, затем поля по алфавиту",
			err: jira.StatusError{
				StatusCode:  http.StatusBadRequest,
				Messages:    []string{"Field cannot be set."},
				FieldErrors: map[string]string{"priority": "Priority is required.", "assignee": "User does not exist."},
			},
			want: "jira: 400 Bad Request: Field cannot be set.; assignee: User does not exist.; priority: Priority is required.",
		},
		{
			name: "только статус",
			err:  jira.StatusError{StatusCode: http.StatusFound},
			want: "jira: 302 Found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.err.Error())
		})
	}
}
