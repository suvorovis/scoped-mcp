package confluence

//go:generate go tool mockgen -destination=mocks/mock_pages.go -package=mocks scoped-mcp/mcp-confluence/confluence Pages
//go:generate go tool mockgen -destination=mocks/mock_comments.go -package=mocks scoped-mcp/mcp-confluence/confluence Comments
//go:generate go tool mockgen -destination=mocks/mock_attachments.go -package=mocks scoped-mcp/mcp-confluence/confluence Attachments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scoped-mcp/mcp-confluence/guard"
)

const (
	contentPath = "/rest/api/content"
	searchPath  = contentPath + "/search"

	downloadPath = "/download/attachments"

	childPages       = "page"
	childComments    = "comment"
	childAttachments = "attachment"

	expandParam = "expand"
	startParam  = "start"
	limitParam  = "limit"
	cqlParam    = "cql"

	MaxListLimit   = 100
	maxListBatches = 20

	jsonMediaType = "application/json"

	requestTimeout   = 60 * time.Second
	maxResponseBytes = 8 << 20
)

var (
	ErrNotFound           = errors.New("page not found")
	ErrVersionConflict    = errors.New("the page was changed since it was read; read it again")
	ErrTitleTaken         = errors.New("this title is already taken in the space")
	ErrRejected           = errors.New("Confluence rejected the request")
	ErrMaybeWritten       = errors.New("the change may have been applied; check the page before retrying")
	ErrUnavailable        = errors.New("Confluence is unavailable, retry later")
	ErrUnauthorized       = errors.New("the token was rejected")
	ErrAttachmentTooLarge = errors.New("the attachment is larger than attachments.max_bytes")
	ErrForbidden          = guard.ErrForbidden
)

type requestKind int

const (
	readRequest requestKind = iota
	writeRequest
)

type Client struct {
	base       url.URL
	token      string
	httpClient *http.Client
	logger     *log.Logger
}

func New(baseURL, token string, transport http.RoundTripper, logger *log.Logger) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("confluence: parse base url: %w", err)
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	return &Client{
		base:   *base,
		token:  token,
		logger: logger,
		httpClient: &http.Client{
			Transport:     transport,
			Timeout:       requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *Client) endpoint(path string, query url.Values) string {
	endpointURL := c.base
	endpointURL.Path = c.base.Path + path
	endpointURL.RawQuery = query.Encode()
	return endpointURL.String()
}

func (c *Client) webURL(webui string) string {
	if webui == "" {
		return ""
	}
	return c.base.String() + webui
}

func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, query), body)
	if err != nil {
		return nil, fmt.Errorf("confluence: build request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	httpRequest.Header.Set("Accept", jsonMediaType)
	return httpRequest, nil
}

func (c *Client) newJSONRequest(ctx context.Context, method, path string, requestBody any) (*http.Request, error) {
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("confluence: encode request: %w", err)
	}
	httpRequest, err := c.newRequest(ctx, method, path, nil, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", jsonMediaType)
	return httpRequest, nil
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, target any) error {
	httpRequest, err := c.newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	return c.doJSON(httpRequest, readRequest, target)
}

func childPath(pageID, child string) string {
	return contentPath + "/" + pageID + "/child/" + child
}

func (c *Client) forEachListed(ctx context.Context, path string, query url.Values, visit func(content contentResponse)) error {
	start := 0
	for range maxListBatches {
		batchQuery := listQuery(start, MaxListLimit)
		maps.Copy(batchQuery, query)
		var collection collectionResponse
		if err := c.getJSON(ctx, path, batchQuery, &collection); err != nil {
			return err
		}
		for _, content := range collection.Results {
			visit(content)
		}
		if collection.Links.Next == "" {
			return nil
		}
		if len(collection.Results) == 0 {
			c.logRoute(http.MethodGet, c.base.Path+path, "empty batch with a next link")
			return ErrUnavailable
		}
		start += len(collection.Results)
	}
	c.logRoute(http.MethodGet, c.base.Path+path, "more than %d batches of results", maxListBatches)
	return ErrUnavailable
}

func (c *Client) doJSON(httpRequest *http.Request, kind requestKind, target any) error {
	response, err := c.send(httpRequest, kind)
	if response != nil {
		defer closeResponseBody(response)
	}
	if err != nil {
		return err
	}
	return c.decodeJSON(httpRequest, response.Body, target, kind)
}

func (c *Client) send(httpRequest *http.Request, kind requestKind) (*http.Response, error) {
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		failureDetail, agentErr := classifyTransportError(err, kind)
		c.logf(httpRequest, "transport: %s", failureDetail)
		return nil, agentErr
	}
	if response.StatusCode != http.StatusOK {
		c.logf(httpRequest, "status %d", response.StatusCode)
		return response, statusError(response.StatusCode, kind)
	}
	return response, nil
}

