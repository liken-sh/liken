// Package schema writes the schema of the observatory.liken.sh
// resources: the CRDs in deploy/ of every kind with procedures, and
// the Go types in observatory/ of the typed fields that come from INDI.
// Its sources are the files in this directory, and the standard
// properties of each INDI interface that the package standard reads
// from indi/standard/.
//
// A kind's fields file, <plural>-fields.yaml, names the kind's INDI
// interfaces, the properties that show that a device serves them, and
// one entry for each standard property of those interfaces. An entry
// names the property's field, gives its role, states the unit of each
// number, and describes the field. The role decides where the field
// goes:
//
//   - setting: a person sets it in spec for a reservation, and the
//     operator writes it once, when the reservation configures the
//     device. Status reports it too.
//   - reading: the driver reports it. Status only.
//   - command: clients such as KStars, Ekos, PHD2, and the operator's
//     own actions drive it, such as an exposure, a slew, or a park.
//     Status only.
//   - tree: the operator writes it from the resources around the
//     device, such as ACTIVE_DEVICES or GEOGRAPHIC_COORD. Status only,
//     so a person and the operator never write the same property.
//
// A number with one member states its unit in `unit`, and a property
// with several members names each member's field and unit in
// `members`. A switch names no members: a field of a switch holds the
// names of the members that are on, as INDI names them. `elements:
// driver` marks a property whose members each driver chooses, such as
// slew rates, track modes, capture formats, park positions, and filter
// names, and its field is a list of what the driver defines. `family`
// marks a property that a driver defines once for each output or each
// parameter, such as DIGITAL_OUTPUT_1, and its field is a list with
// one item for each.
//
// Every entry, and every member it names, has a `description`: what
// the field is for, and its unit in the unit's one word, such as "in
// degrees Celsius". `kubectl explain` prints it, so it follows
// brand/voice.md. The generator adds the property's name and who
// changes it.
//
// The generator fails on a standard property with no entry, and on an
// entry that names no standard property, so a property is never left
// untyped by accident.
package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/indi/standard"
	"sigs.k8s.io/yaml"
)

const fieldsSuffix = "-fields.yaml"

// The roles of a typed field.
const (
	roleSetting = "setting"
	roleReading = "reading"
	roleCommand = "command"
	roleTree    = "tree"
)

// The families of a property.
const (
	familyNumbered = "numbered"
	familyNamed    = "named"
)

// curated is one fields file.
type curated struct {
	Kind       string           `json:"kind"`
	Interfaces []string         `json:"interfaces"`
	Detect     []string         `json:"detect"`
	Properties map[string]entry `json:"properties"`
}

// entry is the field of one standard property.
type entry struct {
	Field       string                 `json:"field"`
	Role        string                 `json:"role"`
	Unit        string                 `json:"unit,omitempty"`
	Elements    string                 `json:"elements,omitempty"`
	Family      string                 `json:"family,omitempty"`
	NamedBy     string                 `json:"namedBy,omitempty"`
	Text        string                 `json:"description,omitempty"`
	MemberUnits map[string]memberEntry `json:"members,omitempty"`
}

// memberEntry is the field of one member of a property.
type memberEntry struct {
	Field       string `json:"field"`
	Unit        string `json:"unit,omitempty"`
	Description string `json:"description,omitempty"`
}

// A kind is one fields file joined with the standard set of its
// interfaces: each standard property beside its entry, in the order of
// the captures.
type kind struct {
	curated
	Plural string
	Fields []property
}

// property is one standard property and its entry.
type property struct {
	standard.Property
	entry
}

func (p property) driverElements() bool { return p.Elements == "driver" }

// readKind reads one fields file and joins it with the standard sets.
func readKind(path string, sets map[string]standard.Interface) (kind, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return kind{}, err
	}
	var c curated
	if err := yaml.UnmarshalStrict(raw, &c); err != nil {
		return kind{}, fmt.Errorf("%s: %w", path, err)
	}
	k := kind{curated: c, Plural: strings.TrimSuffix(filepath.Base(path), fieldsSuffix)}
	seen := map[string]bool{}
	for _, name := range c.Interfaces {
		set, known := sets[name]
		if !known {
			return kind{}, fmt.Errorf("%s names the interface %q, which has no capture", path, name)
		}
		for _, p := range set.Properties {
			e, held := c.Properties[p.Name]
			if !held {
				return kind{}, fmt.Errorf("%s has no entry for the standard property %s of %s", path, p.Name, name)
			}
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			field := property{Property: p, entry: e}
			if err := field.check(); err != nil {
				return kind{}, fmt.Errorf("%s: %s: %w", path, p.Name, err)
			}
			k.Fields = append(k.Fields, field)
		}
	}
	for _, name := range sortedKeys(c.Properties) {
		if !seen[name] {
			return kind{}, fmt.Errorf("%s has an entry for %s, which is no standard property of %v", path, name, c.Interfaces)
		}
	}
	for _, name := range c.Detect {
		if !seen[name] {
			return kind{}, fmt.Errorf("%s detects the kind by %s, which is no standard property of %v", path, name, c.Interfaces)
		}
	}
	return k, nil
}

