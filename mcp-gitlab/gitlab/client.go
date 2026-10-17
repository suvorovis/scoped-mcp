package gitlab

//go:generate go tool mockgen -destination=mocks/mock_merge_request_api.go -package=mocks scoped-mcp/mcp-gitlab/gitlab MergeRequestAPI

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"scoped-mcp/mcp-gitlab/guard"
)

const (
	projectsPath       = "/api/v4/projects/"
	mergeRequestsPath  = "/merge_requests/"
	diffsSegment       = "/diffs"
	notesSegment       = "/notes"
	discussionsSegment = "/discussions"
	draftNotesSegment  = "/draft_notes"

	tokenHeader      = "PRIVATE-TOKEN"
	positionTypeText = "text"

	bodyField      = "body"
	noteField      = "note"
	positionField  = "position"
	inReplyToField = "in_reply_to_discussion_id"

	diffsPerPage = 20
	maxDiffPages = 150

	requestTimeout        = 30 * time.Second
	maxResponseBytes      = 1 << 20
	maxDiffsResponseBytes = 32 << 20
)

var (
	ErrUnauthorized            = errors.New("gitlab: 401 Unauthorized: the token has expired or was revoked; issue a new personal access token with the api scope and save it with mcp-gitlab token or set MCP_GITLAB_TOKEN")
	ErrMergeRequestUnavailable = errors.New("gitlab: the merge request could not be fetched; nothing was posted, it is safe to retry")
	ErrDiffUnavailable         = errors.New("gitlab: the file diff could not be fetched; nothing was posted, it is safe to retry")
	ErrCommentUnconfirmed      = errors.New("gitlab: the comment was sent but GitLab did not confirm it; it may have been added, check the merge request before retrying")
	ErrResponseTooLarge        = errors.New("gitlab: the response exceeds the size limit")
)

type MergeRequestRef struct {
	Project string
	IID     int64
}

type DiffRefs struct {
	BaseSHA  string
	StartSHA string
	HeadSHA  string
}

type MergeRequest struct {
	WebURL   string
	DiffRefs DiffRefs
}

type LineRangeEnd struct {
	LineCode string
	Type     string
}

type LineRange struct {
	Start LineRangeEnd
	End   LineRangeEnd
}

type Position struct {
	DiffRefs  DiffRefs
	OldPath   string
	NewPath   string
	OldLine   int
	NewLine   int
	LineRange *LineRange
}

type Note struct {
	ID int64
}

type Discussion struct {
	ID     string
	NoteID int64
}

type DraftNoteRequest struct {
	Note                  string
	Position              *Position
	InReplyToDiscussionID string
}

type Draft struct {
	ID int64
}

type MergeRequestAPI interface {
	MergeRequest(ctx context.Context, ref MergeRequestRef) (MergeRequest, error)
	FileDiff(ctx context.Context, ref MergeRequestRef, newPath string) (string, error)
	AddNote(ctx context.Context, ref MergeRequestRef, body string) (Note, error)
	AddDiscussion(ctx context.Context, ref MergeRequestRef, body string, position Position) (Discussion, error)
	AddReply(ctx context.Context, ref MergeRequestRef, discussionID, body string) (Note, error)
	AddDraftNote(ctx context.Context, ref MergeRequestRef, request DraftNoteRequest) (Draft, error)
}

func (d DiffRefs) Complete() bool {
	return d.BaseSHA != "" && d.StartSHA != "" && d.HeadSHA != ""
}

type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	text := fmt.Sprintf("gitlab: %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	if e.Message == "" {
		return text
	}
	return text + ": " + e.Message
}

type Client struct {
	origin     *url.URL
	token      string
	httpClient *http.Client
}

