package tools

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/mcp-gitlab/gitlab"
	"scoped-mcp/mcp-gitlab/guard"
)

var (
	ErrProjectInvalid      = errors.New("tools: project must be a numeric id or a path like group/sub/project")
	ErrMRIIDInvalid        = errors.New("tools: mr_iid must be a positive integer")
	ErrFieldRequired       = errors.New("tools: required field is empty")
	ErrQuickAction         = errors.New("tools: no line of the text may start with /, GitLab would run it as a quick action; put paths and commands in backticks")
	ErrDiscussionIDInvalid = errors.New("tools: discussion_id must be 40 lowercase hex characters")
)

type Toolset struct {
	mergeRequestAPI gitlab.MergeRequestAPI
	cfg             config.Config
}

func New(mergeRequestAPI gitlab.MergeRequestAPI, cfg config.Config) *Toolset {
	return &Toolset{mergeRequestAPI: mergeRequestAPI, cfg: cfg}
}

func (t *Toolset) Register(server *mcp.Server) {
	if t.cfg.Tools.AddMRNote {
		t.registerAddMRNote(server)
	}
	if t.cfg.Tools.AddMRLineComment {
		t.registerAddMRLineComment(server)
	}
	if t.cfg.Tools.ReplyToDiscussion {
		t.registerReplyToDiscussion(server)
	}
	if t.cfg.Tools.AddDraftNote {
		t.registerAddDraftNote(server)
	}
}

func mergeRequestRef(project string, iid int64) (gitlab.MergeRequestRef, error) {
	if !guard.ValidProject(project) {
		return gitlab.MergeRequestRef{}, fmt.Errorf("%w: %q", ErrProjectInvalid, project)
	}
	if iid < 1 {
		return gitlab.MergeRequestRef{}, fmt.Errorf("%w: %d", ErrMRIIDInvalid, iid)
	}
	return gitlab.MergeRequestRef{Project: project, IID: iid}, nil
}

func checkText(field, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: %s", ErrFieldRequired, field)
	}
	if guard.HasQuickAction(text) {
		return fmt.Errorf("%w: %s", ErrQuickAction, field)
	}
	return nil
}

func checkDiscussionID(discussionID string) error {
	if !guard.ValidDiscussionID(discussionID) {
		return fmt.Errorf("%w: %q", ErrDiscussionIDInvalid, discussionID)
	}
	return nil
}

func noteURL(webURL string, noteID int64) string {
	return webURL + "#note_" + strconv.FormatInt(noteID, 10)
}
