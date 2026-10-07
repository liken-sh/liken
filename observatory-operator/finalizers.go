package main

// The finalizer observatory.liken.sh/deactivate holds a deleted
// resource while the operator still runs something for it. Without
// it, a delete removes the object at once, and its spec goes with it:
// a dust cap deleted during a session would stop with no deactivation
// and stay open.
//
// A device gets the finalizer just before the operator creates its pod
// for a held server (startDevices). A device with a deletionTimestamp
// is placed on no server (tree.go), so a running server treats it as a
// device that left: it runs the device's deactivation, stops its
// driver, and deletes its pod and its claim (leaves.go, moves.go). The
// supervisor then removes the finalizer. A device whose pod does not
// run carries no finalizer, so the delete of an inventory device is
// instant.
//
// A Telescope gets the finalizer when a reservation takes it (wait),
// and its Observatory with it. The supervisor removes each when no
// reservation holds the telescope, or any telescope of the
// observatory. A deleted Telescope or Observatory ends the reservation
// that holds it, as spec.end does (ending), and the deactivation steps
// still read it from the tree, because the finalizer keeps its object.
//
// While the operator is down, a delete of a running resource waits for
// it. A person can remove the finalizer by hand, and the README says
// what that skips.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// holdsFinalizer reports whether an object's metadata lists the
// operator's finalizer.
func holdsFinalizer(m observatory.ObjectMeta) bool {
	return slices.Contains(m.Finalizers, observatory.Finalizer)
}

// deleting reports whether a person deleted an object that a finalizer
// still holds.
func deleting(m observatory.ObjectMeta) bool { return m.DeletionTimestamp != nil }

// finalizersWith answers an object's finalizers with the operator's
// added or removed. The finalizers of other controllers keep their
// order.
func finalizersWith(m observatory.ObjectMeta, present bool) []string {
	finalizers := slices.DeleteFunc(slices.Clone(m.Finalizers), func(f string) bool {
		return f == observatory.Finalizer
	})
	if present {
		finalizers = append(finalizers, observatory.Finalizer)
	}
	return finalizers
}

// setFinalizerOf adds or removes the finalizer of one resource of any
// kind. It reads the resource from the API server, not from a store,
// and patches its metadata with the resourceVersion of that read, so a
// write of another controller's finalizer in between answers 409 and
// is not lost. The status writer changes the resourceVersion of a
// Telescope or an Observatory often, so a 409 reads the resource and
// patches again at once, up to finalizerTries times, before the caller
// waits out a pause.
func (o *operator) setFinalizerOf(kind observatory.Kind, name string, present bool) error {
	var err error
	for range finalizerTries {
		if err = o.patchFinalizer(kind, name, present); !errors.Is(err, apiclient.ErrConflict) {
			return err
		}
	}
	return err
}

// finalizerTries bounds the reads and patches of one setFinalizerOf.
const finalizerTries = 3

// patchFinalizer reads one resource and patches its finalizers. The
// API server refuses a new finalizer on an object that is being
// deleted, so an add to such an object, or to one that is gone, writes
// nothing.
func (o *operator) patchFinalizer(kind observatory.Kind, name string, present bool) error {
	path := objectPath(kind, o.namespace, name)
	var held struct {
		Metadata observatory.ObjectMeta `json:"metadata"`
	}
	err := o.client.RequestJSON(http.MethodGet, path, nil, &held)
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		return nil
	case err != nil:
		return err
	case holdsFinalizer(held.Metadata) == present:
		return nil
	case present && deleting(held.Metadata):
		return nil
	}
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{
		"resourceVersion": held.Metadata.ResourceVersion,
		"finalizers":      finalizersWith(held.Metadata, present),
	}})
	if err != nil {
		return err
	}
	err = o.client.Request(http.MethodPatch, path, mergePatch, body, nil)
	if !present && errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// holdDevice gives a device the finalizer before the operator creates
// its pod, so a delete from then on waits for the device's
// deactivation. A write that the API server refuses is sent again
// (send), and the step's deadline bounds the tries.
func (o *operator) holdDevice(ctx context.Context, report func(string), d *device) error {
	if holdsFinalizer(d.object.Metadata) {
		return nil
	}
	return o.send(ctx, report, "adding the finalizer of "+d.kind.Name+" "+d.name(), func() error {
		return o.setFinalizerOf(d.kind, d.name(), true)
	})
}

// releaseDevices removes the finalizer of each device whose pod is
// gone and that no held server runs: a deleted device after the
// cleanup of its pod, and every device of a reservation that was
// Released. A device on a held server keeps it while its pod is gone,
// because the runner creates the pod again.
func (o *operator) releaseDevices(t *tree, held map[string]bool) error {
	var problems []error
	for _, d := range t.devices {
		if !holdsFinalizer(d.object.Metadata) {
			continue
		}
		name, err := objectName(d.kind, d.name())
		if err == nil && t.pods[name] != nil {
			continue
		}
		if ref, placed := t.server(d); placed && held[ref.String()] {
			continue
		}
		problems = append(problems, o.setFinalizerOf(d.kind, d.name(), false))
	}
	return joinErrors(problems)
}

// holdParents gives a telescope that a reservation took, and its
// observatory, the finalizer. A write that the API server refuses is
// sent again (send) until ctx ends.
func (o *operator) holdParents(ctx context.Context, report func(string), telescope *observatory.Telescope) error {
	what := "adding the finalizer of Telescope " + telescope.Metadata.Name + " and Observatory " + telescope.Spec.Observatory
	return o.send(ctx, report, what, func() error {
		return errors.Join(
			o.setFinalizerOf(observatory.TelescopeKind, telescope.Metadata.Name, true),
			o.setFinalizerOf(observatory.ObservatoryKind, telescope.Spec.Observatory, true),
		)
	})
}

// keepParents gives each held Telescope and the Observatory of each
// one the finalizer, and removes it from every other. The add covers a
// telescope that a reservation took before the operator added
// finalizers to telescopes. The removal lets a Telescope or an
// Observatory go once the reservation that held it is Released.
func (o *operator) keepParents(t *tree) error {
	held := o.claims.held()
	sites := map[string]bool{}
	for name := range held {
		if scope, ok := t.telescopes[name]; ok {
			sites[scope.Spec.Observatory] = true
		}
	}
	var problems []error
	for name, scope := range t.telescopes {
		problems = append(problems, o.keepFinalizer(observatory.TelescopeKind, scope.Metadata, held[name]))
	}
	for name, site := range t.observatories {
		problems = append(problems, o.keepFinalizer(observatory.ObservatoryKind, site.Metadata, sites[name]))
	}
	return joinErrors(problems)
}

// keepFinalizer adds or removes one resource's finalizer when the
// store's copy differs from want.
func (o *operator) keepFinalizer(kind observatory.Kind, m observatory.ObjectMeta, want bool) error {
	if holdsFinalizer(m) == want || (want && deleting(m)) {
		return nil
	}
	return o.setFinalizerOf(kind, m.Name, want)
}
