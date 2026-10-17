package jira

//go:generate go tool mockgen -destination=mocks/mock_creator.go -package=mocks scoped-mcp/mcp-jira/jira Creator
//go:generate go tool mockgen -destination=mocks/mock_attacher.go -package=mocks scoped-mcp/mcp-jira/jira Attacher
//go:generate go tool mockgen -destination=mocks/mock_commenter.go -package=mocks scoped-mcp/mcp-jira/jira Commenter
//go:generate go tool mockgen -destination=mocks/mock_linker.go -package=mocks scoped-mcp/mcp-jira/jira Linker
//go:generate go tool mockgen -destination=mocks/mock_updater.go -package=mocks scoped-mcp/mcp-jira/jira Updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/guard"
)

const (
	issuePath             = "/rest/api/2/issue"
	tokenOwnerPath        = "/rest/api/2/myself"
	attachmentsPathSuffix = "/attachments"
	commentPathSuffix     = "/comment"
	commentTextField      = "body"
	keyField              = "key"
	nameField             = "name"
	issueLinkPath         = "/rest/api/2/issueLink"
	linkTypeField         = "type"
	inwardIssueField      = "inwardIssue"
	outwardIssueField     = "outwardIssue"
	attachmentField       = "file"
	atlassianTokenHeader  = "X-Atlassian-Token"
	atlassianTokenNoCheck = "no-check"
	assigneeField         = "assignee"
	browsePath            = "/browse/"
	requestTimeout        = 30 * time.Second
	maxResponseBytes      = 1 << 20
)

var (
	ErrUnauthorized       = errors.New("jira: 401 Unauthorized: the token has expired or was revoked; issue a new personal access token and save it with mcp-jira token or set MCP_JIRA_TOKEN")
	ErrMaybeCreated       = errors.New("jira: the request was sent but Jira did not confirm the creation; the issue may have been created, check Jira before retrying")
	ErrUnexpectedResponse = errors.New("jira: the issue was created (201) but its key could not be read from the response; find it in Jira, do not retry")
	ErrTokenOwnerUnknown  = errors.New("jira: could not determine the token owner to assign the issue to; the issue was not created, it is safe to retry")
	ErrAttachUnconfirmed  = errors.New("jira: the file was sent but Jira did not confirm the attachment; the file may have been attached, check the issue before retrying")
	ErrCommentUnconfirmed = errors.New("jira: the comment was sent but Jira did not confirm it; the comment may have been added, check the issue before retrying")
	ErrLinkUnconfirmed    = errors.New("jira: the link was sent but Jira did not confirm it; the link may have been created, check the issue before retrying")
	ErrUpdateUnconfirmed  = errors.New("jira: the update was sent but Jira did not confirm it; the update is idempotent, it is safe to retry")
)

type FieldValue struct {
	Kind  config.Kind
	Text  string
	Items []string
}

type CreateRequest struct {
	ProjectKey   string
	IssueTypeID  string
	Summary      string
	Fields       map[string]FieldValue
	AssignToSelf bool
}

type Issue struct {
	Key string
	URL string
}

type Creator interface {
	CreateIssue(ctx context.Context, request CreateRequest) (Issue, error)
}

type AttachmentFile struct {
	Name    string
	Content []byte
}

type Attachment struct {
	ID string
}

type Attacher interface {
	AttachFile(ctx context.Context, issueKey string, file AttachmentFile) (Attachment, error)
}

type Comment struct {
	ID  string
	URL string
}

type Commenter interface {
	AddComment(ctx context.Context, issueKey, text string) (Comment, error)
}

type LinkRequest struct {
	TypeName  string
	IssueKey  string
	TargetKey string
	Direction config.LinkDirection
}

type Linker interface {
	LinkIssues(ctx context.Context, request LinkRequest) error
}

type UpdateRequest struct {
	IssueKey       string
	ValuesByJiraID map[string]FieldValue
	ClearedJiraIDs []string
}

type Updater interface {
	UpdateIssue(ctx context.Context, request UpdateRequest) error
}

type StatusError struct {
	StatusCode  int
	Messages    []string
	FieldErrors map[string]string
}

