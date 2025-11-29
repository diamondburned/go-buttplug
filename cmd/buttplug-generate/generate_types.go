package main

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	j "github.com/dave/jennifer/jen"
	"github.com/diamondburned/go-buttplug/cmd/buttplug-generate/jsonschema"
)

func (gen *generator) generateSchemaByType(schema *jsonschema.Schema, inline bool) j.Code {
	slog := slog.With(
		"schema", schema,
		"inline", inline)

	switch schema.Type() {
	case jsonschema.ArrayType:
		return gen.generateArray(schema, inline)
	case jsonschema.ObjectType:
		return gen.generateObject(schema, inline)
	case jsonschema.StringType:
		return gen.generateString(schema, inline)
	case jsonschema.IntegerType:
		return gen.generateInteger(schema, inline)
	case jsonschema.NumberType:
		return gen.generateNumber(schema, inline)
	case jsonschema.BooleanType:
		return gen.generateBoolean(schema, inline)
	case jsonschema.EnumType:
		return gen.generateEnum(schema, inline)
	case jsonschema.ByteArrayType:
		return gen.generateByteArray(schema, inline)
	}

	slog.Warn(
		"unknown schema type not being handled",
		"schema", schema,
		"inline", inline,
		"is_enum", false)

	return j.Empty()
}

func (gen *generator) generateToplevelType(
	schema *jsonschema.Schema,
	generator func(schema *jsonschema.Schema, inline bool) j.Code,
) j.Code {
	if generator == nil {
		generator = gen.generateSchemaByType
	}

	return j.
		Add(gen.generateCommentToplevel(schema)).
		Type().Id(schema.Name()).Add(generator(schema, true))
}

func (gen *generator) generateArray(schema *jsonschema.Schema, inline bool) j.Code {
	slog := slog.With(
		"schema", schema,
		"inline", inline)

	if !inline {
		slog.Debug("generating toplevel array schema")
		return gen.generateToplevelType(schema, gen.generateArray)
	}

	items := schema.Items()
	if len(items) != 1 {
		slog.Warn(
			"array schema with multiple item types not yet implemented",
			"item_count", len(items))

		return gen.generateRawMessage()
	}

	item := items[0].Unref()

	slog.Debug(
		"generating array item schema",
		"item_schema", item)

	if item.Type().Is(jsonschema.ObjectType) {
		item = item.WithName(schema.Name() + "Item")

		gen.enqueue(item)
		return j.Index().Id(item.Name())
	}

	return j.Index().Add(gen.generateSchemaByType(item, true))
}

func (gen *generator) generateObject(schema *jsonschema.Schema, inline bool) j.Code {
	slog := slog.With(
		"schema", schema,
		"inline", inline)

	patternProperties, ok := schema.PatternProperties()
	if ok {
		return gen.generateObjectAsMap(schema, inline, patternProperties)
	}

	if !inline {
		slog.Debug("generating toplevel object schema")
		return gen.generateToplevelType(schema, gen.generateObject)
	}

	if anyOfs := schema.AnyOf(); len(anyOfs) > 0 {
		if len(anyOfs) != 1 {
			slog.Warn(
				"object schema with multiple anyOfs not yet implemented, falling back",
				"anyOf", anyOfs)
			return gen.generateRawMessage()
		}

		slog.Debug(
			"unwrapping object with single anyOf (treated as alias)",
			"target", anyOfs[0])

		schema = anyOfs[0].Unref()
	}

	properties, _ := schema.Properties()
	slog.Debug(
		"generating object schema as an inline struct",
		"properties", properties)

	var g j.Group

	for _, property := range properties {
		vstmt := j.Add()

		if property.Schema.IsRef() {
			property.Schema = property.Schema.Unref()
			gen.enqueue(property.Schema)
			// generate as reference to named type.
			vstmt.Id(property.Schema.Name())
		} else {
			// if ref-less, then generate as inline.
			vstmt.Add(gen.generateSchemaByType(property.Schema, true))
		}

		if !property.IsRequired() && !property.Schema.Type().Is(jsonschema.ArrayType) {
			vstmt = gen.wrapOptional(vstmt)
		}

		tags := map[string]string{"json": property.JSONName}
		if property.Schema.Type().Is(jsonschema.ByteArrayType) {
			tags["json"] += ",format:array"
		}
		if !property.IsRequired() {
			tags["json"] += ",omitzero"
		}
		if def := property.Schema.Raw().Default; def != nil {
			tags["default"] = fmt.Sprintf("%v", def)
		}

		g.Add(gen.generateComment(property.Schema, "", 1))
		g.Id(property.Name).Add(vstmt).Tag(tags)

		g.Line()
	}

	return j.Struct(&g)
}

var reValidWordsInUnion = regexp.MustCompile(`^\w+$`)

