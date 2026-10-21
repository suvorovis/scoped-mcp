package tools

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"scoped-mcp/mcp-jira/config"
)

const (
	paramProject   = "project"
	paramIssueType = "issue_type"
	paramSummary   = "summary"
	paramIssueKey  = "issue_key"
	paramPaths     = "paths"
	paramBody      = "body"
	paramRelation  = "relation"
	paramTargetKey = "target_key"
)

const linkReading = paramIssueKey + " " + paramRelation + " " + paramTargetKey

var baseParams = []string{paramProject, paramIssueType, paramSummary}

func CreateIssueInputSchema(cfg config.Config) *jsonschema.Schema {
	summaryMinLength := 1
	properties := map[string]*jsonschema.Schema{
		paramProject:   {Type: "string", Enum: creatableProjectKeys(cfg), Description: "Key of the project to create the issue in"},
		paramIssueType: {Type: "string", Enum: issueTypeNames(cfg), Description: "Issue type name; must be allowed for the chosen project, see the tool description"},
		paramSummary:   {Type: "string", MinLength: &summaryMinLength, Description: "Issue summary (title)"},
	}
	for key, field := range cfg.Fields {
		properties[key] = fieldSchema(field)
	}
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             slices.Clone(baseParams),
		AdditionalProperties: noAdditionalProperties(),
	}
}

func noAdditionalProperties() *jsonschema.Schema {
	return &jsonschema.Schema{Not: &jsonschema.Schema{}}
}

func fieldSchema(field config.Field) *jsonschema.Schema {
	return &jsonschema.Schema{Type: fieldTypeName(field), Items: fieldItemsSchema(field), Description: fieldDescription(field)}
}

func clearableFieldSchema(field config.Field) *jsonschema.Schema {
	return &jsonschema.Schema{
		Types:       []string{fieldTypeName(field), "null"},
		Items:       fieldItemsSchema(field),
		Description: fieldDescription(field) + "; null clears the field",
	}
}

func fieldTypeName(field config.Field) string {
	if field.Kind.IsList() {
		return "array"
	}
	return "string"
}

func fieldItemsSchema(field config.Field) *jsonschema.Schema {
	if field.Kind.IsList() {
		return &jsonschema.Schema{Type: "string"}
	}
	return nil
}

func fieldDescription(field config.Field) string {
	return fmt.Sprintf("Jira field %s (%s)", field.Jira, field.Kind)
}

func creatableProjectKeys(cfg config.Config) []any {
	keys := make([]any, 0, len(cfg.Projects))
	for _, project := range projectsWithIssueTypes(cfg) {
		keys = append(keys, project.Key)
	}
	return keys
}

func issueTypeNames(cfg config.Config) []any {
	seen := make(map[string]struct{})
	names := make([]any, 0)
	for _, project := range cfg.Projects {
		for _, issueType := range project.IssueTypes {
			if _, dup := seen[issueType.Name]; dup {
				continue
			}
			seen[issueType.Name] = struct{}{}
			names = append(names, issueType.Name)
		}
	}
	return names
}

func CreateIssueDescription(cfg config.Config) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Create one Jira issue in %s. Only the projects and issue types listed below are allowed; each issue type accepts only its listed fields (* = required). %s is always required.\n", cfg.BaseURL, paramSummary)
	for _, project := range projectsWithIssueTypes(cfg) {
		fmt.Fprintf(&text, "Project %s%s:\n", project.Key, assignmentNote(project))
		for _, issueType := range project.IssueTypes {
			fmt.Fprintf(&text, "- %s: %s\n", issueType.Name, fieldList(cfg, issueType))
		}
	}
	text.WriteString(fieldValueShapes())
	return text.String()
}

func assignmentNote(project config.Project) string {
	if !project.AssignToSelf {
		return ""
	}
	return " (issues are assigned to the token owner, i.e. the user)"
}

func fieldValueShapes() string {
	var stringKinds, listKinds []string
	for _, kind := range config.Kinds() {
		if kind == config.KindIssueKey {
			continue
		}
		if kind.IsList() {
			listKinds = append(listKinds, string(kind))
		} else {
			stringKinds = append(stringKinds, string(kind))
		}
	}
	return fmt.Sprintf("Field values: %s — string; %s — array of strings; %s — key of an issue in an allowed project, e.g. PROJ-123.",
		strings.Join(stringKinds, ", "), strings.Join(listKinds, ", "), config.KindIssueKey)
}

