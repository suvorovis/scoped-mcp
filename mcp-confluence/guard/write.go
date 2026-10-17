package guard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

const (
	typeField           = "type"
	titleField          = "title"
	spaceField          = "space"
	keyField            = "key"
	ancestorsField      = "ancestors"
	idField             = "id"
	bodyField           = "body"
	storageField        = "storage"
	valueField          = "value"
	representationField = "representation"
	versionField        = "version"
	numberField         = "number"

	storageRepresentation = "storage"
)

func (t *Transport) authorizeCreatePage(req *http.Request) (*http.Request, error) {
	if err := checkNoQuery(req.URL); err != nil {
		return nil, err
	}
	if err := checkHeadersAbsent(req.Header, atlassianTokenHeader); err != nil {
		return nil, err
	}
	requestBody, err := readRequestBody(req, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	parentID, spaceKey, err := checkCreateBody(requestBody)
	if err != nil {
		return nil, forbidden("create page: %v", err)
	}
	parent, err := t.requirePageInZone(req, parentID, t.writeZone)
	if err != nil {
		return nil, err
	}
	if parent.SpaceKey == "" {
		return nil, forbidden("create page: the space of the parent %s is unknown", parentID)
	}
	if spaceKey != parent.SpaceKey {
		return nil, forbidden("create page: space %q is not the space of the parent", spaceKey)
	}
	return withJSONBody(req, requestBody), nil
}

func checkCreateBody(requestBody []byte) (parentID, spaceKey string, err error) {
	object, err := decodeObject(requestBody)
	if err != nil {
		return "", "", fmt.Errorf("request body: %w", err)
	}
	if err := exactKeys(object, typeField, titleField, spaceField, ancestorsField, bodyField); err != nil {
		return "", "", err
	}
	if err := checkPageTypeAndTitle(object); err != nil {
		return "", "", err
	}
	spaceKey, err = soleStringField(object[spaceField], keyField)
	if err != nil {
		return "", "", fmt.Errorf("space: %w", err)
	}
	parentID, err = soleAncestorID(object[ancestorsField])
	if err != nil {
		return "", "", fmt.Errorf("ancestors: %w", err)
	}
	if err := checkPageStorageBody(object[bodyField]); err != nil {
		return "", "", fmt.Errorf("body: %w", err)
	}
	return parentID, spaceKey, nil
}

func (t *Transport) authorizeUpdatePage(req *http.Request, pageID string) (*http.Request, error) {
	if err := checkNoQuery(req.URL); err != nil {
		return nil, err
	}
	if err := checkHeadersAbsent(req.Header, atlassianTokenHeader); err != nil {
		return nil, err
	}
	requestBody, err := readRequestBody(req, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	if err := checkUpdateBody(requestBody, pageID); err != nil {
		return nil, forbidden("update page: %v", err)
	}
	if _, err := t.requirePageInZone(req, pageID, t.writeZone); err != nil {
		return nil, err
	}
	return withJSONBody(req, requestBody), nil
}

func checkUpdateBody(requestBody []byte, pageID string) error {
	object, err := decodeObject(requestBody)
	if err != nil {
		return fmt.Errorf("request body: %w", err)
	}
	if err := exactKeys(object, idField, typeField, titleField, versionField, bodyField); err != nil {
		return err
	}
	bodyID, err := stringValue(object[idField])
	if err != nil || bodyID != pageID {
		return fmt.Errorf("id must be the string %s", pageID)
	}
	if err := checkPageTypeAndTitle(object); err != nil {
		return err
	}
	if err := checkVersion(object[versionField]); err != nil {
		return fmt.Errorf("version: %w", err)
	}
	if err := checkPageStorageBody(object[bodyField]); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	return nil
}

func checkPageTypeAndTitle(object map[string]json.RawMessage) error {
	contentType, err := stringValue(object[typeField])
	if err != nil || contentType != typePage {
		return fmt.Errorf("type must be %q", typePage)
	}
	title, err := stringValue(object[titleField])
	if err != nil || strings.TrimSpace(title) == "" {
		return errors.New("title must be a non-blank string")
	}
	return nil
}

func soleStringField(raw json.RawMessage, key string) (string, error) {
	object, err := decodeObject(raw)
	if err != nil {
		return "", err
	}
	if err := exactKeys(object, key); err != nil {
		return "", err
	}
	return stringValue(object[key])
}

func soleAncestorID(raw json.RawMessage) (string, error) {
	var ancestors []json.RawMessage
	if err := json.Unmarshal(raw, &ancestors); err != nil {
		return "", errors.New("not an array")
	}
	if len(ancestors) != 1 {
		return "", fmt.Errorf("exactly one ancestor is required, got %d", len(ancestors))
	}
	parentID, err := soleStringField(ancestors[0], idField)
	if err != nil {
		return "", err
	}
	if !IsContentID(parentID) {
		return "", fmt.Errorf("id %q is not a content id", parentID)
	}
	return parentID, nil
}

func checkVersion(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if err := exactKeys(object, numberField); err != nil {
		return err
	}
	if !isPositiveNumber(string(object[numberField])) {
		return errors.New("number must be a positive integer")
	}
	return nil
}

func checkPageStorageBody(raw json.RawMessage) error {
	pageBody, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if err := exactKeys(pageBody, storageField); err != nil {
		return err
	}
	storage, err := decodeObject(pageBody[storageField])
	if err != nil {
		return err
	}
	if err := exactKeys(storage, valueField, representationField); err != nil {
		return err
	}
	if _, err := stringValue(storage[valueField]); err != nil {
		return fmt.Errorf("storage.value: %w", err)
	}
	representation, err := stringValue(storage[representationField])
	if err != nil || representation != storageRepresentation {
		return fmt.Errorf("storage.representation must be %q", storageRepresentation)
	}
	return nil
}

func checkNoQuery(requestURL *url.URL) error {
	if requestURL.RawQuery != "" || requestURL.ForceQuery {
		return forbidden("query is not allowed")
	}
	return nil
}

func readRequestBody(req *http.Request, limit int64) ([]byte, error) {
	if !hasBody(req) {
		return nil, forbidden("%s %s: body is required", req.Method, req.URL.EscapedPath())
	}
	requestBody, err := readLimited(req.Body, limit)
	if err != nil {
		return nil, forbidden("request body: %v", err)
	}
	return requestBody, nil
}

func withJSONBody(req *http.Request, requestBody []byte) *http.Request {
	return withCanonicalBody(req, "application/json", requestBody)
}

func withCanonicalBody(req *http.Request, contentType string, requestBody []byte) *http.Request {
	allowedRequest := req.Clone(req.Context())
	allowedRequest.Header.Set(contentTypeHeader, contentType)
	allowedRequest.Body = io.NopCloser(bytes.NewReader(requestBody))
	allowedRequest.ContentLength = int64(len(requestBody))
	allowedRequest.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(requestBody)), nil }
	return allowedRequest
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
	if err := requireEndOfInput(decoder); err != nil {
		return nil, err
	}
	return object, nil
}

func requireEndOfInput(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after JSON object")
	}
	return nil
}

func exactKeys(object map[string]json.RawMessage, keys ...string) error {
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("missing key %s", key)
		}
	}
	for key := range object {
		if !slices.Contains(keys, key) {
			return fmt.Errorf("unexpected key %s", key)
		}
	}
	return nil
}

func stringValue(raw json.RawMessage) (string, error) {
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return "", errors.New("not a string")
	}
	return *value, nil
}
