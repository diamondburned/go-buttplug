package jsonschema

import (
	"fmt"
	"go/doc"
	"go/doc/comment"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/gir/girgen/strcases"
)

// FormatIdentifier formats an identifier to Go case.
func FormatIdentifier(ident string) string {
	// rust uses pascal case.
	return strcases.PascalToGo(ident)
}

const (
	commentsColumnLimit = 80 - len("// ")
	commentsTabWidth    = 4
)

// FormatComment formats a comment as Go code.
func FormatComment(cmt, prefix string, indentLvl int) string {
	if cmt == "" {
		return ""
	}

	if !strings.HasPrefix(cmt, prefix) && !strings.HasPrefix(cmt, FormatIdentifier(prefix)) {
		prefix = FormatIdentifier(prefix)
		cmt = lowerFirstLetter(cmt)
		cmt = fmt.Sprintf("%s: %s", prefix, cmt)
	}

	if !strings.Contains(cmt, "\n") && !strings.HasSuffix(cmt, ".") {
		cmt += "."
	}

	return WrapComment(cmt, indentLvl)
}

// WrapCommentTopLevel calls [WrapComment] with an indentation level of 0.
// It also joins multiple comment strings with spaces.
func WrapCommentTopLevel(cmts ...string) string {
	cmt := strings.Join(cmts, " ")
	return WrapComment(cmt, 0)
}

// WrapComment wraps a comment to the appropriate width.
func WrapComment(cmt string, indentLvl int) string {
	// Account for the indentation in the column limit.
	col := commentsColumnLimit - (commentsTabWidth * indentLvl)

	cmt = docText(cmt, col)
	cmt = strings.TrimSpace(cmt)
	cmt = markComment(cmt)

	return cmt
}

func markComment(cmt string) string {
	lines := strings.Split(cmt, "\n")
	for i, line := range lines {
		lines[i] = "// " + line
	}
	return strings.Join(lines, "\n")
}

func docText(p string, col int) string {
	d := new(doc.Package).Parser().Parse(p)
	pr := &comment.Printer{
		TextCodePrefix: "\t",
		TextWidth:      col,
	}
	return string(pr.Text(d))
}

// lowerFirstLetter lower-cases the first letter in the paragraph.
func lowerFirstLetter(p string) string {
	if p == "" {
		return ""
	}

	r1, r1w := utf8.DecodeRuneInString(p)
	if unicode.IsLower(r1) {
		return p
	}
	if r1w == len(p) {
		return strings.ToLower(p)
	}

	// Edge case: gTK, etc.
	if r2, _ := utf8.DecodeRuneInString(p[r1w:]); unicode.IsUpper(r2) {
		return p
	}

	return strings.ToLower(p[:r1w]) + p[r1w:]
}

var reVersionSuffix = regexp.MustCompile(`V[0-9]+$`)

// TrimVersion trims a version suffix like "V4" off a string.
// These suffixes appear throughout object names in the Buttplug schema.
func TrimVersion(str string) string {
	return reVersionSuffix.ReplaceAllString(str, "")
}
