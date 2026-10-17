package guard

//go:generate go tool mockgen -destination=mocks/mock_round_tripper.go -package=mocks net/http RoundTripper
//go:generate go tool mockgen -destination=mocks/mock_read_closer.go -package=mocks io ReadCloser

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"scoped-mcp/mcp-gitlab/config"
)

const (
	MaxBodyBytes        = 1 << 20
	DiscussionIDPattern = `^` + sha1HexExpr + `$`

	sha1HexExpr = `[0-9a-f]{40}`

	projectsPathPrefix   = "/api/v4/projects/"
	mergeRequestsSegment = "merge_requests"
	diffsSegment         = "diffs"
	notesSegment         = "notes"
	discussionsSegment   = "discussions"
	draftNotesSegment    = "draft_notes"

	pageParam    = "page"
	perPageParam = "per_page"

	bodyField           = "body"
	noteField           = "note"
	positionField       = "position"
	inReplyToField      = "in_reply_to_discussion_id"
	positionTypeField   = "position_type"
	baseSHAField        = "base_sha"
	startSHAField       = "start_sha"
	headSHAField        = "head_sha"
	oldPathField        = "old_path"
	newPathField        = "new_path"
	oldLineField        = "old_line"
	newLineField        = "new_line"
	lineRangeField      = "line_range"
	lineRangeStartField = "start"
	lineRangeEndField   = "end"
	lineCodeField       = "line_code"
	lineTypeField       = "type"

	positionTypeText = "text"
	lineTypeNew      = "new"
	lineTypeOld      = "old"

	tokenHeader       = "PRIVATE-TOKEN"
	acceptHeader      = "Accept"
	contentTypeHeader = "Content-Type"
	jsonMediaType     = "application/json"
	charsetParam      = "charset"
	utf8Charset       = "utf-8"
)

var (
	ErrForbidden = errors.New("guard: forbidden")

	allowedHeaders = []string{tokenHeader, acceptHeader, contentTypeHeader}

	positiveIntPattern    = regexp.MustCompile(`^[1-9][0-9]*$`)
	sha1HexPattern        = regexp.MustCompile(`^` + sha1HexExpr + `$`)
	lineCodePattern       = regexp.MustCompile(`^` + sha1HexExpr + `_[0-9]+_[0-9]+$`)
	projectSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	escapedSlashPattern   = regexp.MustCompile(`(?i)%2f`)

	positionRequiredKeys  = []string{positionTypeField, baseSHAField, startSHAField, headSHAField, oldPathField, newPathField}
	positionOptionalKeys  = []string{oldLineField, newLineField, lineRangeField}
	positionSHAKeys       = []string{baseSHAField, startSHAField, headSHAField}
	lineRangeKeys         = []string{lineRangeStartField, lineRangeEndField}
	lineRangeEndpointKeys = []string{lineCodeField, lineTypeField}
)

type targetKind int

const (
	targetMergeRequest targetKind = iota
	targetDiffs
	targetNote
	targetDiscussion
	targetReply
	targetDraftNote
)

func (kind targetKind) String() string {
	switch kind {
	case targetMergeRequest:
		return "merge request"
	case targetDiffs:
		return "diffs"
	case targetNote:
		return "note"
	case targetDiscussion:
		return "discussion"
	case targetReply:
		return "reply"
	case targetDraftNote:
		return "draft note"
	default:
		return "unknown target"
	}
}

type Transport struct {
	tools         config.Tools
	scheme        string
	host          string
	nextTransport http.RoundTripper
}

