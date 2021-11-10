package buttplugschema

import (
	"fmt"

	_ "embed"

	"github.com/diamondburned/go-buttplugio/internal/buttplugschema/tmplutil"
)

// Type describes any type.
type Type interface {
	// GoName returns the Go name for the type. If it returns an empty string,
	// then the type is anonymous.
	GoName() string
	// GoType returns the Go representation of the type.
	GoType() string
	// SchemaName returns the original name.
	SchemaName() string
	// Description returns the description of the type.
	Description() string
	typ()
}

// BaseType describes the base type that will always implement the Type
// interface but isn't useful by itself.
type BaseType struct {
	schemaName  string
	goName      string
	goType      string
	description string
}

// SchemaName implements Type.
func (t BaseType) SchemaName() string { return t.schemaName }

// GoName implements Type.
func (t BaseType) GoName() string { return t.goName }

// GoType implements Type.
func (t BaseType) GoType() string { return t.goType }

// Description implements Type.
func (t BaseType) Description() string { return t.description }

func (t BaseType) typ() {}

// ObjectType is a Type.
type ObjectType struct {
	BaseType
	Fields []Field
}

//go:embed objectTmpl.tmpl
var objectTmpl string

func (o ObjectType) GoType() string {
	if o.goType != "struct{}" {
		return o.goType
	}
	return o.RawGoType()
}

func (o ObjectType) RawGoType() string {
	return tmplutil.String(objectTmpl, o)
}

// Field is a struct field.
type Field struct {
	Type
	FieldName string // field name
	JSONName  string
	Required  bool
}

// GoName returns the field name.
func (f Field) GoName() string {
	return f.FieldName
}

// ArrayType describes a slice of type.
type ArrayType struct {
	BaseType
	Type Type
}

func (a ArrayType) GoType() string {
	return "[]" + a.Type.GoType()
}

// StringType describes a string type.
type StringType struct {
	BaseType
}

// IntegerType describes an integer type.
type IntegerType struct {
	BaseType
	Minimum *int
	Maximum *int
}

// LimitString returns the limit in bracket notation. Null is translated to
// either "max" or "-max".
func (i IntegerType) LimitString() string {
	switch {
	case i.Minimum != nil && i.Maximum != nil:
		return fmt.Sprintf("[%d, %d]", *i.Minimum, *i.Maximum)
	case i.Minimum != nil:
		return fmt.Sprintf("[%d, max]", *i.Minimum)
	case i.Maximum != nil:
		return fmt.Sprintf("[-max, %d]", *i.Maximum)
	default:
		return ""
	}
}

// NumberType describes a floating point type.
type NumberType struct {
	BaseType
	Minimum *float64
	Maximum *float64
}

// LimitString returns the limit in bracket notation. Null is translated to
// either "max" or "-max".
func (n NumberType) LimitString() string {
	switch {
	case n.Minimum != nil && n.Maximum != nil:
		return fmt.Sprintf("[%g, %g]", *n.Minimum, *n.Maximum)
	case n.Minimum != nil:
		return fmt.Sprintf("[%g, max]", *n.Minimum)
	case n.Maximum != nil:
		return fmt.Sprintf("[-max, %g]", *n.Maximum)
	default:
		return ""
	}
}

// OneOfType describes a type union.
type OneOfType struct {
	BaseType
	Types []Type
}

// EnumType describes an enumeration of string values.
type EnumType struct {
	BaseType
	Values []EnumValue
}

// EnumValue is an enum value.
type EnumValue struct {
	GoName string
	Value  string
}

// BooleanType describes a boolean type.
type BooleanType struct {
	BaseType
}
