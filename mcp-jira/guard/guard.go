package guard

//go:generate go tool mockgen -destination=mocks/mock_round_tripper.go -package=mocks net/http RoundTripper
//go:generate go tool mockgen -destination=mocks/mock_read_closer.go -package=mocks io ReadCloser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"scoped-mcp/mcp-jira/config"
)

const (
	MaxBodyBytes            = 1 << 20
	AttachmentEnvelopeBytes = 64 << 10

	issuePath      = "/rest/api/2/issue"
	tokenOwnerPath = "/rest/api/2/myself"
	summaryField   = "summary"
	assigneeField  = "assignee"
	projectField   = "project"
	issueTypeField = "issuetype"
	keyField       = "key"
	nameField      = "name"

	attachmentsPathSuffix = "/attachments"
	attachmentField       = "file"
	attachmentsMediaType  = "multipart/form-data"
	atlassianTokenHeader  = "X-Atlassian-Token"
	atlassianTokenNoCheck = "no-check"

	commentPathSuffix = "/comment"
	commentTextField  = "body"

	issueLinkPath     = "/rest/api/2/issueLink"
	linkTypeField     = "type"
	inwardIssueField  = "inwardIssue"
	outwardIssueField = "outwardIssue"

	createIssueRoute = "create issue"
	updateIssueRoute = "update issue"
	attachmentsRoute = "attachments"
	commentRoute     = "comment"
	issueLinkRoute   = "issueLink"
)

var (
	ErrForbidden          = errors.New("guard: forbidden")
	ErrTokenOwnerResponse = errors.New("guard: token owner response could not be read")

	errBodyTooLarge = errors.New("body exceeds the size limit")
)

type (
	allowedFields            map[string]struct{}
	allowedTypes             map[string]allowedFields
	allowedProjects          map[string]allowedTypes
	allowedLinkTypes         map[string]struct{}
	updatableFieldsByProject map[string]allowedFields
)

type Transport struct {
	createIssueEnabled       bool
	attachFilesEnabled       bool
	addCommentEnabled        bool
	linkIssuesEnabled        bool
	updateIssueEnabled       bool
	scheme                   string
	host                     string
	createIssuePath          string
	issuePathPrefix          string
	tokenOwnerLookupPath     string
	issueLinkCreatePath      string
	anyProjectAssignsToSelf  bool
	projects                 allowedProjects
	updatableFieldsByProject updatableFieldsByProject
	linkTypes                allowedLinkTypes
	attachmentRules          *config.Attachments
	nextTransport            http.RoundTripper
	tokenOwnerMu             sync.Mutex
	tokenOwner               string
}

func New(cfg config.Config, nextTransport http.RoundTripper) (*Transport, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("guard: parse base_url: %w", err)
	}
	basePath := strings.TrimSuffix(base.Path, "/")
	return &Transport{
		createIssueEnabled:       cfg.Tools.CreateIssue,
		attachFilesEnabled:       cfg.Tools.AttachFiles,
		addCommentEnabled:        cfg.Tools.AddComment,
		linkIssuesEnabled:        cfg.Tools.LinkIssues,
		updateIssueEnabled:       cfg.Tools.UpdateIssue,
		scheme:                   base.Scheme,
		host:                     base.Host,
		createIssuePath:          basePath + issuePath,
		issuePathPrefix:          basePath + issuePath + "/",
		tokenOwnerLookupPath:     basePath + tokenOwnerPath,
		issueLinkCreatePath:      basePath + issueLinkPath,
		anyProjectAssignsToSelf:  anyAssignsToSelf(cfg),
		projects:                 indexProjects(cfg),
		updatableFieldsByProject: indexUpdatableFields(cfg),
		linkTypes:                indexLinkTypes(cfg),
		attachmentRules:          attachmentRulesOf(cfg.Attachments),
		nextTransport:            nextTransport,
	}, nil
}

func anyAssignsToSelf(cfg config.Config) bool {
	return slices.ContainsFunc(cfg.Projects, func(project config.Project) bool { return project.AssignToSelf })
}

func attachmentRulesOf(attachments *config.Attachments) *config.Attachments {
	if attachments == nil {
		return nil
	}
	return &config.Attachments{Extensions: slices.Clone(attachments.Extensions), MaxBytes: attachments.MaxBytes}
}

func indexProjects(cfg config.Config) allowedProjects {
	projects := make(allowedProjects, len(cfg.Projects))
	for _, project := range cfg.Projects {
		types := make(allowedTypes, len(project.IssueTypes))
		for _, issueType := range project.IssueTypes {
			fields := jiraIDs(cfg.Fields, issueType.Fields)
			fields[summaryField] = struct{}{}
			if project.AssignToSelf {
				fields[assigneeField] = struct{}{}
			}
			types[issueType.ID] = fields
		}
		projects[project.Key] = types
	}
	return projects
}

