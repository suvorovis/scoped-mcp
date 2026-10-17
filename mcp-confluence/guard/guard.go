package guard

//go:generate go tool mockgen -destination=mocks/mock_round_tripper.go -package=mocks net/http RoundTripper
//go:generate go tool mockgen -destination=mocks/mock_read_closer.go -package=mocks io ReadCloser

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"scoped-mcp/mcp-confluence/config"
)

const (
	MaxBodyBytes            = 8 << 20
	AttachmentEnvelopeBytes = 64 << 10

	contentPath  = "/rest/api/content"
	downloadPath = "/download/attachments"

	searchSegment     = "search"
	childSegment      = "child"
	pageSegment       = "page"
	commentSegment    = "comment"
	attachmentSegment = "attachment"
	dataSegment       = "data"
	expandParam       = "expand"

	attachmentIDPrefix = "att"

	contentTypeHeader   = "Content-Type"
	authorizationHeader = "Authorization"
)

var (
	ErrForbidden = errors.New("request is not allowed")

	errBodyTooLarge = errors.New("body exceeds the size limit")

	positiveNumberPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	rfc3986QueryPattern   = regexp.MustCompile(`^[A-Za-z0-9\-._~!$&'()*+,;=:@/?%]*$`)
	contentExpand         = []string{"body.storage", "version", "ancestors", "space"}

	allowedRequestHeaders = []string{authorizationHeader, "Accept", "User-Agent", contentTypeHeader, atlassianTokenHeader}
)

type (
	routeFunc         func(req *http.Request) (*http.Request, error)
	contentAuthorizer func(req *http.Request, contentID string) (*http.Request, error)
)

type Transport struct {
	scheme           string
	host             string
	contentPrefix    string
	downloadPrefix   string
	tools            config.Tools
	readZone         zone
	writeZone        zone
	searchCQLOpening string
	maxSearchResults int
	attachmentRules  *config.Attachments
	nextTransport    http.RoundTripper
}

func New(cfg config.Config, nextTransport http.RoundTripper) (*Transport, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("guard: parse base_url: %w", err)
	}
	basePath := strings.TrimSuffix(base.Path, "/")
	readRoots := cfg.ReadZoneRoots()
	return &Transport{
		scheme:           base.Scheme,
		host:             base.Host,
		contentPrefix:    basePath + contentPath,
		downloadPrefix:   basePath + downloadPath,
		tools:            cfg.Tools,
		readZone:         newZone(readRoots),
		writeZone:        newZone(cfg.WriteZoneRoots()),
		searchCQLOpening: searchCQLOpening(readRoots),
		maxSearchResults: cfg.Search.MaxResults,
		attachmentRules:  cloneAttachmentRules(cfg.Attachments),
		nextTransport:    nextTransport,
	}, nil
}

func cloneAttachmentRules(attachments *config.Attachments) *config.Attachments {
	if attachments == nil {
		return nil
	}
	return &config.Attachments{Extensions: slices.Clone(attachments.Extensions), MaxBytes: attachments.MaxBytes}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	allowedRequest, err := t.authorize(req)
	if err != nil {
		closeRequestBody(req)
		return nil, err
	}
	if allowedRequest != req {
		closeRequestBody(req)
	}
	return t.nextTransport.RoundTrip(allowedRequest)
}

func (t *Transport) authorize(req *http.Request) (*http.Request, error) {
	if err := t.checkOrigin(req); err != nil {
		return nil, err
	}
	if err := checkFraming(req); err != nil {
		return nil, err
	}
	if err := checkHeaderKeys(req.Header); err != nil {
		return nil, err
	}
	route := t.matchRoute(req.Method, req.URL.Path)
	if route == nil {
		return nil, forbidden("%s %s", req.Method, req.URL.EscapedPath())
	}
	return route(req)
}

func closeRequestBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

func (t *Transport) checkOrigin(req *http.Request) error {
	switch {
	case req.URL.Scheme != t.scheme || req.URL.Host != t.host:
		return forbidden("host %s://%s", req.URL.Scheme, req.URL.Host)
	case req.Host != "" && req.Host != t.host:
		return forbidden("host header %s", req.Host)
	case req.URL.Opaque != "" || req.URL.RawPath != "":
		return forbidden("path %s", req.URL.EscapedPath())
	default:
		return nil
	}
}

