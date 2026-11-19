package machine

// A newer release can write fields into a machine's proven manifest
// that an older release does not have. After a rollback to the older
// release, a strict parse rejects that manifest as if each new field
// were a typo, and init falls back to the install's seed manifest.
// The machine then boots with its install-time storage and network,
// or powers off when the seed uses a newer field too.
//
// So init and machine-operator read a proven manifest with ParseKnown,
// which skips the unknown fields and returns their paths for the
// console. A staged manifest and a seed still parse strictly, because
// a typo in them must fail before anything boots from them.

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// ParseKnown parses a proven Machine manifest and returns the sorted
// paths of the fields it ignored. A manifest that fails the strict
// parse for another reason, such as a known field with the wrong
// type, fails here too.
func ParseKnown(raw []byte) (*Machine, []string, error) {
	m, strictErr := Parse(raw)
	if strictErr == nil {
		return m, nil, nil
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, strictErr
	}
	unknown := unknownFields(reflect.TypeFor[Machine](), doc, "")
	if len(unknown) == 0 {
		return nil, nil, strictErr
	}
	m = &Machine{}
	if err := yaml.Unmarshal(raw, m); err != nil {
		return nil, nil, strictErr
	}
	if m.Kind != "Machine" {
		return nil, nil, fmt.Errorf("the kind is %q, and a machine manifest must have kind Machine", m.Kind)
	}
	slices.Sort(unknown)
	return m, unknown, nil
}

// LoadKnown reads a proven manifest file with ParseKnown. A missing
// file returns an empty Machine, as Load does.
func LoadKnown(path string) (*Machine, []string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Machine{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	m, ignored, err := ParseKnown(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, ignored, nil
}

// unknownFields walks a decoded document beside the Go type it
// decodes into, and returns the path of each key that the type has no
// field for. It matches names without regard to case, as
// encoding/json does, so it agrees with the strict parse.
func unknownFields(t reflect.Type, value any, path string) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if decodesItself(t) {
		return nil
	}
	var unknown []string
	switch v := value.(type) {
	case map[string]any:
		switch t.Kind() {
		case reflect.Struct:
			for key, child := range v {
				field, ok := jsonField(t, key)
				if !ok {
					unknown = append(unknown, join(path, key))
					continue
				}
				unknown = append(unknown, unknownFields(field.Type, child, join(path, key))...)
			}
		case reflect.Map:
			for key, child := range v {
				unknown = append(unknown, unknownFields(t.Elem(), child, join(path, key))...)
			}
		}
	case []any:
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			for i, child := range v {
				unknown = append(unknown, unknownFields(t.Elem(), child, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	return unknown
}

// decodesItself reports whether a type has its own JSON or text
// decoder. The walk cannot tell which keys such a type accepts, so it
// skips the type.
func decodesItself(t reflect.Type) bool {
	pointer := reflect.PointerTo(t)
	return pointer.Implements(reflect.TypeFor[json.Unmarshaler]()) ||
		pointer.Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

// jsonField finds the struct field that a JSON key decodes into,
// including fields promoted from an embedded struct.
func jsonField(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() && !field.Anonymous {
			continue
		}
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if field.Anonymous && tag == "" {
			embedded := field.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				if promoted, ok := jsonField(embedded, key); ok {
					return promoted, true
				}
				continue
			}
		}
		name := tag
		if name == "" {
			name = field.Name
		}
		if strings.EqualFold(name, key) {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
