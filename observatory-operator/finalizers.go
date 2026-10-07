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
// While the operator is down, a delete of a running device waits for
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
// is not lost. The API server refuses a new finalizer on an object
// that is being deleted, so an add to such an object, or to one that
// is gone, writes nothing.
func (o *operator) setFinalizerOf(kind observatory.Kind, name string, present bool) error {
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