func checkFraming(req *http.Request) error {
	if len(req.TransferEncoding) > 0 || len(req.Trailer) > 0 {
		return forbidden("Transfer-Encoding and trailers are not allowed")
	}
	return nil
}

func checkHeaderKeys(header http.Header) error {
	for key, values := range header {
		if key != http.CanonicalHeaderKey(key) {
			return forbidden("header %q is not in canonical form", key)
		}
		if !slices.Contains(allowedRequestHeaders, key) {
			return forbidden("header %s", key)
		}
		if key == authorizationHeader && len(values) != 1 {
			return forbidden("header %s must have exactly one value", key)
		}
	}
	return nil
}

func (t *Transport) matchRoute(method, path string) routeFunc {
	if rest, ok := strings.CutPrefix(path, t.downloadPrefix+"/"); ok {
		return t.downloadRoute(method, strings.Split(rest, "/"))
	}
	if path == t.contentPrefix {
		return t.contentCollectionRoute(method)
	}
	if path == t.contentPrefix+"/"+searchSegment {
		return t.searchRoute(method)
	}
	rest, ok := strings.CutPrefix(path, t.contentPrefix+"/")
	if !ok {
		return nil
	}
	segments := strings.Split(rest, "/")
	if !IsContentID(segments[0]) {
		return nil
	}
	return t.contentRoute(method, segments[0], segments[1:])
}

func (t *Transport) contentRoute(method, contentID string, subpath []string) routeFunc {
	authorizeContent := t.contentAuthorizerFor(method, subpath)
	if authorizeContent == nil {
		return nil
	}
	return bindContentID(authorizeContent, contentID)
}

func (t *Transport) contentAuthorizerFor(method string, subpath []string) contentAuthorizer {
	switch method {
	case http.MethodGet:
		return t.getContentAuthorizerFor(subpath)
	case http.MethodPost:
		return t.postContentAuthorizerFor(subpath)
	case http.MethodPut:
		return t.putContentAuthorizerFor(subpath)
	default:
		return nil
	}
}

func (t *Transport) getContentAuthorizerFor(subpath []string) contentAuthorizer {
	if len(subpath) == 0 {
		return enabledOnly(t.tools.GetPage || t.tools.Search || t.tools.CreatePage, t.authorizeGetContent)
	}
	switch childType(subpath) {
	case pageSegment:
		return enabledOnly(t.tools.ListChildren, t.authorizeListChildren)
	case commentSegment:
		return enabledOnly(t.tools.ListComments, t.authorizeListComments)
	case attachmentSegment:
		return enabledOnly(t.tools.ListAttachments || t.tools.GetAttachment || t.tools.UploadAttachment, t.authorizeListAttachments)
	default:
		return nil
	}
}

func (t *Transport) putContentAuthorizerFor(subpath []string) contentAuthorizer {
	if len(subpath) > 0 {
		return nil
	}
	return enabledOnly(t.tools.UpdatePage, t.authorizeUpdatePage)
}

func (t *Transport) postContentAuthorizerFor(subpath []string) contentAuthorizer {
	uploadEnabled := t.tools.UploadAttachment && t.attachmentRules != nil
	if attachmentID, ok := attachmentDataID(subpath); ok {
		return enabledOnly(uploadEnabled, t.attachmentVersionAuthorizer(attachmentID))
	}
	if childType(subpath) == attachmentSegment {
		return enabledOnly(uploadEnabled, t.authorizeUploadAttachment)
	}
	return nil
}

func (t *Transport) attachmentVersionAuthorizer(attachmentID string) contentAuthorizer {
	return func(req *http.Request, pageID string) (*http.Request, error) {
		return t.authorizeUploadAttachmentVersion(req, pageID, attachmentID)
	}
}