func New(cfg config.Config, nextTransport http.RoundTripper) (*Transport, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("guard: parse base_url: %w", err)
	}
	return &Transport{tools: cfg.Tools, scheme: base.Scheme, host: base.Host, nextTransport: nextTransport}, nil
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	if err := t.checkOrigin(req); err != nil {
		return nil, err
	}
	if err := checkHeaders(req); err != nil {
		return nil, err
	}
	kind, err := parseTarget(req.URL.EscapedPath())
	if err != nil {
		return nil, err
	}
	switch {
	case kind == targetMergeRequest && req.Method == http.MethodGet:
		return t.forwardRead(req, kind, t.tools.AnyEnabled(), "no tool is enabled", checkNoQuery)
	case kind == targetDiffs && req.Method == http.MethodGet:
		return t.forwardRead(req, kind, t.tools.NeedsDiffPositions(), "neither tools.add_mr_line_comment nor tools.add_draft_note is enabled", checkDiffsQuery)
	case kind == targetNote && req.Method == http.MethodPost:
		return t.forwardWrite(req, kind, t.tools.AddMRNote, "tools.add_mr_note is not enabled", checkBodyTextOnly)
	case kind == targetDiscussion && req.Method == http.MethodPost:
		return t.forwardWrite(req, kind, t.tools.AddMRLineComment, "tools.add_mr_line_comment is not enabled", checkDiscussionBody)
	case kind == targetReply && req.Method == http.MethodPost:
		return t.forwardWrite(req, kind, t.tools.ReplyToDiscussion, "tools.reply_to_discussion is not enabled", checkBodyTextOnly)
	case kind == targetDraftNote && req.Method == http.MethodPost:
		return t.forwardWrite(req, kind, t.tools.AddDraftNote, "tools.add_draft_note is not enabled", checkDraftBody)
	default:
		return nil, forbidden("%s %s", req.Method, req.URL.EscapedPath())
	}
}

func (t *Transport) checkOrigin(req *http.Request) error {
	switch {
	case req.URL.Scheme != t.scheme || req.URL.Host != t.host:
		return forbidden("host %s://%s", req.URL.Scheme, req.URL.Host)
	case req.Host != "" && req.Host != t.host:
		return forbidden("host header %s", req.Host)
	case req.URL.User != nil:
		return forbidden("userinfo in URL")
	case req.URL.Opaque != "":
		return forbidden("opaque URL %s", req.URL.Opaque)
	case req.URL.RawPath != "" && req.URL.RawPath != req.URL.EscapedPath():
		return forbidden("raw path %s does not encode path %s", req.URL.RawPath, req.URL.Path)
	default:
		return nil
	}
}

func checkHeaders(req *http.Request) error {
	for name := range req.Header {
		if !isAllowedHeader(name) {
			return forbidden("header %s", name)
		}
	}
	switch {
	case len(req.Trailer) > 0:
		return forbidden("trailers")
	case len(req.TransferEncoding) > 0:
		return forbidden("transfer encoding %s", strings.Join(req.TransferEncoding, ", "))
	default:
		return nil
	}
}

func isAllowedHeader(name string) bool {
	return slices.ContainsFunc(allowedHeaders, func(allowed string) bool {
		return strings.EqualFold(name, allowed)
	})
}

func headerValues(header http.Header, name string) []string {
	var values []string
	for key, keyValues := range header {
		if strings.EqualFold(key, name) {
			values = append(values, keyValues...)
		}
	}
	return values
}

func parseTarget(escapedPath string) (targetKind, error) {
	rest, isProjectsPath := strings.CutPrefix(escapedPath, projectsPathPrefix)
	if !isProjectsPath {
		return 0, forbidden("path %s", escapedPath)
	}
	segments := strings.Split(rest, "/")
	if len(segments) < 3 {
		return 0, forbidden("path %s", escapedPath)
	}
	project, collection, iid, subresource := segments[0], segments[1], segments[2], segments[3:]
	if !validEscapedProject(project) || collection != mergeRequestsSegment || !positiveIntPattern.MatchString(iid) {
		return 0, forbidden("path %s", escapedPath)
	}
	switch {
	case len(subresource) == 0:
		return targetMergeRequest, nil
	case len(subresource) == 1 && subresource[0] == diffsSegment:
		return targetDiffs, nil
	case len(subresource) == 1 && subresource[0] == notesSegment:
		return targetNote, nil
	case len(subresource) == 1 && subresource[0] == discussionsSegment:
		return targetDiscussion, nil
	case len(subresource) == 3 && subresource[0] == discussionsSegment && ValidDiscussionID(subresource[1]) && subresource[2] == notesSegment:
		return targetReply, nil
	case len(subresource) == 1 && subresource[0] == draftNotesSegment:
		return targetDraftNote, nil
	default:
		return 0, forbidden("path %s", escapedPath)
	}
}

