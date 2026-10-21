package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/tools"
)

var toolConfig = config.Config{
	BaseURL: "https://jira.example.com",
	Tools:   config.Tools{CreateIssue: true},
	Fields: map[string]config.Field{
		"description": {Jira: "description", Kind: config.KindText},
		"priority":    {Jira: "priority", Kind: config.KindName, Required: true},
		"labels":      {Jira: "labels", Kind: config.KindLabels},
		"components":  {Jira: "components", Kind: config.KindNames, Required: true},
		"epic_name":   {Jira: "customfield_10203", Kind: config.KindText, Required: true},
		"epic_link":   {Jira: "customfield_10201", Kind: config.KindIssueKey},
	},
	Projects: []config.Project{
		{Key: "PROJ", AssignToSelf: true, IssueTypes: []config.IssueType{
			{Name: "Epic", ID: "10000", Fields: []string{"description", "priority", "labels", "components", "epic_name"}},
			{Name: "История", ID: "11203", Fields: []string{"description", "priority", "labels", "epic_link"}},
		}},
		{Key: "OTHER", IssueTypes: []config.IssueType{{Name: "Task", ID: "3"}}},
		{Key: "SUPPORT"},
	},
}

func TestCreateIssueInputSchema(t *testing.T) {
	got, err := json.Marshal(tools.CreateIssueInputSchema(toolConfig))
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "project":     {"type": "string", "enum": ["PROJ", "OTHER"], "description": "Key of the project to create the issue in"},
	    "issue_type":  {"type": "string", "enum": ["Epic", "История", "Task"], "description": "Issue type name; must be allowed for the chosen project, see the tool description"},
	    "summary":     {"type": "string", "minLength": 1, "description": "Issue summary (title)"},
	    "description": {"type": "string", "description": "Jira field description (text)"},
	    "priority":    {"type": "string", "description": "Jira field priority (name)"},
	    "labels":      {"type": "array", "items": {"type": "string"}, "description": "Jira field labels (labels)"},
	    "components":  {"type": "array", "items": {"type": "string"}, "description": "Jira field components (names)"},
	    "epic_name":   {"type": "string", "description": "Jira field customfield_10203 (text)"},
	    "epic_link":   {"type": "string", "description": "Jira field customfield_10201 (issue_key)"}
	  },
	  "required": ["project", "issue_type", "summary"],
	  "additionalProperties": false
	}`, string(got))
}

func TestCreateIssueDescription(t *testing.T) {
	want := "Create one Jira issue in https://jira.example.com. Only the projects and issue types listed below are allowed; each issue type accepts only its listed fields (* = required). summary is always required.\n" +
		"Project PROJ (issues are assigned to the token owner, i.e. the user):\n" +
		"- Epic: description (text), priority* (name), labels (labels), components* (names), epic_name* (text)\n" +
		"- История: description (text), priority* (name), labels (labels), epic_link (issue_key)\n" +
		"Project OTHER:\n" +
		"- Task: no extra fields\n" +
		"Field values: text, name, option — string; names, options, labels — array of strings; issue_key — key of an issue in an allowed project, e.g. PROJ-123."

	require.Equal(t, want, tools.CreateIssueDescription(toolConfig))
}

func TestAttachFilesInputSchema(t *testing.T) {
	got, err := json.Marshal(tools.AttachFilesInputSchema())
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "issue_key": {"type": "string", "pattern": "^[A-Z][A-Z0-9_]+-[1-9][0-9]*$", "description": "Key of the issue to attach the files to, e.g. PROJ-123"},
	    "paths":     {"type": "array", "items": {"type": "string", "minLength": 1}, "minItems": 1, "description": "Absolute or ~/ paths of the files to attach, uploaded in this order"}
	  },
	  "required": ["issue_key", "paths"],
	  "additionalProperties": false
	}`, string(got))
}

func TestAttachFilesDescription(t *testing.T) {
	want := "Attach local files to an existing Jira issue in https://jira.example.com.\n" +
		"issue_key: key of an issue in project PROJ or OTHER or SUPPORT, e.g. PROJ-123; numeric issue IDs are not accepted.\n" +
		"paths: absolute or ~/ paths of regular files inside /home/bot/out or /var/reports (symlinks are resolved, the file name is taken from the resolved file); " +
		"extensions: .md, .PNG (any letter case); at most 10485760 bytes per file.\n" +
		"All paths are checked before anything is uploaded. The tool uploads one file per request; already attached files are not deduplicated."

	require.Equal(t, want, tools.AttachFilesDescription(withAttachFiles()))
}