func closeResponseBody(response *http.Response) {
	_ = response.Body.Close()
}

func classifyTransportError(err error, kind requestKind) (string, error) {
	switch {
	case errors.Is(err, guard.ErrOutOfZone):
		return "guard: outside the zone", ErrNotFound
	case errors.Is(err, guard.ErrTokenRejected):
		return "guard: zone check unauthorized", ErrUnauthorized
	case errors.Is(err, guard.ErrForbidden):
		return "guard: request not allowed", ErrForbidden
	case errors.Is(err, guard.ErrZoneCheckFailed):
		return withoutRequestURL(err), ErrUnavailable
	case isTimeout(err):
		return "timeout", unknownOutcome(kind)
	case errors.Is(err, context.Canceled):
		return "canceled", unknownOutcome(kind)
	default:
		return "transport failure", unknownOutcome(kind)
	}
}

func withoutRequestURL(err error) string {
	var requestErr *url.Error
	if errors.As(err, &requestErr) {
		return requestErr.Err.Error()
	}
	return err.Error()
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func statusError(statusCode int, kind requestKind) error {
	switch {
	case statusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case statusCode == http.StatusForbidden || statusCode == http.StatusNotFound:
		return ErrNotFound
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return ErrRejected
	default:
		return unknownOutcome(kind)
	}
}

func unknownOutcome(kind requestKind) error {
	if kind == writeRequest {
		return ErrMaybeWritten
	}
	return ErrUnavailable
}

func (c *Client) decodeJSON(httpRequest *http.Request, body io.Reader, target any, kind requestKind) error {
	data, err := c.readLimited(httpRequest, body, maxResponseBytes, unknownOutcome(kind), unknownOutcome(kind))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		c.logf(httpRequest, "response is not the expected JSON")
		return unknownOutcome(kind)
	}
	return nil
}

func (c *Client) readLimited(httpRequest *http.Request, body io.Reader, limit int64, errUnreadable, errTooLarge error) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		c.logf(httpRequest, "response is unreadable")
		return nil, errUnreadable
	}
	if int64(len(data)) > limit {
		c.logf(httpRequest, "response exceeds %d bytes", limit)
		return nil, errTooLarge
	}
	return data, nil
}

func (c *Client) logf(httpRequest *http.Request, format string, args ...any) {
	c.logRoute(httpRequest.Method, httpRequest.URL.Path, format, args...)
}

func (c *Client) logRoute(method, path, format string, args ...any) {
	c.logger.Printf("%s %s: %s", method, path, fmt.Sprintf(format, args...))
}

func limitQuery(limit int) url.Values {
	return url.Values{limitParam: {strconv.Itoa(limit)}}
}

func listQuery(start, limit int) url.Values {
	query := limitQuery(limit)
	query.Set(startParam, strconv.Itoa(start))
	return query
}

type contentRef struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
}

type spaceRef struct {
	Key string `json:"key"`
}

type versionNumber struct {
	Number int `json:"number"`
}

type storageBody struct {
	Storage storageValue `json:"storage"`
}

type storageValue struct {
	Value          string `json:"value"`
	Representation string `json:"representation"`
}

type contentHistory struct {
	CreatedBy   contentAuthor `json:"createdBy"`
	CreatedDate string        `json:"createdDate"`
}

type contentAuthor struct {
	DisplayName string `json:"displayName"`
}

type attachmentExtensions struct {
	MediaType string `json:"mediaType"`
	FileSize  int64  `json:"fileSize"`
}

type contentLinks struct {
	WebUI string `json:"webui"`
}

type contentResponse struct {
	ID         string               `json:"id"`
	Title      string               `json:"title"`
	Version    versionNumber        `json:"version"`
	Ancestors  []contentRef         `json:"ancestors"`
	Space      spaceRef             `json:"space"`
	Body       storageBody          `json:"body"`
	History    contentHistory       `json:"history"`
	Extensions attachmentExtensions `json:"extensions"`
	Links      contentLinks         `json:"_links"`
}

type collectionLinks struct {
	Next string `json:"next"`
}

type collectionResponse struct {
	Results []contentResponse `json:"results"`
	Links   collectionLinks   `json:"_links"`
}
