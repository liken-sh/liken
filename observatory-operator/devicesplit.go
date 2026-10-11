package main

// One INDI device can serve several resources (served.go), and each of
// its properties is in the status of one resource, so an update to a
// property costs one status write. A standard property is a typed field
// of the resource whose interface defines it: a camera's FILTER_SLOT is
// a field of the FilterWheel that the camera serves, and the camera's
// status does not hold it. Every other property is in status.properties
// of the resource that runs the driver: a vendor's own properties, the
// properties of INDI's DefaultDevice, such as DEBUG, and the standard
// properties of an interface that no resource on the device is, such as
// the FILTER_SLOT of a camera that serves no FilterWheel. So a person
// still reads every property with kubectl.
//
// Some names are in the standard sets of two interfaces, such as
// GEOGRAPHIC_COORD, which a mount and a GPS both define. Such a property
// goes to the first resource whose set holds it: the served resources
// in the order of the tree, then the one that runs the driver. A served
// resource exists only to show its interface, and the resource that runs
// the driver keeps everything else, so a mount that serves a GPS gives
// GEOGRAPHIC_COORD to the GPS.

import (
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// standardSet is the standard set of one kind, as the names of its
// properties.
type standardSet struct {
	names map[string]bool
	// prefixes are the numbered families, such as DIGITAL_OUTPUT_ for
	// DIGITAL_OUTPUT_1 and the rest.
	prefixes []string
	// namedBy are the properties whose members name a property of the
	// set, such as WEATHER_PARAMETERS.
	namedBy []string
}

var standardSets = func() map[observatory.Kind]standardSet {
	out := map[observatory.Kind]standardSet{}
	for kind, s := range observatory.Standards {
		set := standardSet{names: map[string]bool{}, namedBy: s.NamedBy}
		for _, name := range s.Properties {
			if prefix, numbered := strings.CutSuffix(name, "*"); numbered {
				set.prefixes = append(set.prefixes, prefix)
			} else {
				set.names[name] = true
			}
		}
		out[kind] = set
	}
	return out
}()

// holds reports whether a property of a device is in the set. defined
// answers the device's other properties, for a name that a member of
// one of them gives.
func (s standardSet) holds(name string, defined map[string]*indi.Property) bool {
	if s.names[name] {
		return true
	}
	for _, prefix := range s.prefixes {
		if _, numbered := familyNumber(name, prefix); numbered {
			return true
		}
	}
	for _, by := range s.namedBy {
		if p, ok := defined[by]; ok {
			if _, named := p.Member(name); named {
				return true
			}
		}
	}
	return false
}

// familyNumber answers the number at the end of a property of a
// numbered family, such as 2 for DIGITAL_OUTPUT_2.
func familyNumber(name, prefix string) (int32, bool) {
	digits, ok := strings.CutPrefix(name, prefix)
	if !ok || digits == "" || len(digits) > 9 {
		return 0, false
	}
	n := int32(0)
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + c - '0'
	}
	return n, true
}

// sharers answers the resources that one INDI device serves, in the
// order that they take a property that two standard sets hold: each
// device that names it in spec.device and that the tree does not refuse,
// in the order of the tree, then the device that runs its driver. A
// served device that left its server still reads the device until the
// driver stops (status.go), so it shares the device too.
func (t *tree) sharers(owner *device) []*device {
	var out []*device
	for _, d := range t.devices {
		if d.runsDriver() || t.verdict(d).refusal != "" {
			continue
		}
		if named, ok := t.named(d); ok && named == owner {
			out = append(out, d)
		}
	}
	return append(out, owner)
}

// holders answers the resource whose typed field holds each property of
// an INDI device, by name. A property that no typed field holds has no
// entry, and is in status.properties of the device that runs the driver.
func holders(sharers []*device, properties []indi.Property) map[string]*device {
	defined := make(map[string]*indi.Property, len(properties))
	for i := range properties {
		defined[properties[i].Name] = &properties[i]
	}
	out := map[string]*device{}
	for _, p := range properties {
		for _, d := range sharers {
			if standardSets[d.kind].holds(p.Name, defined) {
				out[p.Name] = d
				break
			}
		}
	}
	return out
}

// split answers the properties of an INDI device that a resource holds
// in its typed fields, and the ones that no typed field holds, each in
// the driver's order.
func (t *tree) split(d *device, all []indi.Property) (held, rest []indi.Property) {
	runner, ok := t.runnerOf(d)
	if !ok {
		return nil, nil
	}
	holder := holders(t.sharers(runner), all)
	for _, p := range all {
		switch holder[p.Name] {
		case d:
			held = append(held, p)
		case nil:
			rest = append(rest, p)
		}
	}
	return held, rest
}