func (e *StatusError) Error() string {
	text := fmt.Sprintf("jira: %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	parts := slices.Clone(e.Messages)
	for _, field := range slices.Sorted(maps.Keys(e.FieldErrors)) {
		parts = append(parts, field+": "+e.FieldErrors[field])
	}
	if len(parts) == 0 {
		return text
	}
	return text + ": " + strings.Join(parts, "; ")
}

type Client struct {
	baseURL          string
	token            string
	httpClient       *http.Client
	tokenOwnerMu     sync.Mutex
	cachedTokenOwner string
}

func New(baseURL, token string, transport http.RoundTripper) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Transport:     transport,
			Timeout:       requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

var (
	_ Creator   = (*Client)(nil)
	_ Attacher  = (*Client)(nil)
	_ Commenter = (*Client)(nil)
	_ Linker    = (*Client)(nil)
	_ Updater   = (*Client)(nil)
)

func IssueURL(baseURL, issueKey string) string {
	return baseURL + browsePath + issueKey
}

func (c *Client) CreateIssue(ctx context.Context, request CreateRequest) (Issue, error) {
	assignee, err := c.assigneeFor(ctx, request)
	if err != nil {
		return Issue{}, err
	}
	requestBody, err := encodeCreateIssueBody(request, assignee)
	if err != nil {
		return Issue{}, err
	}
	httpRequest, err := c.newRequest(ctx, http.MethodPost, issuePath, bytes.NewReader(requestBody))
	if err != nil {
		return Issue{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return Issue{}, wrapUnlessForbidden(err, maybeCreated)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusCreated:
		return c.createdIssue(response.Body)
	case response.StatusCode == http.StatusUnauthorized:
		return Issue{}, ErrUnauthorized
	case isSuccess(response.StatusCode):
		return Issue{}, maybeCreated(fmt.Errorf("unexpected status %d", response.StatusCode))
	case response.StatusCode >= http.StatusInternalServerError:
		return Issue{}, maybeCreated(statusError(response))
	default:
		return Issue{}, statusError(response)
	}
}

func (c *Client) AttachFile(ctx context.Context, issueKey string, file AttachmentFile) (Attachment, error) {
	requestBody, contentType, err := encodeAttachmentBody(file)
	if err != nil {
		return Attachment{}, err
	}
	path := issuePath + "/" + url.PathEscape(issueKey) + attachmentsPathSuffix
	httpRequest, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(requestBody))
	if err != nil {
		return Attachment{}, err
	}
	httpRequest.Header.Set("Content-Type", contentType)
	httpRequest.Header.Set(atlassianTokenHeader, atlassianTokenNoCheck)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return Attachment{}, wrapUnlessForbidden(err, attachUnconfirmed)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusOK:
		return confirmedAttachment(response.Body)
	case response.StatusCode == http.StatusUnauthorized:
		return Attachment{}, ErrUnauthorized
	case isSuccess(response.StatusCode):
		return Attachment{}, attachUnconfirmed(fmt.Errorf("unexpected status %d", response.StatusCode))
	case response.StatusCode >= http.StatusInternalServerError:
		return Attachment{}, attachUnconfirmed(statusError(response))
	default:
		return Attachment{}, statusError(response)
	}
}

func (c *Client) AddComment(ctx context.Context, issueKey, text string) (Comment, error) {
	requestBody, err := json.Marshal(map[string]string{commentTextField: text})
	if err != nil {
		return Comment{}, fmt.Errorf("jira: encode comment: %w", err)
	}
	path := issuePath + "/" + url.PathEscape(issueKey) + commentPathSuffix
	httpRequest, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(requestBody))
	if err != nil {
		return Comment{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return Comment{}, wrapUnlessForbidden(err, commentUnconfirmed)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusCreated:
		return c.confirmedComment(issueKey, response.Body)
	case response.StatusCode == http.StatusUnauthorized:
		return Comment{}, ErrUnauthorized
	case isSuccess(response.StatusCode):
		return Comment{}, commentUnconfirmed(fmt.Errorf("unexpected status %d", response.StatusCode))
	case response.StatusCode >= http.StatusInternalServerError:
		return Comment{}, commentUnconfirmed(statusError(response))
	default:
		return Comment{}, statusError(response)
	}
}

func (c *Client) confirmedComment(issueKey string, responseBody io.Reader) (Comment, error) {
	responseData, err := readResponseBody(responseBody)
	if err != nil {
		return Comment{}, commentUnconfirmed(err)
	}
	var added struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseData, &added); err != nil {
		return Comment{}, commentUnconfirmed(err)
	}
	if added.ID == "" {
		return Comment{}, commentUnconfirmed(errors.New("no comment id in the response"))
	}
	return Comment{ID: added.ID, URL: commentBrowseURL(c.baseURL, issueKey, added.ID)}, nil
}