// check refuses an entry that does not fit its property.
func (p property) check() error {
	switch {
	case !validName(p.Field):
		return fmt.Errorf("the field %q is no JSON name in lower camel case", p.Field)
	case !slices.Contains([]string{roleSetting, roleReading, roleCommand, roleTree}, p.Role):
		return fmt.Errorf("the role %q is none of setting, reading, command, and tree", p.Role)
	case p.Role == roleSetting && (p.Perm == indi.ReadOnly || p.Type == indi.LightType || p.Type == indi.BLOBType):
		return fmt.Errorf("a setting must be a property that a client can write, and this is a %s that is %q", p.Type, p.Perm)
	case !slices.Contains([]string{"", "driver"}, p.Elements):
		return fmt.Errorf("elements is %q, and only driver is known", p.Elements)
	case !slices.Contains([]string{"", familyNumbered, familyNamed}, p.Family):
		return fmt.Errorf("family is %q, and only numbered and named are known", p.Family)
	case p.Family != "" && p.driverElements():
		return fmt.Errorf("a family's members are fixed, so it cannot take elements: driver")
	case (p.Family == familyNamed) != (p.NamedBy != ""):
		return fmt.Errorf("a named family, and only a named family, names the property whose members name it in namedBy")
	case strings.TrimSpace(p.Text) == "":
		return fmt.Errorf("the entry has no description")
	case p.Unit != "" && units[p.Unit].sentence == "":
		return fmt.Errorf("the unit %q is not one of %v", p.Unit, sortedKeys(units))
	case !namesUnit(p.Text, p.Unit):
		return fmt.Errorf("the description does not name the unit: %q", units[p.Unit].word)
	}
	return p.checkMembers()
}

// checkMembers refuses members and units that do not fit the
// property's type. A number states a unit for each member, a property
// with several fixed members names a field for each, and a switch, a
// BLOB, or a property whose elements the driver chooses names none.
func (p property) checkMembers() error {
	listed := p.Type == indi.SwitchType || p.Type == indi.BLOBType || p.driverElements()
	single := len(p.Property.Members) == 1
	switch {
	case listed && len(p.MemberUnits) > 0:
		return fmt.Errorf("a %s whose members %s names no members", p.Type, map[bool]string{true: "the driver chooses", false: "are fixed"}[p.driverElements()])
	case p.Type == indi.NumberType && (single || p.driverElements()) && len(p.MemberUnits) == 0 && p.Unit == "":
		return fmt.Errorf("a number needs a unit")
	case p.Unit != "" && (p.Type != indi.NumberType || (!single && !p.driverElements())):
		return fmt.Errorf("unit is only for a number with one member or with the driver's elements; give each member its unit")
	case listed || (single && len(p.MemberUnits) == 0):
		return nil
	}
	for _, m := range p.Property.Members {
		me, held := p.MemberUnits[m.Name]
		switch {
		case !held:
			return fmt.Errorf("the member %s has no entry", m.Name)
		case !validName(me.Field):
			return fmt.Errorf("the member %s names the field %q, which is no JSON name in lower camel case", m.Name, me.Field)
		case p.Type == indi.NumberType && units[me.Unit].sentence == "":
			return fmt.Errorf("the member %s needs one of the units %v", m.Name, sortedKeys(units))
		case p.Type != indi.NumberType && me.Unit != "":
			return fmt.Errorf("the member %s is no number, so it has no unit", m.Name)
		case strings.TrimSpace(me.Description) == "":
			return fmt.Errorf("the member %s has no description", m.Name)
		case !namesUnit(me.Description, me.Unit):
			return fmt.Errorf("the description of the member %s does not name the unit: %q", m.Name, units[me.Unit].word)
		}
	}
	for name := range p.MemberUnits {
		if _, defined := p.Property.Member(name); !defined {
			return fmt.Errorf("the entry names the member %s, which the property does not define", name)
		}
	}
	return nil
}

// jsonName is a field's name: lower camel case. YAML 1.1, which
// sigs.k8s.io/yaml reads, takes a bare y, n, yes, no, on, or off as a
// boolean, and the name true or false shows that a fields file left
// one of them unquoted.
var jsonName = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

// namesUnit reports whether a description names its unit, in the one
// word that the unit has.
func namesUnit(description, unit string) bool {
	return strings.Contains(description, units[unit].word)
}

func validName(name string) bool {
	return jsonName.MatchString(name) && name != "true" && name != "false"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
