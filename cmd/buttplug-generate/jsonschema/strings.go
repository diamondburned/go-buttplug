package jsonschema

import (
	"fmt"
	"go/doc"
	"go/doc/comment"
	"strings"

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
		cmt = fmt.Sprintf("%s: %s", prefix, cmt)
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
