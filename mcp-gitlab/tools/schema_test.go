package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddMRNoteInputSchema(t *testing.T) {
	got, err := json.Marshal(AddMRNoteInputSchema())
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "project": {"type": "string", "minLength": 1, "description": "Project: numeric id or full path like group/sub/project"},
	    "mr_iid":  {"type": "integer", "minimum": 1, "description": "Merge request iid: the number in the merge request URL"},
	    "body":    {"type": "string", "minLength": 1, "description": "Comment text in GitLab Markdown; no line may start with /"}
	  },
	  "required": ["project", "mr_iid", "body"],
	  "additionalProperties": false
	}`, string(got))
}

func TestAddMRNoteDescription(t *testing.T) {
	want := "Add a general comment to a merge request in https://gitlab.example.com. " +
		"project: numeric project id or full path like group/sub/project. mr_iid: merge request iid, the number in its URL. " +
		"body: comment text in GitLab Markdown. " +
		"No line of body may start with / (even after leading whitespace): GitLab would run it as a quick action such as /merge or /approve; put paths and commands in backticks. " +
		"The comment is published immediately under the token owner's name. " +
		"If the error says the comment was sent but GitLab did not confirm it, it may have been added: check the merge request before retrying, GitLab does not deduplicate comments."

	require.Equal(t, want, AddMRNoteDescription(toolConfig))
}

func TestReplyToDiscussionInputSchema(t *testing.T) {
	got, err := json.Marshal(ReplyToDiscussionInputSchema())
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "project":       {"type": "string", "minLength": 1, "description": "Project: numeric id or full path like group/sub/project"},
	    "mr_iid":        {"type": "integer", "minimum": 1, "description": "Merge request iid: the number in the merge request URL"},
	    "discussion_id": {"type": "string", "pattern": "^[0-9a-f]{40}$", "description": "Id of the discussion to reply to: 40 lowercase hex characters"},
	    "body":          {"type": "string", "minLength": 1, "description": "Reply text in GitLab Markdown; no line may start with /"}
	  },
	  "required": ["project", "mr_iid", "discussion_id", "body"],
	  "additionalProperties": false
	}`, string(got))
}

func TestReplyToDiscussionDescription(t *testing.T) {
	want := "Reply in an existing discussion thread of a merge request in https://gitlab.example.com. " +
		"project: numeric project id or full path like group/sub/project. mr_iid: merge request iid, the number in its URL. " +
		"discussion_id: id of the discussion (40 lowercase hex characters, from the discussion list of a read-only GitLab tool). body: reply text in GitLab Markdown. " +
		"No line of body may start with / (even after leading whitespace): GitLab would run it as a quick action such as /merge or /approve; put paths and commands in backticks. " +
		"The comment is published immediately under the token owner's name. " +
		"If the error says the comment was sent but GitLab did not confirm it, it may have been added: check the merge request before retrying, GitLab does not deduplicate comments."

	require.Equal(t, want, ReplyToDiscussionDescription(toolConfig))
}

const linePropertiesJSON = `
	    "new_path":       {"type": "string", "minLength": 1, "description": "File path in the merge request head (new_path of the diff)"},
	    "old_path":       {"type": "string", "minLength": 1, "description": "File path before the change; defaults to new_path, required for renamed files"},
	    "new_line":       {"type": "integer", "minimum": 1, "description": "Line number in the new version: for added or unchanged lines"},
	    "old_line":       {"type": "integer", "minimum": 1, "description": "Line number in the old version: for removed or unchanged lines"},
	    "start_new_line": {"type": "integer", "minimum": 1, "description": "First line of a multi-line comment, new version"},
	    "start_old_line": {"type": "integer", "minimum": 1, "description": "First line of a multi-line comment, old version"}`

