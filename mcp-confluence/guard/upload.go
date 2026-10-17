package guard

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
)

const (
	attachmentField       = "file"
	attachmentsMediaType  = "multipart/form-data"
	atlassianTokenHeader  = "X-Atlassian-Token"
	atlassianTokenNoCheck = "no-check"
)

func (t *Transport) authorizeUploadAttachment(req *http.Request, pageID string) (*http.Request, error) {
	contentType, requestBody, err := t.checkUploadToPage(req, pageID, "upload attachment")
	if err != nil {
		return nil, err
	}
	return withCanonicalBody(req, contentType, requestBody), nil
}

func (t *Transport) authorizeUploadAttachmentVersion(req *http.Request, pageID, attachmentID string) (*http.Request, error) {
	contentType, requestBody, err := t.checkUploadToPage(req, pageID, "upload attachment version")
	if err != nil {
		return nil, err
	}
	if err := t.requireAttachmentOfPage(req, attachmentID, pageID); err != nil {
		return nil, err
	}
	return withCanonicalBody(req, contentType, requestBody), nil
}

func (t *Transport) checkUploadToPage(req *http.Request, pageID, route string) (contentType string, requestBody []byte, err error) {
	if err := checkNoQuery(req.URL); err != nil {
		return "", nil, err
	}
	requestBody, err = readRequestBody(req, t.attachmentRules.MaxBytes+AttachmentEnvelopeBytes)
	if err != nil {
		return "", nil, err
	}
	contentType, err = t.checkUploadRequest(req.Header, requestBody)
	if err != nil {
		return "", nil, forbidden("%s: %v", route, err)
	}
	if _, err := t.requirePageInZone(req, pageID, t.writeZone); err != nil {
		return "", nil, err
	}
	return contentType, requestBody, nil
}

func (t *Transport) requireAttachmentOfPage(req *http.Request, attachmentID, pageID string) error {
	info, err := t.fetchContentInfo(req, attachmentID, sameAttachmentID)
	switch {
	case errors.Is(err, errContentNotVisible):
		return attachmentOutOfZone(attachmentID, "is not visible")
	case err != nil:
		return err
	case !isCurrentAttachment(info):
		return attachmentOutOfZone(attachmentID, "is not a current attachment")
	case !isAttachedToPage(info, pageID):
		return attachmentOutOfZone(attachmentID, "is not attached to page "+pageID)
	default:
		return nil
	}
}

func isCurrentAttachment(info contentInfo) bool {
	return info.Type == typeAttachment && info.Status == statusCurrent
}

func isAttachedToPage(info contentInfo, pageID string) bool {
	return info.ContainerType == typePage && info.ContainerID == pageID
}

func attachmentOutOfZone(attachmentID, reason string) error {
	return fmt.Errorf("%w: attachment %s %s", ErrOutOfZone, attachmentID, reason)
}

func (t *Transport) checkUploadRequest(header http.Header, requestBody []byte) (string, error) {
	if err := checkAtlassianToken(header); err != nil {
		return "", err
	}
	boundary, err := multipartBoundary(header.Values(contentTypeHeader))
	if err != nil {
		return "", err
	}
	return t.checkAttachmentBody(requestBody, boundary)
}

func checkAtlassianToken(header http.Header) error {
	if !slices.Equal(header.Values(atlassianTokenHeader), []string{atlassianTokenNoCheck}) {
		return fmt.Errorf("%s must be exactly %s", atlassianTokenHeader, atlassianTokenNoCheck)
	}
	return nil
}

func multipartBoundary(contentTypes []string) (string, error) {
	if len(contentTypes) != 1 {
		return "", errors.New("exactly one Content-Type is required")
	}
	mediaType, params, err := mime.ParseMediaType(contentTypes[0])
	if err != nil {
		return "", fmt.Errorf("Content-Type: %w", err)
	}
	boundary, ok := params["boundary"]
	if mediaType != attachmentsMediaType || !ok || len(params) != 1 {
		return "", fmt.Errorf("Content-Type must be %s with only a boundary", attachmentsMediaType)
	}
	return boundary, nil
}

func (t *Transport) checkAttachmentBody(requestBody []byte, boundary string) (string, error) {
	part, err := multipart.NewReader(bytes.NewReader(requestBody), boundary).NextRawPart()
	if err != nil {
		return "", fmt.Errorf("request body: %w", err)
	}
	fileName, err := t.allowedFileName(part)
	if err != nil {
		return "", err
	}
	content, err := readLimited(part, t.attachmentRules.MaxBytes)
	if err != nil {
		return "", fmt.Errorf("file content: %w", err)
	}
	contentType, canonicalBody, err := canonicalAttachmentBody(boundary, fileName, content)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(canonicalBody, requestBody) {
		return "", errors.New("request body must be exactly one file part without anything else")
	}
	return contentType, nil
}

func (t *Transport) allowedFileName(part *multipart.Part) (string, error) {
	disposition, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return "", fmt.Errorf("Content-Disposition: %w", err)
	}
	if disposition != "form-data" || params["name"] != attachmentField {
		return "", fmt.Errorf("part must be form-data named %s", attachmentField)
	}
	fileName := params["filename"]
	if !IsAttachmentFileName(fileName) {
		return "", fmt.Errorf(`file name %q must be non-empty, not . or .., without / \ ; " and control or format characters`, fileName)
	}
	if !t.attachmentRules.AllowsExtension(fileName) {
		return "", fmt.Errorf("extension of %q is not allowed", fileName)
	}
	return fileName, nil
}

func canonicalAttachmentBody(boundary, fileName string, content []byte) (contentType string, body []byte, err error) {
	var canonical bytes.Buffer
	writer := multipart.NewWriter(&canonical)
	if err := writer.SetBoundary(boundary); err != nil {
		return "", nil, fmt.Errorf("boundary: %w", err)
	}
	part, err := writer.CreateFormFile(attachmentField, fileName)
	if err != nil {
		return "", nil, fmt.Errorf("encode attachment: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return "", nil, fmt.Errorf("encode attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", nil, fmt.Errorf("encode attachment: %w", err)
	}
	return writer.FormDataContentType(), canonical.Bytes(), nil
}