func fieldList(cfg config.Config, issueType config.IssueType) string {
	if len(issueType.Fields) == 0 {
		return "no extra fields"
	}
	entries := make([]string, 0, len(issueType.Fields))
	for _, key := range issueType.Fields {
		field := cfg.Fields[key]
		marker := ""
		if field.Required {
			marker = "*"
		}
		entries = append(entries, fmt.Sprintf("%s%s (%s)", key, marker, field.Kind))
	}
	return strings.Join(entries, ", ")
}

func AttachFilesInputSchema() *jsonschema.Schema {
	pathMinLength := 1
	pathsMinItems := 1
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			paramIssueKey: issueKeySchema("Key of the issue to attach the files to, e.g. PROJ-123"),
			paramPaths: {
				Type:        "array",
				Items:       &jsonschema.Schema{Type: "string", MinLength: &pathMinLength},
				MinItems:    &pathsMinItems,
				Description: "Absolute or ~/ paths of the files to attach, uploaded in this order",
			},
		},
		Required:             []string{paramIssueKey, paramPaths},
		AdditionalProperties: noAdditionalProperties(),
	}
}

func AttachFilesDescription(cfg config.Config) string {
	attachments := cfg.Attachments
	var text strings.Builder
	fmt.Fprintf(&text, "Attach local files to an existing Jira issue in %s.\n", cfg.BaseURL)
	writeIssueKeyRule(&text, cfg.Projects, "key of an issue", paramIssueKey)
	fmt.Fprintf(&text, "%s: absolute or ~/ paths of regular files inside %s (symlinks are resolved, the file name is taken from the resolved file); "+
		"extensions: %s (any letter case); at most %d bytes per file.\n",
		paramPaths, strings.Join(attachments.Dirs, " or "), strings.Join(attachments.Extensions, ", "), attachments.MaxBytes)
	text.WriteString("All paths are checked before anything is uploaded. The tool uploads one file per request; already attached files are not deduplicated.")
	return text.String()
}

func issueKeySchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: config.IssueKeyExpr, Description: description}
}

func writeIssueKeyRule(text *strings.Builder, projects []config.Project, keyPhrase string, params ...string) {
	fmt.Fprintf(text, "%s: %s in project %s, e.g. %s-123; numeric issue IDs are not accepted.\n",
		strings.Join(params, ", "), keyPhrase, strings.Join(projectKeysOf(projects), " or "), projects[0].Key)
}

func AddCommentInputSchema() *jsonschema.Schema {
	bodyMinLength := 1
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			paramIssueKey: issueKeySchema("Key of the issue to comment on, e.g. PROJ-123"),
			paramBody:     {Type: "string", MinLength: &bodyMinLength, Description: "Comment text in Jira wiki markup"},
		},
		Required:             []string{paramIssueKey, paramBody},
		AdditionalProperties: noAdditionalProperties(),
	}
}

func AddCommentDescription(cfg config.Config) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Add a comment to an existing Jira issue in %s.\n", cfg.BaseURL)
	writeIssueKeyRule(&text, cfg.Projects, "key of an issue", paramIssueKey)
	fmt.Fprintf(&text, "%s: comment text in Jira wiki markup (h3. heading, *bold*, {code}...{code}, [text|url]); must not be blank.\n", paramBody)
	text.WriteString("If the error says the comment was sent but Jira did not confirm it, the comment may have been added: " +
		"Jira does not deduplicate comments, so check the issue before retrying.")
	return text.String()
}

func LinkIssuesInputSchema(cfg config.Config) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			paramIssueKey: issueKeySchema("Key of the issue the relation starts from, e.g. PROJ-123"),
			paramRelation: {
				Type:        "string",
				Enum:        relationPhrases(cfg),
				Description: fmt.Sprintf("How %s relates to %s; read as %q", paramIssueKey, paramTargetKey, linkReading),
			},
			paramTargetKey: issueKeySchema("Key of the issue the relation points to, e.g. PROJ-124"),
		},
		Required:             []string{paramIssueKey, paramRelation, paramTargetKey},
		AdditionalProperties: noAdditionalProperties(),
	}
}

