package schema

// A fields file is written by hand, so the generator refuses an entry
// that does not fit its property, rather than writing a field that a
// driver cannot fill. Each case gives one property and one entry, and
// the start of the error.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/indi/standard"
)

// numberProperty builds a writable number with the members named.
func numberProperty(members ...string) standard.Property {
	p := standard.Property{Name: "SOME_NUMBER", Type: indi.NumberType, Perm: indi.ReadWrite}
	for _, m := range members {
		p.Members = append(p.Members, standard.Member{Name: m, Label: m})
	}
	return p
}

func switchProperty() standard.Property {
	return standard.Property{Name: "SOME_SWITCH", Type: indi.SwitchType, Perm: indi.ReadWrite, Rule: indi.OneOfMany,
		Members: []standard.Member{{Name: "A"}, {Name: "B"}}}
}

func TestTheGeneratorRefusesAnEntryThatDoesNotFit(t *testing.T) {
	readOnly := numberProperty("V")
	readOnly.Perm = indi.ReadOnly
	text := standard.Property{Name: "SOME_TEXT", Type: indi.TextType, Perm: indi.ReadWrite,
		Members: []standard.Member{{Name: "A"}, {Name: "B"}}}
	cases := []struct {
		name  string
		p     standard.Property
		e     entry
		error string
	}{
		{"a field that is no JSON name", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "Some", Role: roleReading, Unit: "s"}, "the field"},
		{"a field that YAML read as a boolean", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "true", Role: roleReading, Unit: "s"}, "the field"},
		{"an unknown role", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "some", Role: "status", Unit: "s"}, "the role"},
		{"a setting that no client can write", readOnly, entry{Text: "A thing, in seconds.", Field: "some", Role: roleSetting, Unit: "s"}, "a setting must"},
		{"unknown elements", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, Unit: "s", Elements: "client"}, "elements is"},
		{"an unknown family", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, Unit: "s", Family: "lettered"}, "family is"},
		{"a family of the driver's elements", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, Unit: "s", Family: familyNumbered, Elements: "driver"}, "a family's members"},
		{"a named family that names nothing", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, Unit: "s", Family: familyNamed}, "a named family"},
		{"no description", numberProperty("V"), entry{Field: "some", Role: roleReading, Unit: "s"}, "the entry has no description"},
		{"a description with no unit", numberProperty("V"), entry{Text: "A thing.", Field: "some", Role: roleReading, Unit: "s"}, "the description does not name the unit"},
		{"an unknown unit", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, Unit: "parsecs"}, "the unit"},
		{"members of a switch", switchProperty(), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"A": {Field: "a"}}}, "a Switch whose"},
		{"a number with no unit", numberProperty("V"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading}, "a number needs"},
		{"a unit for several members", numberProperty("V", "W"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, Unit: "s"}, "unit is only"},
		{"a member with no entry", numberProperty("V", "W"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"V": {Field: "v", Unit: "s", Description: "V, in seconds."}}}, "the member W"},
		{"a member with a bad name", numberProperty("V", "W"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"V": {Field: "V", Unit: "s"}, "W": {Field: "w", Unit: "s", Description: "W, in seconds."}}}, "the member V names"},
		{"a member with no unit", numberProperty("V", "W"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"V": {Field: "v"}, "W": {Field: "w", Unit: "s", Description: "W, in seconds."}}}, "the member V needs"},
		{"a unit on a text", text, entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"A": {Field: "a", Unit: "s"}, "B": {Field: "b", Description: "B."}}}, "the member A is no number"},
		{"a member with no description", numberProperty("V", "W"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"V": {Field: "v", Unit: "s"}, "W": {Field: "w", Unit: "s"}}}, "the member V has no description"},
		{"a member whose description names no unit", numberProperty("V", "W"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"V": {Field: "v", Unit: "s", Description: "V."}, "W": {Field: "w", Unit: "s"}}}, "the description of the member V"},
		{"a member that the property lacks", numberProperty("V", "W"), entry{Text: "A thing, in seconds.", Field: "some", Role: roleReading, MemberUnits: map[string]memberEntry{"V": {Field: "v", Unit: "s", Description: "V, in seconds."}, "W": {Field: "w", Unit: "s", Description: "W, in seconds."}, "X": {Field: "x", Unit: "s", Description: "X, in seconds."}}}, "the entry names the member X"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := property{Property: c.p, entry: c.e}.check()
			if err == nil || !strings.HasPrefix(err.Error(), c.error) {
				t.Errorf("check = %v, want an error that starts %q", err, c.error)
			}
		})
	}
}

// fieldsFile writes a fields file into a directory of its own.
func fieldsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "things"+fieldsSuffix)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The standard set that the cases of a fields file read: one
// interface with one number.
var someSets = map[string]standard.Interface{"some": {Name: "some", Properties: []standard.Property{numberProperty("V")}}}

func TestTheGeneratorRefusesAFieldsFileThatMissesTheStandardSet(t *testing.T) {
	entryOfV := "    SOME_NUMBER: {field: some, role: reading, unit: s, description: in seconds}\n"
	cases := []struct {
		name, body, error string
	}{
		{"a field that the type lacks", "kind: Thing\nshape: round\n", "unknown field"},
		{"an interface with no capture", "kind: Thing\ninterfaces: [other]\nproperties: {}\n", `names the interface "other"`},
		{"a standard property with no entry", "kind: Thing\ninterfaces: [some]\nproperties: {}\n", "has no entry for the standard property SOME_NUMBER"},
		{"an entry with no standard property", "kind: Thing\ninterfaces: [some]\nproperties:\n" + entryOfV + "    OTHER: {field: other, role: reading}\n", "has an entry for OTHER"},
		{"a detection by a property of no interface", "kind: Thing\ninterfaces: [some]\ndetect: [OTHER]\nproperties:\n" + entryOfV, "detects the kind by OTHER"},
		{"an entry that does not fit", "kind: Thing\ninterfaces: [some]\nproperties:\n    SOME_NUMBER: {field: some, role: reading, description: a thing}\n", "SOME_NUMBER: a number needs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := readKind(fieldsFile(t, c.body), someSets)
			if err == nil || !strings.Contains(err.Error(), c.error) {
				t.Errorf("readKind = %v, want an error with %q", err, c.error)
			}
		})
	}
}

// A typed field must not take the name of a field that the template
// writes, because Go would drop both from the JSON with no error.
func TestTheGeneratorRefusesATypedFieldThatCollides(t *testing.T) {
	hand := map[string]map[string]any{"spec": {"driver": nil}, "status": {"pod": nil, "properties": nil}}
	cases := []struct {
		name  string
		typed map[string][]field
		error string
	}{
		{"a status field named pod", map[string][]field{"status": {{JSON: "pod", INDI: "SOME_POD"}}}, "status.pod, from SOME_POD"},
		{"a spec field named driver", map[string][]field{"spec": {{JSON: "driver", INDI: "SOME_DRIVER"}}}, "spec.driver, from SOME_DRIVER"},
		{"two fields of one name", map[string][]field{"status": {{JSON: "speed"}, {JSON: "speed"}}}, "two typed fields named status.speed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := collisions("Thing", hand, c.typed)
			if err == nil || !strings.Contains(err.Error(), c.error) {
				t.Errorf("collisions = %v, want an error with %q", err, c.error)
			}
		})
	}
}