func TestAddMRLineCommentInputSchema(t *testing.T) {
	got, err := json.Marshal(AddMRLineCommentInputSchema())
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "project": {"type": "string", "minLength": 1, "description": "Project: numeric id or full path like group/sub/project"},
	    "mr_iid":  {"type": "integer", "minimum": 1, "description": "Merge request iid: the number in the merge request URL"},
	    "body":    {"type": "string", "minLength": 1, "description": "Comment text in GitLab Markdown; no line may start with /"},`+linePropertiesJSON+`
	  },
	  "required": ["project", "mr_iid", "body", "new_path"],
	  "additionalProperties": false
	}`, string(got))
}

func TestAddMRLineCommentDescription(t *testing.T) {
	want := "Start a new discussion on a line of the diff of a merge request in https://gitlab.example.com. " +
		"project: numeric project id or full path like group/sub/project. mr_iid: merge request iid, the number in its URL. " +
		"body: comment text in GitLab Markdown. " +
		"No line of body may start with / (even after leading whitespace): GitLab would run it as a quick action such as /merge or /approve; put paths and commands in backticks. " +
		"Line position: new_line for an added line, old_line for a removed line, either or both for an unchanged line (the missing one is taken from the file diff); the diff SHAs are taken from the merge request. " +
		"For a multi-line comment also pass start_new_line or start_old_line for the first line; new_line/old_line then mark the last line. " +
		"old_path defaults to new_path and must be passed for renamed files. A line that is not in the diff is rejected by this tool, except a single line with both new_line and old_line: it goes to GitLab unchecked. " +
		"The comment is published immediately under the token owner's name. " +
		"If the error says the comment was sent but GitLab did not confirm it, it may have been added: check the merge request before retrying, GitLab does not deduplicate comments."

	require.Equal(t, want, AddMRLineCommentDescription(toolConfig))
}

func TestAddDraftNoteInputSchema(t *testing.T) {
	got, err := json.Marshal(AddDraftNoteInputSchema())
	require.NoError(t, err)

	require.JSONEq(t, `{
	  "type": "object",
	  "properties": {
	    "project": {"type": "string", "minLength": 1, "description": "Project: numeric id or full path like group/sub/project"},
	    "mr_iid":  {"type": "integer", "minimum": 1, "description": "Merge request iid: the number in the merge request URL"},
	    "note":    {"type": "string", "minLength": 1, "description": "Draft text in GitLab Markdown; no line may start with /"},
	    "in_reply_to_discussion_id": {"type": "string", "pattern": "^[0-9a-f]{40}$", "description": "Id of the discussion to reply to: 40 lowercase hex characters"},`+linePropertiesJSON+`
	  },
	  "required": ["project", "mr_iid", "note"],
	  "additionalProperties": false
	}`, string(got))
}

func TestAddDraftNoteDescription(t *testing.T) {
	want := "Add a draft review note to a merge request in https://gitlab.example.com: a general note, a note on a diff line (pass new_path and a line) or a reply to a discussion (pass in_reply_to_discussion_id), never both a line and a reply. " +
		"project: numeric project id or full path like group/sub/project. mr_iid: merge request iid, the number in its URL. " +
		"note: draft text in GitLab Markdown. " +
		"No line of note may start with / (even after leading whitespace): GitLab would run it as a quick action such as /merge or /approve; put paths and commands in backticks. " +
		"Line position: new_line for an added line, old_line for a removed line, either or both for an unchanged line (the missing one is taken from the file diff); the diff SHAs are taken from the merge request. " +
		"For a multi-line comment also pass start_new_line or start_old_line for the first line; new_line/old_line then mark the last line. " +
		"old_path defaults to new_path and must be passed for renamed files. A line that is not in the diff is rejected by this tool, except a single line with both new_line and old_line: it goes to GitLab unchecked. " +
		"Drafts are visible only to the token owner until they publish the review in GitLab (Submit review); this tool cannot publish, edit or delete drafts. " +
		"If the error says the comment was sent but GitLab did not confirm it, it may have been added: check the merge request before retrying, GitLab does not deduplicate comments."

	require.Equal(t, want, AddDraftNoteDescription(toolConfig))
}
