package diff_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-gitlab/diff"
)

const (
	cmdMainGoPath     = "cmd/main.go"
	cmdMainGoPathSHA1 = "93310a20c22640d80343161f98be28beb14eb4e9"
)

var twoHunksText = strings.Join([]string{
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

var twoHunksLines = []diff.Line{
	{Kind: diff.Context, OldPos: 1, NewPos: 1},
	{Kind: diff.Removed, OldPos: 2, NewPos: 2},
	{Kind: diff.Removed, OldPos: 3, NewPos: 2},
	{Kind: diff.Added, OldPos: 4, NewPos: 2},
	{Kind: diff.Added, OldPos: 4, NewPos: 3},
	{Kind: diff.Context, OldPos: 4, NewPos: 4},
	{Kind: diff.Context, OldPos: 5, NewPos: 5},
	{Kind: diff.Context, OldPos: 10, NewPos: 10},
	{Kind: diff.Added, OldPos: 11, NewPos: 11},
	{Kind: diff.Removed, OldPos: 11, NewPos: 12},
	{Kind: diff.Context, OldPos: 12, NewPos: 12},
}

func TestParseHunks(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    []diff.Line
		wantErr error
	}{
		{name: "два хунка: контекст, удалённые, добавленные", text: twoHunksText, want: twoHunksLines},
		{name: "новый файл: old 0 у добавленных строк", text: "@@ -0,0 +1,2 @@\n+a\n+b\n", want: []diff.Line{
			{Kind: diff.Added, OldPos: 0, NewPos: 1},
			{Kind: diff.Added, OldPos: 0, NewPos: 2},
		}},
		{name: "удалённый файл: new 0 у удалённых строк", text: "@@ -1,2 +0,0 @@\n-a\n-b\n", want: []diff.Line{
			{Kind: diff.Removed, OldPos: 1, NewPos: 0},
			{Kind: diff.Removed, OldPos: 2, NewPos: 0},
		}},
		{name: "маркер No newline пропускается", text: "@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+a\n\\ No newline at end of file\n", want: []diff.Line{
			{Kind: diff.Removed, OldPos: 1, NewPos: 1},
			{Kind: diff.Added, OldPos: 2, NewPos: 1},
		}},
		{name: "заголовок без счётчиков", text: "@@ -3 +4 @@\n x\n", want: []diff.Line{{Kind: diff.Context, OldPos: 3, NewPos: 4}}},
		{name: "заголовок с хвостом функции", text: "@@ -3,1 +4,1 @@ func main() {\n x\n", want: []diff.Line{{Kind: diff.Context, OldPos: 3, NewPos: 4}}},
		{name: "пустая строка — контекст", text: "@@ -1,2 +1,2 @@\n\n b\n", want: []diff.Line{
			{Kind: diff.Context, OldPos: 1, NewPos: 1},
			{Kind: diff.Context, OldPos: 2, NewPos: 2},
		}},
		{name: "без завершающего перевода строки", text: "@@ -1 +1 @@\n x", want: []diff.Line{{Kind: diff.Context, OldPos: 1, NewPos: 1}}},
		{name: "строки с CRLF", text: "@@ -1 +1 @@\r\n x\r\n", want: []diff.Line{{Kind: diff.Context, OldPos: 1, NewPos: 1}}},
		{name: "маркер No newline перед вторым хунком", text: "@@ -1 +1 @@\n-a\n\\ No newline at end of file\n@@ -10 +10 @@\n x\n", want: []diff.Line{
			{Kind: diff.Removed, OldPos: 1, NewPos: 1},
			{Kind: diff.Context, OldPos: 10, NewPos: 10},
		}},
		{name: "текст кончается пустой строкой контекста", text: "@@ -1,2 +1,2 @@\n a\n\n", want: []diff.Line{
			{Kind: diff.Context, OldPos: 1, NewPos: 1},
			{Kind: diff.Context, OldPos: 2, NewPos: 2},
		}},
		{name: "пустой диф", text: ""},

		{name: "кривой заголовок", text: "@@ bad @@\n x\n", wantErr: diff.ErrMalformed},
		{name: "начало хунка не помещается в int", text: "@@ -99999999999999999999 +1 @@\n x\n", wantErr: diff.ErrMalformed},
		{name: "текст до первого заголовка", text: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n x\n", wantErr: diff.ErrMalformed},
		{name: "строка без заголовка", text: "+a\n", wantErr: diff.ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := diff.ParseHunks(tt.text)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFindRange(t *testing.T) {
	tests := []struct {
		name    string
		first   diff.Target
		last    diff.Target
		want    diff.Range
		wantErr error
	}{
		{name: "от контекста до добавленной", first: diff.Target{OldLine: 1, NewLine: 1}, last: diff.Target{NewLine: 3}, want: diff.Range{First: diff.Line{Kind: diff.Context, OldPos: 1, NewPos: 1}, Last: diff.Line{Kind: diff.Added, OldPos: 4, NewPos: 3}}},
		{name: "от удалённой до удалённой", first: diff.Target{OldLine: 2}, last: diff.Target{OldLine: 3}, want: diff.Range{First: diff.Line{Kind: diff.Removed, OldPos: 2, NewPos: 2}, Last: diff.Line{Kind: diff.Removed, OldPos: 3, NewPos: 2}}},
		{name: "диапазон через границу хунков", first: diff.Target{OldLine: 5, NewLine: 5}, last: diff.Target{OldLine: 12, NewLine: 12}, want: diff.Range{First: diff.Line{Kind: diff.Context, OldPos: 5, NewPos: 5}, Last: diff.Line{Kind: diff.Context, OldPos: 12, NewPos: 12}}},
		{name: "одна строка: добавленная по new_line", first: diff.Target{NewLine: 3}, last: diff.Target{NewLine: 3}, want: singleLineRange(diff.Line{Kind: diff.Added, OldPos: 4, NewPos: 3})},
		{name: "одна строка: удалённая по old_line", first: diff.Target{OldLine: 3}, last: diff.Target{OldLine: 3}, want: singleLineRange(diff.Line{Kind: diff.Removed, OldPos: 3, NewPos: 2})},
		{name: "одна строка: контекст по old_line и new_line", first: diff.Target{OldLine: 5, NewLine: 5}, last: diff.Target{OldLine: 5, NewLine: 5}, want: singleLineRange(diff.Line{Kind: diff.Context, OldPos: 5, NewPos: 5})},
		{name: "одна строка: контекст только по new_line", first: diff.Target{NewLine: 4}, last: diff.Target{NewLine: 4}, want: singleLineRange(diff.Line{Kind: diff.Context, OldPos: 4, NewPos: 4})},
		{name: "одна строка: контекст только по old_line", first: diff.Target{OldLine: 5}, last: diff.Target{OldLine: 5}, want: singleLineRange(diff.Line{Kind: diff.Context, OldPos: 5, NewPos: 5})},
		{name: "одна строка: new_line 2 — добавленная строка, а не удалённые с тем же счётчиком", first: diff.Target{NewLine: 2}, last: diff.Target{NewLine: 2}, want: singleLineRange(diff.Line{Kind: diff.Added, OldPos: 4, NewPos: 2})},
		{name: "одна строка: old_line 4 — контекст, а не добавленные с тем же счётчиком", first: diff.Target{OldLine: 4}, last: diff.Target{OldLine: 4}, want: singleLineRange(diff.Line{Kind: diff.Context, OldPos: 4, NewPos: 4})},
		{name: "одна строка: из второго хунка", first: diff.Target{NewLine: 11}, last: diff.Target{NewLine: 11}, want: singleLineRange(diff.Line{Kind: diff.Added, OldPos: 11, NewPos: 11})},

		{name: "одна строка: удалённая строка с new_line не находится", first: diff.Target{OldLine: 11, NewLine: 12}, last: diff.Target{OldLine: 11, NewLine: 12}, wantErr: diff.ErrLineNotInDiff},
		{name: "одна строка: добавленная строка с old_line не находится", first: diff.Target{OldLine: 4, NewLine: 3}, last: diff.Target{OldLine: 4, NewLine: 3}, wantErr: diff.ErrLineNotInDiff},
		{name: "одна строка: строка вне хунков", first: diff.Target{OldLine: 7}, last: diff.Target{OldLine: 7}, wantErr: diff.ErrLineNotInDiff},
		{name: "одна строка: new_line за концом файла", first: diff.Target{NewLine: 99}, last: diff.Target{NewLine: 99}, wantErr: diff.ErrLineNotInDiff},
		{name: "одна строка: ничего не передано", first: diff.Target{}, last: diff.Target{}, wantErr: diff.ErrLineNotInDiff},
		{name: "первая после последней", first: diff.Target{NewLine: 3}, last: diff.Target{OldLine: 1, NewLine: 1}, wantErr: diff.ErrLineRangeOrder},
		{name: "первая не найдена", first: diff.Target{OldLine: 7}, last: diff.Target{NewLine: 3}, wantErr: diff.ErrLineNotInDiff},
		{name: "последняя не найдена", first: diff.Target{NewLine: 3}, last: diff.Target{NewLine: 99}, wantErr: diff.ErrLineNotInDiff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := diff.FindRange(twoHunksLines, tt.first, tt.last)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func singleLineRange(line diff.Line) diff.Range {
	return diff.Range{First: line, Last: line}
}

func TestLineCodeAndLineRangeType(t *testing.T) {
	tests := []struct {
		name              string
		line              diff.Line
		wantLineCode      string
		wantLineRangeType string
	}{
		{name: "контекст", line: diff.Line{Kind: diff.Context, OldPos: 1, NewPos: 1}, wantLineCode: cmdMainGoPathSHA1 + "_1_1", wantLineRangeType: "old"},
		{name: "удалённая", line: diff.Line{Kind: diff.Removed, OldPos: 3, NewPos: 2}, wantLineCode: cmdMainGoPathSHA1 + "_3_2", wantLineRangeType: "old"},
		{name: "добавленная", line: diff.Line{Kind: diff.Added, OldPos: 4, NewPos: 3}, wantLineCode: cmdMainGoPathSHA1 + "_4_3", wantLineRangeType: "new"},
		{name: "добавленная в новом файле", line: diff.Line{Kind: diff.Added, OldPos: 0, NewPos: 2}, wantLineCode: cmdMainGoPathSHA1 + "_0_2", wantLineRangeType: "new"},
		{name: "удалённая в удалённом файле", line: diff.Line{Kind: diff.Removed, OldPos: 2, NewPos: 0}, wantLineCode: cmdMainGoPathSHA1 + "_2_0", wantLineRangeType: "old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantLineCode, diff.LineCode(cmdMainGoPath, tt.line))
			require.Equal(t, tt.wantLineRangeType, tt.line.LineRangeType())
		})
	}
}
