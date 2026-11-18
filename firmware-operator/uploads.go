package main

// A create that gets 409 Conflict and a delete that gets 404 Not Found
// count as success, because the object is already in the state that
// the request would produce.

import (
	"errors"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// start creates the pod and then the claim of one upload, and returns
// the pod's name. When the pod exists already, start reads it, because
// the claim's owner reference needs the pod's UID.
func (o *operator) start(f *firmware, d device) (string, error) {
	upload := buildUploadPod(f, d, o.namespace)
	path := podPath(o.namespace, upload.Metadata.Name)
	var created pod
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(upload), &created)
	if errors.Is(err, apiclient.ErrConflict) {
		var existing *pod
		existing, err = apiclient.Get[pod](o.client, path)
		if existing != nil {
			created = *existing
		}
	}
	if err != nil {
		return "", err
	}
	return created.Metadata.Name, o.claim(created)
}

func (o *operator) claim(upload pod) error {
	err := o.client.RequestJSON(http.MethodPost, claimsPathIn(upload.Metadata.Namespace), mustJSON(buildUploadClaim(upload)), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// remove deletes an upload pod. The garbage collector then deletes the
// claim, which the pod owns.
func (o *operator) remove(upload pod) error {
	err := o.client.RequestJSON(http.MethodDelete, podPath(upload.Metadata.Namespace, upload.Metadata.Name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// podGone reads the API server and not the watch's store, because the
// store can lag behind a pod that the operator just created, and a pod
// wrongly reported as gone fails an upload that is running.
func (o *operator) podGone(name string) (bool, error) {
	_, err := apiclient.Get[pod](o.client, podPath(o.namespace, name))
	if errors.Is(err, apiclient.ErrNotFound) {
		return true, nil
	}
	return false, err
}
