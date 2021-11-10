// Package buttplugschema provides functions to fetch the buttplug.io JSON
// schema.
package buttplugschema

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pkg/errors"
	"github.com/qri-io/jsonpointer"
	"github.com/qri-io/jsonschema"
)

func init() {
	jsonschema.LoadDraft2019_09()
	jsonschema.RegisterKeyword("properties", newProperties)
	jsonschema.RegisterKeyword("components", newProperties)
	jsonschema.RegisterKeyword("messages", newProperties)
}

// SchemaURL gets the direct schema URL for the buttplug.io schema.
func SchemaURL(ref string) string {
	return `https://raw.githubusercontent.com/buttplugio/buttplug/` + ref + `/buttplug/buttplug-schema/schema/buttplug-schema.json`
}

// Schema provides methods to work with the buttplug.io JSON schema.
type Schema struct {
	Messages Messages
	Types    []Type
}

// Messages is the top-level message type.
type Messages struct {
	BaseType
	Fields []Field
}

// DownloadRaw downloads the schema from GitHub with the given ref name.
func DownloadRaw(ref string) (*jsonschema.Schema, error) {
	r, err := http.Get(SchemaURL(ref))
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	var schema jsonschema.Schema

	if err := json.NewDecoder(r.Body).Decode(&schema); err != nil {
		return nil, errors.Wrap(err, "failed to decode schema")
	}

	return &schema, nil
}

// Parse parses the given JSON schema.
func Parse(s *jsonschema.Schema) *Schema {
	var dst Schema
	// spew.Dump(s)
	// panic("a")

	items := s.JSONProp("items").(*jsonschema.Items)

	for _, schema := range items.Schemas {
		title := *schema.JSONProp("title").(*jsonschema.Title)
		if title != "Messages" {
			continue
		}

		r := &resolver{&dst, s}
		t := r.addForce(newNamePiece(string(title)), schema)

		ob, ok := t.(ObjectType)
		if !ok {
			log.Println("Messages is not object?")
		}

		dst.Messages = Messages{
			BaseType: ob.BaseType,
			Fields:   ob.Fields,
		}
	}

	return &dst
}

// FindType finds the type with the given name from the global list of types.
func (s *Schema) FindType(typeName string) Type {
	for _, t := range s.Types {
		if t.SchemaName() == typeName {
			return t
		}
	}
	return nil
}

type resolver struct {
	*Schema
	root *jsonschema.Schema
}

type namePiece struct {
	name   string
	goName string
	export bool
}

var unnamed = namePiece{}

func newNamePiece(name string) namePiece {
	return namePiece{name, transformGoName(name), false}
}

func newGoNamePiece(name, goName string) namePiece {
	return namePiece{name, goName, false}
}

func (n namePiece) String() string {
	if n == unnamed {
		return "<anonymous>"
	}
	return fmt.Sprintf("%s (%s)", n.name, n.goName)
}

func (r *resolver) add(name namePiece, typ *jsonschema.Schema) Type {
	if oneOf, ok := typ.JSONProp("oneOf").(*jsonschema.OneOf); ok {
		union := OneOfType{
			BaseType: newBaseType(name, "interface{}", typ),
		}
		for _, typ := range *oneOf {
			t := r.add(namePiece{}, typ)
			if t != nil {
				union.Types = append(union.Types, t)
			}
		}
		if len(union.Types) > 0 {
			return union
		}
		log.Println("no types resolved for union", name)
		return nil
	}

	if refID := refID(typ); refID != "" {
		p, err := jsonpointer.Parse(refID)
		if err != nil {
			log.Printf("pointer %q invalid: %s", string(refID), err)
			return nil
		}

		name.name = p[len(p)-1]
		name.export = true

		if name.goName == "" {
			name.goName = transformGoName(name.name)
		}

		// Search the tail. Return that if we find one.
		if t := r.FindType(p[len(p)-1]); t != nil {
			return t
		}

		ref := r.root.Resolve(p, "")
		if ref == nil {
			log.Printf("unknown $ref %q", p)
			return nil
		}

		typ = ref
		goto resolve
	}

	if anyOf, ok := typ.JSONProp("anyOf").(*jsonschema.AnyOf); ok {
		typ = (*anyOf)[0]
		// TODO: pointer
		goto resolve
	}

resolve:
	// name can be empty if we're resolving an array. Don't save the type then.
	if name.export && name.name != "" {
		if t := r.FindType(name.name); t != nil {
			return t
		}
	}

	t := r.addForce(name, typ)
	if name.export && t != nil && t.SchemaName() != "" {
		r.Types = append(r.Types, t)
	}

	return t
}

