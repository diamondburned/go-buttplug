// Package jsonschema wraps around the dreadful santhosh-tekuri/jsonschema
// package.
package jsonschema

import (
	"fmt"
	"iter"
	"log/slog"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Schema defines a JSON Schema object.
// Note that schema pointers are NOT interned, so two identical schemas
// may be different pointers. To compare them, compare their locations
// or use [Schema.Equals].
type Schema struct {
	self     *jsonschema.Schema
	parent   *Schema
	forceRef bool
}

// ParseFile parses the JSON Schema file at the given path and returns the root
// schema.
func ParseFile(path string) (*Schema, error) {
	schema, err := jsonschema.NewCompiler().Compile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot compile jsonschema file: %w", err)
	}
	return wrapSchema(schema, nil), nil
}

func wrapSchema(self *jsonschema.Schema, parent *Schema) *Schema {
	return &Schema{
		self:   self,
		parent: parent,
	}
}

func wrapSchemas(selves []*jsonschema.Schema, parent *Schema) []*Schema {
	wrapped := make([]*Schema, len(selves))
	for i, self := range selves {
		wrapped[i] = wrapSchema(self, parent)
	}
	return wrapped
}

func (s *Schema) copy() *Schema {
	self2 := *s.self
	s2 := *s
	s2.self = &self2
	return &s2
}

// Name returns the name of the schema, usually derived from the base name of
// its location.
func (s *Schema) Name() string {
	id := TrimVersion(FormatIdentifier(s.Location().BaseName()))
	return id
}

// WithName clones the schema to have the given name by changing its location.
// Use this scarcely and carefully! It may have unintended consequences.
func (s *Schema) WithName(name string) *Schema {
	s = s.copy()
	s.self.Location = path.Join(string(s.Location().DirPath()), name)
	return s
}

// Description returns the description of the schema. For code generation
// purposes, use [Schema.GoComment] instead.
func (s *Schema) Description() string {
	return s.self.Description
}

// WithDescription clones the schema to have the given description.
func (s *Schema) WithDescription(description string) *Schema {
	s = s.copy()
	s.self.Description = description
	return s
}

// HasDescription returns true if the schema has a description.
func (s *Schema) HasDescription() bool {
	return s.self.Description != ""
}

// Location returns the location of the schema.
func (s *Schema) Location() SchemaLocation {
	return SchemaLocation(s.self.Location)
}

// GoComment returns the Go comment for the schema's description.
// If [name] is not empty, it is used as the name in the comment formatting
// instead of the schema's name.
func (s *Schema) GoComment(name string) string {
	return s.GoCommentIndented(name, 0)
}

// GoCommentIndented returns the Go comment for the schema's description,
// indented by the given number of tabs.
// If [name] is not empty, it is used as the name in the comment formatting
// instead of the schema's name.
func (s *Schema) GoCommentIndented(name string, indent int) string {
	if name == "" {
		name = s.Name()
	}
	return FormatComment(s.self.Description, name, indent)
}

// Unref returns the underlying schema.
func (s *Schema) Unref() *Schema {
	if s.self.Ref != nil {
		return wrapSchema(s.self.Ref, s)
	}
	return s
}

// IsRef returns true if the schema is a ref.
func (s *Schema) IsRef() bool {
	return s.self.Ref != nil || s.forceRef
}

// Type returns the type of the schema without dereferencing any references.
func (s *Schema) Type() SchemaType {
	if s.self.Types == nil {
		if s.self.Enum != nil {
			return EnumType
		}
		return InvalidType
	}
	return SchemaType(*s.self.Types)
}

// UnderlyingType returns the underlying type of the schema, dereferencing
// any references first.
func (s *Schema) UnderlyingType() SchemaType {
	return s.Unref().Type()
}

// Items returns the items of the schema.
func (s *Schema) Items() []*Schema {
	switch items := s.self.Items.(type) {
	case *jsonschema.Schema:
		return []*Schema{wrapSchema(items, s)}
	case []*jsonschema.Schema:
		schemas := make([]*Schema, len(items))
		for i, item := range items {
			schemas[i] = wrapSchema(item, s)
		}
		return schemas
	default:
		return nil
	}
}

// OrderedProperty defines an object property with its name and schema.
type OrderedProperty struct {
	// Name is the Go-cased name of the property.
	Name string
	// JSONName is the original JSON name of the property.
	JSONName string
	// Schema is the schema of the property.
	Schema *Schema

	requiredIx int
}