func attachmentDataID(subpath []string) (string, bool) {
	if len(subpath) == 4 && subpath[0] == childSegment && subpath[1] == attachmentSegment && IsContentID(subpath[2]) && subpath[3] == dataSegment {
		return subpath[2], true
	}
	return "", false
}

func childType(subpath []string) string {
	if len(subpath) == 2 && subpath[0] == childSegment {
		return subpath[1]
	}
	return ""
}

func enabledOnly(enabled bool, authorize contentAuthorizer) contentAuthorizer {
	if !enabled {
		return nil
	}
	return authorize
}

func bindContentID(authorize contentAuthorizer, contentID string) routeFunc {
	return func(req *http.Request) (*http.Request, error) { return authorize(req, contentID) }
}

func (t *Transport) downloadRoute(method string, segments []string) routeFunc {
	if method != http.MethodGet || !t.tools.GetAttachment || !isDownloadPath(segments) {
		return nil
	}
	return bindContentID(t.authorizeDownload, segments[0])
}

func IsContentID(value string) bool {
	return isPositiveNumber(value)
}

func isPositiveNumber(value string) bool {
	return positiveNumberPattern.MatchString(value)
}

func AttachmentContentID(attachmentID string) (string, bool) {
	contentID := strings.TrimPrefix(attachmentID, attachmentIDPrefix)
	if !IsContentID(contentID) {
		return "", false
	}
	return contentID, true
}

func isDownloadPath(segments []string) bool {
	return len(segments) == 2 && IsContentID(segments[0]) && IsAttachmentFileName(segments[1])
}

func IsAttachmentFileName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\;"`) && !strings.ContainsFunc(name, isControlOrFormat)
}

func isControlOrFormat(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

func (t *Transport) contentCollectionRoute(method string) routeFunc {
	if method != http.MethodPost || !t.tools.CreatePage {
		return nil
	}
	return t.authorizeCreatePage
}

func (t *Transport) searchRoute(method string) routeFunc {
	if method != http.MethodGet || !t.tools.Search {
		return nil
	}
	return t.authorizeSearch
}

type (
	queryRule  func(value string) bool
	queryParam struct {
		rule     queryRule
		required bool
	}
	queryRules map[string]queryParam
)

func checkQuery(requestURL *url.URL, rules queryRules) error {
	if requestURL.ForceQuery && requestURL.RawQuery == "" {
		return forbidden("empty query")
	}
	if !rfc3986QueryPattern.MatchString(requestURL.RawQuery) {
		return forbidden("query has characters outside RFC 3986")
	}
	values, err := url.ParseQuery(requestURL.RawQuery)
	if err != nil {
		return forbidden("query: %v", err)
	}
	for key, occurrences := range values {
		param, allowed := rules[key]
		if !allowed {
			return forbidden("query parameter %s", key)
		}
		if len(occurrences) != 1 || !param.rule(occurrences[0]) {
			return forbidden("query parameter %s=%q", key, strings.Join(occurrences, ","))
		}
	}
	for key, param := range rules {
		if param.required && !values.Has(key) {
			return forbidden("query parameter %s is required", key)
		}
	}
	return nil
}

func expandDistinctWithin(allowed []string) queryRule {
	return func(value string) bool {
		parts := strings.Split(value, ",")
		for i, part := range parts {
			if !slices.Contains(allowed, part) || slices.Contains(parts[:i], part) {
				return false
			}
		}
		return true
	}
}

func checkReadOnlyRequest(req *http.Request) error {
	if hasBody(req) {
		return forbidden("%s %s: body is not allowed", req.Method, req.URL.EscapedPath())
	}
	return checkHeadersAbsent(req.Header, contentTypeHeader, atlassianTokenHeader)
}

func checkHeadersAbsent(header http.Header, keys ...string) error {
	for _, key := range keys {
		if _, present := header[key]; present {
			return forbidden("header %s is not allowed on this route", key)
		}
	}
	return nil
}

func hasBody(req *http.Request) bool {
	return req.Body != nil && req.Body != http.NoBody
}

func readLimited(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %d bytes", errBodyTooLarge, limit)
	}
	return data, nil
}

func forbidden(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrForbidden, fmt.Sprintf(format, args...))
}
