package main

// The settings of a device resource: the fields of its spec that hold
// a standard property of its interface, which schema/ generates, such
// as a camera's gain or a filter wheel's filter names. Each field's
// indi tag names its property, and the field's shape follows the
// property's (schema/fields.go): a number or a text with one member is
// the value itself, a switch names the members that are on, a property
// with several members is an object with a field for each, and a
// property whose members the driver chooses lists them. The writer reads
// the property's definition from the driver, so a field writes the
// members that the driver defines, and a property or a member that the
// driver lacks gets a note in the step's summary.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// writeSettings writes each setting that a resource's spec states to
// the INDI device that serves it, and answers whether it sent any.
func writeSettings(ctx context.Context, h handle, notes *[]string) (bool, error) {
	newSettings := observatory.Standards[h.d.kind].Settings
	if newSettings == nil || len(h.d.object.Spec.raw) == 0 {
		return false, nil
	}
	settings := newSettings()
	if err := json.Unmarshal(h.d.object.Spec.raw, settings); err != nil {
		return false, fmt.Errorf("%s: reading the settings in spec: %w", h, err)
	}
	value := reflect.ValueOf(settings).Elem()
	changed := false
	for i, field := range slices.Collect(value.Type().Fields()) {
		v := value.Field(i)
		if v.IsZero() {
			continue
		}
		sent, err := writeSetting(ctx, h, field.Tag.Get("indi"), v, notes)
		if err != nil {
			return changed, err
		}
		changed = changed || sent
	}
	return changed, nil
}

// writeSetting writes one field to the property that its tag names.
func writeSetting(ctx context.Context, h handle, property string, v reflect.Value, notes *[]string) (bool, error) {
	if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Struct && v.Type().Elem() != reflect.TypeFor[observatory.NumberSetting]() {
		return writeFamily(ctx, h, property, v, notes)
	}
	p, ok := h.property(ctx, property)
	if !ok {
		*notes = append(*notes, fmt.Sprintf("no %s on %s", property, h))
		return false, nil
	}
	values := map[string]any{}
	switch {
	case v.Kind() == reflect.Pointer && v.Elem().Kind() == reflect.Struct:
		values = members(v.Elem())
	case v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[observatory.NumberSetting]():
		for _, s := range v.Interface().([]observatory.NumberSetting) {
			values[s.Name] = s.Value
		}
	case v.Kind() == reflect.Slice && p.Type == indi.SwitchType:
		on := v.Interface().([]string)
		for _, m := range p.Members {
			values[m.Name] = slices.Contains(on, m.Name)
		}
	case v.Kind() == reflect.Slice:
		// A text whose members the driver chooses, such as FILTER_NAME,
		// takes the values in the order of its members.
		texts := v.Interface().([]string)
		for i, text := range texts {
			if i >= len(p.Members) {
				*notes = append(*notes, fmt.Sprintf("no member for %d of the values of %s on %s (%d members)", len(texts)-i, property, h, len(p.Members)))
				break
			}
			values[p.Members[i].Name] = text
		}
	case p.Type == indi.SwitchType:
		values[v.String()] = true
	case len(p.Members) > 0 && v.Kind() == reflect.Pointer:
		values[p.Members[0].Name] = v.Elem().Interface()
	case len(p.Members) > 0:
		values[p.Members[0].Name] = v.String()
	}
	return writeMembers(ctx, h, p, values, notes)
}

// writeFamily writes each item of a family, such as the limits of each
// weather parameter. An item names its property by a name or by the
// number at the end of the property's name.
func writeFamily(ctx context.Context, h handle, tagged string, items reflect.Value, notes *[]string) (bool, error) {
	changed := false
	for i := range items.Len() {
		item := items.Index(i)
		property := tagged
		if name := item.FieldByName("Name"); name.IsValid() {
			property = name.String()
		} else if number := item.FieldByName("Number"); number.IsValid() {
			property = strings.TrimRight(tagged, "0123456789") + strconv.FormatInt(number.Int(), 10)
		}
		p, ok := h.property(ctx, property)
		if !ok {
			*notes = append(*notes, fmt.Sprintf("no %s on %s", property, h))
			continue
		}
		sent, err := writeMembers(ctx, h, p, members(item), notes)
		if err != nil {
			return changed, err
		}
		changed = changed || sent
	}
	return changed, nil
}

// members answers the values of an object's fields that name a member
// in their indi tag, by member. A field that is absent writes nothing.
func members(object reflect.Value) map[string]any {
	out := map[string]any{}
	for i, field := range slices.Collect(object.Type().Fields()) {
		member, v := field.Tag.Get("indi"), object.Field(i)
		if member == "" || v.IsZero() {
			continue
		}
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		out[member] = v.Interface()
	}
	return out
}

// writeMembers writes the values of a property's members, by the
// property's type, and notes each member that the driver lacks.
func writeMembers(ctx context.Context, h handle, p indi.Property, values map[string]any, notes *[]string) (bool, error) {
	numbers, texts, switches := map[string]float64{}, map[string]string{}, map[string]bool{}
	for _, member := range sortedKeys(values) {
		if _, defined := p.Member(member); !defined {
			*notes = append(*notes, fmt.Sprintf("no member %s of %s on %s", member, p.Name, h))
			continue
		}
		switch value := values[member].(type) {
		case float64:
			numbers[member] = value
		case string:
			texts[member] = value
		case bool:
			switches[member] = value
		}
	}
	switch {
	case p.Type == indi.NumberType && len(numbers) > 0:
		return h.setNumbers(ctx, p.Name, numbers)
	case p.Type == indi.TextType && len(texts) > 0:
		return h.setTexts(ctx, p.Name, texts)
	case p.Type == indi.SwitchType && len(switches) > 0:
		return h.setSwitches(ctx, p.Name, switches)
	}
	return false, nil
}
