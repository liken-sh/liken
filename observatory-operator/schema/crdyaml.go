package schema

import (
	"strconv"
	"strings"
)

// fieldsYAML renders fields as the YAML properties of a CRD schema,
// starting at the left margin. It matches the style of the hand-written
// parts of the CRD templates: type first, then the folded description,
// then the nested content.
func fieldsYAML(fields []field) string {
	var b strings.Builder
	for _, f := range fields {
		writeField(&b, 0, f.JSON, f.Description, f.Type)
	}
	return b.String()
}

func writeField(b *strings.Builder, indent int, key, description string, s *shape) {
	pad := strings.Repeat(" ", indent)
	if yamlBooleans[strings.ToLower(key)] {
		key = strconv.Quote(key)
	}
	b.WriteString(pad + key + ":\n")
	inner := pad + "  "
	switch s.Kind {
	case numberShape:
		b.WriteString(inner + "type: number\n")
	case int32Shape:
		b.WriteString(inner + "type: integer\n" + inner + "format: int32\n")
	case boolShape:
		b.WriteString(inner + "type: boolean\n")
	case stateShape:
		b.WriteString(inner + "type: string\n" + inner + "enum: [Idle, Ok, Busy, Alert]\n")
	case stringShape:
		b.WriteString(inner + "type: string\n")
		writeStringLimits(b, inner, s)
	case listShape:
		b.WriteString(inner + "type: array\n")
	case objectShape:
		b.WriteString(inner + "type: object\n")
		var required []string
		for _, f := range s.Fields {
			if f.Required {
				required = append(required, f.JSON)
			}
		}
		if len(required) > 0 {
			b.WriteString(inner + "required: [" + strings.Join(required, ", ") + "]\n")
		}
	}
	writeDescription(b, indent+2, description)
	switch s.Kind {
	case listShape:
		b.WriteString(inner + "x-kubernetes-list-type: atomic\n")
		if s.Item.Kind == objectShape {
			writeField(b, indent+2, "items", s.Item.Description, s.Item)
			return
		}
		b.WriteString(inner + "items:\n")
		b.WriteString(inner + "  type: string\n")
		writeStringLimits(b, inner+"  ", s.Item)
	case objectShape:
		b.WriteString(inner + "properties:\n")
		for _, f := range s.Fields {
			writeField(b, indent+4, f.JSON, f.Description, f.Type)
		}
	}
}

// yamlBooleans lists the words that YAML 1.1 parses as booleans. A key
// with one of these names must be quoted.
var yamlBooleans = map[string]bool{
	"y": true, "n": true, "yes": true, "no": true, "on": true, "off": true, "true": true, "false": true,
}

func writeStringLimits(b *strings.Builder, pad string, s *shape) {
	if len(s.Enum) > 0 {
		quoted := make([]string, len(s.Enum))
		for i, v := range s.Enum {
			quoted[i] = strconv.Quote(v)
		}
		b.WriteString(pad + "enum: [" + strings.Join(quoted, ", ") + "]\n")
	}
	if s.MinLength > 0 {
		b.WriteString(pad + "minLength: " + strconv.Itoa(s.MinLength) + "\n")
	}
}

// writeDescription writes description as a folded YAML scalar. It wraps
// the text at the column that the CRD's indentation gives it, so that
// tidy, which rewraps descriptions, leaves it unchanged.
func writeDescription(b *strings.Builder, indent int, text string) {
	pad := strings.Repeat(" ", indent)
	b.WriteString(pad + "description: >-\n")
	for _, line := range wrap(text, indent+2) {
		b.WriteString(pad + "  " + line + "\n")
	}
}
