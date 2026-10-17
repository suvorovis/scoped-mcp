package tools

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/mcp-gitlab/gitlab"
	"scoped-mcp/mcp-gitlab/gitlab/mocks"
)

const (
	gitlabBaseURL = "https://gitlab.example.com"
	project       = "group/ci-test"
	mrIID         = 7
	discussionID  = "6a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3"
	noteText      = "Looks good"
)

var (
	toolConfig = config.Config{
		BaseURL: gitlabBaseURL,
		Tools:   config.Tools{AddMRNote: true, AddMRLineComment: true, ReplyToDiscussion: true, AddDraftNote: true},
	}
	webURL                      = gitlabBaseURL + "/" + project + "/-/merge_requests/" + strconv.Itoa(mrIID)
	pathRef                     = gitlab.MergeRequestRef{Project: project, IID: mrIID}
	diffRefs                    = gitlab.DiffRefs{BaseSHA: strings.Repeat("1", 40), StartSHA: strings.Repeat("2", 40), HeadSHA: strings.Repeat("3", 40)}
	mergeRequestWithDiffRefs    = gitlab.MergeRequest{WebURL: webURL, DiffRefs: diffRefs}
	mergeRequestWithoutDiffRefs = gitlab.MergeRequest{WebURL: webURL}
)

func withTools(tools config.Tools) config.Config {
	cfg := toolConfig
	cfg.Tools = tools
	return cfg
}

func connectMCP(t *testing.T, toolset *Toolset) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-gitlab-test", Version: "test"}, nil)
	toolset.Register(server)
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func expectMergeRequest(mergeRequestAPI *mocks.MockMergeRequestAPI) *gomock.Call {
	return mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(mergeRequestWithDiffRefs, nil)
}

func TestRegisterListsTools(t *testing.T) {
	addMRNoteTool := &mcp.Tool{Name: AddMRNoteToolName, Description: AddMRNoteDescription(toolConfig), InputSchema: AddMRNoteInputSchema()}
	replyTool := &mcp.Tool{Name: ReplyToDiscussionToolName, Description: ReplyToDiscussionDescription(toolConfig), InputSchema: ReplyToDiscussionInputSchema()}
	lineCommentTool := &mcp.Tool{Name: AddMRLineCommentToolName, Description: AddMRLineCommentDescription(toolConfig), InputSchema: AddMRLineCommentInputSchema()}
	draftTool := &mcp.Tool{Name: AddDraftNoteToolName, Description: AddDraftNoteDescription(toolConfig), InputSchema: AddDraftNoteInputSchema()}
	tests := []struct {
		name      string
		cfg       config.Config
		wantTools []*mcp.Tool
	}{
		{name: "только add_mr_note", cfg: withTools(config.Tools{AddMRNote: true}), wantTools: []*mcp.Tool{addMRNoteTool}},
		{name: "только reply_to_discussion", cfg: withTools(config.Tools{ReplyToDiscussion: true}), wantTools: []*mcp.Tool{replyTool}},
		{name: "add_mr_note и reply_to_discussion — по алфавиту", cfg: withTools(config.Tools{AddMRNote: true, ReplyToDiscussion: true}), wantTools: []*mcp.Tool{addMRNoteTool, replyTool}},
		{name: "только add_mr_line_comment", cfg: withTools(config.Tools{AddMRLineComment: true}), wantTools: []*mcp.Tool{lineCommentTool}},
		{name: "только add_draft_note", cfg: withTools(config.Tools{AddDraftNote: true}), wantTools: []*mcp.Tool{draftTool}},
		{name: "только инструменты позиций — add_draft_note и add_mr_line_comment", cfg: withTools(config.Tools{AddMRLineComment: true, AddDraftNote: true}), wantTools: []*mcp.Tool{draftTool, lineCommentTool}},
		{name: "все четыре — по алфавиту", cfg: toolConfig, wantTools: []*mcp.Tool{draftTool, lineCommentTool, addMRNoteTool, replyTool}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := connectMCP(t, New(mocks.NewMockMergeRequestAPI(gomock.NewController(t)), tt.cfg))

			listed, err := session.ListTools(t.Context(), nil)
			require.NoError(t, err)

			require.Len(t, listed.Tools, len(tt.wantTools))
			for i, wantTool := range tt.wantTools {
				tool := listed.Tools[i]
				require.Equal(t, wantTool.Name, tool.Name)
				require.Equal(t, wantTool.Description, tool.Description)
				gotSchema, err := json.Marshal(tool.InputSchema)
				require.NoError(t, err)
				wantSchema, err := json.Marshal(wantTool.InputSchema)
				require.NoError(t, err)
				require.JSONEq(t, string(wantSchema), string(gotSchema))
			}
		})
	}
}

