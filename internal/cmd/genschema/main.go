package main

import (
	"bytes"
	"flag"
	"go/doc"
	"go/format"
	"io"
	"log"
	"os"
	"strings"
	"text/template"

	_ "embed"

	"github.com/diamondburned/go-buttplugio/internal/buttplugschema"
)

var (
	//go:embed tmpl-go.tmpl
	tmplGo string
)

var (
	ref = "buttplug-5.0.1"
	pkg = "buttplug" // TODO
	out = "buttplug-generated.go"
)

func main() {
	flag.StringVar(&ref, "ref", ref, "branch/commit to fetch schema from")
	flag.Parse()

	s, err := buttplugschema.DownloadRaw(ref)
	if err != nil {
		log.Fatalln("cannot download schema:", err)
	}

	tmpl := template.New("")
	tmpl = tmpl.Funcs(template.FuncMap{
		"Comment": comment,
	})
	tmpl = template.Must(tmpl.Parse(tmplGo))

	var buf bytes.Buffer

	render := newRenderer(tmpl, &buf)
	render("", nil)

	p := buttplugschema.Parse(s)
	render("messages", p.Messages)

	for _, typ := range p.Types {
		switch typ := typ.(type) {
		case buttplugschema.IntegerType:
			render("integer", typ)
		case buttplugschema.NumberType:
			render("number", typ)
		case buttplugschema.StringType:
			render("string", typ)
		case buttplugschema.ObjectType:
			render("object", typ)
		case buttplugschema.ArrayType:
			render("array", typ)
		case buttplugschema.BooleanType:
			render("boolean", typ)
		default:
			log.Printf("not rendering %T", typ)
		}
	}

	o, err := format.Source(buf.Bytes())
	if err != nil {
		log.Println("cannot format:", err)
		// Fail later.
		defer os.Exit(1)
	}

	if out != "-" {
		if err := os.WriteFile(out, o, os.ModePerm); err != nil {
			log.Fatalln("cannot write output:", err)
		}
	} else {
		os.Stdout.Write(o)
	}
}

func newRenderer(t *template.Template, w io.Writer) func(string, interface{}) {
	return func(name string, v interface{}) {
		if err := t.ExecuteTemplate(w, name, v); err != nil {
			log.Panicf("cannot render %s: %v", name, err)
		}
	}
}

const (
	CommentsColumnLimit = 80 - 3 // account for prefix "// "
	CommentsTabWidth    = 4
)

func comment(cmt string, indentLvl int) string {
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
