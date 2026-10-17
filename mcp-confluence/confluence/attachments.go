package confluence

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"scoped-mcp/mcp-confluence/guard"
)

const (
	attachmentDataSuffix = "/data"
	attachmentListExpand = "version"

	versionParam = "version"
	apiParam     = "api"
	apiV2        = "v2"

	anyMediaType          = "*/*"
	attachmentField       = "file"
	atlassianTokenHeader  = "X-Atlassian-Token"
	atlassianTokenNoCheck = "no-check"
)

type Attachment struct {
	ID        string
	Title     string
	MediaType string
	Size      int64
	Version   int
}

type AttachmentFile struct {
	Name    string
	Content []byte
}

type Attachments interface {
	ListAttachments(ctx context.Context, pageID string) ([]Attachment, error)
	DownloadAttachment(ctx context.Context, pageID string, attachment Attachment, maxBytes int64) ([]byte, error)
	UploadAttachment(ctx context.Context, pageID string, file AttachmentFile) (Attachment, error)
}

var _ Attachments = (*Client)(nil)

func (c *Client) ListAttachments(ctx context.Context, pageID string) ([]Attachment, error) {
	attachments := []Attachment{}
	err := c.forEachListed(ctx, childPath(pageID, childAttachments), url.Values{expandParam: {attachmentListExpand}}, func(content contentResponse) {
		attachments = append(attachments, attachmentOf(content))
	})
	if err != nil {
		return nil, err
	}
	return attachments, nil
}

func attachmentDataPath(pageID, attachmentContentID string) string {
	return childPath(pageID, childAttachments) + "/" + attachmentContentID + attachmentDataSuffix
}

func attachmentOf(content contentResponse) Attachment {
	return Attachment{
		ID:        content.ID,
		Title:     content.Title,
		MediaType: content.Extensions.MediaType,
		Size:      content.Extensions.FileSize,
		Version:   content.Version.Number,
	}
}

func (c *Client) DownloadAttachment(ctx context.Context, pageID string, attachment Attachment, maxBytes int64) ([]byte, error) {
	query := url.Values{versionParam: {strconv.Itoa(attachment.Version)}, apiParam: {apiV2}}
	httpRequest, err := c.newRequest(ctx, http.MethodGet, downloadPath+"/"+pageID+"/"+attachment.Title, query, nil)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Accept", anyMediaType)
	response, err := c.send(httpRequest, readRequest)
	if response != nil {
		defer closeResponseBody(response)
	}
	if err != nil {
		return nil, err
	}
	return c.readLimited(httpRequest, response.Body, maxBytes, ErrUnavailable, ErrAttachmentTooLarge)
}

func (c *Client) UploadAttachment(ctx context.Context, pageID string, file AttachmentFile) (Attachment, error) {
	existing, err := c.ListAttachments(ctx, pageID)
	if err != nil {
		return Attachment{}, err
	}
	sameName, found := findAttachment(existing, file.Name)
	if !found {
		return c.uploadNew(ctx, childPath(pageID, childAttachments), file)
	}
	sameNameContentID, ok := guard.AttachmentContentID(sameName.ID)
	if !ok {
		c.logRoute(http.MethodGet, c.base.Path+childPath(pageID, childAttachments), "listed attachment has an unrecognized id")
		return Attachment{}, ErrUnavailable
	}
	return c.uploadVersion(ctx, attachmentDataPath(pageID, sameNameContentID), file)
}

func findAttachment(attachments []Attachment, title string) (Attachment, bool) {
	index := slices.IndexFunc(attachments, func(attachment Attachment) bool { return attachment.Title == title })
	if index < 0 {
		return Attachment{}, false
	}
	return attachments[index], true
}

func (c *Client) uploadNew(ctx context.Context, path string, file AttachmentFile) (Attachment, error) {
	httpRequest, err := c.newUploadRequest(ctx, path, file)
	if err != nil {
		return Attachment{}, err
	}
	var collection collectionResponse
	if err := c.doJSON(httpRequest, writeRequest, &collection); err != nil {
		return Attachment{}, err
	}
	if len(collection.Results) == 0 {
		return c.unconfirmedUpload(httpRequest)
	}
	return c.uploadedAttachment(httpRequest, collection.Results[0])
}

func (c *Client) uploadVersion(ctx context.Context, path string, file AttachmentFile) (Attachment, error) {
	httpRequest, err := c.newUploadRequest(ctx, path, file)
	if err != nil {
		return Attachment{}, err
	}
	var content contentResponse
	if err := c.doJSON(httpRequest, writeRequest, &content); err != nil {
		return Attachment{}, err
	}
	return c.uploadedAttachment(httpRequest, content)
}

func (c *Client) uploadedAttachment(httpRequest *http.Request, content contentResponse) (Attachment, error) {
	if content.ID == "" {
		return c.unconfirmedUpload(httpRequest)
	}
	return attachmentOf(content), nil
}

func (c *Client) unconfirmedUpload(httpRequest *http.Request) (Attachment, error) {
	c.logf(httpRequest, "response without attachment id")
	return Attachment{}, ErrMaybeWritten
}

func (c *Client) newUploadRequest(ctx context.Context, path string, file AttachmentFile) (*http.Request, error) {
	requestBody, contentType, err := multipartFile(file)
	if err != nil {
		return nil, err
	}
	httpRequest, err := c.newRequest(ctx, http.MethodPost, path, nil, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", contentType)
	httpRequest.Header.Set(atlassianTokenHeader, atlassianTokenNoCheck)
	return httpRequest, nil
}

func multipartFile(file AttachmentFile) (requestBody []byte, contentType string, err error) {
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	part, err := writer.CreateFormFile(attachmentField, file.Name)
	if err != nil {
		return nil, "", fmt.Errorf("confluence: encode attachment: %w", err)
	}
	if _, err := part.Write(file.Content); err != nil {
		return nil, "", fmt.Errorf("confluence: encode attachment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("confluence: encode attachment: %w", err)
	}
	return encoded.Bytes(), writer.FormDataContentType(), nil
}