func TestCallOverMCP(t *testing.T) {
	tests := []struct {
		name        string
		tool        string
		args        map[string]any
		expect      func(mergeRequestAPI *mocks.MockMergeRequestAPI)
		wantJSON    string
		wantIsError bool
	}{
		{
			name: "add_mr_note: успех → note_id и url",
			tool: AddMRNoteToolName,
			args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), pathRef, noteText).Return(gitlab.Note{ID: 101}, nil),
				)
			},
			wantJSON: `{"note_id":101,"url":"` + webURL + `#note_101"}`,
		},
		{
			name: "reply_to_discussion: успех → note_id и url",
			tool: ReplyToDiscussionToolName,
			args: map[string]any{"project": project, "mr_iid": mrIID, "discussion_id": discussionID, "body": noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddReply(gomock.Any(), pathRef, discussionID, noteText).Return(gitlab.Note{ID: 303}, nil),
				)
			},
			wantJSON: `{"note_id":303,"url":"` + webURL + `#note_303"}`,
		},
		{
			name: "add_mr_line_comment: успех → discussion_id, note_id и url",
			tool: AddMRLineCommentToolName,
			args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText, "new_path": cmdMainGoPath, "old_line": 12, "new_line": 12},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(unchangedLine, cmdMainGoPath, nil)).Return(createdDiscussion, nil),
				)
			},
			wantJSON: `{"discussion_id":"` + discussionID + `","note_id":202,"url":"` + webURL + `#note_202"}`,
		},
		{
			name: "add_draft_note: успех → draft_note_id и url MR",
			tool: AddDraftNoteToolName,
			args: map[string]any{"project": project, "mr_iid": mrIID, "note": noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText}).Return(gitlab.Draft{ID: 404}, nil),
				)
			},
			wantJSON: `{"draft_note_id":404,"url":"` + webURL + `"}`,
		},
		{name: "add_mr_note: без body → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": project, "mr_iid": mrIID}, wantIsError: true},
		{name: "add_mr_note: body пустой → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": ""}, wantIsError: true},
		{name: "add_mr_note: body с quick action → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": "/merge"}, wantIsError: true},
		{name: "add_mr_note: mr_iid 0 → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": project, "mr_iid": 0, "body": noteText}, wantIsError: true},
		{name: "add_mr_note: mr_iid дробный → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": project, "mr_iid": 7.5, "body": noteText}, wantIsError: true},
		{name: "add_mr_note: mr_iid строкой → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": project, "mr_iid": "7", "body": noteText}, wantIsError: true},
		{name: "add_mr_note: project из одного сегмента → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": "group", "mr_iid": mrIID, "body": noteText}, wantIsError: true},
		{name: "add_mr_note: лишний параметр internal → IsError", tool: AddMRNoteToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText, "internal": true}, wantIsError: true},
		{name: "reply_to_discussion: discussion_id не по шаблону → IsError", tool: ReplyToDiscussionToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "discussion_id": "abc", "body": noteText}, wantIsError: true},
		{name: "reply_to_discussion: без discussion_id → IsError", tool: ReplyToDiscussionToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText}, wantIsError: true},
		{name: "add_mr_line_comment: без new_path → IsError", tool: AddMRLineCommentToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText, "new_line": 12}, wantIsError: true},
		{name: "add_mr_line_comment: new_line 0 → IsError", tool: AddMRLineCommentToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText, "new_path": cmdMainGoPath, "new_line": 0}, wantIsError: true},
		{name: "add_mr_line_comment: new_line строкой → IsError", tool: AddMRLineCommentToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText, "new_path": cmdMainGoPath, "new_line": "12"}, wantIsError: true},
		{name: "add_mr_line_comment: без строк → IsError", tool: AddMRLineCommentToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText, "new_path": cmdMainGoPath}, wantIsError: true},
		{name: "add_mr_line_comment: лишний параметр base_sha → IsError", tool: AddMRLineCommentToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText, "new_path": cmdMainGoPath, "new_line": 12, "base_sha": "abc"}, wantIsError: true},
		{name: "add_draft_note: body вместо note → IsError", tool: AddDraftNoteToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText}, wantIsError: true},
		{name: "add_draft_note: позиция и in_reply_to_discussion_id → IsError", tool: AddDraftNoteToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "note": noteText, "in_reply_to_discussion_id": discussionID, "new_path": cmdMainGoPath, "old_line": 12, "new_line": 12}, wantIsError: true},
		{name: "add_draft_note: лишний параметр position → IsError", tool: AddDraftNoteToolName, args: map[string]any{"project": project, "mr_iid": mrIID, "note": noteText, "position": map[string]any{}}, wantIsError: true},
		{
			name: "add_mr_note: ошибка клиента → IsError",
			tool: AddMRNoteToolName,
			args: map[string]any{"project": project, "mr_iid": mrIID, "body": noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(gitlab.MergeRequest{}, gitlab.ErrMergeRequestUnavailable)
			},
			wantIsError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeRequestAPI := mocks.NewMockMergeRequestAPI(gomock.NewController(t))
			if tt.expect != nil {
				tt.expect(mergeRequestAPI)
			}
			session := connectMCP(t, New(mergeRequestAPI, toolConfig))

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})

			require.NoError(t, err)
			require.Equal(t, tt.wantIsError, result.IsError)
			if tt.wantIsError {
				require.NotEmpty(t, result.Content)
				return
			}
			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantJSON, string(structured))
		})
	}
}

func TestMergeRequestRef(t *testing.T) {
	tests := []struct {
		name    string
		project string
		iid     int64
		want    gitlab.MergeRequestRef
		wantErr error
	}{
		{name: "путь проекта", project: project, iid: mrIID, want: pathRef},
		{name: "числовой id", project: "42", iid: 1, want: gitlab.MergeRequestRef{Project: "42", IID: 1}},
		{name: "проект из одного сегмента", project: "group", iid: mrIID, wantErr: ErrProjectInvalid},
		{name: "пустой проект", project: "", iid: mrIID, wantErr: ErrProjectInvalid},
		{name: "проект с %2F", project: "group%2Fci-test", iid: mrIID, wantErr: ErrProjectInvalid},
		{name: "проект с ..", project: "group/../ci-test", iid: mrIID, wantErr: ErrProjectInvalid},
		{name: "iid 0", project: project, iid: 0, wantErr: ErrMRIIDInvalid},
		{name: "iid отрицательный", project: project, iid: -1, wantErr: ErrMRIIDInvalid},
		{name: "проект проверяется раньше iid", project: "group", iid: 0, wantErr: ErrProjectInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeRequestRef(tt.project, tt.iid)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNoteURL(t *testing.T) {
	require.Equal(t, webURL+"#note_101", noteURL(webURL, 101))
}
