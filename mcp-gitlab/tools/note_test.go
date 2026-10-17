package tools

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-gitlab/gitlab"
	"scoped-mcp/mcp-gitlab/gitlab/mocks"
	"scoped-mcp/mcp-gitlab/guard"
)

var (
	statusNotFound = &gitlab.StatusError{StatusCode: http.StatusNotFound, Message: "404 Not found"}
	guardRefusal   = &url.Error{Op: "Post", URL: gitlabBaseURL + "/api/v4/projects/1/merge_requests/1/notes", Err: guard.ErrForbidden}
)

func TestAddMRNote(t *testing.T) {
	numericRef := gitlab.MergeRequestRef{Project: "42", IID: mrIID}
	tests := []struct {
		name    string
		input   AddMRNoteInput
		expect  func(mergeRequestAPI *mocks.MockMergeRequestAPI)
		want    AddMRNoteOutput
		wantErr error
	}{
		{
			name:  "комментарий к MR → note_id и ссылка на заметку",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), pathRef, noteText).Return(gitlab.Note{ID: 101}, nil),
				)
			},
			want: AddMRNoteOutput{NoteID: 101, URL: webURL + "#note_101"},
		},
		{
			name:  "числовой id проекта",
			input: AddMRNoteInput{Project: "42", MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), numericRef).Return(mergeRequestWithDiffRefs, nil),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), numericRef, noteText).Return(gitlab.Note{ID: 102}, nil),
				)
			},
			want: AddMRNoteOutput{NoteID: 102, URL: webURL + "#note_102"},
		},
		{
			name:  "текст уходит без обрезки пробелов",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: "  ok\n"},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), pathRef, "  ok\n").Return(gitlab.Note{ID: 103}, nil),
				)
			},
			want: AddMRNoteOutput{NoteID: 103, URL: webURL + "#note_103"},
		},
		{
			name:  "MR без diff_refs — общему комментарию они не нужны",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(mergeRequestWithoutDiffRefs, nil),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), pathRef, noteText).Return(gitlab.Note{ID: 104}, nil),
				)
			},
			want: AddMRNoteOutput{NoteID: 104, URL: webURL + "#note_104"},
		},
		{
			name:  "MR недоступен → ErrMergeRequestUnavailable, записи нет",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(gitlab.MergeRequest{}, gitlab.ErrMergeRequestUnavailable)
			},
			wantErr: gitlab.ErrMergeRequestUnavailable,
		},
		{
			name:  "запись не подтверждена → ErrCommentUnconfirmed как есть",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), pathRef, noteText).Return(gitlab.Note{}, gitlab.ErrCommentUnconfirmed),
				)
			},
			wantErr: gitlab.ErrCommentUnconfirmed,
		},
		{
			name:  "StatusError записи как есть",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), pathRef, noteText).Return(gitlab.Note{}, statusNotFound),
				)
			},
			wantErr: statusNotFound,
		},
		{
			name:  "отказ guard при чтении MR → ErrForbidden как есть, записи нет",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(gitlab.MergeRequest{}, guardRefusal)
			},
			wantErr: guard.ErrForbidden,
		},
		{
			name:  "отказ guard при записи → ErrForbidden как есть",
			input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddNote(gomock.Any(), pathRef, noteText).Return(gitlab.Note{}, guardRefusal),
				)
			},
			wantErr: guard.ErrForbidden,
		},

		{name: "проект из одного сегмента", input: AddMRNoteInput{Project: "group", MRIID: mrIID, Body: noteText}, wantErr: ErrProjectInvalid},
		{name: "пустой проект", input: AddMRNoteInput{MRIID: mrIID, Body: noteText}, wantErr: ErrProjectInvalid},
		{name: "проект с ..", input: AddMRNoteInput{Project: "group/../ci-test", MRIID: mrIID, Body: noteText}, wantErr: ErrProjectInvalid},
		{name: "mr_iid 0", input: AddMRNoteInput{Project: project, Body: noteText}, wantErr: ErrMRIIDInvalid},
		{name: "mr_iid отрицательный", input: AddMRNoteInput{Project: project, MRIID: -5, Body: noteText}, wantErr: ErrMRIIDInvalid},
		{name: "пустой текст", input: AddMRNoteInput{Project: project, MRIID: mrIID}, wantErr: ErrFieldRequired},
		{name: "текст из пробелов", input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: " \n\t "}, wantErr: ErrFieldRequired},
		{name: "quick action в первой строке → ErrQuickAction", input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: "/merge"}, wantErr: ErrQuickAction},
		{name: "текст с quick action в середине → ErrQuickAction", input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: "LGTM\n  /approve\nthanks"}, wantErr: ErrQuickAction},
		{name: "путь в начале строки → ErrQuickAction", input: AddMRNoteInput{Project: project, MRIID: mrIID, Body: "/etc/fstab is read here"}, wantErr: ErrQuickAction},
		{name: "проект проверяется раньше текста", input: AddMRNoteInput{Project: "group", MRIID: mrIID, Body: "/merge"}, wantErr: ErrProjectInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeRequestAPI := mocks.NewMockMergeRequestAPI(gomock.NewController(t))
			if tt.expect != nil {
				tt.expect(mergeRequestAPI)
			}

			got, err := New(mergeRequestAPI, toolConfig).AddMRNote(t.Context(), tt.input)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestReplyToDiscussion(t *testing.T) {
	tests := []struct {
		name    string
		input   ReplyToDiscussionInput
		expect  func(mergeRequestAPI *mocks.MockMergeRequestAPI)
		want    ReplyToDiscussionOutput
		wantErr error
	}{
		{
			name:  "ответ в дискуссию → note_id и ссылка",
			input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: discussionID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddReply(gomock.Any(), pathRef, discussionID, noteText).Return(gitlab.Note{ID: 303}, nil),
				)
			},
			want: ReplyToDiscussionOutput{NoteID: 303, URL: webURL + "#note_303"},
		},
		{
			name:  "MR недоступен → записи нет",
			input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: discussionID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(gitlab.MergeRequest{}, gitlab.ErrMergeRequestUnavailable)
			},
			wantErr: gitlab.ErrMergeRequestUnavailable,
		},
		{
			name:  "дискуссии нет → StatusError как есть",
			input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: discussionID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddReply(gomock.Any(), pathRef, discussionID, noteText).Return(gitlab.Note{}, statusNotFound),
				)
			},
			wantErr: statusNotFound,
		},
		{
			name:  "запись не подтверждена → ErrCommentUnconfirmed как есть",
			input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: discussionID, Body: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddReply(gomock.Any(), pathRef, discussionID, noteText).Return(gitlab.Note{}, gitlab.ErrCommentUnconfirmed),
				)
			},
			wantErr: gitlab.ErrCommentUnconfirmed,
		},

		{name: "discussion_id в верхнем регистре", input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: "6A1B2C3D4E5F60718293A4B5C6D7E8F9A0B1C2D3", Body: noteText}, wantErr: ErrDiscussionIDInvalid},
		{name: "discussion_id из 39 символов", input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: discussionID[:39], Body: noteText}, wantErr: ErrDiscussionIDInvalid},
		{name: "пустой discussion_id", input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, Body: noteText}, wantErr: ErrDiscussionIDInvalid},
		{name: "числовой discussion_id", input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: "12345", Body: noteText}, wantErr: ErrDiscussionIDInvalid},
		{name: "проект из одного сегмента", input: ReplyToDiscussionInput{Project: "group", MRIID: mrIID, DiscussionID: discussionID, Body: noteText}, wantErr: ErrProjectInvalid},
		{name: "mr_iid 0", input: ReplyToDiscussionInput{Project: project, DiscussionID: discussionID, Body: noteText}, wantErr: ErrMRIIDInvalid},
		{name: "пустой текст", input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: discussionID}, wantErr: ErrFieldRequired},
		{name: "quick action", input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: discussionID, Body: "/unresolve"}, wantErr: ErrQuickAction},
		{name: "discussion_id проверяется раньше текста", input: ReplyToDiscussionInput{Project: project, MRIID: mrIID, DiscussionID: "abc", Body: "/merge"}, wantErr: ErrDiscussionIDInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeRequestAPI := mocks.NewMockMergeRequestAPI(gomock.NewController(t))
			if tt.expect != nil {
				tt.expect(mergeRequestAPI)
			}

			got, err := New(mergeRequestAPI, toolConfig).ReplyToDiscussion(t.Context(), tt.input)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
