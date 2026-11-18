package main

// One INDI device can back several resources (served.go). For example,
// a camera's driver can also provide a filter wheel. The operator writes
// each property of the device to the status of exactly one of those
// resources, so a property update costs one status write.
//
// A standard property goes to a typed field of a resource whose kind's
// standard set contains it. The camera's FILTER_SLOT goes to the
// FilterWheel that the camera serves, and the camera's status does not
// repeat it. Every other property goes to status.properties of the
// resource that runs the driver. That covers vendor-specific
// properties, the properties of INDI's DefaultDevice such as DEBUG, and
// standard properties of a kind that has no resource on the device,
// such as FILTER_SLOT when the camera serves no FilterWheel. So
// kubectl still shows every property.
//
// When the standard sets of two kinds contain the same name, such as
// GEOGRAPHIC_COORD for Mount and GPS, the property goes to the served
// resource before the resource that runs the driver (resourcesOf). A
// served resource exists to show its interface, so when a mount serves a
// GPS, GEOGRAPHIC_COORD goes to the GPS.

import (
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type standardSet struct {
	names map[string]bool
	// prefixes are numbered families, such as DIGITAL_OUTPUT_, which
	// matches DIGITAL_OUTPUT_1, DIGITAL_OUTPUT_2, and so on.
	prefixes []string
	// namedBy are properties whose member names are themselves standard
	// property names, such as WEATHER_PARAMETERS.
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

// contains reports whether a property is in the set. defined is the
// device's properties by name, which the set reads to match a name that
// is a member of one of its namedBy properties.
func (s standardSet) contains(name string, defined map[string]*indi.Property) bool {
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

// familyNumber returns the number at the end of a numbered family's
// property name, such as 2 for DIGITAL_OUTPUT_2, and false when name does
// not have the prefix followed by one to nine digits.
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

// resourcesOf returns the resources that back owner's INDI device, in
// the order in which they take a property that two standard sets
// contain:
//  1. the served devices that the tree places on the owner, in tree
//     order,
//  2. the served devices that left their server but still hold the
//     finalizer, so they read the owner's INDI device while their
//     deactivation runs,
//  3. the owner.
func (t *tree) resourcesOf(owner *device) []*device {
	var served, leaving []*device
	for _, d := range t.devices {
		if d.runsDriver() || t.ownershipOf(d).refusal != "" || !t.shares(d) {
			continue
		}
		switch named, _ := t.named(d); {
		case t.ownershipOf(d).owner == owner:
			served = append(served, d)
		case named == owner:
			leaving = append(leaving, d)
		}
	}
	return append(append(served, leaving...), owner)
}

// fieldOwners returns, for each property name, the first of resources
// whose standard set contains it. A property that no set contains has
// no entry, and goes to status.properties of the device that runs the
// driver.
func fieldOwners(resources []*device, properties []indi.Property) map[string]*device {
	defined := make(map[string]*indi.Property, len(properties))
	for i := range properties {
		defined[properties[i].Name] = &properties[i]
	}
	out := map[string]*device{}
	for _, p := range properties {
		for _, d := range resources {
			if standardSets[d.kind].contains(p.Name, defined) {
				out[p.Name] = d
				break
			}
		}
	}
	return out
}

// split divides the properties of d's INDI device into the ones that
// belong in d's typed fields and the ones that belong in
// status.properties, each in the driver's order. The caller writes rest
// only when d runs the driver.
func (t *tree) split(d *device, all []indi.Property) (typed, rest []indi.Property) {
	runner, ok := t.runnerOf(d)
	if !ok {
		return nil, nil
	}
	owner := fieldOwners(t.resourcesOf(runner), all)
	for _, p := range all {
		switch owner[p.Name] {
		case d:
			typed = append(typed, p)
		case nil:
			rest = append(rest, p)
		}
	}
	return typed, rest
}