func New(baseURL, token string, transport http.RoundTripper) (*Client, error) {
	origin, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("gitlab: parse base_url: %w", err)
	}
	return &Client{
		origin: origin,
		token:  token,
		httpClient: &http.Client{
			Transport:     transport,
			Timeout:       requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

var _ MergeRequestAPI = (*Client)(nil)

func (c *Client) MergeRequest(ctx context.Context, ref MergeRequestRef) (MergeRequest, error) {
	response, err := c.get(ctx, c.mergeRequestURL(ref, "", nil))
	if err != nil {
		return MergeRequest{}, wrapUnlessForbidden(err, mergeRequestUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return MergeRequest{}, mergeRequestUnavailable(responseError(response))
	}
	mergeRequest, err := decodeMergeRequest(response.Body)
	if err != nil {
		return MergeRequest{}, mergeRequestUnavailable(err)
	}
	return mergeRequest, nil
}

func decodeMergeRequest(body io.Reader) (MergeRequest, error) {
	var parsed struct {
		WebURL   string `json:"web_url"`
		DiffRefs struct {
			BaseSHA  string `json:"base_sha"`
			StartSHA string `json:"start_sha"`
			HeadSHA  string `json:"head_sha"`
		} `json:"diff_refs"`
	}
	if err := decodeJSON(body, maxResponseBytes, &parsed); err != nil {
		return MergeRequest{}, err
	}
	if parsed.WebURL == "" {
		return MergeRequest{}, errors.New("no web_url in the response")
	}
	return MergeRequest{
		WebURL:   parsed.WebURL,
		DiffRefs: DiffRefs(parsed.DiffRefs),
	}, nil
}

func (c *Client) FileDiff(ctx context.Context, ref MergeRequestRef, newPath string) (string, error) {
	for page := 1; page <= maxDiffPages; page++ {
		entries, err := c.diffsPage(ctx, ref, page)
		if err != nil {
			return "", wrapUnlessForbidden(err, diffUnavailable)
		}
		for _, entry := range entries {
			if entry.NewPath == newPath {
				return entry.text()
			}
		}
		if len(entries) < diffsPerPage {
			return "", diffUnavailable(fmt.Errorf("file %q is not in the merge request diff", newPath))
		}
	}
	return "", diffUnavailable(fmt.Errorf("file %q is not among the first %d diff pages", newPath, maxDiffPages))
}

type diffEntry struct {
	NewPath   string `json:"new_path"`
	Diff      string `json:"diff"`
	Collapsed bool   `json:"collapsed"`
	TooLarge  bool   `json:"too_large"`
}

func (e diffEntry) text() (string, error) {
	switch {
	case e.Collapsed:
		return "", diffUnavailable(fmt.Errorf("GitLab collapsed the diff of %q", e.NewPath))
	case e.TooLarge:
		return "", diffUnavailable(fmt.Errorf("the diff of %q is too large for GitLab to return", e.NewPath))
	default:
		return e.Diff, nil
	}
}

func (c *Client) diffsPage(ctx context.Context, ref MergeRequestRef, page int) ([]diffEntry, error) {
	query := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(diffsPerPage)}}
	response, err := c.get(ctx, c.mergeRequestURL(ref, diffsSegment, query))
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(response)
	}
	var entries []diffEntry
	if err := decodeJSON(response.Body, maxDiffsResponseBytes, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *Client) AddNote(ctx context.Context, ref MergeRequestRef, body string) (Note, error) {
	return c.postNote(ctx, c.mergeRequestURL(ref, notesSegment, nil), map[string]any{bodyField: body})
}

func (c *Client) AddReply(ctx context.Context, ref MergeRequestRef, discussionID, body string) (Note, error) {
	return c.postNote(ctx, c.mergeRequestURL(ref, replyNotesSubpath(discussionID), nil), map[string]any{bodyField: body})
}

func replyNotesSubpath(discussionID string) string {
	return discussionsSegment + "/" + discussionID + notesSegment
}

func (c *Client) postNote(ctx context.Context, requestURL *url.URL, requestBody map[string]any) (Note, error) {
	var created struct {
		ID int64 `json:"id"`
	}
	if err := c.postAndDecode(ctx, requestURL, requestBody, &created); err != nil {
		return Note{}, err
	}
	if created.ID == 0 {
		return Note{}, commentUnconfirmed(errors.New("no note id in the response"))
	}
	return Note{ID: created.ID}, nil
}

func (c *Client) AddDiscussion(ctx context.Context, ref MergeRequestRef, body string, position Position) (Discussion, error) {
	var created struct {
		ID    string `json:"id"`
		Notes []struct {
			ID int64 `json:"id"`
		} `json:"notes"`
	}
	requestBody := map[string]any{bodyField: body, positionField: encodePosition(position)}
	if err := c.postAndDecode(ctx, c.mergeRequestURL(ref, discussionsSegment, nil), requestBody, &created); err != nil {
		return Discussion{}, err
	}
	if created.ID == "" || len(created.Notes) == 0 || created.Notes[0].ID == 0 {
		return Discussion{}, commentUnconfirmed(errors.New("no discussion id or note id in the response"))
	}
	return Discussion{ID: created.ID, NoteID: created.Notes[0].ID}, nil
}

func (c *Client) AddDraftNote(ctx context.Context, ref MergeRequestRef, request DraftNoteRequest) (Draft, error) {
	requestBody := map[string]any{noteField: request.Note}
	if request.Position != nil {
		requestBody[positionField] = encodePosition(*request.Position)
	}
	if request.InReplyToDiscussionID != "" {
		requestBody[inReplyToField] = request.InReplyToDiscussionID
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := c.postAndDecode(ctx, c.mergeRequestURL(ref, draftNotesSegment, nil), requestBody, &created); err != nil {
		return Draft{}, err
	}
	if created.ID == 0 {
		return Draft{}, commentUnconfirmed(errors.New("no draft note id in the response"))
	}
	return Draft{ID: created.ID}, nil
}

func (c *Client) postAndDecode(ctx context.Context, requestURL *url.URL, requestBody map[string]any, created any) error {
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("gitlab: encode request: %w", err)
	}
	response, err := c.post(ctx, requestURL, encoded)
	if err != nil {
		return wrapUnlessForbidden(err, commentUnconfirmed)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case isSuccess(response.StatusCode):
		if err := decodeJSON(response.Body, maxResponseBytes, created); err != nil {
			return commentUnconfirmed(err)
		}
		return nil
	case response.StatusCode >= http.StatusInternalServerError:
		return commentUnconfirmed(newStatusError(response))
	default:
		return responseError(response)
	}
}

type positionJSON struct {
	PositionType string         `json:"position_type"`
	BaseSHA      string         `json:"base_sha"`
	StartSHA     string         `json:"start_sha"`
	HeadSHA      string         `json:"head_sha"`
	OldPath      string         `json:"old_path"`
	NewPath      string         `json:"new_path"`
	OldLine      int            `json:"old_line,omitempty"`
	NewLine      int            `json:"new_line,omitempty"`
	LineRange    *lineRangeJSON `json:"line_range,omitempty"`
}

type lineRangeJSON struct {
	Start lineRangeEndJSON `json:"start"`
	End   lineRangeEndJSON `json:"end"`
}

type lineRangeEndJSON struct {
	LineCode string `json:"line_code"`
	Type     string `json:"type"`
}

func encodePosition(position Position) positionJSON {
	encoded := positionJSON{
		PositionType: positionTypeText,
		BaseSHA:      position.DiffRefs.BaseSHA,
		StartSHA:     position.DiffRefs.StartSHA,
		HeadSHA:      position.DiffRefs.HeadSHA,
		OldPath:      position.OldPath,
		NewPath:      position.NewPath,
		OldLine:      position.OldLine,
		NewLine:      position.NewLine,
	}
	if position.LineRange != nil {
		encoded.LineRange = &lineRangeJSON{
			Start: lineRangeEndJSON(position.LineRange.Start),
			End:   lineRangeEndJSON(position.LineRange.End),
		}
	}
	return encoded
}

func (c *Client) mergeRequestURL(ref MergeRequestRef, subpath string, query url.Values) *url.URL {
	iid := strconv.FormatInt(ref.IID, 10)
	requestURL := *c.origin
	requestURL.Path = projectsPath + ref.Project + mergeRequestsPath + iid + subpath
	requestURL.RawPath = projectsPath + url.PathEscape(ref.Project) + mergeRequestsPath + iid + subpath
	requestURL.RawQuery = query.Encode()
	return &requestURL
}

func (c *Client) get(ctx context.Context, requestURL *url.URL) (*http.Response, error) {
	return c.send(ctx, http.MethodGet, requestURL, nil)
}

func (c *Client) post(ctx context.Context, requestURL *url.URL, body []byte) (*http.Response, error) {
	return c.send(ctx, http.MethodPost, requestURL, body)
}

func (c *Client) send(ctx context.Context, method string, requestURL *url.URL, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	httpRequest, err := newRequestPreservingRawPath(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("gitlab: build request: %w", err)
	}
	httpRequest.Header.Set(tokenHeader, c.token)
	httpRequest.Header.Set("Accept", "application/json")
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(httpRequest)
}

func newRequestPreservingRawPath(ctx context.Context, method string, requestURL *url.URL, body io.Reader) (*http.Request, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, err
	}
	// url.Parse drops RawPath whenever it equals the default escaping of Path.
	httpRequest.URL = requestURL
	return httpRequest, nil
}

