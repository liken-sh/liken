package main

// The requests that create, check, and delete an upload's pod and
// claim. Each one takes the answer that means the work is already done
// as success: a 409 on a create, and a 404 on a delete.

import (
	"errors"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// start creates the pod and then the claim of one upload. A pod that
// already exists under the name answers 409, and the operator reads it
// for the UID that the claim's owner reference names.
func (o *operator) start(f *firmware, d device) (string, error) {
	upload := buildUploadPod(f, d, o.namespace)
	path := podPath(o.namespace, upload.Metadata.Name)
	var created pod
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(upload), &created)
	if errors.Is(err, apiclient.ErrConflict) {
		var held *pod
		held, err = apiclient.Get[pod](o.client, path)
		if held != nil {
			created = *held
		}
	}
	if err != nil {
		return "", err
	}
	return created.Metadata.Name, o.claim(created)
}

// claim creates an upload pod's claim. A claim that already exists
// answers 409.
func (o *operator) claim(upload pod) error {
	err := o.client.RequestJSON(http.MethodPost, claimsPathIn(upload.Metadata.Namespace), mustJSON(buildUploadClaim(upload)), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// remove deletes an upload pod, and the garbage collector deletes its
// claim. A 404 means the pod is already gone, which is the same
// result.
func (o *operator) remove(upload pod) error {
	err := o.client.RequestJSON(http.MethodDelete, podPath(upload.Metadata.Namespace, upload.Metadata.Name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// podGone reports whether an upload pod no longer exists. The pass
// asks the API server and not the watch's store, because the store can
// lag a pod that the operator created a moment ago, and a pod reported
// gone in error would fail an upload that runs.
func (o *operator) podGone(name string) (bool, error) {
	_, err := apiclient.Get[pod](o.client, podPath(o.namespace, name))
	if errors.Is(err, apiclient.ErrNotFound) {
		return true, nil
	}
	return false, err
}
