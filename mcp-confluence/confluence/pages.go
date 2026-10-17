package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	pageExpand  = "body.storage,version,ancestors,space"
	checkExpand = "version"

	typePage              = "page"
	representationStorage = "storage"

	titleTakenMessagePrefix = "A page with this title already exists"
	maxRejectionMessageSize = 1 << 20
)

type Page struct {
	ID          string
	Title       string
	Version     int
	ParentID    string
	AncestorIDs []string
	SpaceKey    string
	WebURL      string
	Body        string
}

type PageSummary struct {
	ID    string
	Title string
}

type ChildrenBatch struct {
	Children []PageSummary
	HasMore  bool
}

type PageDraft struct {
	Title string
	Body  string
}

type WrittenPage struct {
	ID      string
	Title   string
	Version int
	WebURL  string
}

type Pages interface {
	GetPage(ctx context.Context, pageID string) (Page, error)
	CheckPage(ctx context.Context, pageID string) error
	ListChildren(ctx context.Context, pageID string, start, limit int) (ChildrenBatch, error)
	Search(ctx context.Context, cql string, limit int) ([]PageSummary, error)
	CreatePage(ctx context.Context, parentID, spaceKey string, draft PageDraft) (WrittenPage, error)
	UpdatePage(ctx context.Context, pageID string, version int, draft PageDraft) (WrittenPage, error)
}

var _ Pages = (*Client)(nil)

type createPageBody struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Space     spaceRef     `json:"space"`
	Ancestors []contentRef `json:"ancestors"`
	Body      storageBody  `json:"body"`
}

type updatePageBody struct {
	ID      string        `json:"id"`
	Type    string        `json:"type"`
	Title   string        `json:"title"`
	Version versionNumber `json:"version"`
	Body    storageBody   `json:"body"`
}

func (c *Client) GetPage(ctx context.Context, pageID string) (Page, error) {
	var content contentResponse
	if err := c.getJSON(ctx, contentPath+"/"+pageID, url.Values{expandParam: {pageExpand}}, &content); err != nil {
		return Page{}, err
	}
	return c.pageOf(content), nil
}

func (c *Client) pageOf(content contentResponse) Page {
	page := Page{ID: content.ID, Title: content.Title, Version: content.Version.Number, SpaceKey: content.Space.Key, WebURL: c.webURL(content.Links.WebUI), Body: content.Body.Storage.Value}
	for _, ancestor := range content.Ancestors {
		page.AncestorIDs = append(page.AncestorIDs, ancestor.ID)
	}
	if len(page.AncestorIDs) > 0 {
		page.ParentID = page.AncestorIDs[len(page.AncestorIDs)-1]
	}
	return page
}

func (c *Client) CheckPage(ctx context.Context, pageID string) error {
	var content contentRef
	return c.getJSON(ctx, contentPath+"/"+pageID, url.Values{expandParam: {checkExpand}}, &content)
}

func (c *Client) ListChildren(ctx context.Context, pageID string, start, limit int) (ChildrenBatch, error) {
	var collection collectionResponse
	if err := c.getJSON(ctx, childPath(pageID, childPages), listQuery(start, limit), &collection); err != nil {
		return ChildrenBatch{}, err
	}
	return ChildrenBatch{Children: summariesOf(collection.Results), HasMore: collection.Links.Next != ""}, nil
}

func (c *Client) Search(ctx context.Context, cql string, limit int) ([]PageSummary, error) {
	query := limitQuery(limit)
	query.Set(cqlParam, cql)
	var collection collectionResponse
	if err := c.getJSON(ctx, searchPath, query, &collection); err != nil {
		return nil, err
	}
	return summariesOf(collection.Results), nil
}

func summariesOf(results []contentResponse) []PageSummary {
	summaries := make([]PageSummary, 0, len(results))
	for _, content := range results {
		summaries = append(summaries, PageSummary{ID: content.ID, Title: content.Title})
	}
	return summaries
}

func (c *Client) CreatePage(ctx context.Context, parentID, spaceKey string, draft PageDraft) (WrittenPage, error) {
	requestBody := createPageBody{Type: typePage, Title: draft.Title, Space: spaceRef{Key: spaceKey}, Ancestors: []contentRef{{ID: parentID}}, Body: storageBodyOf(draft)}
	return c.writePage(ctx, http.MethodPost, contentPath, requestBody)
}

func (c *Client) UpdatePage(ctx context.Context, pageID string, version int, draft PageDraft) (WrittenPage, error) {
	requestBody := updatePageBody{ID: pageID, Type: typePage, Title: draft.Title, Version: versionNumber{Number: version}, Body: storageBodyOf(draft)}
	return c.writePage(ctx, http.MethodPut, contentPath+"/"+pageID, requestBody)
}

func storageBodyOf(draft PageDraft) storageBody {
	return storageBody{Storage: storageValue{Value: draft.Body, Representation: representationStorage}}
}

func (c *Client) writePage(ctx context.Context, method, path string, requestBody any) (WrittenPage, error) {
	httpRequest, err := c.newJSONRequest(ctx, method, path, requestBody)
	if err != nil {
		return WrittenPage{}, err
	}
	response, err := c.send(httpRequest, writeRequest)
	if response != nil {
		defer closeResponseBody(response)
	}
	if errors.Is(err, ErrRejected) {
		return WrittenPage{}, pageWriteRejection(method, response)
	}
	if err != nil {
		return WrittenPage{}, err
	}
	var content contentResponse
	if err := c.decodeJSON(httpRequest, response.Body, &content, writeRequest); err != nil {
		return WrittenPage{}, err
	}
	if content.ID == "" {
		c.logf(httpRequest, "response without id")
		return WrittenPage{}, ErrMaybeWritten
	}
	return WrittenPage{ID: content.ID, Title: content.Title, Version: content.Version.Number, WebURL: c.webURL(content.Links.WebUI)}, nil
}

func pageWriteRejection(method string, response *http.Response) error {
	if method == http.MethodPut && response.StatusCode == http.StatusConflict {
		return ErrVersionConflict
	}
	if response.StatusCode != http.StatusBadRequest {
		return ErrRejected
	}
	if strings.HasPrefix(rejectionMessage(response.Body), titleTakenMessagePrefix) {
		return ErrTitleTaken
	}
	return ErrRejected
}

func rejectionMessage(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, maxRejectionMessageSize))
	if err != nil {
		return ""
	}
	var rejection struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &rejection) != nil {
		return ""
	}
	return rejection.Message
}
