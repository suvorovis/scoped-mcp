package diff

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Kind int

const (
	Context Kind = iota
	Added
	Removed
)

type Line struct {
	Kind   Kind
	OldPos int
	NewPos int
}

type Target struct {
	OldLine int
	NewLine int
}

type Range struct {
	First Line
	Last  Line
}

const (
	lineTypeNew = "new"
	lineTypeOld = "old"
)

var (
	ErrMalformed      = errors.New("diff: malformed diff text")
	ErrLineNotInDiff  = errors.New("diff: the line is not in the diff of this file")
	ErrLineRangeOrder = errors.New("diff: the first line of the range comes after the last one")

	hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
)

func ParseHunks(text string) ([]Line, error) {
	if text == "" {
		return nil, nil
	}
	var lines []Line
	var oldPos, newPos int
	inHunk := false
	for _, raw := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if strings.HasPrefix(raw, "@@") {
			var err error
			oldPos, newPos, err = parseHunkHeader(raw)
			if err != nil {
				return nil, err
			}
			inHunk = true
			continue
		}
		if !inHunk {
			return nil, fmt.Errorf("%w: %q before the first hunk header", ErrMalformed, raw)
		}
		switch {
		case strings.HasPrefix(raw, `\`):
			continue
		case strings.HasPrefix(raw, "+"):
			lines = append(lines, Line{Kind: Added, OldPos: oldPos, NewPos: newPos})
			newPos++
		case strings.HasPrefix(raw, "-"):
			lines = append(lines, Line{Kind: Removed, OldPos: oldPos, NewPos: newPos})
			oldPos++
		default:
			lines = append(lines, Line{Kind: Context, OldPos: oldPos, NewPos: newPos})
			oldPos++
			newPos++
		}
	}
	return lines, nil
}

func parseHunkHeader(raw string) (oldPos, newPos int, err error) {
	header := hunkHeaderPattern.FindStringSubmatch(raw)
	if header == nil {
		return 0, 0, fmt.Errorf("%w: hunk header %q", ErrMalformed, raw)
	}
	oldPos, oldErr := strconv.Atoi(header[1])
	newPos, newErr := strconv.Atoi(header[2])
	if oldErr != nil || newErr != nil {
		return 0, 0, fmt.Errorf("%w: hunk header %q", ErrMalformed, raw)
	}
	return oldPos, newPos, nil
}

func (l Line) Matches(target Target) bool {
	if target.OldLine == 0 && target.NewLine == 0 {
		return false
	}
	if target.OldLine != 0 && !l.isOldLine(target.OldLine) {
		return false
	}
	if target.NewLine != 0 && !l.isNewLine(target.NewLine) {
		return false
	}
	return true
}

func (l Line) isOldLine(number int) bool {
	return l.Kind != Added && l.OldPos == number
}

func (l Line) isNewLine(number int) bool {
	return l.Kind != Removed && l.NewPos == number
}

func FindRange(lines []Line, first, last Target) (Range, error) {
	firstIndex, err := indexOf(lines, first)
	if err != nil {
		return Range{}, err
	}
	lastIndex, err := indexOf(lines, last)
	if err != nil {
		return Range{}, err
	}
	if firstIndex > lastIndex {
		return Range{}, fmt.Errorf("%w: first old_line %d, new_line %d; last old_line %d, new_line %d",
			ErrLineRangeOrder, first.OldLine, first.NewLine, last.OldLine, last.NewLine)
	}
	return Range{First: lines[firstIndex], Last: lines[lastIndex]}, nil
}

func indexOf(lines []Line, target Target) (int, error) {
	index := slices.IndexFunc(lines, func(line Line) bool { return line.Matches(target) })
	if index < 0 {
		return 0, fmt.Errorf("%w: old_line %d, new_line %d", ErrLineNotInDiff, target.OldLine, target.NewLine)
	}
	return index, nil
}

func LineCode(newPath string, line Line) string {
	sum := sha1.Sum([]byte(newPath))
	return fmt.Sprintf("%s_%d_%d", hex.EncodeToString(sum[:]), line.OldPos, line.NewPos)
}

func (l Line) LineRangeType() string {
	if l.Kind == Added {
		return lineTypeNew
	}
	return lineTypeOld
}
