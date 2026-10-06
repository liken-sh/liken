package main

// The resources that state procedures: the 14 device kinds, the
// Telescope, and the Observatory. One type reads them all, with the
// place of each in the tree, so a procedure resolves its references
// the same way on every kind.

import (
	"fmt"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// resource is one resource with procedures, as one reading of the tree
// holds it.
type resource struct {
	kind       observatory.Kind
	meta       observatory.ObjectMeta
	procedures observatory.Procedures[action]
	conditions []observatory.Condition
	// device is nil for a Telescope or an Observatory.
	device *device
	// telescope is the resource's own Telescope, or "" for the
	// Observatory and the devices of the observatory's server.
	telescope string
	// observatory is the resource's own Observatory, or "" while a
	// parent is missing.
	observatory string
}

func (r resource) name() string { return r.meta.Name }

// key names the resource across kinds, such as Dome/lab, the same way
// device.key does.
func (r resource) key() string { return r.kind.Name + "/" + r.meta.Name }

func (r resource) String() string { return r.kind.Name + " " + r.meta.Name }

// actionsOf answers the actions of one trigger.
func (r resource) actionsOf(trigger string) []action {
	switch trigger {
	case observatory.TriggerActivation:
		return r.procedures.Activation
	case observatory.TriggerDeactivation:
		return r.procedures.Deactivation
	}
	return nil
}

// ofDevice answers a device as a resource.
func (t *tree) ofDevice(d *device) resource {
	r := resource{kind: d.kind, meta: d.object.Metadata, procedures: d.object.Spec.Procedures,
		conditions: d.object.Status.Conditions, device: d}
	if ref, ok := t.server(d); ok && ref.kind == observatory.TelescopeKind {
		r.telescope = ref.name
		if telescope, ok := t.telescopes[ref.name]; ok {
			r.observatory = telescope.Spec.Observatory
		}
	} else if ok {
		r.observatory = ref.name
	}
	return r
}

func ofTelescope(telescope *observatory.Telescope) resource {
	return resource{kind: observatory.TelescopeKind, meta: telescope.Metadata, conditions: telescope.Status.Conditions,
		procedures: observatory.Procedures[action]{
			Activation:   actions(telescope.Spec.Activation),
			Deactivation: actions(telescope.Spec.Deactivation),
		},
		telescope: telescope.Metadata.Name, observatory: telescope.Spec.Observatory}
}

func ofObservatory(site *observatory.Observatory) resource {
	return resource{kind: observatory.ObservatoryKind, meta: site.Metadata, conditions: site.Status.Conditions,
		procedures: observatory.Procedures[action]{
			Activation:   actions(site.Spec.Activation),
			Deactivation: actions(site.Spec.Deactivation),
		},
		observatory: site.Metadata.Name}
}

// resource answers the resource of one kind and name.
func (t *tree) resource(kind observatory.Kind, name string) (resource, bool) {
	switch kind {
	case observatory.TelescopeKind:
		if telescope, ok := t.telescopes[name]; ok {
			return ofTelescope(telescope), true
		}
	case observatory.ObservatoryKind:
		if site, ok := t.observatories[name]; ok {
			return ofObservatory(site), true
		}
	default:
		if d, ok := t.device(kind, name); ok {
			return t.ofDevice(d), true
		}
	}
	return resource{}, false
}

// conditionsOf answers the stored conditions of any resource that a
// reference can name, and whether it exists.
func (t *tree) conditionsOf(kind observatory.Kind, name string) ([]observatory.Condition, bool) {
	switch kind {
	case observatory.OpticalTubeKind:
		if tube, ok := t.tubes[name]; ok {
			return tube.Status.Conditions, true
		}
		return nil, false
	case observatory.OpticalTrainKind:
		if train, ok := t.trains[name]; ok {
			return train.Status.Conditions, true
		}
		return nil, false
	case observatory.GuiderKind:
		if guider, ok := t.guiders[name]; ok {
			return guider.Status.Conditions, true
		}
		return nil, false
	}
	r, ok := t.resource(kind, name)
	return r.conditions, ok
}

// observatoryOf answers the Observatory of any resource that a
// reference can name.
func (t *tree) observatoryOf(kind observatory.Kind, name string) string {
	telescope := ""
	switch kind {
	case observatory.OpticalTubeKind:
		if tube, ok := t.tubes[name]; ok {
			telescope = tube.Spec.Telescope
		}
	case observatory.OpticalTrainKind:
		if train, ok := t.trains[name]; ok {
			telescope = train.Spec.Telescope
		}
	case observatory.GuiderKind:
		if guider, ok := t.guiders[name]; ok {
			telescope = guider.Spec.Telescope
		}
	default:
		r, _ := t.resource(kind, name)
		return r.observatory
	}
	if scope, ok := t.telescopes[telescope]; ok {
		return scope.Spec.Observatory
	}
	return ""
}

// target is one resource that a reference names.
type target struct {
	kind observatory.Kind
	name string
}

func (g target) String() string { return g.kind.Name + " " + g.name }

func (g target) key() string { return g.kind.Name + "/" + g.name }

// resolve answers the resources that a reference names, from the
// resource that holds the reference. field names the reference in
// the spec, such as after[0], so a reference that names nothing fails
// with a message that a person can find. With no kind, the reference
// names the resource itself. With no name, a kind of Observatory or
// Telescope names the resource's own, and any other kind names every
// resource of the kind in the observatory when many allows it.
func (t *tree) resolve(from resource, field, kindName, name string, many bool) ([]target, error) {
	if kindName == "" {
		return []target{{from.kind, from.name()}}, nil
	}
	kind, ok := observatory.KindNamed(kindName)
	if !ok || kind == observatory.ReservationKind {
		return nil, fmt.Errorf("%s: no kind %s", field, kindName)
	}
	if name != "" {
		if _, exists := t.conditionsOf(kind, name); !exists {
			return nil, fmt.Errorf("%s: no %s %s", field, kind.Name, name)
		}
		return []target{{kind, name}}, nil
	}
	switch {
	case kind == observatory.ObservatoryKind && from.observatory != "":
		return []target{{kind, from.observatory}}, nil
	case kind == observatory.TelescopeKind && from.telescope != "":
		return []target{{kind, from.telescope}}, nil
	case kind == observatory.ObservatoryKind || kind == observatory.TelescopeKind:
		return nil, fmt.Errorf("%s: %s belongs to no %s", field, from, kind.Name)
	case !many:
		return nil, fmt.Errorf("%s: a %s reference needs a name", field, kind.Name)
	}
	var out []target
	for _, name := range t.namesOf(kind) {
		if t.observatoryOf(kind, name) == from.observatory {
			out = append(out, target{kind, name})
		}
	}
	return out, nil
}

// namesOf answers the names of the resources of a kind, in order.
func (t *tree) namesOf(kind observatory.Kind) []string {
	switch kind {
	case observatory.TelescopeKind:
		return sortedNames(t.telescopes)
	case observatory.ObservatoryKind:
		return sortedNames(t.observatories)
	case observatory.OpticalTubeKind:
		return sortedNames(t.tubes)
	case observatory.OpticalTrainKind:
		return sortedNames(t.trains)
	case observatory.GuiderKind:
		return sortedNames(t.guiders)
	}
	var out []string
	for _, d := range t.devices {
		if d.kind == kind {
			out = append(out, d.name())
		}
	}
	return out
}