func commentBrowseURL(baseURL, issueKey, commentID string) string {
	commentAnchor := url.URL{Fragment: "comment-" + commentID}
	return IssueURL(baseURL, issueKey) + "?focusedCommentId=" + url.QueryEscape(commentID) + "#" + commentAnchor.EscapedFragment()
}

func (c *Client) LinkIssues(ctx context.Context, request LinkRequest) error {
	inwardIssueKey, outwardIssueKey := linkEnds(request)
	requestBody, err := json.Marshal(map[string]map[string]string{
		linkTypeField:     {nameField: request.TypeName},
		inwardIssueField:  {keyField: inwardIssueKey},
		outwardIssueField: {keyField: outwardIssueKey},
	})
	if err != nil {
		return fmt.Errorf("jira: encode link: %w", err)
	}
	httpRequest, err := c.newRequest(ctx, http.MethodPost, issueLinkPath, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	return c.doWithoutResponseBody(httpRequest, http.StatusCreated, linkUnconfirmed)
}

func (c *Client) UpdateIssue(ctx context.Context, request UpdateRequest) error {
	requestBody, err := encodeUpdateIssueBody(request)
	if err != nil {
		return err
	}
	path := issuePath + "/" + url.PathEscape(request.IssueKey)
	httpRequest, err := c.newRequest(ctx, http.MethodPut, path, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	return c.doWithoutResponseBody(httpRequest, http.StatusNoContent, updateUnconfirmed)
}

func (c *Client) doWithoutResponseBody(httpRequest *http.Request, successStatus int, unconfirmed func(error) error) error {
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return wrapUnlessForbidden(err, unconfirmed)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == successStatus:
		return nil
	case response.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case isSuccess(response.StatusCode):
		return unconfirmed(fmt.Errorf("unexpected status %d", response.StatusCode))
	case response.StatusCode >= http.StatusInternalServerError:
		return unconfirmed(statusError(response))
	default:
		return statusError(response)
	}
}

func encodeUpdateIssueBody(request UpdateRequest) ([]byte, error) {
	fields := make(map[string]any, len(request.ValuesByJiraID)+len(request.ClearedJiraIDs))
	for id, value := range request.ValuesByJiraID {
		fields[id] = value.jsonValue()
	}
	for _, id := range request.ClearedJiraIDs {
		fields[id] = nil
	}
	requestBody, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		return nil, fmt.Errorf("jira: encode update: %w", err)
	}
	return requestBody, nil
}

func linkEnds(request LinkRequest) (inwardIssueKey, outwardIssueKey string) {
	if request.Direction == config.LinkOutward {
		return request.IssueKey, request.TargetKey
	}
	return request.TargetKey, request.IssueKey
}

