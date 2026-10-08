package machine

// A manifest that this machine already proved can name fields that
// this release does not know. A person sets a field that a newer
// release added, the field applies, and the manifest that holds it
// becomes the proven manifest. A rollback then boots the older
// release, whose strict parse cannot tell that field from a typo.
// Without the proven manifest, init falls back to the seed from the
// install: the machine boots its install-time storage and network,
// or, when the seed names the newer field too, it powers off.
//
// So the readers of a manifest that a boot already proved parse it
// with ParseKnown. The strict parse runs first. When it fails only
// because of fields this release does not know, ParseKnown parses the
// manifest again without them and names each field it skipped, so the
// console says what the rollback leaves out. A staged manifest and a
// seed still parse strictly, because those are where a typo must fail
// before anything boots it.

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

// ParseKnown reads a Machine manifest that a boot already proved. It
// answers the manifest and the path of each field it skipped, sorted.
// A manifest that fails the strict parse for any other reason, such
// as a known field with the wrong type, fails here too.
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
		return nil, nil, fmt.Errorf("expected kind Machine, got %q", m.Kind)
	}
	slices.Sort(unknown)
	return m, unknown, nil
}

// LoadKnown reads a manifest file that a boot already proved, with
// ParseKnown. A missing file is a machine with every field at its
// default, as it is for Load.
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
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, ignored, nil
}

// unknownFields walks a decoded document beside the Go type it
// decodes into, and answers the path of each key that the type has
// no field for. It matches names the way encoding/json does, without
// regard to case, so it agrees with the strict parse about which
// fields are known.
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

// decodesItself reports whether a type decodes its own JSON, so its
// keys are its own business.
func decodesItself(t reflect.Type) bool {
	pointer := reflect.PointerTo(t)
	return pointer.Implements(reflect.TypeFor[json.Unmarshaler]()) ||
		pointer.Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

// jsonField finds the field of a struct that a JSON key decodes into,
// including the fields that an embedded struct promotes.
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
