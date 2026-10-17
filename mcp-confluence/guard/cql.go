package guard

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	cqlParam          = "cql"
	textClauseOpening = ` and type = page and text ~ "`
	textClauseClosing = `"`
)

var (
	ErrInvalidSearchQuery = errors.New("guard: search query must be non-empty valid UTF-8 without control characters or line breaks")

	cqlStringEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
)

func SearchPrefix(roots []string) string {
	terms := make([]string, 0, 2*len(roots))
	for _, root := range roots {
		terms = append(terms, "id = "+root, "ancestor = "+root)
	}
	return "(" + strings.Join(terms, " or ") + ")"
}

func SearchCQL(roots []string, query string) (string, error) {
	if !isSearchableText(query) {
		return "", ErrInvalidSearchQuery
	}
	return searchCQLOpening(roots) + cqlStringEscaper.Replace(query) + textClauseClosing, nil
}

func searchCQLOpening(roots []string) string {
	return SearchPrefix(roots) + textClauseOpening
}

func (t *Transport) authorizeSearch(req *http.Request) (*http.Request, error) {
	rules := queryRules{
		cqlParam:   {rule: t.isSearchCQL, required: true},
		limitParam: {rule: limitWithin(t.maxSearchResults), required: true},
	}
	if err := checkQuery(req.URL, rules); err != nil {
		return nil, err
	}
	if err := checkReadOnlyRequest(req); err != nil {
		return nil, err
	}
	return req, nil
}

func (t *Transport) isSearchCQL(cql string) bool {
	rest, hasPrefix := strings.CutPrefix(cql, t.searchCQLOpening)
	if !hasPrefix {
		return false
	}
	literal, hasSuffix := strings.CutSuffix(rest, textClauseClosing)
	return hasSuffix && isSearchableText(literal) && isEscapedCQLLiteral(literal)
}

func isSearchableText(text string) bool {
	return text != "" && utf8.ValidString(text) && !strings.ContainsFunc(text, isControlOrLineBreak)
}

func isControlOrLineBreak(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp)
}

func isEscapedCQLLiteral(literal string) bool {
	escaped := false
	for _, r := range literal {
		if escaped {
			if r != '\\' && r != '"' {
				return false
			}
			escaped = false
			continue
		}
		if r == '"' {
			return false
		}
		escaped = r == '\\'
	}
	return !escaped
}
