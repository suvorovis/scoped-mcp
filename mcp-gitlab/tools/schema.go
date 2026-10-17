package tools

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/mcp-gitlab/guard"
)

const (
	paramProject               = "project"
	paramMRIID                 = "mr_iid"
	paramBody                  = "body"
	paramNote                  = "note"
	paramDiscussionID          = "discussion_id"
	paramInReplyToDiscussionID = "in_reply_to_discussion_id"
	paramNewPath               = "new_path"
	paramOldPath               = "old_path"
	paramNewLine               = "new_line"
	paramOldLine               = "old_line"
	paramStartNewLine          = "start_new_line"
	paramStartOldLine          = "start_old_line"
)

const (
	mergeRequestParamsRule = "project: numeric project id or full path like group/sub/project. mr_iid: merge request iid, the number in its URL. "
	quickActionRuleFormat  = "No line of %s may start with / (even after leading whitespace): GitLab would run it as a quick action such as /merge or /approve; put paths and commands in backticks. "
	linePositionRule       = "Line position: new_line for an added line, old_line for a removed line, either or both for an unchanged line (the missing one is taken from the file diff); the diff SHAs are taken from the merge request. " +
		"For a multi-line comment also pass start_new_line or start_old_line for the first line; new_line/old_line then mark the last line. " +
		"old_path defaults to new_path and must be passed for renamed files. A line that is not in the diff is rejected by this tool, except a single line with both new_line and old_line: it goes to GitLab unchecked. "
	publishedImmediatelyRule = "The comment is published immediately under the token owner's name. "
	draftVisibilityRule      = "Drafts are visible only to the token owner until they publish the review in GitLab (Submit review); this tool cannot publish, edit or delete drafts. "
	unconfirmedRetryRule     = "If the error says the comment was sent but GitLab did not confirm it, it may have been added: check the merge request before retrying, GitLab does not deduplicate comments."
)

func AddMRNoteInputSchema() *jsonschema.Schema {
	return objectSchema(map[string]*jsonschema.Schema{
		paramProject: projectSchema(),
		paramMRIID:   mrIIDSchema(),
		paramBody:    textSchema("Comment text in GitLab Markdown; no line may start with /"),
	}, []string{paramProject, paramMRIID, paramBody})
}

func AddMRNoteDescription(cfg config.Config) string {
	return fmt.Sprintf("Add a general comment to a merge request in %s. ", cfg.BaseURL) +
		mergeRequestParamsRule +
		"body: comment text in GitLab Markdown. " +
		fmt.Sprintf(quickActionRuleFormat, paramBody) +
		publishedImmediatelyRule +
		unconfirmedRetryRule
}

func ReplyToDiscussionInputSchema() *jsonschema.Schema {
	return objectSchema(map[string]*jsonschema.Schema{
		paramProject:      projectSchema(),
		paramMRIID:        mrIIDSchema(),
		paramDiscussionID: discussionIDSchema(),
		paramBody:         textSchema("Reply text in GitLab Markdown; no line may start with /"),
	}, []string{paramProject, paramMRIID, paramDiscussionID, paramBody})
}

func ReplyToDiscussionDescription(cfg config.Config) string {
	return fmt.Sprintf("Reply in an existing discussion thread of a merge request in %s. ", cfg.BaseURL) +
		mergeRequestParamsRule +
		"discussion_id: id of the discussion (40 lowercase hex characters, from the discussion list of a read-only GitLab tool). body: reply text in GitLab Markdown. " +
		fmt.Sprintf(quickActionRuleFormat, paramBody) +
		publishedImmediatelyRule +
		unconfirmedRetryRule
}

func AddMRLineCommentInputSchema() *jsonschema.Schema {
	properties := lineProperties()
	properties[paramProject] = projectSchema()
	properties[paramMRIID] = mrIIDSchema()
	properties[paramBody] = textSchema("Comment text in GitLab Markdown; no line may start with /")
	return objectSchema(properties, []string{paramProject, paramMRIID, paramBody, paramNewPath})
}

func AddMRLineCommentDescription(cfg config.Config) string {
	return fmt.Sprintf("Start a new discussion on a line of the diff of a merge request in %s. ", cfg.BaseURL) +
		mergeRequestParamsRule +
		"body: comment text in GitLab Markdown. " +
		fmt.Sprintf(quickActionRuleFormat, paramBody) +
		linePositionRule +
		publishedImmediatelyRule +
		unconfirmedRetryRule
}

func AddDraftNoteInputSchema() *jsonschema.Schema {
	properties := lineProperties()
	properties[paramProject] = projectSchema()
	properties[paramMRIID] = mrIIDSchema()
	properties[paramNote] = textSchema("Draft text in GitLab Markdown; no line may start with /")
	properties[paramInReplyToDiscussionID] = discussionIDSchema()
	return objectSchema(properties, []string{paramProject, paramMRIID, paramNote})
}

func AddDraftNoteDescription(cfg config.Config) string {
	return fmt.Sprintf("Add a draft review note to a merge request in %s: a general note, a note on a diff line (pass new_path and a line) or a reply to a discussion (pass in_reply_to_discussion_id), never both a line and a reply. ", cfg.BaseURL) +
		mergeRequestParamsRule +
		"note: draft text in GitLab Markdown. " +
		fmt.Sprintf(quickActionRuleFormat, paramNote) +
		linePositionRule +
		draftVisibilityRule +
		unconfirmedRetryRule
}

func objectSchema(properties map[string]*jsonschema.Schema, required []string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

func projectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", MinLength: jsonschema.Ptr(1), Description: "Project: numeric id or full path like group/sub/project"}
}

func mrIIDSchema() *jsonschema.Schema {
	return positiveIntegerSchema("Merge request iid: the number in the merge request URL")
}

func textSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", MinLength: jsonschema.Ptr(1), Description: description}
}

func discussionIDSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: guard.DiscussionIDPattern, Description: "Id of the discussion to reply to: 40 lowercase hex characters"}
}

func lineProperties() map[string]*jsonschema.Schema {
	return map[string]*jsonschema.Schema{
		paramNewPath:      textSchema("File path in the merge request head (new_path of the diff)"),
		paramOldPath:      textSchema("File path before the change; defaults to new_path, required for renamed files"),
		paramNewLine:      positiveIntegerSchema("Line number in the new version: for added or unchanged lines"),
		paramOldLine:      positiveIntegerSchema("Line number in the old version: for removed or unchanged lines"),
		paramStartNewLine: positiveIntegerSchema("First line of a multi-line comment, new version"),
		paramStartOldLine: positiveIntegerSchema("First line of a multi-line comment, old version"),
	}
}

func positiveIntegerSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "integer", Minimum: jsonschema.Ptr(1.0), Description: description}
}
