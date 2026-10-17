package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/guard"
)

const (
	GetPageToolName          = "get_page"
	ListChildrenToolName     = "list_children"
	SearchToolName           = "search"
	ListCommentsToolName     = "list_comments"
	ListAttachmentsToolName  = "list_attachments"
	GetAttachmentToolName    = "get_attachment"
	CreatePageToolName       = "create_page"
	UpdatePageToolName       = "update_page"
	UploadAttachmentToolName = "upload_attachment"

	defaultListLimit = 25
	maxReadVersion   = math.MaxInt32 - 1

	pageEnvelopeBytes       = 64 << 10
	maxEncodedPageBodyBytes = guard.MaxBodyBytes - pageEnvelopeBytes
)

var (
	ErrInvalidContentID    = errors.New("tools: ids must be positive decimal integers of at most 19 digits without leading zeros")
	ErrInvalidAttachmentID = errors.New("tools: attachment_id must be an id from list_attachments: a positive decimal integer of at most 19 digits without leading zeros, optionally prefixed with att")
	ErrInvalidTitle        = errors.New("tools: title must be a non-blank string")
	ErrInvalidVersion      = errors.New("tools: version must be the positive version number read from the page")
	ErrInvalidQuery        = errors.New("tools: query must be non-empty and must not contain control characters")
	ErrInvalidLimit        = errors.New("tools: limit is out of range")
	ErrInvalidStart        = errors.New("tools: start must be non-negative")
	ErrInvalidBody         = errors.New("tools: body is too large for one request")
)

type Toolset struct {
	pages         confluence.Pages
	comments      confluence.Comments
	attachments   confluence.Attachments
	cfg           config.Config
	configPath    string
	readZoneRoots []string
}

func New(pages confluence.Pages, comments confluence.Comments, attachments confluence.Attachments, cfg config.Config, configPath string) *Toolset {
	return &Toolset{pages: pages, comments: comments, attachments: attachments, cfg: cfg, configPath: configPath, readZoneRoots: cfg.ReadZoneRoots()}
}

func (t *Toolset) Register(server *mcp.Server) {
	if t.cfg.Tools.GetPage {
		t.registerGetPage(server)
	}
	if t.cfg.Tools.ListChildren {
		t.registerListChildren(server)
	}
	if t.cfg.Tools.Search {
		t.registerSearch(server)
	}
	if t.cfg.Tools.ListComments {
		t.registerListComments(server)
	}
	if t.cfg.Tools.ListAttachments {
		t.registerListAttachments(server)
	}
	if t.cfg.Tools.GetAttachment {
		t.registerGetAttachment(server)
	}
	if t.cfg.Tools.CreatePage {
		t.registerCreatePage(server)
	}
	if t.cfg.Tools.UpdatePage {
		t.registerUpdatePage(server)
	}
	if t.cfg.Tools.UploadAttachment {
		t.registerUploadAttachment(server)
	}
}

func addTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handle(ctx, input)
		return nil, output, err
	})
}

func checkContentID(field, value string) error {
	if !guard.IsContentID(value) {
		return fmt.Errorf("%w: %s %q", ErrInvalidContentID, field, value)
	}
	return nil
}

func checkAttachmentID(value string) (string, error) {
	attachmentContentID, ok := guard.AttachmentContentID(value)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrInvalidAttachmentID, value)
	}
	return attachmentContentID, nil
}

func checkStart(start int) error {
	if start < 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidStart, start)
	}
	return nil
}

func checkLimit(limit, defaultLimit, maxLimit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 1 || limit > maxLimit {
		return 0, fmt.Errorf("%w: limit %d, allowed 1..%d", ErrInvalidLimit, limit, maxLimit)
	}
	return limit, nil
}

func checkTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return ErrInvalidTitle
	}
	return nil
}

func checkVersion(version int) error {
	if version < 1 || version > maxReadVersion {
		return fmt.Errorf("%w: got %d", ErrInvalidVersion, version)
	}
	return nil
}

func checkBody(body string) error {
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidBody, err)
	}
	if len(encodedBody) > maxEncodedPageBodyBytes {
		return fmt.Errorf("%w: %d bytes as JSON, allowed %d", ErrInvalidBody, len(encodedBody), maxEncodedPageBodyBytes)
	}
	return nil
}