func ValidProject(project string) bool {
	return positiveIntPattern.MatchString(project) || validPathSegments(strings.Split(project, "/"))
}

func validEscapedProject(escapedProject string) bool {
	return positiveIntPattern.MatchString(escapedProject) || validPathSegments(escapedSlashPattern.Split(escapedProject, -1))
}

func validPathSegments(segments []string) bool {
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if segment == "." || segment == ".." || !projectSegmentPattern.MatchString(segment) {
			return false
		}
	}
	return true
}

func ValidDiscussionID(id string) bool {
	return sha1HexPattern.MatchString(id)
}

func (t *Transport) forwardRead(req *http.Request, kind targetKind, enabled bool, disabledReason string, checkQuery func(*url.URL) error) (*http.Response, error) {
	if !enabled {
		return nil, forbidden("%s: %s", kind, disabledReason)
	}
	if req.Body != nil && req.Body != http.NoBody {
		return nil, forbidden("%s: body is not allowed", kind)
	}
	if err := checkQuery(req.URL); err != nil {
		return nil, forbidden("%s: %v", kind, err)
	}
	return t.nextTransport.RoundTrip(req)
}

func checkNoQuery(u *url.URL) error {
	if u.RawQuery != "" || u.ForceQuery {
		return errors.New("query is not allowed")
	}
	return nil
}

func checkDiffsQuery(u *url.URL) error {
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	for name, items := range values {
		if name != pageParam && name != perPageParam {
			return fmt.Errorf("query parameter %s is not allowed", name)
		}
		if len(items) != 1 || !positiveIntPattern.MatchString(items[0]) {
			return fmt.Errorf("query parameter %s must be a single positive integer", name)
		}
	}
	return nil
}

