package schema

import (
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// field describes one field of a generated Go type and of its CRD
// schema. The Go emitter and the CRD emitter read the same fields, so
// the Go types and the CRDs cannot differ.
type field struct {
	JSON        string
	Go          string
	INDI        string
	Description string
	Type        *shape
	// A required field is always present, so its Go type is not a
	// pointer.
	Required bool
}

type shape struct {
	Kind shapeKind
	Enum []string
	Item *shape
	Name string
	Doc  string
	// Description is the CRD description of the objects in a list.
	Description string
	Fields      []field
	MinLength   int
	// The Go emitter declares a Shared type once, for all the kinds
	// that use it.
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

// stateField is the INDI state that every property in status has
// beside its values.
func stateField() field {
	return field{JSON: "state", Go: "State", Type: &shape{Kind: stateShape}, Description: "The property's state: " +
		"`Busy` while the driver carries out a change, `Ok` when the last change ended well, " +
		"`Alert` when it failed, and `Idle` before the first change."}
}

// values returns the fields for the members of a property. A number,
// text, or light property gets one field for each member, named value
// when MemberUnits gives the member no field name. A switch property
// gets the one field from switchField. A BLOB property gets no fields,
// because the operator does not request BLOB data.
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

// switchField returns the field that names the members of a switch
// property that are on: a string named value, or a list named values
// when the rule is AnyOfMany. In spec the field has an enum of the
// members that libindi defines, unless the driver defines them. Status
// has no enum, because a driver can define more members, and the API
// server rejects a status value that is not in its enum. The field is
// not named on, because YAML 1.1, which kubectl reads, parses a bare
// on key as the boolean true.
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
