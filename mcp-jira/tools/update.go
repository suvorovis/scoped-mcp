package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/jira"
)

const UpdateIssueToolName = "update_issue"

type UpdateIssueOutput struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

func (t *Toolset) registerUpdateIssue(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        UpdateIssueToolName,
		Description: UpdateIssueDescription(t.cfg),
		InputSchema: UpdateIssueInputSchema(t.cfg),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, UpdateIssueOutput, error) {
		out, err := t.UpdateIssue(ctx, args)
		return nil, out, err
	})
}

func (t *Toolset) UpdateIssue(ctx context.Context, args map[string]any) (UpdateIssueOutput, error) {
	request, err := t.buildUpdateRequest(args)
	if err != nil {
		return UpdateIssueOutput{}, err
	}
	if err := t.updater.UpdateIssue(ctx, request); err != nil {
		return UpdateIssueOutput{}, err
	}
	return UpdateIssueOutput{Key: request.IssueKey, URL: jira.IssueURL(t.cfg.BaseURL, request.IssueKey)}, nil
}

func (t *Toolset) buildUpdateRequest(args map[string]any) (jira.UpdateRequest, error) {
	issueKey, err := baseParam(args, paramIssueKey)
	if err != nil {
		return jira.UpdateRequest{}, err
	}
	project, err := t.allowedIssueProject(paramIssueKey, issueKey)
	if err != nil {
		return jira.UpdateRequest{}, err
	}
	if key, found := firstDisallowedField(args, []string{paramIssueKey}, project.UpdateFields); found {
		return jira.UpdateRequest{}, fmt.Errorf("%w: %s: not in update_fields of project %s", ErrFieldNotAllowed, key, project.Key)
	}
	valuesByJiraID, clearedJiraIDs, err := t.parseUpdateFields(project, args)
	if err != nil {
		return jira.UpdateRequest{}, err
	}
	if len(valuesByJiraID) == 0 && len(clearedJiraIDs) == 0 {
		return jira.UpdateRequest{}, fmt.Errorf("%w: %s", ErrNoFieldsToUpdate, issueKey)
	}
	return jira.UpdateRequest{IssueKey: issueKey, ValuesByJiraID: valuesByJiraID, ClearedJiraIDs: clearedJiraIDs}, nil
}

func (t *Toolset) parseUpdateFields(project config.Project, args map[string]any) (valuesByJiraID map[string]jira.FieldValue, clearedJiraIDs []string, err error) {
	var keysToSet []string
	for _, key := range project.UpdateFields {
		raw, present := args[key]
		if !present {
			continue
		}
		if raw == nil {
			clearedJiraIDs = append(clearedJiraIDs, t.cfg.Fields[key].Jira)
			continue
		}
		keysToSet = append(keysToSet, key)
	}
	valuesByJiraID, err = t.parseFieldValues(keysToSet, args)
	if err != nil {
		return nil, nil, err
	}
	return valuesByJiraID, clearedJiraIDs, nil
}
