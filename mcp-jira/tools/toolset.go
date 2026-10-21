package tools

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/jira"
)

var (
	ErrProjectNotAllowed   = errors.New("tools: project is not allowed by the config")
	ErrIssueTypeNotAllowed = errors.New("tools: issue type is not allowed for this project")
	ErrFieldNotAllowed     = errors.New("tools: field is not allowed by the config")
	ErrFieldRequired       = errors.New("tools: required field is empty")
	ErrFieldValue          = errors.New("tools: field value has wrong shape for its kind")
	ErrIssueKeyInvalid     = errors.New("tools: issue key must look like PROJ-123 and belong to an allowed project")
	ErrRelationNotAllowed  = errors.New("tools: relation is not allowed by the config")
	ErrLinkToItself        = errors.New("tools: issue_key and target_key must be different issues")
	ErrNoFieldsToUpdate    = errors.New("tools: update_issue needs at least one field besides issue_key")
)

const CreateIssueToolName = "create_issue"

type CreateIssueOutput struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

type Toolset struct {
	creator    jira.Creator
	attacher   jira.Attacher
	commenter  jira.Commenter
	linker     jira.Linker
	updater    jira.Updater
	cfg        config.Config
	configPath string
}

type Clients struct {
	Creator   jira.Creator
	Attacher  jira.Attacher
	Commenter jira.Commenter
	Linker    jira.Linker
	Updater   jira.Updater
}

func New(clients Clients, cfg config.Config, configPath string) *Toolset {
	return &Toolset{
		creator:    clients.Creator,
		attacher:   clients.Attacher,
		commenter:  clients.Commenter,
		linker:     clients.Linker,
		updater:    clients.Updater,
		cfg:        cfg,
		configPath: configPath,
	}
}

func (t *Toolset) Register(server *mcp.Server) {
	if t.cfg.Tools.CreateIssue {
		t.registerCreateIssue(server)
	}
	if t.cfg.Tools.AttachFiles {
		t.registerAttachFiles(server)
	}
	if t.cfg.Tools.AddComment {
		t.registerAddComment(server)
	}
	if t.cfg.Tools.LinkIssues {
		t.registerLinkIssues(server)
	}
	if t.cfg.Tools.UpdateIssue {
		t.registerUpdateIssue(server)
	}
}

func (t *Toolset) registerCreateIssue(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        CreateIssueToolName,
		Description: CreateIssueDescription(t.cfg),
		InputSchema: CreateIssueInputSchema(t.cfg),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, CreateIssueOutput, error) {
		out, err := t.CreateIssue(ctx, args)
		return nil, out, err
	})
}

func (t *Toolset) CreateIssue(ctx context.Context, args map[string]any) (CreateIssueOutput, error) {
	request, err := t.buildCreateRequest(args)
	if err != nil {
		return CreateIssueOutput{}, err
	}
	issue, err := t.creator.CreateIssue(ctx, request)
	if err != nil {
		return CreateIssueOutput{}, err
	}
	return CreateIssueOutput{Key: issue.Key, URL: issue.URL}, nil
}

func (t *Toolset) buildCreateRequest(args map[string]any) (jira.CreateRequest, error) {
	projectKey, err := baseParam(args, paramProject)
	if err != nil {
		return jira.CreateRequest{}, err
	}
	project, ok := t.cfg.FindProject(projectKey)
	if !ok || !project.HasIssueTypes() {
		return jira.CreateRequest{}, fmt.Errorf("%w: %q", ErrProjectNotAllowed, projectKey)
	}
	issueTypeName, err := baseParam(args, paramIssueType)
	if err != nil {
		return jira.CreateRequest{}, err
	}
	issueType, ok := project.FindIssueType(issueTypeName)
	if !ok {
		return jira.CreateRequest{}, fmt.Errorf("%w: %q in %s", ErrIssueTypeNotAllowed, issueTypeName, project.Key)
	}
	summary, err := baseParam(args, paramSummary)
	if err != nil {
		return jira.CreateRequest{}, err
	}
	if isBlank(summary) {
		return jira.CreateRequest{}, fmt.Errorf("%w: %s", ErrFieldRequired, paramSummary)
	}
	if key, found := firstDisallowedField(args, baseParams, issueType.Fields); found {
		return jira.CreateRequest{}, fmt.Errorf("%w: %s for issue type %s", ErrFieldNotAllowed, key, issueType.Name)
	}
	valuesByJiraID, err := t.parseFieldValues(issueType.Fields, args)
	if err != nil {
		return jira.CreateRequest{}, err
	}
	if err := t.checkRequiredFields(issueType, valuesByJiraID); err != nil {
		return jira.CreateRequest{}, err
	}
	return jira.CreateRequest{
		ProjectKey:   project.Key,
		IssueTypeID:  issueType.ID,
		Summary:      summary,
		Fields:       valuesByJiraID,
		AssignToSelf: project.AssignToSelf,
	}, nil
}

