package tmplutil

import (
	"go/doc"
	"io"
	"log"
	"strings"
	"sync"
	"text/template"
	"unicode"
	"unicode/utf8"
)

var (
	renderTmpls = map[string]*template.Template{}
	tmplMutex   sync.Mutex
)

// Funcs contains the exported tmplutil functions.
var Funcs = template.FuncMap{
	"Comment":     Comment,
	"FirstLetter": FirstLetter,
}

// Render renders the given template string with the given key-value pair.
func Render(w io.Writer, tmpl string, v interface{}) {
	tmpl = strings.TrimSpace(tmpl)

	tmplMutex.Lock()
	renderTmpl, ok := renderTmpls[tmpl]
	if !ok {
		renderTmpl = template.New("(anonymous)")
		renderTmpl = renderTmpl.Funcs(template.FuncMap{
			"Comment": Comment,
		})
		renderTmpl = template.Must(renderTmpl.Parse(tmpl))
		renderTmpls[tmpl] = renderTmpl
	}
	tmplMutex.Unlock()

	if err := renderTmpl.ExecuteTemplate(w, "(anonymous)", v); err != nil {
		log.Panicln("inline render fail:", err)
	}
}

// FirstLetter returns the first letter in lower-case.
func FirstLetter(p string) string {
	r, sz := utf8.DecodeRuneInString(p)
	if sz > 0 && r != utf8.RuneError {
		return string(unicode.ToLower(r))
	}

	return string(p[0]) // fallback
}

// String renders the given template to a string.
func String(tmpl string, v interface{}) string {
	var s strings.Builder
	Render(&s, tmpl, v)
	return s.String()
}

const (
	CommentsColumnLimit = 80 - 3 // account for prefix "// "
	CommentsTabWidth    = 4
)

// Comment renders a comment.
func Comment(cmt string, indentLvl int) string {
	preIndent := strings.Repeat("\t", indentLvl)
	// Account for the indentation in the column limit.
	col := CommentsColumnLimit - (CommentsTabWidth * indentLvl)

	cmt = docText(cmt, col)
	cmt = strings.TrimSpace(cmt)

	if cmt == "" {
		return ""
	}

	if cmt != "" {
		lines := strings.Split(cmt, "\n")
		for i, line := range lines {
			lines[i] = preIndent + "// " + line
		}

		cmt = strings.Join(lines, "\n")
	}

	return cmt
}

func docText(p string, col int) string {
	builder := strings.Builder{}
	builder.Grow(len(p) + 64)

	doc.ToText(&builder, p, "", "   ", col)
	return builder.String()
}
