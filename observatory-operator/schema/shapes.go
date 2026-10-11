package schema

import (
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// A field is one field of a generated type, and of the schema that
// the CRD gives it. Both emitters read the same fields, so the Go
// types and the CRDs cannot differ.
type field struct {
	JSON        string
	Go          string
	INDI        string
	Description string
	Type        *shape
	// Required fields are never absent, so they hold a value, not a
	// pointer.
	Required bool
}

// A shape is the type of a field.
type shape struct {
	Kind shapeKind
	Enum []string
	Item *shape
	Name string
	Doc  string
	// Description describes an object in a list, for the CRD.
	Description string
	Fields      []field
	MinLength   int
	// Shared is a type that several kinds use, which the Go emitter
	// declares once, beside the kinds' own types.
	Shared bool
}

type shapeKind int

const (
	numberShape shapeKind = iota
	stringShape
	int32Shape
	boolShape
	stateShape
	listShape
	objectShape
)

func number() *shape          { return &shape{Kind: numberShape} }
func text() *shape            { return &shape{Kind: stringShape} }
func list(item *shape) *shape { return &shape{Kind: listShape, Item: item} }
func object(name, doc string, fields ...field) *shape {
	return &shape{Kind: objectShape, Name: name, Doc: doc, Fields: fields}
}

// stateField is the light of a property, which every field in status
// holds beside the values.
func stateField() field {
	return field{JSON: "state", Go: "State", Type: &shape{Kind: stateShape}, Description: "The property's state: " +
		"`Busy` while the driver carries out a change, `Ok` when the last change ended well, " +
		"`Alert` when it failed, and `Idle` before the first change."}
}

// values answers the fields of a property's members, for status, or
// for spec when forSpec. A number or a text gives one field for each
// member, named by the entry, or value for a property with one member.
// A light gives one state for each member. A switch gives one field,
// value, with the name of the member that is on, or values with the
// names of the members that are on. A BLOB gives none,
// because the operator never asks for BLOB data.
func (p property) values(names namer, forSpec bool) []field {
	switch p.Type {
	case indi.SwitchType:
		return []field{p.switchField(forSpec)}
	case indi.BLOBType:
		return nil
	}
	var out []field
	for _, m := range p.Property.Members {
		me := p.MemberUnits[m.Name]
		json, unit := me.Field, me.Unit
		if json == "" {
			json, unit = "value", p.Unit
		}
		f := field{JSON: json, Go: names.goName(json), INDI: m.Name, Description: p.memberText(m, me, unit)}
		switch p.Type {
		case indi.NumberType:
			f.Type = number()
		case indi.TextType:
			f.Type = text()
		case indi.LightType:
			f.Type = &shape{Kind: stateShape}
		}
		out = append(out, f)
	}
	return out
}

// switchField is the field of a switch that names the members that
// are on: one name for a switch whose rule lets one member at most be
// on, and a list for a switch whose rule lets any number be on. Spec
// holds an enum of the members that libindi defines. Status holds no
// enum, because a driver can define more members, and the API server
// refuses a status with a value that its enum lacks. The field is
// named value, not on, because YAML 1.1, which kubectl reads, takes a
// bare on key as the boolean true.
func (p property) switchField(forSpec bool) field {
	value := text()
	if forSpec && !p.driverElements() {
		for _, m := range p.Property.Members {
			value.Enum = append(value.Enum, m.Name)
		}
	}
	example := ""
	if len(p.Property.Members) > 0 {
		example = ", such as `" + p.Property.Members[0].Name + "`"
	}
	f := field{JSON: "value", Go: "Value", Type: value,
		Description: "The name of the member that is on" + example + ". Absent when no member is on."}
	if p.Rule == indi.AnyOfMany {
		f.JSON, f.Go, f.Type = "values", "Values", list(value)
		f.Description = "The names of the members that are on" + example + "."
	}
	return f
}