func (t *Transport) forwardWrite(req *http.Request, kind targetKind, enabled bool, disabledReason string, checkBody func([]byte) error) (*http.Response, error) {
	if !enabled {
		return nil, forbidden("%s: %s", kind, disabledReason)
	}
	if err := checkNoQuery(req.URL); err != nil {
		return nil, forbidden("%s: %v", kind, err)
	}
	if err := checkJSONContentType(req.Header); err != nil {
		return nil, forbidden("%s: %v", kind, err)
	}
	if req.Body == nil {
		return nil, forbidden("%s: empty body", kind)
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("guard: read body: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, forbidden("%s: body exceeds %d bytes", kind, MaxBodyBytes)
	}
	if !utf8.Valid(body) {
		return nil, forbidden("%s: body is not valid UTF-8", kind)
	}
	if err := checkBody(body); err != nil {
		return nil, forbidden("%s: %v", kind, err)
	}
	return t.forwardCheckedBody(req, body)
}

func checkJSONContentType(header http.Header) error {
	contentTypes := headerValues(header, contentTypeHeader)
	if len(contentTypes) != 1 {
		return fmt.Errorf("exactly one %s header is required, got %d", contentTypeHeader, len(contentTypes))
	}
	mediaType, params, err := mime.ParseMediaType(contentTypes[0])
	if err != nil {
		return fmt.Errorf("%s: %w", contentTypeHeader, err)
	}
	if mediaType != jsonMediaType {
		return fmt.Errorf("%s must be %s", contentTypeHeader, jsonMediaType)
	}
	switch {
	case len(params) == 0:
		return nil
	case len(params) == 1 && strings.EqualFold(params[charsetParam], utf8Charset):
		return nil
	default:
		return fmt.Errorf("%s allows only the %s=%s parameter", contentTypeHeader, charsetParam, utf8Charset)
	}
}

func checkBodyTextOnly(body []byte) error {
	object, err := decodeRequestBody(body, []string{bodyField}, nil)
	if err != nil {
		return err
	}
	return checkTextField(object, bodyField)
}

func checkDiscussionBody(body []byte) error {
	object, err := decodeRequestBody(body, []string{bodyField, positionField}, nil)
	if err != nil {
		return err
	}
	if err := checkTextField(object, bodyField); err != nil {
		return err
	}
	return checkPosition(object[positionField])
}

func checkDraftBody(body []byte) error {
	object, err := decodeRequestBody(body, []string{noteField}, []string{positionField, inReplyToField})
	if err != nil {
		return err
	}
	if err := checkTextField(object, noteField); err != nil {
		return err
	}
	positionRaw, hasPosition := object[positionField]
	replyRaw, hasReply := object[inReplyToField]
	switch {
	case hasPosition && hasReply:
		return fmt.Errorf("%s and %s are mutually exclusive", positionField, inReplyToField)
	case hasPosition:
		return checkPosition(positionRaw)
	case hasReply:
		return checkInReplyToDiscussionID(replyRaw)
	default:
		return nil
	}
}

func decodeRequestBody(body []byte, required, optional []string) (map[string]json.RawMessage, error) {
	object, err := decodeObjectWithKeys(body, required, optional)
	if err != nil {
		return nil, fmt.Errorf("request body: %w", err)
	}
	return object, nil
}

func decodeObjectWithKeys(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	object, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	for _, key := range required {
		if _, present := object[key]; !present {
			return nil, fmt.Errorf("missing key %s", key)
		}
	}
	for key := range object {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			return nil, fmt.Errorf("unexpected key %s", key)
		}
	}
	return object, nil
}

func checkTextField(object map[string]json.RawMessage, key string) error {
	text, isString := decodeString(object[key])
	if !isString {
		return fmt.Errorf("%s must be a string", key)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%s is blank", key)
	}
	if HasQuickAction(text) {
		return fmt.Errorf("%s has a line starting with / (a GitLab quick action)", key)
	}
	return nil
}

func checkInReplyToDiscussionID(raw json.RawMessage) error {
	id, isString := decodeString(raw)
	if !isString || !ValidDiscussionID(id) {
		return fmt.Errorf("%s must be a discussion id", inReplyToField)
	}
	return nil
}

func checkPosition(raw json.RawMessage) error {
	if err := checkPositionObject(raw); err != nil {
		return fmt.Errorf("%s: %w", positionField, err)
	}
	return nil
}

func checkPositionObject(raw json.RawMessage) error {
	position, err := decodeObjectWithKeys(raw, positionRequiredKeys, positionOptionalKeys)
	if err != nil {
		return err
	}
	if positionType, isString := decodeString(position[positionTypeField]); !isString || positionType != positionTypeText {
		return fmt.Errorf("%s must be %s", positionTypeField, positionTypeText)
	}
	for _, key := range positionSHAKeys {
		if err := checkSHA1Hex(key, position[key]); err != nil {
			return err
		}
	}
	if _, err := checkPath(oldPathField, position[oldPathField]); err != nil {
		return err
	}
	newPath, err := checkPath(newPathField, position[newPathField])
	if err != nil {
		return err
	}
	if err := checkPositionLines(position); err != nil {
		return err
	}
	lineRangeRaw, hasLineRange := position[lineRangeField]
	if !hasLineRange {
		return nil
	}
	return checkLineRange(lineRangeRaw, newPath)
}

func checkSHA1Hex(key string, raw json.RawMessage) error {
	text, isString := decodeString(raw)
	if !isString || !sha1HexPattern.MatchString(text) {
		return fmt.Errorf("%s must match %s", key, sha1HexPattern)
	}
	return nil
}