func relationPhrases(cfg config.Config) []any {
	phraseList := relationPhraseList(cfg)
	phrases := make([]any, 0, len(phraseList))
	for _, phrase := range phraseList {
		phrases = append(phrases, phrase)
	}
	return phrases
}

func relationPhraseList(cfg config.Config) []string {
	phrases := make([]string, 0, len(cfg.IssueLinks))
	for _, link := range cfg.IssueLinks {
		phrases = append(phrases, link.Relation)
	}
	return phrases
}

func LinkIssuesDescription(cfg config.Config) string {
	exampleIssueKey := cfg.Projects[0].Key + "-123"
	exampleTargetKey := cfg.Projects[0].Key + "-124"
	exampleRelation := cfg.IssueLinks[0].Relation
	var text strings.Builder
	fmt.Fprintf(&text, "Link two existing Jira issues in %s. ", cfg.BaseURL)
	fmt.Fprintf(&text, "The link reads %q: %s=%s, %s=%s, %s=%s means %s %s %s.\n", linkReading,
		paramIssueKey, exampleIssueKey, paramRelation, exampleRelation, paramTargetKey, exampleTargetKey,
		exampleIssueKey, exampleRelation, exampleTargetKey)
	writeIssueKeyRule(&text, cfg.Projects, "keys of two different issues", paramIssueKey, paramTargetKey)
	fmt.Fprintf(&text, "%s: one of %s.\n", paramRelation, quoteEach(relationPhraseList(cfg)))
	text.WriteString("If the error says the link was sent but Jira did not confirm it, the link may have been created: check the issue before retrying.")
	return text.String()
}

func quoteEach(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, strconv.Quote(item))
	}
	return strings.Join(quoted, ", ")
}

func UpdateIssueInputSchema(cfg config.Config) *jsonschema.Schema {
	properties := map[string]*jsonschema.Schema{
		paramIssueKey: issueKeySchema("Key of the issue to update, e.g. PROJ-123"),
	}
	for _, project := range cfg.Projects {
		for _, key := range project.UpdateFields {
			properties[key] = clearableFieldSchema(cfg.Fields[key])
		}
	}
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             []string{paramIssueKey},
		AdditionalProperties: noAdditionalProperties(),
	}
}

func UpdateIssueDescription(cfg config.Config) string {
	updatableProjects := projectsWithUpdateFields(cfg)
	var text strings.Builder
	fmt.Fprintf(&text, "Update fields of an existing Jira issue in %s. Only the fields listed for the issue's project can be changed:\n", cfg.BaseURL)
	for _, project := range updatableProjects {
		fmt.Fprintf(&text, "- %s: %s\n", project.Key, updateFieldList(cfg, project))
	}
	writeIssueKeyRule(&text, updatableProjects, "key of an issue", paramIssueKey)
	fmt.Fprintf(&text, "Pass at least one field besides %s.\n", paramIssueKey)
	text.WriteString("Use null to clear a field; for an Epic Link field null removes the issue from its epic.\n")
	text.WriteString(fieldValueShapes())
	text.WriteString("\nThe update is idempotent: if the error says Jira did not confirm the update, it is safe to retry.")
	return text.String()
}

func projectsWithIssueTypes(cfg config.Config) []config.Project {
	return slices.DeleteFunc(slices.Clone(cfg.Projects), func(project config.Project) bool {
		return !project.HasIssueTypes()
	})
}

func projectsWithUpdateFields(cfg config.Config) []config.Project {
	return slices.DeleteFunc(slices.Clone(cfg.Projects), func(project config.Project) bool {
		return len(project.UpdateFields) == 0
	})
}

func projectKeysOf(projects []config.Project) []string {
	keys := make([]string, 0, len(projects))
	for _, project := range projects {
		keys = append(keys, project.Key)
	}
	return keys
}

func updateFieldList(cfg config.Config, project config.Project) string {
	entries := make([]string, 0, len(project.UpdateFields))
	for _, key := range project.UpdateFields {
		entries = append(entries, fmt.Sprintf("%s (%s)", key, cfg.Fields[key].Kind))
	}
	return strings.Join(entries, ", ")
}