func baseParam(args map[string]any, name string) (string, error) {
	raw, present := args[name]
	if !present {
		return "", fmt.Errorf("%w: %s", ErrFieldRequired, name)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s expects a string", ErrFieldValue, name)
	}
	return value, nil
}

func firstDisallowedField(args map[string]any, toolParams, allowedKeys []string) (string, bool) {
	for _, key := range slices.Sorted(maps.Keys(args)) {
		if !slices.Contains(toolParams, key) && !slices.Contains(allowedKeys, key) {
			return key, true
		}
	}
	return "", false
}

func (t *Toolset) parseFieldValues(keys []string, args map[string]any) (map[string]jira.FieldValue, error) {
	var valuesByJiraID map[string]jira.FieldValue
	for _, key := range keys {
		raw, present := args[key]
		if !present {
			continue
		}
		field := t.cfg.Fields[key]
		value, err := t.fieldValue(key, field, raw)
		if err != nil {
			return nil, err
		}
		if valuesByJiraID == nil {
			valuesByJiraID = make(map[string]jira.FieldValue)
		}
		valuesByJiraID[field.Jira] = value
	}
	return valuesByJiraID, nil
}

func (t *Toolset) checkRequiredFields(issueType config.IssueType, valuesByJiraID map[string]jira.FieldValue) error {
	for _, key := range issueType.Fields {
		field := t.cfg.Fields[key]
		if field.Required && isEmpty(valuesByJiraID[field.Jira]) {
			return fmt.Errorf("%w: %s", ErrFieldRequired, key)
		}
	}
	return nil
}

func (t *Toolset) fieldValue(key string, field config.Field, raw any) (jira.FieldValue, error) {
	if field.Kind.IsList() {
		items, ok := stringItems(raw)
		if !ok {
			return jira.FieldValue{}, fmt.Errorf("%w: %s expects an array of strings", ErrFieldValue, key)
		}
		return jira.FieldValue{Kind: field.Kind, Items: items}, nil
	}
	text, ok := raw.(string)
	if !ok {
		return jira.FieldValue{}, fmt.Errorf("%w: %s expects a string", ErrFieldValue, key)
	}
	if field.Kind == config.KindIssueKey {
		if err := t.checkIssueKey(key, text); err != nil {
			return jira.FieldValue{}, err
		}
	}
	return jira.FieldValue{Kind: field.Kind, Text: text}, nil
}

func stringItems(raw any) ([]string, bool) {
	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	items := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		items = append(items, text)
	}
	return items, true
}

func isEmpty(value jira.FieldValue) bool {
	if value.Kind.IsList() {
		return allBlank(value.Items)
	}
	return isBlank(value.Text)
}

func allBlank(items []string) bool {
	for _, item := range items {
		if !isBlank(item) {
			return false
		}
	}
	return true
}

func isBlank(text string) bool {
	return strings.TrimSpace(text) == ""
}

func (t *Toolset) checkIssueKey(param, issueKey string) error {
	_, err := t.allowedIssueProject(param, issueKey)
	return err
}

func (t *Toolset) allowedIssueProject(param, issueKey string) (config.Project, error) {
	project, ok := t.configuredProjectOf(issueKey)
	if !ok {
		return config.Project{}, fmt.Errorf("%w: %s=%q", ErrIssueKeyInvalid, param, issueKey)
	}
	return project, nil
}

func (t *Toolset) configuredProjectOf(issueKey string) (config.Project, bool) {
	projectKey, ok := config.IssueKeyProject(issueKey)
	if !ok {
		return config.Project{}, false
	}
	return t.cfg.FindProject(projectKey)
}