func indexUpdatableFields(cfg config.Config) updatableFieldsByProject {
	updatableFieldsByProject := make(updatableFieldsByProject, len(cfg.Projects))
	for _, project := range cfg.Projects {
		updatableFieldsByProject[project.Key] = jiraIDs(cfg.Fields, project.UpdateFields)
	}
	return updatableFieldsByProject
}

func jiraIDs(fields map[string]config.Field, keys []string) allowedFields {
	ids := make(allowedFields, len(keys))
	for _, key := range keys {
		ids[fields[key].Jira] = struct{}{}
	}
	return ids
}

func indexLinkTypes(cfg config.Config) allowedLinkTypes {
	linkTypes := make(allowedLinkTypes, len(cfg.IssueLinks))
	for _, link := range cfg.IssueLinks {
		linkTypes[link.Type] = struct{}{}
	}
	return linkTypes
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	if err := t.checkOrigin(req); err != nil {
		return nil, err
	}
	issueKey, subresource, isIssuePath := t.splitIssuePath(req.URL.Path)
	switch {
	case req.Method == http.MethodPost && req.URL.Path == t.createIssuePath:
		return t.forwardCreateIssue(req)
	case req.Method == http.MethodPost && isIssuePath && subresource == attachmentsPathSuffix:
		return t.forwardAttachment(req, issueKey)
	case req.Method == http.MethodPost && isIssuePath && subresource == commentPathSuffix:
		return t.forwardComment(req, issueKey)
	case req.Method == http.MethodPost && req.URL.Path == t.issueLinkCreatePath:
		return t.forwardIssueLink(req)
	case req.Method == http.MethodPut && isIssuePath && subresource == "":
		return t.forwardUpdateIssue(req, issueKey)
	case req.Method == http.MethodGet && req.URL.Path == t.tokenOwnerLookupPath:
		return t.forwardTokenOwnerLookup(req)
	default:
		return nil, forbidden("%s %s", req.Method, req.URL.EscapedPath())
	}
}

func (t *Transport) splitIssuePath(path string) (issueKey, subresource string, ok bool) {
	rest, hasPrefix := strings.CutPrefix(path, t.issuePathPrefix)
	if !hasPrefix {
		return "", "", false
	}
	subresourceStart := strings.Index(rest, "/")
	if subresourceStart < 0 {
		return rest, "", true
	}
	return rest[:subresourceStart], rest[subresourceStart:], true
}

