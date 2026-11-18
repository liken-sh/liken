package main

// The writes of objects other than a status: pods, Services,
// ConfigMaps, and ResourceClaimTemplates, and a Reservation's finalizer
// and retry annotation. finalizers.go writes the finalizers of every
// other resource.

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

func templatePath(namespace, name string) string {
	return "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaimtemplates/" + name
}

func objectPath(kind observatory.Kind, namespace, name string) string {
	return kind.Path(namespace) + "/" + name
}

// create posts an object to its collection. An object that already
// exists is not an error, because a pass can read the store before the
// watch delivers the operator's own earlier create, and then create the
// object again.
func (o *operator) create(collection string, object any) error {
	_, err := o.post(collection, object)
	return err
}

// post is create, and also returns false when the object already
// existed.
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

func (o *operator) writeJSON(method, path string, object any) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return o.client.RequestJSON(method, path, body, nil)
}

// deleteObject treats an object that does not exist as deleted.
func (o *operator) deleteObject(path string) error {
	err := o.client.RequestJSON(http.MethodDelete, path, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

func hasFinalizer(r *observatory.Reservation) bool {
	return hasOperatorFinalizer(r.Metadata)
}

// setFinalizer adds or removes the reservation's finalizer. The patch
// includes the resourceVersion, so the API server refuses it with 409
// when another writer changed the reservation after the caller read
// it. The callers then read the reservation again and retry. Removing
// the finalizer from a reservation that is already gone succeeds.
func (o *operator) setFinalizer(r *observatory.Reservation, present bool) error {
	err := o.patchMetadata(r, map[string]any{"finalizers": finalizersWith(r.Metadata, present)})
	if !present && errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// A person adds annotationRetry to a Reservation to run its failed
// step again, or to another resource to run its failed triggers again
// (retry.go). The operator removes the annotation when it acts on it.
const annotationRetry = Group + "/retry"

func (o *operator) clearRetry(r *observatory.Reservation) error {
	return o.patchMetadata(r, map[string]any{"annotations": map[string]any{annotationRetry: nil}})
}

// patchMetadata records the resourceVersion that the API server
// returns, so that a read of the reservation does not use the watch's
// copy from before this patch.
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

func joinErrors(problems []error) error {
	return errors.Join(problems...)
}