// NewOrderedProperty creates a new OrderedProperty from the name, schema, and
// optionally whether it is required.
func NewOrderedProperty(name string, schema *Schema, required bool) OrderedProperty {
	ix := -1
	if required {
		ix = 0
	}
	return OrderedProperty{
		Name:       FormatIdentifier(name),
		JSONName:   name,
		Schema:     schema,
		requiredIx: ix,
	}
}

// Unref returns the underlying schema of the property.
func (p OrderedProperty) Unref() OrderedProperty {
	return OrderedProperty{
		Name:       p.Name,
		JSONName:   p.JSONName,
		Schema:     p.Schema.Unref(),
		requiredIx: p.requiredIx,
	}
}

// IsRequired returns true if the property is required.
func (p OrderedProperty) IsRequired() bool {
	return p.requiredIx >= 0
}

// OrderedProperties represents a parsed and sorted JSON schema object's
// properties.
type OrderedProperties []OrderedProperty

// Properties returns the properties of the schema.
func (s *Schema) Properties() (OrderedProperties, bool) {
	if s.self.Properties == nil {
		return nil, false
	}

	properties := make([]OrderedProperty, 0, len(s.self.Properties))
	for name, schema := range s.self.Properties {
		properties = append(properties, OrderedProperty{
			Name:       FormatIdentifier(name),
			JSONName:   name,
			Schema:     wrapSchema(schema, s),
			requiredIx: slices.Index(s.self.Required, name),
		})
	}

	slices.SortFunc(properties, func(a, b OrderedProperty) int {
		aIsID := a.Name == "ID"
		bIsID := b.Name == "ID"
		// Sort ID property first.
		if aIsID != bIsID {
			if aIsID {
				return -1
			}
			return 1
		}
		// Sort required properties before optional properties.
		if a.IsRequired() != b.IsRequired() {
			if a.IsRequired() {
				return -1
			}
			return 1
		}
		if a.requiredIx != b.requiredIx {
			return a.requiredIx - b.requiredIx
		}
		// Finally, sort alphabetically.
		return strings.Compare(a.Name, b.Name)
	})

	return properties, true
}

// NumProperties returns the number of properties in the schema.
func (s *Schema) NumProperties() int {
	return len(s.self.Properties)
}

// AdditionalProperties returns whether the schema allows additional properties
// and the schema for those properties if applicable.
func (s *Schema) AdditionalProperties() (*Schema, bool) {
	switch ap := s.self.AdditionalProperties.(type) {
	case bool:
		return nil, ap
	case *jsonschema.Schema:
		return wrapSchema(ap, s), true
	case nil:
		return nil, false
	default:
		panic("unreachable")
	}
}

// PatternProperty defines a pattern property of a schema.
type PatternProperty struct {
	// KeyPattern is the regexp pattern for the property key.
	KeyPattern string
	// ValueSchema is the schema for the property value corresponding to the key
	// pattern.
	ValueSchema *Schema
}

// PatternProperties represents a parsed and sorted JSON schema object's pattern
// properties.
type PatternProperties []PatternProperty

func (p PatternProperties) KeyPatterns() iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, prop := range p {
			if !yield(prop.KeyPattern) {
				return
			}
		}
	}
}

func (p PatternProperties) ValueSchemas() iter.Seq[*Schema] {
	return func(yield func(*Schema) bool) {
		for _, prop := range p {
			if !yield(prop.ValueSchema) {
				return
			}
		}
	}
}

func (p PatternProperties) Each() iter.Seq2[string, *Schema] {
	return func(yield func(string, *Schema) bool) {
		for _, prop := range p {
			if !yield(prop.KeyPattern, prop.ValueSchema) {
				return
			}
		}
	}
}

// PatternProperties returns an iterator for the pattern properties of the
// schema, or false if there are none.
func (s *Schema) PatternProperties() (PatternProperties, bool) {
	if len(s.self.PatternProperties) == 0 {
		return nil, false
	}

	props := make([]PatternProperty, 0, len(s.self.PatternProperties))
	for pattern, schema := range s.self.PatternProperties {
		props = append(props, PatternProperty{
			KeyPattern:  pattern.String(),
			ValueSchema: wrapSchema(schema, s),
		})
	}

	slices.SortFunc(props, func(a, b PatternProperty) int {
		return strings.Compare(a.KeyPattern, b.KeyPattern)
	})

	return props, true
}

// EnumValues returns the enum values of the schema assumed to be an enum.
func (s *Schema) EnumValues() iter.Seq[string] {
	if s.self.Enum == nil {
		return nil
	}

	return func(yield func(string) bool) {
		for _, v := range s.self.Enum.Values {
			switch val := v.(type) {
			case string:
				if !yield(val) {
					return
				}
			default:
				slog.Warn(
					"enum value is not a string, which is not supported",
					"location", s.Location(),
					"value", val)
			}
		}
	}
}

