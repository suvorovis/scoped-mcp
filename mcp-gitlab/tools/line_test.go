package tools

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-gitlab/diff"
	"scoped-mcp/mcp-gitlab/gitlab"
	"scoped-mcp/mcp-gitlab/gitlab/mocks"
	"scoped-mcp/mcp-gitlab/guard"
)

const (
	cmdMainGoPath     = "cmd/main.go"
	cmdMainGoPathSHA1 = "93310a20c22640d80343161f98be28beb14eb4e9"
	renamedFromPath   = "cmd/old.go"
)

var twoHunksFileDiff = strings.Join([]string{
	"@@ -1,5 +1,5 @@",
	" package main",
	`-import "os"`,
	`-import "io"`,
	`+import "fmt"`,
	`+import "os"`,
	" ",
	" func main() {",
	"@@ -10,3 +10,3 @@",
	" x",
	"+y",
	"-z",
	" w",
	"",
}, "\n")

var (
	unchangedLine  = LineInput{NewPath: cmdMainGoPath, OldLine: 12, NewLine: 12}
	multiLine      = LineInput{NewPath: cmdMainGoPath, StartOldLine: 1, StartNewLine: 1, NewLine: 3}
	multiLineRange = gitlab.LineRange{
		Start: gitlab.LineRangeEnd{LineCode: lineCode(1, 1), Type: "old"},
		End:   gitlab.LineRangeEnd{LineCode: lineCode(4, 3), Type: "new"},
	}
	createdDiscussion = gitlab.Discussion{ID: discussionID, NoteID: 202}
	fileDiffRefusal   = &url.Error{Op: "Get", URL: gitlabBaseURL + "/api/v4/projects/1/merge_requests/1/diffs", Err: guard.ErrForbidden}
)

func lineCode(oldPos, newPos int) string {
	return cmdMainGoPathSHA1 + "_" + strconv.Itoa(oldPos) + "_" + strconv.Itoa(newPos)
}

func positionOf(line LineInput, oldPath string, lineRange *gitlab.LineRange) gitlab.Position {
	return gitlab.Position{DiffRefs: diffRefs, OldPath: oldPath, NewPath: line.NewPath, OldLine: line.OldLine, NewLine: line.NewLine, LineRange: lineRange}
}

func expectFileDiff(mergeRequestAPI *mocks.MockMergeRequestAPI, fileDiff string, err error) *gomock.Call {
	return mergeRequestAPI.EXPECT().FileDiff(gomock.Any(), pathRef, cmdMainGoPath).Return(fileDiff, err)
}

