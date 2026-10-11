package schema

import (
	"strings"
	"unicode"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// namer names the Go identifiers of one kind's generated types.
type namer struct{ kind string }

// initialisms are the words that Go spells in capitals.
var initialisms = map[string]string{
	"Fps": "FPS", "Wcs": "WCS", "Ota": "OTA", "Tle": "TLE", "Utc": "UTC", "Dsp": "DSP",
	"Dft": "DFT", "Idft": "IDFT", "Pec": "PEC", "Cfa": "CFA", "Gps": "GPS",
}

// goName answers the Go name of a JSON name: each word capitalized, and
// an initialism in capitals.
func (namer) goName(json string) string {
	var words []string
	start := 0
	for i, r := range json {
		if i > 0 && (unicode.IsUpper(r) || unicode.IsDigit(r) && !unicode.IsDigit(rune(json[i-1]))) {
			words = append(words, json[start:i])
			start = i
		}
	}
	words = append(words, json[start:])
	for i, w := range words {
		w = strings.ToUpper(w[:1]) + w[1:]
		if up, ok := initialisms[w]; ok {
			w = up
		}
		words[i] = w
	}
	return strings.Join(words, "")
}

// typeName answers the name of a generated type of one field, such as
// CameraFrameStatus.
func (n namer) typeName(json, suffix string) string {
	return n.kind + n.goName(json) + suffix
}

// statusField answers the field of a standard property in status. A
// property with fixed members holds its state and its values. A
// property whose elements the driver chooses holds its state and the
// list of its elements. A family holds one item for each property of
// the family, with its number or its name, its state, and its values.
func (k kind) statusField(p property) field {
	n := namer{k.Kind}
	f := field{JSON: p.Field, Go: n.goName(p.Field), INDI: p.Name, Description: p.statusText()}
	name := n.typeName(p.Field, "Status")
	doc := name + " is " + p.Name + " as the driver reports it."
	switch {
	case p.Family != "":
		item := object(name, doc, append([]field{p.familyKey(n)}, append([]field{stateField()}, p.values(n, false)...)...)...)
		item.Description = "One property of the family."
		f.Type = list(item)
	case p.driverElements():
		f.Type = object(name, doc, stateField(), field{JSON: "elements", Go: "Elements",
			Description: "Each element that the driver defines, in the driver's order.", Type: list(element(p))})
	default:
		f.Type = object(name, doc, append([]field{stateField()}, p.values(n, false)...)...)
	}
	return f
}

// familyKey is the field that tells the properties of a family apart:
// the number in a numbered property's name, or the whole name of a
// property that a parameter names.
func (p property) familyKey(n namer) field {
	if p.Family == familyNumbered {
		prefix := strings.TrimRight(p.Name, "0123456789")
		return field{JSON: "number", Go: "Number", Required: true, Type: &shape{Kind: int32Shape},
			Description: "The number at the end of the property's name, such as " +
				strings.TrimPrefix(p.Name, prefix) + " for `" + p.Name + "`."}
	}
	return field{JSON: "name", Go: "Name", Required: true, Type: text(),
		Description: "The property's name, which a member of `" + p.NamedBy + "` gives, such as `" + p.Name + "`."}
}

// element is the item of a list of the elements that a driver chooses:
// the element's name and label, and its value.
func element(p property) *shape {
	s := elementOf(p)
	s.Shared = true
	s.Description = "One element that the driver defines."
	return s
}

func elementOf(p property) *shape {
	name := field{JSON: "name", Go: "Name", Required: true, Type: text(), Description: "The element's name, as the driver defines it."}
	label := field{JSON: "label", Go: "Label", Type: text(), Description: "The label that the driver gives the element."}
	switch p.Type {
	case indi.SwitchType:
		return object("SwitchElement", "SwitchElement is one element of a switch that the driver defines.", name, label,
			field{JSON: "active", Go: "Active", Type: &shape{Kind: boolShape}, Description: "Whether the element is on."})
	case indi.NumberType:
		return object("NumberElement", "NumberElement is one element of a number that the driver defines.", name, label,
			field{JSON: "value", Go: "Value", Type: number(), Description: "The element's value. " + units[p.Unit].sentence})
	case indi.LightType:
		return object("LightElement", "LightElement is one light that the driver defines.", name, label,
			field{JSON: "state", Go: "State", Type: &shape{Kind: stateShape}, Description: "The light's state."})
	}
	return object("TextElement", "TextElement is one element of a text that the driver defines.", name, label,
		field{JSON: "value", Go: "Value", Type: text(), Description: "The element's text."})
}

// specField answers the field of a setting in spec. The value of a
// property with one member, or of a switch, is the field itself. A
// property with several members is an object of its values. A switch
// whose elements the driver chooses names an element, a text whose
// elements the driver chooses lists its texts in the driver's order,
// and a number whose elements the driver chooses lists each element by
// name. A family lists its items by number or by name.
func (k kind) specField(p property) field {
	n := namer{k.Kind}
	f := field{JSON: p.Field, Go: n.goName(p.Field), INDI: p.Name, Description: p.specText()}
	values := p.values(n, true)
	name := n.typeName(p.Field, "Spec")
	doc := name + " is the value that the operator writes to " + p.Name + "."
	switch {
	case p.Family != "":
		item := object(name, doc, append([]field{p.familyKey(n)}, values...)...)
		item.Description = "One property of the family, and the values to write to it."
		f.Type = list(item)
	case p.driverElements() && p.Type == indi.SwitchType:
		f.Type = text()
		if p.Rule == indi.AnyOfMany {
			f.Type = list(text())
		}
	case p.driverElements() && p.Type == indi.TextType:
		f.Type = list(&shape{Kind: stringShape, MinLength: 1})
	case p.driverElements():
		setting := object("NumberSetting", "NumberSetting is the value of one element of a number that the driver defines.",
			field{JSON: "name", Go: "Name", Required: true, Type: text(), Description: "The element's name, as the driver defines it."},
			field{JSON: "value", Go: "Value", Required: true, Type: number(), Description: "The element's value. " + units[p.Unit].sentence})
		setting.Shared = true
		setting.Description = "One element, and the value to write to it."
		f.Type = list(setting)
	case len(values) == 1 && (p.Type == indi.SwitchType || len(p.Property.Members) == 1):
		f.Type = values[0].Type
		f.INDI = p.Name
	default:
		f.Type = object(name, doc, values...)
	}
	return f
}
