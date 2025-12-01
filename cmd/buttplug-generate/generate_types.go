package main

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	j "github.com/dave/jennifer/jen"
	"github.com/diamondburned/gotk4/gir/girgen/strcases"
	"libdb.so/go-buttplug/cmd/buttplug-generate/jsonschema"
	"libdb.so/go-buttplug/schema/ptr"
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
			"array schema with multiple item types not implemented",
			"item_count", len(items))
		return gen.generateRawMessage()
	}

	item := items[0].Unref()

	var array *j.Statement

	minItems := ptr.ValueOrZero(item.MinItems())
	maxItems := ptr.ValueOrZero(item.MaxItems())
	if minItems > 0 && minItems == maxItems {
		array = j.Index(j.Lit(minItems))
	} else {
		array = j.Index()
	}

	slog.Debug(
		"generating array item schema",
		"item_schema", item)

	if item.Type().Is(jsonschema.ObjectType) {
		var itemName string
		if singularForm, ok := endsWithKnownPlural(schema.Name()); ok {
			itemName = singularForm
		} else {
			itemName = concatStringsNoOverlap(schema.Name(), "Item")
		}
		item = item.WithName(itemName)

		gen.enqueue(item)
		return j.Add(array).Id(item.Name())
	}

	return j.Add(array, gen.generateSchemaByType(item, true))
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
	return gen.generateObjectInlineFromProperties(properties)
}

func (gen *generator) generateObjectInlineFromProperties(properties jsonschema.OrderedProperties) j.Code {
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
		if !property.IsRequired() {
			tags["json"] += ",omitzero"
		}
		if def := property.Schema.Raw().Default; def != nil {
			tags["default"] = fmt.Sprintf("%v", def)
		}

		g.Add(gen.generateComment(property.Schema, property.Name, 1))
		g.Id(property.Name).Add(vstmt).Tag(tags)

		g.Line()
	}

	return j.Struct(&g)
}

var (
	reAllNumbers    = []string{"^[0-9]*$", "^[0-9]*"}
	reEverything    = []string{"^.*$"}
	reKnownPatterns = slices.Concat(reAllNumbers, reEverything)
)