func checkPath(key string, raw json.RawMessage) (string, error) {
	path, isString := decodeString(raw)
	if !isString || !ValidPath(path) {
		return "", fmt.Errorf("%s must be a non-empty string without control characters", key)
	}
	return path, nil
}

func ValidPath(path string) bool {
	return path != "" && !strings.ContainsFunc(path, unicode.IsControl)
}

func checkPositionLines(position map[string]json.RawMessage) error {
	oldLineRaw, hasOldLine := position[oldLineField]
	newLineRaw, hasNewLine := position[newLineField]
	if !hasOldLine && !hasNewLine {
		return fmt.Errorf("%s or %s is required", oldLineField, newLineField)
	}
	if hasOldLine && !isPositiveIntLiteral(oldLineRaw) {
		return fmt.Errorf("%s must be a positive integer", oldLineField)
	}
	if hasNewLine && !isPositiveIntLiteral(newLineRaw) {
		return fmt.Errorf("%s must be a positive integer", newLineField)
	}
	return nil
}

// encoding/json hands a value over without surrounding whitespace, so the raw bytes are the number literal itself.
func isPositiveIntLiteral(raw json.RawMessage) bool {
	return positiveIntPattern.Match(raw)
}

func checkLineRange(raw json.RawMessage, newPath string) error {
	lineRange, err := decodeObjectWithKeys(raw, lineRangeKeys, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", lineRangeField, err)
	}
	wantLineCodePrefix := lineCodePrefix(newPath)
	for _, endpointName := range lineRangeKeys {
		if err := checkLineRangeEndpoint(lineRange[endpointName], wantLineCodePrefix); err != nil {
			return fmt.Errorf("%s.%s: %w", lineRangeField, endpointName, err)
		}
	}
	return nil
}

func checkLineRangeEndpoint(raw json.RawMessage, wantLineCodePrefix string) error {
	endpoint, err := decodeObjectWithKeys(raw, lineRangeEndpointKeys, nil)
	if err != nil {
		return err
	}
	lineCode, isString := decodeString(endpoint[lineCodeField])
	if !isString || !lineCodePattern.MatchString(lineCode) || !strings.HasPrefix(lineCode, wantLineCodePrefix) {
		return fmt.Errorf("%s must be <sha1 of %s>_<old>_<new>", lineCodeField, newPathField)
	}
	lineType, isString := decodeString(endpoint[lineTypeField])
	if !isString || (lineType != lineTypeNew && lineType != lineTypeOld) {
		return fmt.Errorf("%s must be %s or %s", lineTypeField, lineTypeNew, lineTypeOld)
	}
	return nil
}

func lineCodePrefix(newPath string) string {
	newPathSHA1 := sha1.Sum([]byte(newPath))
	return hex.EncodeToString(newPathSHA1[:]) + "_"
}

func HasQuickAction(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeftFunc(strings.TrimSuffix(line, "\r"), unicode.IsSpace)
		if strings.HasPrefix(trimmed, "/") {
			return true
		}
	}
	return false
}

func (t *Transport) forwardCheckedBody(req *http.Request, body []byte) (*http.Response, error) {
	forwarded := req.Clone(req.Context())
	forwarded.Body = io.NopCloser(bytes.NewReader(body))
	// net/http rebuilds the body from GetBody on retries, so it must replay the checked bytes.
	forwarded.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	forwarded.ContentLength = int64(len(body))
	return t.nextTransport.RoundTrip(forwarded)
}

func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if opening != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyToken.(string)
		if _, duplicate := object[key]; duplicate {
			return nil, fmt.Errorf("duplicate key %s", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		object[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON object")
	}
	return object, nil
}

func decodeString(raw json.RawMessage) (string, bool) {
	var text *string
	if err := json.Unmarshal(raw, &text); err != nil || text == nil {
		return "", false
	}
	return *text, true
}

func forbidden(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrForbidden, fmt.Sprintf(format, args...))
}
