package main

// The device kinds, Telescope, and Observatory have procedures in their
// spec. The resource type wraps any of them with the names of its
// Telescope and Observatory, so a reference in a procedure resolves the
// same way for every kind.

import (
	"fmt"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// resource is a device, a Telescope, or an Observatory, read from one
// snapshot of the tree.
type resource struct {
	kind       observatory.Kind
	meta       observatory.ObjectMeta
	procedures observatory.Procedures[action]
	conditions []observatory.Condition
	// device is nil for a Telescope or an Observatory.
	device *device
	// telescope is the Telescope that the resource is, or whose INDI
	// server the device runs on. Otherwise it is "".
	telescope string
	// observatory is the Observatory that the resource is or belongs
	// to. It is "" for a device on no INDI server, and for a Telescope
	// with no spec.observatory.
	observatory string
}

func (r resource) name() string { return r.meta.Name }

// key identifies the resource across kinds, such as Dome/lab, in the
// same format as device.key.
func (r resource) key() string { return r.kind.Name + "/" + r.meta.Name }

func (r resource) String() string { return r.kind.Name + " " + r.meta.Name }

// actionsOf returns the actions of the named lifecycle trigger.
func (r resource) actionsOf(name string) []action {
	switch name {
	case observatory.TriggerActivation:
		return r.procedures.Activation
	case observatory.TriggerDeactivation:
		return r.procedures.Deactivation
	}
	return nil
}

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
		procedures: procedures(telescope.Spec.Procedures),
		telescope:  telescope.Metadata.Name, observatory: telescope.Spec.Observatory}
}

func ofObservatory(site *observatory.Observatory) resource {
	return resource{kind: observatory.ObservatoryKind, meta: site.Metadata, conditions: site.Status.Conditions,
		procedures:  procedures(site.Spec.Procedures),
		observatory: site.Metadata.Name}
}

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

// conditionsOf returns the status conditions of a resource of any kind
// that a reference can point to, and whether the resource exists.
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

// observatoryOf returns the Observatory that a resource of any kind
// belongs to, or "" when it belongs to none.
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

// target is one resource that a reference points to.
type target struct {
	kind observatory.Kind
	name string
}

func (g target) String() string { return g.kind.Name + " " + g.name }

func (g target) key() string { return g.kind.Name + "/" + g.name }

// resolve returns the resources that a reference in a procedure points
// to. from is the resource whose procedure holds the reference, and
// field is the reference's path in the spec, such as after[0], which
// the error message quotes. The reference is resolved by its parts:
//
//   - With no kind, it points to from itself.
//   - With a kind and a name, it points to that resource, which must
//     exist.
//   - With an Observatory or a Telescope kind and no name, it points to
//     the one that from belongs to.
//   - With any other kind and no name, it points to every resource of
//     that kind in from's Observatory, but only when many is true.
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

// namesOf returns the names of the resources of a kind, sorted by name.
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