func (r *resolver) addForce(name namePiece, typ *jsonschema.Schema) Type {
	jsonType := typ.TopLevelType()

	properties, ok := typ.JSONProp("properties").(*properties)
	if ok && jsonType == "unknown" {
		// This is missing sometimes for some weird reason.
		jsonType = "object"
	}

	consts, ok := typ.JSONProp("enum").(*jsonschema.Enum)
	if ok && jsonType == "unknown" {
		// This is also missing sometimes.
		jsonType = "enum"
	}

	switch jsonType {
	case "object":
		obj := ObjectType{
			BaseType: newBaseType(name, "struct{}", typ),
		}
		if anyOf, ok := typ.JSONProp("anyOf").(*jsonschema.AnyOf); ok {
			t, ok := r.add(unnamed, (*anyOf)[0]).(ObjectType)
			if ok {
				obj.Fields = t.Fields
				return obj
			}
			return nil
		}
		var requiredFields map[string]bool
		if required, ok := typ.JSONProp("required").(*jsonschema.Required); ok {
			requiredFields = make(map[string]bool, len(*required))
			for _, req := range *required {
				requiredFields[req] = true
			}
		}
		parentName := name
		properties.Each(func(name string, value *jsonschema.Schema) {
			t := r.add(unnamed, value)
			if t == nil {
				log.Printf("%s skipping field %s", parentName, name)
				return
			}

			obj.Fields = append(obj.Fields, Field{
				Type:      t,
				FieldName: transformGoName(name),
				JSONName:  name,
				Required:  requiredFields[name],
			})
		})
		return obj

	case "string":
		return StringType{
			BaseType: newBaseType(name, "string", typ),
		}

	case "integer":
		integer := IntegerType{
			BaseType: newBaseType(name, "int", typ),
		}
		if min, ok := typ.JSONProp("minimum").(*jsonschema.Minimum); ok {
			i := int(*min)
			integer.Minimum = &i
		}
		if max, ok := typ.JSONProp("maximum").(*jsonschema.Maximum); ok {
			i := int(*max)
			integer.Maximum = &i
		}
		return integer

	case "number":
		number := NumberType{
			BaseType: newBaseType(name, "float64", typ),
		}
		if min, ok := typ.JSONProp("minimum").(*jsonschema.Minimum); ok {
			number.Minimum = (*float64)(min)
		}
		if max, ok := typ.JSONProp("maximum").(*jsonschema.Maximum); ok {
			number.Maximum = (*float64)(max)
		}
		return number

	case "array":
		array := ArrayType{
			BaseType: newBaseType(name, "", typ),
		}
		if items, ok := typ.JSONProp("items").(*jsonschema.Items); ok {
			first := items.Schemas[0]

			t := r.add(unnamed, first)
			if t != nil {
				array.Type = t
				array.goType = "[]" + t.GoType()
				// log.Printf("%s: got array %s %#v", name, innerName, array.GoType())
				return array
			}
			// log.Printf("%s: DIDN'T GET array %s", name, innerName)
		}
		return nil

	case "enum":
		enum := EnumType{
			BaseType: newBaseType(name, "string", typ),
		}
		if consts != nil {
			for _, v := range *consts {
				enum.Values = append(enum.Values, EnumValue{
					GoName: exportGoName(transformGoName(v.String())),
					Value:  v.String(),
				})
			}
			return enum
		}
		log.Println(name, "no field 'enum'")
		return nil

	case "boolean":
		return BooleanType{
			BaseType: newBaseType(name, "bool", typ),
		}

	default:
		log.Println(name, "unknown type", typ.TopLevelType())
		return nil
	}
}

var cases = strings.NewReplacer(
	"Id", "ID",
	"Ok", "OK",
)

func transformGoName(name string) string {
	return cases.Replace(name)
}

func exportGoName(name string) string {
	r, sz := utf8.DecodeRuneInString(name)
	if sz > 0 && r != utf8.RuneError {
		return name
	}

	return string(unicode.ToUpper(r)) + name[sz:]
}

func newBaseType(name namePiece, goType string, typ *jsonschema.Schema) BaseType {
	base := BaseType{
		schemaName: name.name,
		goName:     name.goName,
		goType:     name.goName,
	}
	// Empty name means anonymous type; use goType instead.
	if !name.export {
		base.goType = goType
	}
	if desc, ok := typ.JSONProp("description").(*jsonschema.Description); ok {
		base.description = string(*desc)
	}
	return base
}

func refID(typ *jsonschema.Schema) string {
	ref, ok := typ.JSONProp("$ref").(*jsonschema.Ref)
	if !ok {
		return ""
	}

	refIDBlob, err := ref.MarshalJSON()
	if err != nil {
		log.Panicln("cannot remarshal $ref:", err)
	}

	refID, err := strconv.Unquote(string(refIDBlob))
	if err != nil {
		log.Panicln("cannot unquote $ref:", err)
	}

	return refID
}

// func (s *Schema) Messages() []MessageField {
// 	items := s.JSONProp("items").(*jsonschema.Items)
// 	items.Resolve()
// 	// for _, schema := range items.Schemas {
// 	// 	title := *schema.JSONProp("title").(*jsonschema.Title)
// 	// 	log.Println(schema.TopLevelType(), ":")
// 	// 	for name, v := range schema.JSONChildren() {
// 	// 		log.Printf("%s %T", name, v)
// 	// 	}
// 	// }

// 	panic("a")
// }
