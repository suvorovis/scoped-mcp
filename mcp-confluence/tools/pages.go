package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/guard"
)

type GetPageInput struct {
	PageID string `json:"page_id" jsonschema:"Numeric id of the page"`
}

type PageOutput struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Version  int    `json:"version"`
	ParentID string `json:"parent_id,omitempty"`
	WebURL   string `json:"webui"`
	Body     string `json:"body"`
}

type PageSummaryOutput struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ListChildrenInput struct {
	PageID string `json:"page_id" jsonschema:"Numeric id of the parent page"`
	Start  int    `json:"start,omitempty" jsonschema:"Index of the first child to return, default 0"`
	Limit  int    `json:"limit,omitempty" jsonschema:"How many children to return; the range and the default are in the tool description"`
}

type ListChildrenOutput struct {
	Children []PageSummaryOutput `json:"children"`
	HasMore  bool                `json:"has_more"`
}

type SearchInput struct {
	Query string `json:"query" jsonschema:"Text to search for in titles, bodies and labels of pages inside the allowed subtrees"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of results; default and upper bound come from the config"`
}

type SearchOutput struct {
	Results []PageSummaryOutput `json:"results"`
}

type CreatePageInput struct {
	ParentID string `json:"parent_id" jsonschema:"Numeric id of the parent page; the new page is created under it"`
	Title    string `json:"title" jsonschema:"Title, unique within the space"`
	Body     string `json:"body" jsonschema:"Page body in Confluence storage format (XHTML)"`
}

type UpdatePageInput struct {
	PageID  string `json:"page_id" jsonschema:"Numeric id of the page"`
	Version int    `json:"version" jsonschema:"Version number returned by get_page; the update fails if the page changed since"`
	Title   string `json:"title" jsonschema:"New title"`
	Body    string `json:"body" jsonschema:"New body in Confluence storage format (XHTML); replaces the whole body"`
}

type WrittenPageOutput struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Version int    `json:"version"`
	WebURL  string `json:"webui"`
}

func (t *Toolset) registerGetPage(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        GetPageToolName,
		Description: "Read a Confluence page (title, version, body in storage format, parent id, link) from the allowed subtrees in " + t.cfg.BaseURL + ". Pages outside the subtrees look like they do not exist.",
	}, t.GetPage)
}

func (t *Toolset) GetPage(ctx context.Context, input GetPageInput) (PageOutput, error) {
	if err := checkContentID("page_id", input.PageID); err != nil {
		return PageOutput{}, err
	}
	page, err := t.pages.GetPage(ctx, input.PageID)
	if err != nil {
		return PageOutput{}, err
	}
	parentInZone, err := t.isInReadZone(ctx, page.ParentID)
	if err != nil {
		return PageOutput{}, err
	}
	output := PageOutput{ID: page.ID, Title: page.Title, Version: page.Version, WebURL: page.WebURL, Body: page.Body}
	if parentInZone {
		output.ParentID = page.ParentID
	}
	return output, nil
}

func (t *Toolset) isInReadZone(ctx context.Context, pageID string) (bool, error) {
	if pageID == "" {
		return false, nil
	}
	if slices.Contains(t.readZoneRoots, pageID) {
		return true, nil
	}
	return t.isReadable(ctx, pageID)
}

func (t *Toolset) isReadable(ctx context.Context, pageID string) (bool, error) {
	err := t.pages.CheckPage(ctx, pageID)
	if errors.Is(err, confluence.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (t *Toolset) registerListChildren(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        ListChildrenToolName,
		Description: fmt.Sprintf("List direct child pages (id and title) of a page from the allowed subtrees. Paginated: pass start to continue while has_more is true; limit is 1..%d, default %d.", confluence.MaxListLimit, defaultListLimit),
	}, t.ListChildren)
}

func (t *Toolset) ListChildren(ctx context.Context, input ListChildrenInput) (ListChildrenOutput, error) {
	if err := checkContentID("page_id", input.PageID); err != nil {
		return ListChildrenOutput{}, err
	}
	if err := checkStart(input.Start); err != nil {
		return ListChildrenOutput{}, err
	}
	limit, err := checkLimit(input.Limit, defaultListLimit, confluence.MaxListLimit)
	if err != nil {
		return ListChildrenOutput{}, err
	}
	batch, err := t.pages.ListChildren(ctx, input.PageID, input.Start, limit)
	if err != nil {
		return ListChildrenOutput{}, err
	}
	return ListChildrenOutput{Children: summariesOutput(batch.Children), HasMore: batch.HasMore}, nil
}