func encodeAttachmentBody(file AttachmentFile) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(attachmentField, file.Name)
	if err != nil {
		return nil, "", fmt.Errorf("jira: encode attachment: %w", err)
	}
	if _, err := part.Write(file.Content); err != nil {
		return nil, "", fmt.Errorf("jira: encode attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("jira: encode attachment: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func confirmedAttachment(body io.Reader) (Attachment, error) {
	responseBody, err := readResponseBody(body)
	if err != nil {
		return Attachment{}, attachUnconfirmed(err)
	}
	var attached []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &attached); err != nil {
		return Attachment{}, attachUnconfirmed(err)
	}
	if len(attached) == 0 || attached[0].ID == "" {
		return Attachment{}, attachUnconfirmed(errors.New("no attachment id in the response"))
	}
	return Attachment{ID: attached[0].ID}, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("jira: build request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	httpRequest.Header.Set("Accept", "application/json")
	return httpRequest, nil
}

func wrapUnlessForbidden(err error, wrap func(error) error) error {
	if errors.Is(err, guard.ErrForbidden) {
		return err
	}
	return wrap(err)
}

func maybeCreated(err error) error {
	return fmt.Errorf("%w: %w", ErrMaybeCreated, err)
}

func attachUnconfirmed(err error) error {
	return fmt.Errorf("%w: %w", ErrAttachUnconfirmed, err)
}

func commentUnconfirmed(err error) error {
	return fmt.Errorf("%w: %w", ErrCommentUnconfirmed, err)
}

func linkUnconfirmed(err error) error {
	return fmt.Errorf("%w: %w", ErrLinkUnconfirmed, err)
}

func updateUnconfirmed(err error) error {
	return fmt.Errorf("%w: %w", ErrUpdateUnconfirmed, err)
}

func tokenOwnerUnknown(err error) error {
	return fmt.Errorf("%w: %w", ErrTokenOwnerUnknown, err)
}

func (c *Client) assigneeFor(ctx context.Context, request CreateRequest) (string, error) {
	if !request.AssignToSelf {
		return "", nil
	}
	return c.resolveTokenOwner(ctx)
}

func encodeCreateIssueBody(request CreateRequest, assignee string) ([]byte, error) {
	fields := map[string]any{
		"project":   map[string]string{keyField: request.ProjectKey},
		"issuetype": map[string]string{"id": request.IssueTypeID},
		"summary":   request.Summary,
	}
	for id, value := range request.Fields {
		fields[id] = value.jsonValue()
	}
	if assignee != "" {
		fields[assigneeField] = map[string]string{nameField: assignee}
	}
	requestBody, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		return nil, fmt.Errorf("jira: encode request: %w", err)
	}
	return requestBody, nil
}

func (c *Client) resolveTokenOwner(ctx context.Context) (string, error) {
	c.tokenOwnerMu.Lock()
	defer c.tokenOwnerMu.Unlock()
	if c.cachedTokenOwner != "" {
		return c.cachedTokenOwner, nil
	}
	fetched, err := c.fetchTokenOwner(ctx)
	if err != nil {
		return "", err
	}
	c.cachedTokenOwner = fetched
	return fetched, nil
}

func (c *Client) fetchTokenOwner(ctx context.Context) (string, error) {
	httpRequest, err := c.newRequest(ctx, http.MethodGet, tokenOwnerPath, nil)
	if err != nil {
		return "", err
	}
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return "", wrapUnlessForbidden(err, tokenOwnerUnknown)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
		return readTokenOwnerName(response.Body)
	case http.StatusUnauthorized:
		return "", ErrUnauthorized
	default:
		return "", tokenOwnerUnknown(statusError(response))
	}
}

func readTokenOwnerName(body io.Reader) (string, error) {
	responseBody, err := readResponseBody(body)
	if err != nil {
		return "", tokenOwnerUnknown(err)
	}
	var tokenOwner struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(responseBody, &tokenOwner); err != nil {
		return "", tokenOwnerUnknown(err)
	}
	if tokenOwner.Name == "" {
		return "", tokenOwnerUnknown(errors.New("no name in the response"))
	}
	return tokenOwner.Name, nil
}

func (v FieldValue) jsonValue() any {
	switch v.Kind {
	case config.KindName:
		return map[string]string{nameField: v.Text}
	case config.KindOption:
		return map[string]string{"value": v.Text}
	case config.KindNames:
		return wrapEach(nameField, v.Items)
	case config.KindOptions:
		return wrapEach("value", v.Items)
	case config.KindLabels:
		return append([]string{}, v.Items...)
	case config.KindText, config.KindIssueKey:
		return v.Text
	}
	return v.Text
}

func wrapEach(key string, items []string) []map[string]string {
	wrapped := make([]map[string]string, 0, len(items))
	for _, item := range items {
		wrapped = append(wrapped, map[string]string{key: item})
	}
	return wrapped
}

func isSuccess(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func readResponseBody(body io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(body, maxResponseBytes))
}

func (c *Client) createdIssue(body io.Reader) (Issue, error) {
	responseBody, err := readResponseBody(body)
	if err != nil {
		return Issue{}, fmt.Errorf("%w: %w", ErrUnexpectedResponse, err)
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(responseBody, &created); err != nil {
		return Issue{}, fmt.Errorf("%w: %w", ErrUnexpectedResponse, err)
	}
	if created.Key == "" {
		return Issue{}, fmt.Errorf("%w: no issue key in the response", ErrUnexpectedResponse)
	}
	return Issue{Key: created.Key, URL: IssueURL(c.baseURL, created.Key)}, nil
}

func statusError(response *http.Response) error {
	responseBody, err := readResponseBody(response.Body)
	if err != nil {
		return &StatusError{StatusCode: response.StatusCode}
	}
	var parsed struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	// The body may be an HTML page from a proxy; then only the status remains.
	_ = json.Unmarshal(responseBody, &parsed)
	return &StatusError{StatusCode: response.StatusCode, Messages: parsed.ErrorMessages, FieldErrors: parsed.Errors}
}
