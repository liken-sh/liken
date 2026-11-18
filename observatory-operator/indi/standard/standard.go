package standard

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// An Interface is the standard set of one INDI interface: every
// property that a bare driver for it defines in any of its variants,
// plus the properties that the conventions file lists for it. Name is
// the prefix of its capture files, such as ccd for ccd.xml or telescope
// for telescope-ra-dec.xml.
type Interface struct {
	Name       string
	Properties []Property
}

func (i Interface) Property(name string) (Property, bool) {
	for _, p := range i.Properties {
		if p.Name == name {
			return p, true
		}
	}
	return Property{}, false
}

// The files in the captures directory that are not the capture of an
// interface. ConventionsFile lists the properties that libindi does
// not define, and defaultDevice holds the properties that no interface
// owns.
const (
	ConventionsFile = "conventions.yaml"
	defaultDevice   = "device.xml"
)

// Load reads the standard set of each interface from the captures and
// the conventions in dir, and checks the conventions against the
// simulator transcripts in transcripts. It returns the interfaces by
// name and the sorted disagreements between the sources. A
// disagreement does not change the result: a property's structure
// comes from its capture, or from its convention when libindi does not
// define the property.
func Load(dir, transcripts string) (map[string]Interface, []string, error) {
	interfaces, report, err := readCaptures(dir)
	if err != nil {
		return nil, nil, err
	}
	conventions, err := readConventions(filepath.Join(dir, ConventionsFile))
	if err != nil {
		return nil, nil, err
	}
	for _, c := range conventions {
		i, known := interfaces[c.Interface]
		if !known {
			return nil, nil, fmt.Errorf("conventions.yaml puts %s in the interface %q, which has no capture", c.Name, c.Interface)
		}
		if _, defined := i.Property(c.Name); defined {
			return nil, nil, fmt.Errorf("conventions.yaml lists %s, and the capture of %s defines it", c.Name, c.Interface)
		}
		i.Properties = append(i.Properties, c.property())
		interfaces[c.Interface] = i
	}
	simulated, err := checkSimulators(conventions, transcripts)
	if err != nil {
		return nil, nil, err
	}
	report = append(report, simulated...)
	slices.Sort(report)
	return interfaces, slices.Compact(report), nil
}

// readCaptures reads every capture in dir except the one for
// DefaultDevice, and merges the variants of each interface into the
// union of their properties by name and, within a property, the union
// of its members. When two variants define a property with a different
// type, permission, or rule, the report records a disagreement and the
// first variant's structure is kept.
func readCaptures(dir string) (map[string]Interface, []string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		return nil, nil, err
	}
	// Read the captures with fewer hyphens first, such as focuser.xml
	// before focuser-timer.xml, so that an interface's base capture
	// sets the order of the properties.
	slices.SortStableFunc(files, func(a, b string) int {
		return strings.Count(filepath.Base(a), "-") - strings.Count(filepath.Base(b), "-")
	})
	interfaces := map[string]Interface{}
	firstFile := map[string]string{}
	var report []string
	for _, file := range files {
		base := filepath.Base(file)
		if base == defaultDevice {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, err
		}
		props, err := definitionsOf(data)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the definitions in %s: %w", file, err)
		}
		name, _, _ := strings.Cut(strings.TrimSuffix(base, ".xml"), "-")
		i := interfaces[name]
		i.Name = name
		for _, p := range props {
			held, seen := i.Property(p.Name)
			if !seen {
				i.Properties = append(i.Properties, p)
				firstFile[name+"/"+p.Name] = base
				continue
			}
			report = append(report, structure(p.Name, firstFile[name+"/"+p.Name], base, held, p)...)
			for _, m := range p.Members {
				if _, has := held.Member(m.Name); !has {
					held.Members = append(held.Members, m)
				}
			}
			i.Properties[slices.IndexFunc(i.Properties, func(q Property) bool { return q.Name == p.Name })] = held
		}
		interfaces[name] = i
	}
	return interfaces, report, nil
}
