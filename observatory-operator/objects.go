package main

// The writes of objects other than a status: the creates and deletes of
// pods, Services, ConfigMaps, and ResourceClaims, the finalizer of a
// Reservation, and the annotation that asks for a retry. finalizers.go
// holds the finalizer of every other resource.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const mergePatch = "application/merge-patch+json"

func podPath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/pods/" + name
}

func servicePath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/services/" + name
}

func configMapPath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/configmaps/" + name
}

func claimPath(namespace, name string) string {
	return "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims/" + name
}

func objectPath(kind observatory.Kind, namespace, name string) string {
	return kind.Path(namespace) + "/" + name
}

// create posts an object to its collection. An object that exists
// already is no error: a pass that read a store before the watch
// delivered the operator's own create sends the create again.
func (o *operator) create(collection string, object any) error {
	_, err := o.post(collection, object)
	return err
}

// post creates an object as create does, and reports whether the API
// server created it, not answered that it exists.
func (o *operator) post(collection string, object any) (bool, error) {
	body, err := json.Marshal(object)
	if err != nil {
		return false, err
	}
	err = o.client.RequestJSON(http.MethodPost, collection, body, nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

// writeJSON sends one object to a path with a method, such as the PUT
// that replaces a ConfigMap.
func (o *operator) writeJSON(method, path string, object any) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return o.client.RequestJSON(method, path, body, nil)
}

// deleteObject deletes one object. An object that is gone already is no
// error.
func (o *operator) deleteObject(path string) error {
	err := o.client.RequestJSON(http.MethodDelete, path, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

func hasFinalizer(r *observatory.Reservation) bool {
	return holdsFinalizer(r.Metadata)
}

// setFinalizer adds or removes the reservation's finalizer with a merge
// patch that states the resourceVersion, so the API server refuses it
// with a 409 when another writer changed the list since the read. The
// caller reads the reservation again and calls once more. A removal
// that finds the reservation gone is done: the finalizer went with it.
func (o *operator) setFinalizer(r *observatory.Reservation, present bool) error {
	err := o.patchMetadata(r, map[string]any{"finalizers": finalizersWith(r.Metadata, present)})
	if !present && errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// annotationRetry asks the operator to run a failed step again. The
// operator removes the annotation when it starts the step.
const annotationRetry = Group + "/retry"

func (o *operator) clearRetry(r *observatory.Reservation) error {
	return o.patchMetadata(r, map[string]any{"annotations": map[string]any{annotationRetry: nil}})
}

// patchMetadata sends a merge patch of the reservation's metadata, and
// notes the version that the API server answers.
func (o *operator) patchMetadata(r *observatory.Reservation, fields map[string]any) error {
	fields["resourceVersion"] = r.Metadata.ResourceVersion
	body, err := json.Marshal(map[string]any{"metadata": fields})
	if err != nil {
		return err
	}
	path := objectPath(observatory.ReservationKind, r.Metadata.Namespace, r.Metadata.Name)
	_, err = memo.Written[observatory.Reservation](o.versions, r.Metadata.Namespace+"/"+r.Metadata.Name, func() (*observatory.Reservation, error) {
		answer := new(observatory.Reservation)
		return answer, o.client.Request(http.MethodPatch, path, mergePatch, body, answer)
	})
	return err
}

// joinErrors joins the errors that are not nil, and answers nil when
// none is.
func joinErrors(problems []error) error {
	return errors.Join(problems...)
}