func TestAddCommentInputSchema(t *testing.T) {
	got, err := json.Marshal(tools.AddCommentInputSchema())
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "issue_key": {"type": "string", "pattern": "^[A-Z][A-Z0-9_]+-[1-9][0-9]*$", "description": "Key of the issue to comment on, e.g. PROJ-123"},
	    "body":      {"type": "string", "minLength": 1, "description": "Comment text in Jira wiki markup"}
	  },
	  "required": ["issue_key", "body"],
	  "additionalProperties": false
	}`, string(got))
}

func TestAddCommentDescription(t *testing.T) {
	want := "Add a comment to an existing Jira issue in https://jira.example.com.\n" +
		"issue_key: key of an issue in project PROJ or OTHER or SUPPORT, e.g. PROJ-123; numeric issue IDs are not accepted.\n" +
		"body: comment text in Jira wiki markup (h3. heading, *bold*, {code}...{code}, [text|url]); must not be blank.\n" +
		"If the error says the comment was sent but Jira did not confirm it, the comment may have been added: " +
		"Jira does not deduplicate comments, so check the issue before retrying."

	require.Equal(t, want, tools.AddCommentDescription(withAddComment()))
}

func TestLinkIssuesInputSchema(t *testing.T) {
	got, err := json.Marshal(tools.LinkIssuesInputSchema(withLinkIssues()))
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "issue_key":  {"type": "string", "pattern": "^[A-Z][A-Z0-9_]+-[1-9][0-9]*$", "description": "Key of the issue the relation starts from, e.g. PROJ-123"},
	    "relation":   {"type": "string", "enum": ["blocks", "is blocked by", "relates to"], "description": "How issue_key relates to target_key; read as \"issue_key relation target_key\""},
	    "target_key": {"type": "string", "pattern": "^[A-Z][A-Z0-9_]+-[1-9][0-9]*$", "description": "Key of the issue the relation points to, e.g. PROJ-124"}
	  },
	  "required": ["issue_key", "relation", "target_key"],
	  "additionalProperties": false
	}`, string(got))
}

func TestLinkIssuesDescription(t *testing.T) {
	want := "Link two existing Jira issues in https://jira.example.com. The link reads \"issue_key relation target_key\": " +
		"issue_key=PROJ-123, relation=blocks, target_key=PROJ-124 means PROJ-123 blocks PROJ-124.\n" +
		"issue_key, target_key: keys of two different issues in project PROJ or OTHER or SUPPORT, e.g. PROJ-123; numeric issue IDs are not accepted.\n" +
		"relation: one of \"blocks\", \"is blocked by\", \"relates to\".\n" +
		"If the error says the link was sent but Jira did not confirm it, the link may have been created: check the issue before retrying."

	require.Equal(t, want, tools.LinkIssuesDescription(withLinkIssues()))
}

func TestUpdateIssueInputSchema(t *testing.T) {
	got, err := json.Marshal(tools.UpdateIssueInputSchema(withUpdateIssue()))
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "issue_key": {"type": "string", "pattern": "^[A-Z][A-Z0-9_]+-[1-9][0-9]*$", "description": "Key of the issue to update, e.g. PROJ-123"},
	    "epic_link": {"type": ["string", "null"], "description": "Jira field customfield_10201 (issue_key); null clears the field"},
	    "labels":    {"type": ["array", "null"], "items": {"type": "string"}, "description": "Jira field labels (labels); null clears the field"},
	    "priority":  {"type": ["string", "null"], "description": "Jira field priority (name); null clears the field"}
	  },
	  "required": ["issue_key"],
	  "additionalProperties": false
	}`, string(got))
}

func TestUpdateIssueDescription(t *testing.T) {
	want := "Update fields of an existing Jira issue in https://jira.example.com. Only the fields listed for the issue's project can be changed:\n" +
		"- PROJ: epic_link (issue_key), labels (labels)\n" +
		"- OTHER: priority (name)\n" +
		"issue_key: key of an issue in project PROJ or OTHER, e.g. PROJ-123; numeric issue IDs are not accepted.\n" +
		"Pass at least one field besides issue_key.\n" +
		"Use null to clear a field; for an Epic Link field null removes the issue from its epic.\n" +
		"Field values: text, name, option — string; names, options, labels — array of strings; issue_key — key of an issue in an allowed project, e.g. PROJ-123.\n" +
		"The update is idempotent: if the error says Jira did not confirm the update, it is safe to retry."

	require.Equal(t, want, tools.UpdateIssueDescription(withUpdateIssue()))
}
