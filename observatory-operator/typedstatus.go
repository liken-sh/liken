package main

// The typed status fields of a resource: one field for each standard
// property that the resource holds of its INDI device (devicesplit.go),
// which schema/ generates, such as a camera's temperature or a filter
// wheel's slot. Each field's indi tag names its property, and the
// field's shape follows the property's (schema/fields.go): the state,
// then a field for each member that the tag names, the names of the
// switches that are on, or the list of the elements that the driver
// chooses. A family, such as DIGITAL_OUTPUT_1 and the rest, is a list
// with one item for each property. The schema holds each definition, so
// a field holds the state and the values alone, and status.limits holds
// the ranges that the driver chooses.

import (
	"math"
	"reflect"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// standardStatus answers the typed fields of a resource's kind, such as
// a *observatory.CameraStandard, from the properties that the resource
// holds, or nil when it holds none.
func standardStatus(kind observatory.Kind, r reader) any {
	newStatus := observatory.Standards[kind].Status
	if newStatus == nil || len(r.held) == 0 {
		return nil
	}
	status := newStatus()
	value := reflect.ValueOf(status).Elem()
	for i := range value.NumField() {
		field, property := value.Field(i), value.Type().Field(i).Tag.Get("indi")
		if field.Kind() == reflect.Slice {
			field.Set(family(field.Type(), property, kind, r))
			continue
		}
		if p, ok := r.property(property); ok {
			typed := reflect.New(field.Type().Elem())
			fill(typed.Elem(), p)
			field.Set(typed)
		}
	}
	return status
}

// family answers the items of a family: one for each numbered
// property, such as DIGITAL_OUTPUT_2 for the tag DIGITAL_OUTPUT_1, or
// one for each property that a member of the kind's NamedBy names. An
// item holds its number or its name beside the property's values.
func family(list reflect.Type, tagged string, kind observatory.Kind, r reader) reflect.Value {
	items := reflect.MakeSlice(list, 0, 0)
	add := func(p indi.Property, key func(item reflect.Value)) {
		item := reflect.New(list.Elem()).Elem()
		fill(item, p)
		key(item)
		items = reflect.Append(items, item)
	}
	if _, named := list.Elem().FieldByName("Name"); named {
		for _, by := range observatory.Standards[kind].NamedBy {
			names, _ := r.property(by)
			for _, m := range names.Members {
				if p, ok := r.property(m.Name); ok {
					add(p, func(item reflect.Value) { item.FieldByName("Name").SetString(p.Name) })
				}
			}
		}
		return items
	}
	prefix := strings.TrimRight(tagged, "0123456789")
	for _, p := range r.held {
		if n, ok := familyNumber(p.Name, prefix); ok {
			add(p, func(item reflect.Value) { item.FieldByName("Number").SetInt(int64(n)) })
		}
	}
	return items
}

// fill sets the fields of one property's struct from the property.
func fill(object reflect.Value, p indi.Property) {
	for i := range object.NumField() {
		f, field := object.Type().Field(i), object.Field(i)
		if member := f.Tag.Get("indi"); member != "" {
			if m, ok := p.Member(member); ok {
				setMember(field, m)
			}
			continue
		}
		switch f.Name {
		case "State":
			field.Set(reflect.ValueOf(p.State))
		case "Value":
			if on := p.On(); len(on) > 0 {
				field.SetString(on[0])
			}
		case "Values":
			field.Set(reflect.ValueOf(p.On()))
		case "Elements":
			field.Set(reflect.ValueOf(elements(field.Interface(), p)))
		}
	}
}

// setMember sets a field to the value of one member, by the field's
// type: a number, a text, or a light. A number that JSON cannot hold,
// such as NaN, stays absent, because a status that does not encode
// would never be written.
func setMember(field reflect.Value, m indi.Member) {
	switch field.Interface().(type) {
	case *float64:
		if finite(m.Number) {
			number := m.Number
			field.Set(reflect.ValueOf(&number))
		}
	case string:
		field.SetString(m.Text)
	case indi.State:
		field.Set(reflect.ValueOf(m.Light))
	}
}

// elements answers the list of the elements that the driver chooses,
// in the type of the field it goes in.
func elements(field any, p indi.Property) any {
	switch field.(type) {
	case []observatory.SwitchElement:
		out := make([]observatory.SwitchElement, 0, len(p.Members))
		for _, m := range p.Members {
			out = append(out, observatory.SwitchElement{Name: m.Name, Label: m.Label, Active: m.Switch})
		}
		return out
	case []observatory.NumberElement:
		out := make([]observatory.NumberElement, 0, len(p.Members))
		for _, m := range p.Members {
			element := observatory.NumberElement{Name: m.Name, Label: m.Label}
			if finite(m.Number) {
				element.Value = &m.Number
			}
			out = append(out, element)
		}
		return out
	case []observatory.LightElement:
		out := make([]observatory.LightElement, 0, len(p.Members))
		for _, m := range p.Members {
			out = append(out, observatory.LightElement{Name: m.Name, Label: m.Label, State: m.Light})
		}
		return out
	}
	out := make([]observatory.TextElement, 0, len(p.Members))
	for _, m := range p.Members {
		out = append(out, observatory.TextElement{Name: m.Name, Label: m.Label, Value: m.Text})
	}
	return out
}

// limits answers the range of each number that a client can write,
// of the properties that a resource holds, by property and member. A
// read-only number has a range too, but nothing that a client sends
// meets it, so the status leaves it out. A range whose min equals its
// max is no range: libindi's clients read it as a number with no
// limit, so it carries none, and the status leaves it out too.
func limits(r reader) map[string]map[string]observatory.Limit {
	var out map[string]map[string]observatory.Limit
	for _, p := range r.held {
		if p.Type != indi.NumberType || p.Perm == indi.ReadOnly {
			continue
		}
		for _, m := range p.Members {
			if m.Min == m.Max || !finite(m.Min) || !finite(m.Max) || !finite(m.Step) {
				continue
			}
			if out == nil {
				out = map[string]map[string]observatory.Limit{}
			}
			if out[p.Name] == nil {
				out[p.Name] = map[string]observatory.Limit{}
			}
			out[p.Name][m.Name] = observatory.Limit{Min: m.Min, Max: m.Max, Step: m.Step}
		}
	}
	return out
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