func (gen *generator) generateObjectAsMap(schema *jsonschema.Schema, inline bool, patternProperties jsonschema.PatternProperties) j.Code {
	slog := slog.With(
		"schema", schema,
		"inline", inline)
	slog.Debug("generating object schema as map with patternProperties")

	var parentName string
	schemaName := schema.Name()

	if parent := schema.Parent(); parent != nil && parent.Type().Is(jsonschema.ObjectType) {
		// If this schema is nested inside another object, mangle the name
		// to also have the object's name for clarity.
		parentName = parent.Name()
		schemaName = joinStringDetectOverlap(parentName, schemaName)
	}

	typeName := schemaName

	if inline {
		// requeue this schema as a toplevel type.
		gen.enqueue(schema)
		return j.Id(typeName)
	}

	if !schema.HasDescription() {
		var targetType string
		if parentName != "" {
			targetType = fmt.Sprintf("%s.%s", parentName, schema.Name())
		} else {
			targetType = schema.Name()
		}
		schema = schema.
			WithName(typeName).
			WithDescription(fmt.Sprintf("Properties map for [%s].", targetType))
	}

	extras := j.Add()

	kstmt := j.Add()
	kUnionType := []string(nil)
	if len(patternProperties) == 1 {
		k := patternProperties[0].KeyPattern

		if kUnion, ok := keyPatternIsStringUnion(k); ok {
			kUnionType = kUnion
			kstmt.Id(schemaName + "Key")
		} else if k == "^[0-9]*" {
			kstmt.Int()
		} else if k == "^.*$" {
			kstmt.String()
		} else {
			kstmt.String().Commentf("/* %s */", k)
		}
	} else if isAllFunc(patternProperties, func(p jsonschema.PatternProperty) bool {
		_, isStringUnion := keyPatternIsStringUnion(p.KeyPattern)
		return isStringUnion
	}) {
		// Join all string union keys into one union type.
		// This covers just one specific edge case, but it is the only edge case
		// we need :3
		for k := range patternProperties.KeyPatterns() {
			p, _ := keyPatternIsStringUnion(k)
			kUnionType = slices.Concat(kUnionType, p)
		}
		kstmt.Id(schemaName + "Key")
	} else {
		kstmt.Map(j.String())
	}

	if kUnionType != nil {
		mapKeyType := schemaName + "Key"

		m := j.Add()
		m.Commentf("%s represents valid keys in [%s].", mapKeyType, typeName).Line()
		m.Type().Id(mapKeyType).String().Line()
		m.Line()
		m.Commentf("Constants for valid keys in [%s].", typeName).Line()
		m.Const().DefsFunc(func(g *j.Group) {
			for _, k := range kUnionType {
				g.Id(schemaName + jsonschema.FormatIdentifier(k)).Id(mapKeyType).Op("=").Lit(k)
			}
		})
		m.Line()

		extras.Add(m)
	}

	vstmt := j.Add()
	if len(patternProperties) == 1 {
		_, v := patternProperties[0].KeyPattern, patternProperties[0].ValueSchema
		if v.IsRef() {
			v = v.Unref()
		} else {
			v = v.
				WithName(schemaName + "Value").
				WithDescription(clarifyPropertiesTypeOrigin(v.Description(), typeName))
		}

		switch v.Type() {
		case jsonschema.EnumType, jsonschema.ObjectType, jsonschema.ArrayType:
			// ensure value schema is generated as a top-level named type.
			vstmt.Id(v.Name())
			vstmt.Line()
			vstmt.Add(gen.generateSchemaByType(v, false))
		default:
			// inline type is ok.
			vstmt.Add(gen.generateSchemaByType(v, true))
		}
	} else {
		wrappingType := schemaName + "Value"

		var counter int // TODO: figure out a smarter way to mangle name
		for i, patternProperty := range patternProperties {
			k := patternProperty.KeyPattern
			v := patternProperty.ValueSchema
			if v.IsRef() {
				continue
			}

			var tail string
			if keyUnion, ok := keyPatternIsStringUnion(k); ok {
				tail = jsonschema.FormatIdentifier(strings.Join(keyUnion, ""))
			} else {
				tail = fmt.Sprintf("Case%d", counter)
			}

			propName := wrappingType + tail
			v = v.
				WithName(propName).
				WithDescription(fmt.Sprintf(
					"%s is the value matching pattern for %q of [%s].",
					propName, k, typeName,
				))
			patternProperties[i].ValueSchema = v
			counter++
		}

		extras.Comment(jsonschema.WrapCommentTopLevel(fmt.Sprintf(
			"%s is a type that represents all possible values of the map [%s]. Only one field will be non-nil.",
			wrappingType, typeName,
		))).Line()
		extras.Type().Id(wrappingType).StructFunc(func(g *j.Group) {
			for v := range patternProperties.ValueSchemas() {
				g.Op("*").Id(v.Name()).Tag(map[string]string{"json": ",omitzero"})
			}
		})
		extras.Line()
		extras.Line()

		// inlining is impossible.
		vstmt.Id(wrappingType)

		for v := range patternProperties.ValueSchemas() {
			extras.Add(gen.generateSchemaByType(v, false)).Line()
		}
	}

	return j.
		Comment(schema.GoComment("")).Line().
		Type().Id(typeName).Map(kstmt).Add(vstmt).
		Line().
		Add(extras)
}

