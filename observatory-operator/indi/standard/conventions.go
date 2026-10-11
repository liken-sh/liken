package standard

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"sigs.k8s.io/yaml"
)

// A convention is one entry of conventions.yaml: a property that
// drivers share and libindi does not define.
type convention struct {
	Name       string          `json:"name"`
	Interface  string          `json:"interface"`
	Type       indi.Type       `json:"type"`
	Perm       indi.Permission `json:"perm"`
	Rule       indi.Rule       `json:"rule,omitempty"`
	Label      string          `json:"label"`
	Members    []Member        `json:"members"`
	Simulators []string        `json:"simulators"`
}

func (c convention) property() Property {
	return Property{Name: c.Name, Type: c.Type, Perm: c.Perm, Rule: c.Rule, Label: c.Label,
		Members: slices.Clone(c.Members), Convention: true}
}

func readConventions(path string) ([]convention, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []convention
	if err := yaml.UnmarshalStrict(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// checkSimulators compares each convention with the transcripts in
// dir, one directory for each simulator. A convention must name
// exactly the simulators that define it, or the read fails. Each
// difference between a simulator's definition and the convention is
// a disagreement.
func checkSimulators(conventions []convention, dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*", "*.xml"))
	if err != nil {
		return nil, err
	}
	defined := map[string]map[string]Property{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		props, err := definitionsOf(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		simulator := filepath.Base(filepath.Dir(file))
		for _, p := range props {
			if defined[p.Name] == nil {
				defined[p.Name] = map[string]Property{}
			}
			if _, seen := defined[p.Name][simulator]; !seen {
				defined[p.Name][simulator] = p
			}
		}
	}
	var report []string
	for _, c := range conventions {
		simulators := sortedKeys(defined[c.Name])
		want := slices.Sorted(slices.Values(c.Simulators))
		if !slices.Equal(simulators, want) {
			return nil, fmt.Errorf("conventions.yaml names the simulators %v for %s, and the transcripts that define it are %v", want, c.Name, simulators)
		}
		for _, simulator := range simulators {
			where := "the " + simulator + " simulator"
			report = append(report, compare(c.Name, "conventions.yaml", where, c.property(), defined[c.Name][simulator])...)
		}
	}
	return report, nil
}

// compare answers each difference between two definitions of one
// property: in structure, and each member that one has and the other
// lacks.
func compare(name, first, second string, a, b Property) []string {
	out := structure(name, first, second, a, b)
	out = append(out, missingMembers(name, first, second, a, b)...)
	return append(out, missingMembers(name, second, first, b, a)...)
}

// structure answers each difference in the type, the permission, and
// the rule of two definitions of one property.
func structure(name, first, second string, a, b Property) []string {
	var out []string
	differ := func(what, x, y string) {
		if x != y {
			out = append(out, fmt.Sprintf("%s: %s gives the %s %q, and %s gives %q", name, first, what, x, second, y))
		}
	}
	differ("type", string(a.Type), string(b.Type))
	differ("permission", string(a.Perm), string(b.Perm))
	differ("rule", string(a.Rule), string(b.Rule))
	return out
}

// missingMembers answers each member that a defines and b lacks.
func missingMembers(name, first, second string, a, b Property) []string {
	var out []string
	for _, m := range a.Members {
		if _, held := b.Member(m.Name); !held {
			out = append(out, fmt.Sprintf("%s: %s defines the member %s, and %s does not", name, first, m.Name, second))
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
