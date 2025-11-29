package main

import (
	"fmt"
	"log/slog"

	j "github.com/dave/jennifer/jen"
	"github.com/diamondburned/go-buttplug/cmd/buttplug-generate/jsonschema"
)

type generator struct {
	file      *j.File
	queue     []*jsonschema.Schema
	generated map[jsonschema.SchemaLocation]struct{}
}

func newGenerator(file *j.File) *generator {
	return &generator{
		file:      file,
		queue:     make([]*jsonschema.Schema, 0, 12),
		generated: make(map[jsonschema.SchemaLocation]struct{}),
	}
}

func (gen *generator) enqueue(schemas ...*jsonschema.Schema) {
	gen.queue = append(gen.queue, schemas...)
}

func (gen *generator) generate(schema *jsonschema.Schema) {
	if !schema.Type().Is(jsonschema.ArrayType) {
		slog.Error(
			"expected root message type to be an array",
			"location", schema.Location(),
			"type", schema.Type())
		return
	}

	gen.file.ImportName("encoding/json/v2", "json")
	gen.file.ImportName("encoding/json/jsontext", "jsontext")
	gen.file.ImportName("github.com/diamondburned/go-buttplug/schema/ptr", "ptr")

	item := schema.Items()[0]
	gen.generateMessageSpec(item)

	for len(gen.queue) > 0 {
		schema := gen.queue[0]
		gen.queue = gen.queue[1:]

		if _, ok := gen.generated[schema.Location()]; ok {
			slog.Debug(
				"skipping schema with already-generated location",
				"schema.location", schema.Location())
			continue
		}
		gen.generated[schema.Location()] = struct{}{}

		gen.file.Add(gen.generateSchemaByType(schema, false))
		gen.file.Line()
	}
}

func (gen *generator) generateCommentToplevel(schema *jsonschema.Schema) j.Code {
	return gen.generateComment(schema, "", 0)
}

func (gen *generator) generateComment(schema *jsonschema.Schema, name string, indent int) j.Code {
	comment := schema.GoCommentIndented(name, indent)
	if comment == "" {
		return j.Empty()
	}
	return j.
		Comment(comment).
		Line()
}

func (gen *generator) generateRawMessage() j.Code {
	return j.Qual("encoding/json/jsontext", "Value")
}

func (gen *generator) wrapOptional(code j.Code) *j.Statement {
	return j.Qual("github.com/diamondburned/go-buttplug/schema/ptr", "Optional").Types(code)
}

// schemaState is the generation state of a schema.
type schemaState int

const (
	schemaInline schemaState = iota
	schemaRef
	schemaRefGenerated
)

func (s schemaState) Is(other schemaState) bool {
	return s == other
}

func (s schemaState) String() string {
	switch s {
	case schemaInline:
		return "inline"
	case schemaRef:
		return "ref"
	case schemaRefGenerated:
		return "ref_generated"
	default:
		return fmt.Sprintf("schemaState(%d)", int(s))
	}
}