func TestCheckLine(t *testing.T) {
	tests := []struct {
		name    string
		line    LineInput
		wantErr error
	}{
		{name: "new_line", line: LineInput{NewPath: cmdMainGoPath, NewLine: 12}},
		{name: "old_line", line: LineInput{NewPath: cmdMainGoPath, OldLine: 3}},
		{name: "обе строки и old_path", line: LineInput{NewPath: cmdMainGoPath, OldPath: renamedFromPath, OldLine: 3, NewLine: 4}},
		{name: "многострочный", line: multiLine},

		{name: "без new_path", line: LineInput{NewLine: 12}, wantErr: ErrFieldRequired},
		{name: "new_path с переводом строки", line: LineInput{NewPath: cmdMainGoPath + "\n", NewLine: 12}, wantErr: ErrPathInvalid},
		{name: "old_path с нулевым байтом", line: LineInput{NewPath: cmdMainGoPath, OldPath: "a\x00b", NewLine: 12}, wantErr: ErrPathInvalid},
		{name: "new_line отрицательный", line: LineInput{NewPath: cmdMainGoPath, NewLine: -1}, wantErr: ErrLineInvalid},
		{name: "start_old_line отрицательный", line: LineInput{NewPath: cmdMainGoPath, NewLine: 3, StartOldLine: -1}, wantErr: ErrLineInvalid},
		{name: "без строк", line: LineInput{NewPath: cmdMainGoPath}, wantErr: ErrLineRequired},
		{name: "только первая строка диапазона", line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 1}, wantErr: ErrLineRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkLine(tt.line)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestBuildPosition(t *testing.T) {
	tests := []struct {
		name            string
		fileDiff        string
		line            LineInput
		wantLineNumbers diff.Target
		wantLineRange   *gitlab.LineRange
		wantErr         error
	}{
		{
			name: "неизменённая → неизменённая", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 1, StartNewLine: 1, OldLine: 5, NewLine: 5}, wantLineNumbers: diff.Target{OldLine: 5, NewLine: 5},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(1, 1), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(5, 5), Type: "old"},
			},
		},
		{
			name: "неизменённая → удалённая", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 1, StartNewLine: 1, OldLine: 3}, wantLineNumbers: diff.Target{OldLine: 3},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(1, 1), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(3, 2), Type: "old"},
			},
		},
		{name: "неизменённая → добавленная", fileDiff: twoHunksFileDiff, line: multiLine, wantLineNumbers: diff.Target{NewLine: 3}, wantLineRange: &multiLineRange},
		{
			name: "удалённая → неизменённая", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 2, OldLine: 4, NewLine: 4}, wantLineNumbers: diff.Target{OldLine: 4, NewLine: 4},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(2, 2), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(4, 4), Type: "old"},
			},
		},
		{
			name: "удалённая → удалённая", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 2, OldLine: 3}, wantLineNumbers: diff.Target{OldLine: 3},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(2, 2), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(3, 2), Type: "old"},
			},
		},
		{
			name: "удалённая → добавленная", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 2, NewLine: 2}, wantLineNumbers: diff.Target{NewLine: 2},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(2, 2), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(4, 2), Type: "new"},
			},
		},
		{
			name: "добавленная → неизменённая", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 2, OldLine: 5, NewLine: 5}, wantLineNumbers: diff.Target{OldLine: 5, NewLine: 5},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(4, 2), Type: "new"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(5, 5), Type: "old"},
			},
		},
		{
			name: "добавленная → добавленная", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 2, NewLine: 3}, wantLineNumbers: diff.Target{NewLine: 3},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(4, 2), Type: "new"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(4, 3), Type: "new"},
			},
		},
		{
			name: "добавленная → удалённая (второй хунк)", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 11, OldLine: 11}, wantLineNumbers: diff.Target{OldLine: 11},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(11, 11), Type: "new"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(11, 12), Type: "old"},
			},
		},
		{
			name: "диапазон через границу хунков", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 5, StartNewLine: 5, OldLine: 12, NewLine: 12}, wantLineNumbers: diff.Target{OldLine: 12, NewLine: 12},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(5, 5), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(12, 12), Type: "old"},
			},
		},
		{
			name: "новый файл: old 0 в line_code", fileDiff: "@@ -0,0 +1,2 @@\n+a\n+b\n", line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 1, NewLine: 2}, wantLineNumbers: diff.Target{NewLine: 2},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(0, 1), Type: "new"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(0, 2), Type: "new"},
			},
		},
		{
			name: "удалённый файл: new 0 в line_code", fileDiff: "@@ -1,2 +0,0 @@\n-a\n-b\n", line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 1, OldLine: 2}, wantLineNumbers: diff.Target{OldLine: 2},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(1, 0), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(2, 0), Type: "old"},
			},
		},

		{
			name: "неизменённая → неизменённая только с new_line: old_line из дифа", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 1, StartNewLine: 1, NewLine: 5}, wantLineNumbers: diff.Target{OldLine: 5, NewLine: 5},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(1, 1), Type: "old"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(5, 5), Type: "old"},
			},
		},
		{
			name: "добавленная → неизменённая только с old_line: new_line из дифа", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 2, OldLine: 4}, wantLineNumbers: diff.Target{OldLine: 4, NewLine: 4},
			wantLineRange: &gitlab.LineRange{
				Start: gitlab.LineRangeEnd{LineCode: lineCode(4, 2), Type: "new"},
				End:   gitlab.LineRangeEnd{LineCode: lineCode(4, 4), Type: "old"},
			},
		},
		{name: "одна неизменённая строка только с new_line: old_line из дифа", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, NewLine: 12}, wantLineNumbers: diff.Target{OldLine: 12, NewLine: 12}},
		{name: "одна неизменённая строка только с old_line: new_line из дифа", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, OldLine: 4}, wantLineNumbers: diff.Target{OldLine: 4, NewLine: 4}},
		{name: "одна добавленная строка: номер как есть", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, NewLine: 11}, wantLineNumbers: diff.Target{NewLine: 11}},
		{name: "одна удалённая строка: номер как есть", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, OldLine: 11}, wantLineNumbers: diff.Target{OldLine: 11}},

		{name: "первая после последней", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 3, OldLine: 1, NewLine: 1}, wantErr: diff.ErrLineRangeOrder},
		{name: "первая не в дифе", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 7, NewLine: 3}, wantErr: diff.ErrLineNotInDiff},
		{name: "удалённая строка с new_line не в дифе", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, StartOldLine: 11, StartNewLine: 12, OldLine: 12, NewLine: 12}, wantErr: diff.ErrLineNotInDiff},
		{name: "пустой диф", fileDiff: "", line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 1, NewLine: 2}, wantErr: diff.ErrLineNotInDiff},
		{name: "кривой диф", fileDiff: "garbage\n", line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 1, NewLine: 2}, wantErr: ErrDiffUnreadable},
		{name: "диф из пробелов", fileDiff: " \n", line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 1, NewLine: 2}, wantErr: ErrDiffUnreadable},
		{name: "бинарный файл", fileDiff: "Binary files a/logo.png and b/logo.png differ\n", line: LineInput{NewPath: cmdMainGoPath, StartNewLine: 1, NewLine: 2}, wantErr: ErrDiffUnreadable},
		{name: "одна строка не в дифе", fileDiff: twoHunksFileDiff, line: LineInput{NewPath: cmdMainGoPath, NewLine: 7}, wantErr: diff.ErrLineNotInDiff},
		{name: "одна строка, бинарный файл", fileDiff: "Binary files a/logo.png and b/logo.png differ\n", line: LineInput{NewPath: cmdMainGoPath, NewLine: 1}, wantErr: ErrDiffUnreadable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeRequestAPI := mocks.NewMockMergeRequestAPI(gomock.NewController(t))
			expectFileDiff(mergeRequestAPI, tt.fileDiff, nil)

			got, err := New(mergeRequestAPI, toolConfig).buildPosition(t.Context(), pathRef, gitlab.MergeRequest{WebURL: webURL, DiffRefs: diffRefs}, tt.line)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, gitlab.Position{
				DiffRefs:  diffRefs,
				OldPath:   cmdMainGoPath,
				NewPath:   cmdMainGoPath,
				OldLine:   tt.wantLineNumbers.OldLine,
				NewLine:   tt.wantLineNumbers.NewLine,
				LineRange: tt.wantLineRange,
			}, got)
		})
	}
}