// Minimum returns the minimum value of the schema assumed to be a number.
func (s *Schema) Minimum() *float64 {
	return bigratToNum[float64](s.self.Minimum)
}

// Maximum returns the maximum value of the schema assumed to be a number.
func (s *Schema) Maximum() *float64 {
	return bigratToNum[float64](s.self.Maximum)
}

// MinItems returns the minItems value of the schema assumed to be an array.
func (s *Schema) MinItems() *int {
	return s.self.MinItems
}

// MaxItems returns the maxItems value of the schema assumed to be an array.
func (s *Schema) MaxItems() *int {
	return s.self.MaxItems
}

// AnyOf returns the anyOf schemas of the schema.
func (s *Schema) AnyOf() []*Schema {
	return wrapSchemas(s.self.AnyOf, s)
}

// AllOf returns the allOf schemas of the schema.
func (s *Schema) AllOf() []*Schema {
	return wrapSchemas(s.self.AllOf, s)
}

// OneOf returns the oneOf schemas of the schema.
func (s *Schema) OneOf() []*Schema {
	return wrapSchemas(s.self.OneOf, s)
}

// Parent returns the parent schema of the schema.
func (s *Schema) Parent() *Schema {
	return s.parent
}

// Equals returns true if the schema is equal to the other schema,
// based on their locations.
func (s *Schema) Equals(other *Schema) bool {
	return s.Location() == other.Location()
}

// Raw returns the underlying raw jsonschema.Schema.
func (s *Schema) Raw() *jsonschema.Schema {
	return s.self
}

// String returns the string representation of the schema.
func (s *Schema) String() string {
	return fmt.Sprintf(
		"{location: %s, type: %s, ref: %v}",
		s.Location().Relative(), s.Type().String(), s.IsRef())
}

// LogValue returns the slog value representation of the schema.
func (s *Schema) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("name", s.Name()),
		slog.Any("type", s.Type().String()),
		slog.Any("ref", s.IsRef()),
		slog.Any("location", s.Location().Relative()))
}

// SchemaLocation defines the location of a JSON Schema.
type SchemaLocation string

// IsAbsolute returns true if the schema location is absolute.
func (l SchemaLocation) IsAbsolute() bool {
	u, err := url.Parse(string(l))
	return err == nil && u.Scheme != ""
}

// IsRelative returns true if the schema location is relative.
func (l SchemaLocation) IsRelative() bool {
	return !l.IsAbsolute()
}

// Relative returns the schema location relative to the file,
// stripping any leading file path and the '#' character.
func (l SchemaLocation) Relative() SchemaLocation {
	u, err := url.Parse(string(l))
	if err != nil {
		slog.Error(
			"schema contains invalid URL location; returning as-is",
			"location", l,
			"err", err)
		return l
	}
	if u.Fragment == "" {
		return l
	}
	return SchemaLocation(u.Fragment)
}

// BaseName returns the base name of the schema location.
func (l SchemaLocation) BaseName() string {
	return path.Base(string(l.Relative()))
}

// DirPath returns the directory path of the schema location.
func (l SchemaLocation) DirPath() SchemaLocation {
	return SchemaLocation(path.Dir(string(l)))
}

// SchemaType is copied from jsonschema.jsonType.
// No idea why this type is private; the library is kind of awful.
type SchemaType int

const (
	InvalidType SchemaType = 0
	NullType    SchemaType = 1 << iota
	BooleanType
	NumberType
	IntegerType
	StringType
	ArrayType
	ObjectType
	EnumType // not in jsonschema.jsonType
)

// Has returns true if t has the given other type.
func (t SchemaType) Has(other SchemaType) bool { return t&other != 0 }

// HasAll returns true if t has all of the given other types.
func (t SchemaType) HasAll(others ...SchemaType) bool {
	for _, other := range others {
		if !t.Has(other) {
			return false
		}
	}
	return true
}

// Is returns true if t is exactly the given other type.
func (t SchemaType) Is(other SchemaType) bool {
	return t == other
}

// IsOneOf returns true if t is exactly one of the given other types.
func (t SchemaType) IsOneOf(others ...SchemaType) bool {
	return slices.ContainsFunc(others, func(other SchemaType) bool { return t.Is(other) })
}

// String returns the string representation of the schema types.
func (t SchemaType) String() string { return jsonschema.Types(t).String() }
