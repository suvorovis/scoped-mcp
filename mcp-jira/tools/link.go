package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-jira/jira"
)

const LinkIssuesToolName = "link_issues"

type LinkIssuesInput struct {
	IssueKey  string `json:"issue_key"`
	Relation  string `json:"relation"`
	TargetKey string `json:"target_key"`
}

type LinkIssuesOutput struct {
	IssueKey  string `json:"issue_key"`
	Relation  string `json:"relation"`
	TargetKey string `json:"target_key"`
	URL       string `json:"url"`
}

func (t *Toolset) registerLinkIssues(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        LinkIssuesToolName,
		Description: LinkIssuesDescription(t.cfg),
		InputSchema: LinkIssuesInputSchema(t.cfg),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input LinkIssuesInput) (*mcp.CallToolResult, LinkIssuesOutput, error) {
		out, err := t.LinkIssues(ctx, input)
		return nil, out, err
	})
}

func (t *Toolset) LinkIssues(ctx context.Context, input LinkIssuesInput) (LinkIssuesOutput, error) {
	if err := t.checkIssueKey(paramIssueKey, input.IssueKey); err != nil {
		return LinkIssuesOutput{}, err
	}
	if err := t.checkIssueKey(paramTargetKey, input.TargetKey); err != nil {
		return LinkIssuesOutput{}, err
	}
	link, ok := t.cfg.FindIssueLink(input.Relation)
	if !ok {
		return LinkIssuesOutput{}, fmt.Errorf("%w: %q", ErrRelationNotAllowed, input.Relation)
	}
	if input.IssueKey == input.TargetKey {
		return LinkIssuesOutput{}, fmt.Errorf("%w: %s", ErrLinkToItself, input.IssueKey)
	}
	request := jira.LinkRequest{TypeName: link.Type, IssueKey: input.IssueKey, TargetKey: input.TargetKey, Direction: link.Direction}
	if err := t.linker.LinkIssues(ctx, request); err != nil {
		return LinkIssuesOutput{}, err
	}
	return LinkIssuesOutput{
		IssueKey:  input.IssueKey,
		Relation:  input.Relation,
		TargetKey: input.TargetKey,
		URL:       jira.IssueURL(t.cfg.BaseURL, input.IssueKey),
	}, nil
}