func TestAddMRLineComment(t *testing.T) {
	lineCommentOutput := AddMRLineCommentOutput{DiscussionID: discussionID, NoteID: 202, URL: webURL + "#note_202"}
	renamedUnchangedLineOldOnly := LineInput{NewPath: cmdMainGoPath, OldPath: renamedFromPath, OldLine: 5}
	renamedUnchangedLine := LineInput{NewPath: cmdMainGoPath, OldPath: renamedFromPath, OldLine: 5, NewLine: 5}
	unchangedLineNewOnly := LineInput{NewPath: cmdMainGoPath, NewLine: 12}
	addedLine := LineInput{NewPath: cmdMainGoPath, NewLine: 11}
	rangeToUnchangedNewOnly := LineInput{NewPath: cmdMainGoPath, StartOldLine: 1, StartNewLine: 1, NewLine: 5}
	rangeToUnchanged := LineInput{NewPath: cmdMainGoPath, StartOldLine: 1, StartNewLine: 1, OldLine: 5, NewLine: 5}
	rangeToUnchangedLineRange := gitlab.LineRange{
		Start: gitlab.LineRangeEnd{LineCode: lineCode(1, 1), Type: "old"},
		End:   gitlab.LineRangeEnd{LineCode: lineCode(5, 5), Type: "old"},
	}
	tests := []struct {
		name    string
		input   AddMRLineCommentInput
		expect  func(mergeRequestAPI *mocks.MockMergeRequestAPI)
		want    AddMRLineCommentOutput
		wantErr error
	}{
		{
			name:  "одна строка с обоими номерами → диф не нужен, position без line_range, old_path = new_path",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: unchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(unchangedLine, cmdMainGoPath, nil)).Return(createdDiscussion, nil),
				)
			},
			want: lineCommentOutput,
		},
		{
			name:  "old_path передан → уходит как есть",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: renamedUnchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(renamedUnchangedLine, renamedFromPath, nil)).Return(createdDiscussion, nil),
				)
			},
			want: lineCommentOutput,
		},
		{
			name:  "переименованный файл, неизменённая строка только с old_line → диф по new_path, old_path как передан, new_line из дифа",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: renamedUnchangedLineOldOnly},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(renamedUnchangedLine, renamedFromPath, nil)).Return(createdDiscussion, nil),
				)
			},
			want: lineCommentOutput,
		},
		{
			name:  "неизменённая строка только с new_line → диф файла, old_line из него",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: unchangedLineNewOnly},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(unchangedLine, cmdMainGoPath, nil)).Return(createdDiscussion, nil),
				)
			},
			want: lineCommentOutput,
		},
		{
			name:  "добавленная строка → диф файла, номер как есть",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: addedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(addedLine, cmdMainGoPath, nil)).Return(createdDiscussion, nil),
				)
			},
			want: lineCommentOutput,
		},
		{
			name:  "многострочный → диф файла, line_range из него",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: multiLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(multiLine, cmdMainGoPath, &multiLineRange)).Return(createdDiscussion, nil),
				)
			},
			want: lineCommentOutput,
		},
		{
			name:  "многострочный до неизменённой строки только с new_line → old_line из дифа",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: rangeToUnchangedNewOnly},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(rangeToUnchanged, cmdMainGoPath, &rangeToUnchangedLineRange)).Return(createdDiscussion, nil),
				)
			},
			want: lineCommentOutput,
		},
		{
			name:  "MR недоступен → ErrMergeRequestUnavailable, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: unchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(gitlab.MergeRequest{}, gitlab.ErrMergeRequestUnavailable)
			},
			wantErr: gitlab.ErrMergeRequestUnavailable,
		},
		{
			name:  "MR без diff_refs → ErrDiffRefsMissing, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: unchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(mergeRequestWithoutDiffRefs, nil)
			},
			wantErr: ErrDiffRefsMissing,
		},
		{
			name:  "диф файла недоступен → ErrDiffUnavailable, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: multiLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, "", gitlab.ErrDiffUnavailable),
				)
			},
			wantErr: gitlab.ErrDiffUnavailable,
		},
		{
			name:  "одна строка с одним номером, диф файла недоступен → ErrDiffUnavailable, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: unchangedLineNewOnly},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, "", gitlab.ErrDiffUnavailable),
				)
			},
			wantErr: gitlab.ErrDiffUnavailable,
		},
		{
			name:  "отказ guard при чтении дифа → ErrForbidden как есть, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: multiLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, "", fileDiffRefusal),
				)
			},
			wantErr: guard.ErrForbidden,
		},
		{
			name:  "диф бинарного файла → ErrDiffUnreadable, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: multiLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, "Binary files a/cmd/main.go and b/cmd/main.go differ\n", nil),
				)
			},
			wantErr: ErrDiffUnreadable,
		},
		{
			name:  "первая строка не в дифе → ErrLineNotInDiff, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewPath: cmdMainGoPath, StartOldLine: 7, NewLine: 3}},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
				)
			},
			wantErr: diff.ErrLineNotInDiff,
		},
		{
			name:  "одна строка с одним номером не в дифе → ErrLineNotInDiff, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewPath: cmdMainGoPath, NewLine: 7}},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
				)
			},
			wantErr: diff.ErrLineNotInDiff,
		},
		{
			name:  "удалённая строка с new_line → ErrLineNotInDiff, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewPath: cmdMainGoPath, StartOldLine: 11, StartNewLine: 12, OldLine: 12, NewLine: 12}},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
				)
			},
			wantErr: diff.ErrLineNotInDiff,
		},
		{
			name:  "первая после последней → ErrLineRangeOrder, записи нет",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewPath: cmdMainGoPath, StartNewLine: 3, OldLine: 1, NewLine: 1}},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
				)
			},
			wantErr: diff.ErrLineRangeOrder,
		},
		{
			name:  "GitLab отклонил позицию → StatusError как есть",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: unchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(unchangedLine, cmdMainGoPath, nil)).Return(gitlab.Discussion{}, statusNotFound),
				)
			},
			wantErr: statusNotFound,
		},
		{
			name:  "запись не подтверждена → ErrCommentUnconfirmed как есть",
			input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: unchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDiscussion(gomock.Any(), pathRef, noteText, positionOf(unchangedLine, cmdMainGoPath, nil)).Return(gitlab.Discussion{}, gitlab.ErrCommentUnconfirmed),
				)
			},
			wantErr: gitlab.ErrCommentUnconfirmed,
		},

		{name: "проект из одного сегмента", input: AddMRLineCommentInput{Project: "group", MRIID: mrIID, Body: noteText, LineInput: unchangedLine}, wantErr: ErrProjectInvalid},
		{name: "mr_iid 0", input: AddMRLineCommentInput{Project: project, Body: noteText, LineInput: unchangedLine}, wantErr: ErrMRIIDInvalid},
		{name: "пустой текст", input: AddMRLineCommentInput{Project: project, MRIID: mrIID, LineInput: unchangedLine}, wantErr: ErrFieldRequired},
		{name: "quick action", input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: "/approve", LineInput: unchangedLine}, wantErr: ErrQuickAction},
		{name: "без new_path", input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewLine: 12}}, wantErr: ErrFieldRequired},
		{name: "без строк", input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewPath: cmdMainGoPath}}, wantErr: ErrLineRequired},
		{name: "отрицательная строка", input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewPath: cmdMainGoPath, NewLine: -1}}, wantErr: ErrLineInvalid},
		{name: "путь с управляющим символом", input: AddMRLineCommentInput{Project: project, MRIID: mrIID, Body: noteText, LineInput: LineInput{NewPath: "a\tb", NewLine: 1}}, wantErr: ErrPathInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeRequestAPI := mocks.NewMockMergeRequestAPI(gomock.NewController(t))
			if tt.expect != nil {
				tt.expect(mergeRequestAPI)
			}

			got, err := New(mergeRequestAPI, toolConfig).AddMRLineComment(t.Context(), tt.input)

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

func TestAddDraftNote(t *testing.T) {
	draftOutput := AddDraftNoteOutput{DraftNoteID: 404, URL: webURL}
	unchangedLinePosition := positionOf(unchangedLine, cmdMainGoPath, nil)
	multiLinePosition := positionOf(multiLine, cmdMainGoPath, &multiLineRange)
	tests := []struct {
		name    string
		input   AddDraftNoteInput
		expect  func(mergeRequestAPI *mocks.MockMergeRequestAPI)
		want    AddDraftNoteOutput
		wantErr error
	}{
		{
			name:  "общий черновик",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText}).Return(gitlab.Draft{ID: 404}, nil),
				)
			},
			want: draftOutput,
		},
		{
			name:  "общий черновик к MR без diff_refs — позиция не нужна",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(mergeRequestWithoutDiffRefs, nil),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText}).Return(gitlab.Draft{ID: 404}, nil),
				)
			},
			want: draftOutput,
		},
		{
			name:  "черновик к строке",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, LineInput: unchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText, Position: &unchangedLinePosition}).Return(gitlab.Draft{ID: 404}, nil),
				)
			},
			want: draftOutput,
		},
		{
			name:  "черновик к неизменённой строке только с new_line → диф файла, old_line из него",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, LineInput: LineInput{NewPath: cmdMainGoPath, NewLine: 12}},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText, Position: &unchangedLinePosition}).Return(gitlab.Draft{ID: 404}, nil),
				)
			},
			want: draftOutput,
		},
		{
			name:  "многострочный черновик",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, LineInput: multiLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText, Position: &multiLinePosition}).Return(gitlab.Draft{ID: 404}, nil),
				)
			},
			want: draftOutput,
		},
		{
			name:  "черновик-ответ в дискуссию",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, InReplyToDiscussionID: discussionID},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText, InReplyToDiscussionID: discussionID}).Return(gitlab.Draft{ID: 404}, nil),
				)
			},
			want: draftOutput,
		},
		{
			name:  "MR недоступен → записи нет",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(gitlab.MergeRequest{}, gitlab.ErrMergeRequestUnavailable)
			},
			wantErr: gitlab.ErrMergeRequestUnavailable,
		},
		{
			name:  "MR без diff_refs при позиции → ErrDiffRefsMissing, записи нет",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, LineInput: unchangedLine},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				mergeRequestAPI.EXPECT().MergeRequest(gomock.Any(), pathRef).Return(mergeRequestWithoutDiffRefs, nil)
			},
			wantErr: ErrDiffRefsMissing,
		},
		{
			name:  "многострочный черновик, строки нет в дифе → ErrLineNotInDiff, записи нет",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, LineInput: LineInput{NewPath: cmdMainGoPath, StartOldLine: 7, NewLine: 3}},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					expectFileDiff(mergeRequestAPI, twoHunksFileDiff, nil),
				)
			},
			wantErr: diff.ErrLineNotInDiff,
		},
		{
			name:  "ошибка записи как есть",
			input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText},
			expect: func(mergeRequestAPI *mocks.MockMergeRequestAPI) {
				gomock.InOrder(
					expectMergeRequest(mergeRequestAPI),
					mergeRequestAPI.EXPECT().AddDraftNote(gomock.Any(), pathRef, gitlab.DraftNoteRequest{Note: noteText}).Return(gitlab.Draft{}, gitlab.ErrCommentUnconfirmed),
				)
			},
			wantErr: gitlab.ErrCommentUnconfirmed,
		},

		{name: "позиция и in_reply_to_discussion_id вместе", input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, InReplyToDiscussionID: discussionID, LineInput: unchangedLine}, wantErr: ErrDraftTarget},
		{name: "in_reply_to_discussion_id не по шаблону", input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, InReplyToDiscussionID: "abc"}, wantErr: ErrDiscussionIDInvalid},
		{name: "in_reply_to_discussion_id в верхнем регистре", input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, InReplyToDiscussionID: strings.ToUpper(discussionID)}, wantErr: ErrDiscussionIDInvalid},
		{name: "пустой note", input: AddDraftNoteInput{Project: project, MRIID: mrIID}, wantErr: ErrFieldRequired},
		{name: "note с quick action", input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: "LGTM\n/approve"}, wantErr: ErrQuickAction},
		{name: "позиция без строк", input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, LineInput: LineInput{NewPath: cmdMainGoPath}}, wantErr: ErrLineRequired},
		{name: "строка без new_path", input: AddDraftNoteInput{Project: project, MRIID: mrIID, Note: noteText, LineInput: LineInput{NewLine: 3}}, wantErr: ErrFieldRequired},
		{name: "проект из одного сегмента", input: AddDraftNoteInput{Project: "group", MRIID: mrIID, Note: noteText}, wantErr: ErrProjectInvalid},
		{name: "mr_iid 0", input: AddDraftNoteInput{Project: project, Note: noteText}, wantErr: ErrMRIIDInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeRequestAPI := mocks.NewMockMergeRequestAPI(gomock.NewController(t))
			if tt.expect != nil {
				tt.expect(mergeRequestAPI)
			}

			got, err := New(mergeRequestAPI, toolConfig).AddDraftNote(t.Context(), tt.input)

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