func summariesOutput(summaries []confluence.PageSummary) []PageSummaryOutput {
	output := make([]PageSummaryOutput, 0, len(summaries))
	for _, summary := range summaries {
		output = append(output, summaryOutput(summary))
	}
	return output
}

func summaryOutput(summary confluence.PageSummary) PageSummaryOutput {
	return PageSummaryOutput{ID: summary.ID, Title: summary.Title}
}

func (t *Toolset) registerSearch(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        SearchToolName,
		Description: fmt.Sprintf("Full-text search for pages (id and title) inside the allowed subtrees. At most %d results.", t.cfg.Search.MaxResults),
	}, t.Search)
}

func (t *Toolset) Search(ctx context.Context, input SearchInput) (SearchOutput, error) {
	maxResults := t.cfg.Search.MaxResults
	limit, err := checkLimit(input.Limit, maxResults, maxResults)
	if err != nil {
		return SearchOutput{}, err
	}
	cql, err := guard.SearchCQL(t.readZoneRoots, input.Query)
	if err != nil {
		return SearchOutput{}, fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	}
	found, err := t.pages.Search(ctx, cql, limit)
	if err != nil {
		return SearchOutput{}, err
	}
	results, err := t.summariesInZone(ctx, found)
	if err != nil {
		return SearchOutput{}, err
	}
	return SearchOutput{Results: results}, nil
}

func (t *Toolset) summariesInZone(ctx context.Context, found []confluence.PageSummary) ([]PageSummaryOutput, error) {
	inZone := make([]PageSummaryOutput, 0, len(found))
	for _, summary := range found {
		if !guard.IsContentID(summary.ID) {
			continue
		}
		readable, err := t.isReadable(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		if readable {
			inZone = append(inZone, summaryOutput(summary))
		}
	}
	return inZone, nil
}

func (t *Toolset) registerCreatePage(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        CreatePageToolName,
		Description: "Create a page under a parent page from the allowed writable subtrees. The title must be unique within the space.",
	}, t.CreatePage)
}

func (t *Toolset) CreatePage(ctx context.Context, input CreatePageInput) (WrittenPageOutput, error) {
	if err := checkContentID("parent_id", input.ParentID); err != nil {
		return WrittenPageOutput{}, err
	}
	if err := checkTitle(input.Title); err != nil {
		return WrittenPageOutput{}, err
	}
	if err := checkBody(input.Body); err != nil {
		return WrittenPageOutput{}, err
	}
	parent, err := t.pages.GetPage(ctx, input.ParentID)
	if err != nil {
		return WrittenPageOutput{}, err
	}
	created, err := t.pages.CreatePage(ctx, input.ParentID, parent.SpaceKey, confluence.PageDraft{Title: input.Title, Body: input.Body})
	if err != nil {
		return WrittenPageOutput{}, err
	}
	return writtenOutput(created), nil
}

func (t *Toolset) registerUpdatePage(server *mcp.Server) {
	addTool(server, &mcp.Tool{
		Name:        UpdatePageToolName,
		Description: "Replace the title and body of a page from the allowed writable subtrees. Pass the version returned by get_page; a stale version is rejected.",
	}, t.UpdatePage)
}

func (t *Toolset) UpdatePage(ctx context.Context, input UpdatePageInput) (WrittenPageOutput, error) {
	if err := checkContentID("page_id", input.PageID); err != nil {
		return WrittenPageOutput{}, err
	}
	if err := checkVersion(input.Version); err != nil {
		return WrittenPageOutput{}, err
	}
	if err := checkTitle(input.Title); err != nil {
		return WrittenPageOutput{}, err
	}
	if err := checkBody(input.Body); err != nil {
		return WrittenPageOutput{}, err
	}
	nextVersion := input.Version + 1
	updated, err := t.pages.UpdatePage(ctx, input.PageID, nextVersion, confluence.PageDraft{Title: input.Title, Body: input.Body})
	if err != nil {
		return WrittenPageOutput{}, err
	}
	return writtenOutput(updated), nil
}

func writtenOutput(page confluence.WrittenPage) WrittenPageOutput {
	return WrittenPageOutput{ID: page.ID, Title: page.Title, Version: page.Version, WebURL: page.WebURL}
}