func (gen *generator) generateObjectAsMap(schema *jsonschema.Schema, inline bool, patterns jsonschema.PatternProperties) j.Code {
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
		schemaName = concatStringsNoOverlap(strings.TrimSuffix(parentName, "Message"), schemaName)
	}

	mapType := schemaName

	if inline {
		// requeue this schema as a toplevel type.
		gen.enqueue(schema)
		return j.Id(mapType)
	}

	if !schema.HasDescription() {
		var targetType string
		if parentName != "" {
			targetType = fmt.Sprintf("%s.%s", parentName, schema.Name())
		} else {
			targetType = schema.Name()
		}

		schema = schema.
			WithName(mapType).
			WithDescription(fmt.Sprintf("Properties map for [%s].", targetType))
	}

	extras := j.Add()

	kUnions := make([]exrexStrings, len(patterns))
	for i, k := range slices.Collect(patterns.KeyPatterns()) {
		if strings.Trim(k, "^$") == k && !strings.ContainsAny(k, "|.+*") {
			// key doesn't have the usual regex anchors, so we can treat it as a
			// literal.
			kUnions[i] = exrexStrings{k}
			continue
		}

		if slices.Contains(reKnownPatterns, k) {
			// ignore known board patterns.
			continue
		}

		strs, err := exrex(k)
		if err != nil {
			slog.Error(
				"exrex failed on this particular key pattern!",
				"key_pattern", k,
				"err", err)
			kUnions[i] = exrexStrings{"!ERROR!"}
			continue
		}

		kUnions[i] = strs
	}

	// kUnioned is true if all key patterns were expanded into unions.
	kUnioned := allFunc(slices.Values(kUnions), exrexStrings.IsExpanded)

	kUnionsIDs := make([][]string, len(kUnions))
	for i, ku := range kUnions {
		if ku.IsExpanded() {
			kUnionsIDs[i] = make([]string, len(ku))
			for j, k := range ku {
				kUnionsIDs[i][j] = schemaName + formatIdentifier(k)
			}
		}
	}

	kstmt := j.Empty()
	switch {
	case kUnioned:
		mapKeyType := schemaName + "Key"
		kstmt.Id(mapKeyType)

		m := j.Add()
		m.Commentf("%s represents valid keys in [%s].", mapKeyType, mapType).Line()
		m.Type().Id(mapKeyType).String().Line()
		m.Line()
		m.Commentf("Constants for valid keys in [%s].", mapType).Line()
		m.Const().DefsFunc(func(g *j.Group) {
			for _, kUnion := range kUnions {
				for _, k := range kUnion {
					g.Id(schemaName + formatIdentifier(k)).Id(mapKeyType).Op("=").Lit(k)
				}
			}
		})
		m.Line()
		extras.Add(m)

	case allFunc(patterns.KeyPatterns(), func(s string) bool { return slices.Contains(reAllNumbers, s) }):
		kstmt.Int()

	case allFunc(patterns.KeyPatterns(), func(s string) bool { return slices.Contains(reEverything, s) }):
		kstmt.String()

	default:
		kstmt.String()
	}

	vstmt := j.Add()
	if len(patterns) == 1 {
		_, v := patterns[0].KeyPattern, patterns[0].ValueSchema
		if v.IsRef() {
			v = v.Unref()
		} else {
			v = v.
				WithName(concatStringsNoOverlap(schemaName, "Value")).
				WithDescription(clarifyPropertiesTypeOrigin(v.Description(), mapType))
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
		valueType := concatStringsNoOverlap(schemaName, "Value")

		var counter int // TODO: figure out a smarter way to mangle name
		for i, patternProperty := range patterns {
			k := patternProperty.KeyPattern
			v := patternProperty.ValueSchema
			if v.IsRef() {
				continue
			}

			var tail string

			// We use a variety of heuristics to try to get a nice name for the
			// generated type.
			switch {
			// If the value struct only has 1 property, then we can just name it
			// after that property.
			case v.Type().Is(jsonschema.ObjectType) && v.NumProperties() == 1:
				props, _ := v.Properties()
				tail = props[0].Name + "Value"

			// If the key pattern is a string union, join the words together.
			case kUnioned:
				tail = formatIdentifier(strings.Join(kUnions[i], "")) + "Value"

			// Otherwise, fall back to using a number.
			default:
				tail = fmt.Sprintf("Case%d", counter)
			}

			caseType := concatStringsNoOverlap(mapType, tail)
			// Avoid this silly case of collision when using NoOverlap.
			if caseType == valueType {
				valueType += "Type"
			}

			var kHelp string
			if kUnioned {
				// Print out possible cases for better readability than just a
				// raw regex string.
				kUnionIDs := kUnionsIDs[i]
				for i, kID := range kUnionIDs {
					kHelp += fmt.Sprintf("[%s]", kID)
					switch {
					case i == len(kUnionIDs)-1:
						// last item, do nothing.
					case i == len(kUnionIDs)-2:
						kHelp += " and "
					default:
						kHelp += ", "
					}
				}
			} else {
				kHelp = strconv.Quote(k)
			}

			v = v.
				WithName(caseType).
				WithDescription(fmt.Sprintf(
					"%s is the value matching pattern for %s of [%s].",
					caseType, kHelp, mapType,
				))
			patterns[i].ValueSchema = v
			counter++
		}

		comment := fmt.Sprintf(""+
			"%s is a type that represents possible values of the map [%s].\n"+
			"\n"+
			"The following types can be used for this interface:\n",
			valueType, mapType,
		)
		for v := range patterns.ValueSchemas() {
			comment += fmt.Sprintf("\t- [%s]\n", v.Name())
		}

		extras.Comment(jsonschema.WrapCommentTopLevel(comment)).Line()
		extras.Type().Id(valueType).Interface(
			j.Id(strcases.UnexportPascal(valueType)).Call(),
		)

		extras.Line()
		extras.Line()

		// inlining is impossible.
		vstmt.Id(valueType)

		for v := range patterns.ValueSchemas() {
			extras.Add(gen.generateSchemaByType(v, false)).Line()
		}

		for v := range patterns.ValueSchemas() {
			extras.
				Func().
				Params(j.Id(v.Name())).
				Id(strcases.UnexportPascal(valueType)).
				Params().
				Params().
				Block().
				Line()
		}
	}

	return j.
		Comment(schema.GoComment("")).Line().
		Type().Id(mapType).Map(kstmt).Add(vstmt).
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
		typeName = concatStringsNoOverlap(parentName, schema.Name())
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
			id := concatStringsNoOverlap(typeName, formatIdentifier(value))
			g.Id(id).Id(typeName).Op("=").Lit(value)
		}
	})
	g.Line()

	return &g
}