func responseError(response *http.Response) error {
	if response.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	return newStatusError(response)
}

func newStatusError(response *http.Response) error {
	responseBody, err := readLimited(response.Body, maxResponseBytes)
	if err != nil {
		return &StatusError{StatusCode: response.StatusCode}
	}
	return &StatusError{StatusCode: response.StatusCode, Message: errorMessage(responseBody)}
}

func errorMessage(responseBody []byte) string {
	var parsed struct {
		Message json.RawMessage `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(responseBody, &parsed) != nil {
		return ""
	}
	if text, ok := fieldText(parsed.Message); ok {
		return text
	}
	text, _ := fieldText(parsed.Error)
	return text
}

func fieldText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return string(raw), true
	}
	return text, true
}

func isSuccess(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func decodeJSON(body io.Reader, limit int64, into any) error {
	responseBody, err := readLimited(body, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(responseBody, into)
}

func readLimited(body io.Reader, limit int64) ([]byte, error) {
	responseBody, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(responseBody)) > limit {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrResponseTooLarge, limit)
	}
	return responseBody, nil
}

func wrapUnlessForbidden(err error, wrap func(error) error) error {
	if errors.Is(err, guard.ErrForbidden) {
		return err
	}
	return wrap(err)
}

func mergeRequestUnavailable(err error) error {
	return fmt.Errorf("%w: %w", ErrMergeRequestUnavailable, err)
}

func diffUnavailable(err error) error {
	return fmt.Errorf("%w: %w", ErrDiffUnavailable, err)
}

func commentUnconfirmed(err error) error {
	return fmt.Errorf("%w: %w", ErrCommentUnconfirmed, err)
}