func (t *Transport) forwardCreateIssue(req *http.Request) (*http.Response, error) {
	if !t.createIssueEnabled {
		return nil, forbidden("%s: tools.create_issue is not enabled", createIssueRoute)
	}
	body, err := readBody(req.Body, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	if err := t.checkBody(body); err != nil {
		return nil, err
	}
	return t.forwardCheckedBody(req, body)
}

func (t *Transport) forwardAttachment(req *http.Request, rawIssueKey string) (*http.Response, error) {
	if !t.attachFilesEnabled {
		return nil, forbidden("%s: tools.attach_files is not enabled", attachmentsRoute)
	}
	if t.attachmentRules == nil {
		return nil, forbidden("%s: the config has no attachments section", attachmentsRoute)
	}
	if _, err := t.checkIssueProject(attachmentsRoute, rawIssueKey); err != nil {
		return nil, err
	}
	if err := checkAtlassianToken(req.Header); err != nil {
		return nil, err
	}
	boundary, err := multipartBoundary(req.Header.Values("Content-Type"))
	if err != nil {
		return nil, err
	}
	body, err := readBody(req.Body, t.attachmentRules.MaxBytes+AttachmentEnvelopeBytes)
	if err != nil {
		return nil, err
	}
	if err := t.checkAttachmentBody(body, boundary); err != nil {
		return nil, err
	}
	return t.forwardCheckedBody(req, body)
}

func (t *Transport) checkIssueProject(route, rawIssueKey string) (projectKey string, err error) {
	projectKey, ok := config.IssueKeyProject(rawIssueKey)
	if !ok {
		return "", forbidden("%s: issue key %q", route, rawIssueKey)
	}
	if _, ok := t.projects[projectKey]; !ok {
		return "", forbidden("%s: project %s", route, projectKey)
	}
	return projectKey, nil
}

func checkAtlassianToken(header http.Header) error {
	if !slices.Equal(header.Values(atlassianTokenHeader), []string{atlassianTokenNoCheck}) {
		return forbidden("%s: %s must be exactly %s", attachmentsRoute, atlassianTokenHeader, atlassianTokenNoCheck)
	}
	return nil
}

func multipartBoundary(contentTypes []string) (string, error) {
	if len(contentTypes) != 1 {
		return "", forbidden("%s: exactly one Content-Type is required", attachmentsRoute)
	}
	mediaType, params, err := mime.ParseMediaType(contentTypes[0])
	if err != nil {
		return "", forbidden("%s: Content-Type: %v", attachmentsRoute, err)
	}
	boundary, ok := params["boundary"]
	if mediaType != attachmentsMediaType || !ok || len(params) != 1 {
		return "", forbidden("%s: Content-Type must be %s with only a boundary", attachmentsRoute, attachmentsMediaType)
	}
	return boundary, nil
}

func (t *Transport) checkAttachmentBody(body []byte, boundary string) error {
	part, err := multipart.NewReader(bytes.NewReader(body), boundary).NextRawPart()
	if err != nil {
		return forbidden("%s: request body: %v", attachmentsRoute, err)
	}
	fileName, err := attachmentFileName(part.Header.Get("Content-Disposition"))
	if err != nil {
		return err
	}
	if !t.attachmentRules.AllowsExtension(fileName) {
		return forbidden("%s: extension of %q", attachmentsRoute, fileName)
	}
	content, err := io.ReadAll(io.LimitReader(part, t.attachmentRules.MaxBytes+1))
	if err != nil {
		return forbidden("%s: file content: %v", attachmentsRoute, err)
	}
	if int64(len(content)) > t.attachmentRules.MaxBytes {
		return forbidden("%s: file exceeds %d bytes", attachmentsRoute, t.attachmentRules.MaxBytes)
	}
	isCanonical, err := isCanonicalAttachmentBody(body, boundary, fileName, content)
	if err != nil {
		return err
	}
	if !isCanonical {
		return forbidden("%s: request body must be exactly one file part without anything else", attachmentsRoute)
	}
	return nil
}

func isCanonicalAttachmentBody(body []byte, boundary, fileName string, content []byte) (bool, error) {
	canonical, err := canonicalAttachmentBody(boundary, fileName, content)
	if err != nil {
		return false, err
	}
	return bytes.Equal(canonical, body), nil
}

func attachmentFileName(contentDisposition string) (string, error) {
	disposition, params, err := mime.ParseMediaType(contentDisposition)
	if err != nil {
		return "", forbidden("%s: Content-Disposition: %v", attachmentsRoute, err)
	}
	if disposition != "form-data" || params["name"] != attachmentField {
		return "", forbidden("%s: part must be form-data named %s", attachmentsRoute, attachmentField)
	}
	fileName := params["filename"]
	if fileName == "" || strings.ContainsAny(fileName, `/\`) {
		return "", forbidden("%s: file name %q", attachmentsRoute, fileName)
	}
	return fileName, nil
}

func canonicalAttachmentBody(boundary, fileName string, content []byte) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, forbidden("%s: boundary: %v", attachmentsRoute, err)
	}
	part, err := writer.CreateFormFile(attachmentField, fileName)
	if err != nil {
		return nil, fmt.Errorf("guard: encode attachment: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return nil, fmt.Errorf("guard: encode attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("guard: encode attachment: %w", err)
	}
	return body.Bytes(), nil
}

func (t *Transport) forwardComment(req *http.Request, rawIssueKey string) (*http.Response, error) {
	if !t.addCommentEnabled {
		return nil, forbidden("%s: tools.add_comment is not enabled", commentRoute)
	}
	if _, err := t.checkIssueProject(commentRoute, rawIssueKey); err != nil {
		return nil, err
	}
	requestBody, err := readBody(req.Body, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	if err := checkCommentRequestBody(requestBody); err != nil {
		return nil, err
	}
	return t.forwardCheckedBody(req, requestBody)
}

func checkCommentRequestBody(requestBody []byte) error {
	commentText, err := soleString(requestBody, commentTextField)
	if err != nil {
		return forbidden("%s: request body: %v", commentRoute, err)
	}
	if strings.TrimSpace(commentText) == "" {
		return forbidden("%s: %s is blank", commentRoute, commentTextField)
	}
	return nil
}

func (t *Transport) forwardUpdateIssue(req *http.Request, rawIssueKey string) (*http.Response, error) {
	if !t.updateIssueEnabled {
		return nil, forbidden("%s: tools.update_issue is not enabled", updateIssueRoute)
	}
	projectKey, err := t.checkIssueProject(updateIssueRoute, rawIssueKey)
	if err != nil {
		return nil, err
	}
	requestBody, err := readBody(req.Body, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	if err := t.checkUpdateIssueRequestBody(projectKey, requestBody); err != nil {
		return nil, err
	}
	return t.forwardCheckedBody(req, requestBody)
}

func (t *Transport) checkUpdateIssueRequestBody(projectKey string, requestBody []byte) error {
	fields, err := decodeFields(requestBody)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return forbidden("%s: fields is empty", updateIssueRoute)
	}
	if name, found := disallowedField(fields, t.updatableFieldsByProject[projectKey]); found {
		return forbidden("%s: field %s in project %s", updateIssueRoute, name, projectKey)
	}
	return nil
}

func (t *Transport) forwardIssueLink(req *http.Request) (*http.Response, error) {
	if !t.linkIssuesEnabled {
		return nil, forbidden("%s: tools.link_issues is not enabled", issueLinkRoute)
	}
	requestBody, err := readBody(req.Body, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	if err := t.checkIssueLinkRequestBody(requestBody); err != nil {
		return nil, err
	}
	return t.forwardCheckedBody(req, requestBody)
}

func (t *Transport) checkIssueLinkRequestBody(requestBody []byte) error {
	linkRequest, err := decodeObject(requestBody)
	if err != nil {
		return forbidden("%s: request body: %v", issueLinkRoute, err)
	}
	if !hasExactlyKeys(linkRequest, linkTypeField, inwardIssueField, outwardIssueField) {
		return forbidden("%s: request body must contain only %s, %s and %s", issueLinkRoute, linkTypeField, inwardIssueField, outwardIssueField)
	}
	if err := t.checkLinkType(linkRequest); err != nil {
		return err
	}
	inwardIssueKey, err := t.linkedIssueKey(linkRequest, inwardIssueField)
	if err != nil {
		return err
	}
	outwardIssueKey, err := t.linkedIssueKey(linkRequest, outwardIssueField)
	if err != nil {
		return err
	}
	if inwardIssueKey == outwardIssueKey {
		return forbidden("%s: %s and %s are the same issue", issueLinkRoute, inwardIssueField, outwardIssueField)
	}
	return nil
}

func (t *Transport) checkLinkType(linkRequest map[string]json.RawMessage) error {
	name, err := soleString(linkRequest[linkTypeField], nameField)
	if err != nil {
		return forbidden("%s: %s: %v", issueLinkRoute, linkTypeField, err)
	}
	if _, ok := t.linkTypes[name]; !ok {
		return forbidden("%s: link type %q", issueLinkRoute, name)
	}
	return nil
}

func (t *Transport) linkedIssueKey(linkRequest map[string]json.RawMessage, field string) (string, error) {
	issueKey, err := soleString(linkRequest[field], keyField)
	if err != nil {
		return "", forbidden("%s: %s: %v", issueLinkRoute, field, err)
	}
	if _, err := t.checkIssueProject(issueLinkRoute, issueKey); err != nil {
		return "", err
	}
	return issueKey, nil
}

func hasExactlyKeys(object map[string]json.RawMessage, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
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

func (t *Transport) forwardTokenOwnerLookup(req *http.Request) (*http.Response, error) {
	if !t.createIssueEnabled {
		return nil, forbidden("token owner lookup: tools.create_issue is not enabled")
	}
	if !t.anyProjectAssignsToSelf {
		return nil, forbidden("token owner lookup: no project has assign_to_self")
	}
	if req.Body != nil && req.Body != http.NoBody {
		return nil, forbidden("token owner lookup: body is not allowed")
	}
	response, err := t.nextTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return response, nil
	}
	body, err := bufferResponseBody(response)
	if err != nil {
		return nil, err
	}
	t.rememberTokenOwner(body)
	return response, nil
}

func bufferResponseBody(response *http.Response) ([]byte, error) {
	originalBody := response.Body
	defer func() { _ = originalBody.Close() }()
	body, err := readLimited(originalBody, MaxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenOwnerResponse, err)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return body, nil
}

func (t *Transport) rememberTokenOwner(body []byte) {
	name, ok := decodeTokenOwnerName(body)
	if !ok {
		return
	}
	t.tokenOwnerMu.Lock()
	defer t.tokenOwnerMu.Unlock()
	t.tokenOwner = name
}

func decodeTokenOwnerName(body []byte) (string, bool) {
	var tokenOwner struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &tokenOwner); err != nil {
		return "", false
	}
	return tokenOwner.Name, tokenOwner.Name != ""
}

func (t *Transport) rememberedTokenOwner() string {
	t.tokenOwnerMu.Lock()
	defer t.tokenOwnerMu.Unlock()
	return t.tokenOwner
}

func (t *Transport) checkOrigin(req *http.Request) error {
	switch {
	case req.URL.Scheme != t.scheme || req.URL.Host != t.host:
		return forbidden("host %s://%s", req.URL.Scheme, req.URL.Host)
	case req.Host != "" && req.Host != t.host:
		return forbidden("host header %s", req.Host)
	case req.URL.Opaque != "" || req.URL.RawPath != "":
		return forbidden("path %s", req.URL.EscapedPath())
	case req.URL.RawQuery != "" || req.URL.ForceQuery:
		return forbidden("query is not allowed")
	default:
		return nil
	}
}

func readBody(body io.Reader, limit int64) ([]byte, error) {
	if body == nil {
		return nil, forbidden("empty body")
	}
	data, err := readLimited(body, limit)
	switch {
	case errors.Is(err, errBodyTooLarge):
		return nil, forbidden("%v", err)
	case err != nil:
		return nil, fmt.Errorf("guard: read body: %w", err)
	default:
		return data, nil
	}
}

func readLimited(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w of %d bytes", errBodyTooLarge, limit)
	}
	return data, nil
}

func (t *Transport) checkBody(body []byte) error {
	fields, err := decodeFields(body)
	if err != nil {
		return err
	}
	allowedForType, err := t.allowedFieldsFor(fields)
	if err != nil {
		return err
	}
	if err := checkCreateFieldNames(fields, allowedForType); err != nil {
		return err
	}
	return t.checkAssigneeIsTokenOwner(fields)
}

func (t *Transport) checkAssigneeIsTokenOwner(fields map[string]json.RawMessage) error {
	assigneeRaw, present := fields[assigneeField]
	if !present {
		return nil
	}
	assigneeName, err := soleString(assigneeRaw, nameField)
	if err != nil {
		return forbidden("assignee: %v", err)
	}
	tokenOwner := t.rememberedTokenOwner()
	if tokenOwner == "" {
		return forbidden("assignee before the token owner is known")
	}
	if assigneeName != tokenOwner {
		return forbidden("assignee %s is not the token owner", assigneeName)
	}
	return nil
}

func decodeFields(body []byte) (map[string]json.RawMessage, error) {
	envelope, err := decodeObject(body)
	if err != nil {
		return nil, forbidden("body: %v", err)
	}
	fieldsRaw, ok := soleValue(envelope, "fields")
	if !ok {
		return nil, forbidden("body must contain only fields")
	}
	fields, err := decodeObject(fieldsRaw)
	if err != nil {
		return nil, forbidden("fields: %v", err)
	}
	return fields, nil
}

func (t *Transport) allowedFieldsFor(fields map[string]json.RawMessage) (allowedFields, error) {
	projectKey, err := soleString(fields[projectField], keyField)
	if err != nil {
		return nil, forbidden("project: %v", err)
	}
	types, ok := t.projects[projectKey]
	if !ok {
		return nil, forbidden("project %s", projectKey)
	}
	issueTypeID, err := soleString(fields[issueTypeField], "id")
	if err != nil {
		return nil, forbidden("issuetype: %v", err)
	}
	allowedForType, ok := types[issueTypeID]
	if !ok {
		return nil, forbidden("issue type %s in project %s", issueTypeID, projectKey)
	}
	return allowedForType, nil
}

func checkCreateFieldNames(fields map[string]json.RawMessage, allowedForType allowedFields) error {
	fieldsExceptProjectAndIssueType := maps.Clone(fields)
	delete(fieldsExceptProjectAndIssueType, projectField)
	delete(fieldsExceptProjectAndIssueType, issueTypeField)
	if name, found := disallowedField(fieldsExceptProjectAndIssueType, allowedForType); found {
		return forbidden("field %s", name)
	}
	return nil
}

func disallowedField(fields map[string]json.RawMessage, allowed allowedFields) (string, bool) {
	for name := range fields {
		if _, isAllowed := allowed[name]; !isAllowed {
			return name, true
		}
	}
	return "", false
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

func soleString(raw json.RawMessage, key string) (string, error) {
	object, err := decodeObject(raw)
	if err != nil {
		return "", err
	}
	valueRaw, ok := soleValue(object, key)
	if !ok {
		return "", fmt.Errorf("must contain only %s", key)
	}
	var value string
	if err := json.Unmarshal(valueRaw, &value); err != nil {
		return "", fmt.Errorf("%s is not a string", key)
	}
	return value, nil
}

func soleValue(object map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	return object[key], hasExactlyKeys(object, key)
}

func forbidden(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrForbidden, fmt.Sprintf(format, args...))
}
