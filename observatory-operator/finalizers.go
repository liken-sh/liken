package main

// The finalizer observatory.liken.sh/deactivate keeps a deleted
// resource in the API while the operator still runs something for it.
// Without it, a delete removes the object and its spec at once, so a
// dust cap deleted during a session would never get its deactivation
// and would stay open.
//
// The operator adds the finalizer to a device just before it creates
// the device's pod for a server that a reservation holds
// (startDevices), and to a served device together with the device that
// serves it (served.go). tree.go places no device that has a
// deletionTimestamp on a server, so a running server handles a deleted
// device as one that left: it runs the device's deactivation, stops its
// driver, and deletes its pod and claim (leaves.go, deletes.go). Then
// releaseDevices removes the finalizer. A device with no running pod
// has no finalizer, so deleting an inventory device takes effect at
// once.
//
// The operator adds the finalizer to a Telescope and its Observatory
// when a reservation takes the telescope (wait in activation.go).
// syncParentFinalizers removes it when no reservation holds that telescope, or
// any telescope of that observatory. Deleting a held Telescope or
// Observatory ends the reservation as spec.end does (ending), and the
// deactivation steps can still read the deleted object because the
// finalizer keeps it.
//
// While the operator is down, deleting a resource that has the
// finalizer waits for the operator to come back. A person can remove
// the finalizer by hand, and the manual's troubleshooting guide says
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

// hasOperatorFinalizer reports whether the object has the operator's
// finalizer.
func hasOperatorFinalizer(m observatory.ObjectMeta) bool {
	return slices.Contains(m.Finalizers, observatory.Finalizer)
}

func deleting(m observatory.ObjectMeta) bool { return m.DeletionTimestamp != nil }

// finalizersWith returns the object's finalizers with the operator's
// added or removed, and the finalizers of other controllers in their
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

// setFinalizerOf adds or removes the operator's finalizer on one
// resource. It reads the resource from the API server, not from the
// operator's store, and patches with the resourceVersion of that read,
// so a change that another controller made in between fails with a 409
// and is not lost. The status writer updates a Telescope or an
// Observatory often, so on a 409 setFinalizerOf reads and patches again
// at once, up to finalizerTries times, before it returns the error to
// the caller.
func (o *operator) setFinalizerOf(kind observatory.Kind, name string, present bool) error {
	var err error
	for range finalizerTries {
		if err = o.patchFinalizer(kind, name, present); !errors.Is(err, apiclient.ErrConflict) {
			return err
		}
	}
	return err
}

const finalizerTries = 3

// patchFinalizer reads one resource and patches its finalizers. It
// writes nothing to an object that no longer exists, or when it would
// add the finalizer to an object that is being deleted, because the API
// server refuses to add a finalizer to such an object.
func (o *operator) patchFinalizer(kind observatory.Kind, name string, present bool) error {
	path := objectPath(kind, o.namespace, name)
	var current struct {
		Metadata observatory.ObjectMeta `json:"metadata"`
	}
	err := o.client.RequestJSON(http.MethodGet, path, nil, &current)
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		return nil
	case err != nil:
		return err
	case hasOperatorFinalizer(current.Metadata) == present:
		return nil
	case present && deleting(current.Metadata):
		return nil
	}
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{
		"resourceVersion": current.Metadata.ResourceVersion,
		"finalizers":      finalizersWith(current.Metadata, present),
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

// addDeviceFinalizer adds the finalizer to a device before the operator creates
// its pod, so a later delete waits for the device's deactivation. It
// retries a failed write with send until ctx ends.
func (o *operator) addDeviceFinalizer(ctx context.Context, report func(string), d *device) error {
	if hasOperatorFinalizer(d.object.Metadata) {
		return nil
	}
	return o.send(ctx, report, "adding the finalizer of "+d.kind.Name+" "+d.name(), func() error {
		return o.setFinalizerOf(d.kind, d.name(), true)
	})
}

// releaseDevices removes the finalizer from each device that has no
// running pod, such as a deleted device after its pod is deleted, or a
// device of a Released reservation. A device whose pod is gone keeps
// the finalizer while it is placed on a held server, because the
// runner creates the pod again. A device that is not being deleted also
// keeps it while the record of a held server names it, because that
// server's PowerOff switches the output off after StopDevices deleted
// the pod. A served device's pod is the pod of the driver that serves
// it, so it keeps the finalizer while that driver runs, and leaveServed
// removes it after the served device's deactivation.
func (o *operator) releaseDevices(t *tree, held map[string]bool) error {
	var problems []error
	for _, d := range t.devices {
		if !hasOperatorFinalizer(d.object.Metadata) {
			continue
		}
		if _, p, running := t.podOf(d); running && p != nil {
			continue
		}
		ref, placed := t.server(d)
		if !placed && !deleting(d.object.Metadata) {
			ref, placed = d.recordedOn, d.record != nil
		}
		if placed && held[ref.String()] {
			continue
		}
		problems = append(problems, o.setFinalizerOf(d.kind, d.name(), false))
	}
	return joinErrors(problems)
}

// addParentFinalizers adds the finalizer to a telescope that a reservation took
// and to its observatory. It retries a failed write with send until ctx
// ends.
func (o *operator) addParentFinalizers(ctx context.Context, report func(string), telescope *observatory.Telescope) error {
	what := "adding the finalizer of Telescope " + telescope.Metadata.Name + " and Observatory " + telescope.Spec.Observatory
	return o.send(ctx, report, what, func() error {
		return errors.Join(
			o.setFinalizerOf(observatory.TelescopeKind, telescope.Metadata.Name, true),
			o.setFinalizerOf(observatory.ObservatoryKind, telescope.Spec.Observatory, true),
		)
	})
}

// syncParentFinalizers adds the finalizer to each held Telescope and to the
// Observatory of each one, and removes it from every other Telescope
// and Observatory, so a deleted Telescope or Observatory is removed
// once the reservation that held it is Released.
func (o *operator) syncParentFinalizers(t *tree) error {
	held := o.claims.held()
	sites := map[string]bool{}
	for name := range held {
		if scope, ok := t.telescopes[name]; ok {
			sites[scope.Spec.Observatory] = true
		}
	}
	var problems []error
	for name, scope := range t.telescopes {
		problems = append(problems, o.syncFinalizer(observatory.TelescopeKind, scope.Metadata, held[name]))
	}
	for name, site := range t.observatories {
		problems = append(problems, o.syncFinalizer(observatory.ObservatoryKind, site.Metadata, sites[name]))
	}
	return joinErrors(problems)
}

// syncFinalizer adds or removes one resource's finalizer when the
// operator's stored copy does not match want. It does not add the
// finalizer to an object that is being deleted.
func (o *operator) syncFinalizer(kind observatory.Kind, m observatory.ObjectMeta, want bool) error {
	if hasOperatorFinalizer(m) == want || (want && deleting(m)) {
		return nil
	}
	return o.setFinalizerOf(kind, m.Name, want)
}
