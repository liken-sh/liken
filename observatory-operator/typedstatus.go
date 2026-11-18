package main

// The typed status fields give each standard INDI property that a
// resource reads (devicesplit.go) its own field, such as a camera's
// temperature or a filter wheel's slot. schema/ generates their types,
// and each field's indi tag names its property. A field has the
// property's state and, depending on the property, the values of the
// members that the tag names, the names of the switches that are on, or
// the property's elements. A numbered family of properties, such as
// DIGITAL_OUTPUT_1, DIGITAL_OUTPUT_2, and so on, is a list with one item
// for each property. The ranges of writable numbers go in status.limits
// instead (limits below).

import (
	"math"
	"reflect"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// standardStatus returns the typed fields of a resource's kind, such as
// a *observatory.CameraStandard, filled from r's properties. It returns
// nil when the kind has no typed fields or r has no properties.
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

// family returns the list for a field whose type is a slice. When the
// item type has a Name field, there is one item for each property that
// a member of one of the kind's NamedBy properties names. Otherwise
// there is one item for each property numbered like the tag, such as
// DIGITAL_OUTPUT_2 for the tag DIGITAL_OUTPUT_1.
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

// setMember leaves a number that JSON cannot encode, such as NaN, nil,
// because a status that fails to encode is never written.
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

// limits returns the range of each writable number member, keyed by
// property name and member name. It leaves out read-only numbers,
// because a client never writes them, and a range whose min equals its
// max, because the INDI protocol says to ignore such a range.
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