func endDescriptionSentenceForNext(description string) string {
	if description == "" {
		return ""
	}
	if !strings.HasSuffix(description, ".") {
		description += "."
	}
	return description + " "
}

func clarifyPropertiesTypeOrigin(description, typeName string) string {
	return endDescriptionSentenceForNext(description) + fmt.Sprintf(" This is the properties map type for [%s].", typeName)
}

func keyPatternIsStringUnion(k string) ([]string, bool) {
	k = strings.Trim(k, "^()$")

	// Allow single string word case.
	if reValidWordsInUnion.MatchString(k) {
		return []string{k}, true
	}

	if !strings.Contains(k, "|") {
		return nil, false
	}

	words := strings.Split(k, "|")
	// Ensure that the split words are strictly just words.
	if len(words) < 2 {
		return nil, false
	}
	for _, word := range words {
		if !reValidWordsInUnion.MatchString(word) {
			return nil, false
		}
	}

	return words, true
}

func (gen *generator) generateString(schema *jsonschema.Schema, inline bool) j.Code {
	if !inline {
		return gen.generateToplevelType(schema, gen.generateString)
	}
	return j.String()
}

func (gen *generator) generateInteger(schema *jsonschema.Schema, inline bool) j.Code {
	if !inline {
		return gen.generateToplevelType(schema, gen.generateInteger)
	}
	return j.Int()
}

func (gen *generator) generateNumber(schema *jsonschema.Schema, inline bool) j.Code {
	if !inline {
		return gen.generateToplevelType(schema, gen.generateNumber)
	}
	return j.Float64()
}

func (gen *generator) generateBoolean(schema *jsonschema.Schema, inline bool) j.Code {
	if !inline {
		return gen.generateToplevelType(schema, gen.generateBoolean)
	}
	return j.Bool()
}

func (gen *generator) generateEnum(schema *jsonschema.Schema, inline bool) j.Code {
	typeName := schema.Name()
	var parentName string

	location := schema.Location().Relative()
	locationParent := location.DirPath()
	if locationParent.BaseName() == "properties" {
		// If the enum is not defined in components, then it's likely generated
		// as a nested field of something, so we need to mangle the name to
		// avoid collisions.
		locationParent = locationParent.DirPath()
		parentName = locationParent.BaseName()
		typeName = joinStringDetectOverlap(parentName, schema.Name())
	}

	if inline {
		// Force this to be generated as a top-level type.
		gen.enqueue(schema)
		return j.Id(typeName)
	}

	var g j.Group
	g.Add(gen.generateComment(schema, "", 0))
	g.Type().Id(typeName).String()
	g.Line()

	var comment string
	if parentName != "" {
		comment = fmt.Sprintf("Enumeration values for [%s] of [%s].", typeName, parentName)
	} else {
		comment = fmt.Sprintf("Enumeration values for [%s].", typeName)
	}

	g.Comment(comment).Line()
	g.Const().DefsFunc(func(g *j.Group) {
		for value := range schema.EnumValues() {
			id := typeName + jsonschema.FormatIdentifier(value)
			g.Id(id).Id(typeName).Op("=").Lit(value)
		}
	})
	g.Line()

	return &g
}

func (gen *generator) generateByteArray(schema *jsonschema.Schema, inline bool) j.Code {
	if !inline {
		return gen.generateToplevelType(schema, gen.generateByteArray)
	}
	return j.Index().Byte()
}

// joinStringDetectOverlap joins two Go-cased strings, detecting any overlapping
// word parts to avoid duplication.
func joinStringDetectOverlap(a, b string) string {
	aParts := splitGoNameParts(a)
	bParts := splitGoNameParts(b)
	for len(aParts) > 0 && len(bParts) > 0 {
		if aParts[len(aParts)-1] != bParts[0] {
			break
		}
		bParts = bParts[1:]
	}
	return strings.Join(slices.Concat(aParts, bParts), "")
}

var (
	reWords    = regexp.MustCompile(`([A-Z][^A-Z\s]*)`)
	wordMapper = strings.NewReplacer(
		"Cmd", "Command",
	)
)

func splitGoNameParts(name string) []string {
	words := reWords.FindAllString(name, -1)
	for i, word := range words {
		words[i] = wordMapper.Replace(word)
	}
	return words
}
