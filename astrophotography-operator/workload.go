package main

// A workload is a Deployment of one pod and the Service in front of
// it, which the operator runs for a Solver and for each reservation's
// worker. The operator creates each one that is missing and replaces a
// Deployment whose template it would now write differently, such as
// after an upgrade that changes the image's tag. It compares a hash of
// its own template, stamped on the Deployment, and not the API
// server's copy, which holds every default the API server filled in.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// templateAnnotation holds the hash of the Deployment's spec as the
// operator wrote it.
const templateAnnotation = astrophotography.Group + "/template"

type workload struct {
	deployment *deployment
	service    *service
}

// stamp records the hash of the Deployment's spec on the Deployment.
func (w workload) stamp() string {
	spec, _ := json.Marshal(w.deployment.Spec)
	sum := sha256.Sum256(spec)
	return hex.EncodeToString(sum[:8])
}

// ensure creates the workload's objects that are missing, and replaces
// a Deployment whose stamp differs.
func (o *operator) ensure(w workload) error {
	d := w.deployment
	stamp := w.stamp()
	d.Metadata.Annotations = map[string]string{templateAnnotation: stamp}
	held, found := informer.Cached[deployment](o.stores.collections["Deployment"].View(), d.Metadata.Namespace+"/"+d.Metadata.Name)
	switch {
	case !found:
		if err := o.create("/apis/apps/v1/namespaces/"+d.Metadata.Namespace+"/deployments", d); err != nil {
			return err
		}
	case held.Metadata.Annotations[templateAnnotation] != stamp:
		d.Metadata.ResourceVersion = held.Metadata.ResourceVersion
		body, _ := json.Marshal(d)
		if err := o.client.RequestJSON(http.MethodPut, deploymentPath(d.Metadata.Namespace, d.Metadata.Name), body, nil); err != nil {
			return err
		}
	}
	s := w.service
	if _, found := informer.Cached[service](o.stores.collections["Service"].View(), s.Metadata.Namespace+"/"+s.Metadata.Name); found {
		return nil
	}
	return o.create("/api/v1/namespaces/"+s.Metadata.Namespace+"/services", s)
}

// create posts an object. An object that exists already is no error:
// the store can lag behind the operator's own create by a moment, and
// the next pass reads it.
func (o *operator) create(collection string, object any) error {
	body, _ := json.Marshal(object)
	err := o.client.RequestJSON(http.MethodPost, collection, body, nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// remove deletes a workload's Deployment and Service when the stores
// hold them. An object that is gone already is no error.
func (o *operator) remove(namespace, name string) error {
	key := namespace + "/" + name
	if _, found := informer.Cached[deployment](o.stores.collections["Deployment"].View(), key); found {
		if err := o.client.RequestJSON(http.MethodDelete, deploymentPath(namespace, name), nil, nil); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			return err
		}
	}
	if _, found := informer.Cached[service](o.stores.collections["Service"].View(), key); found {
		if err := o.client.RequestJSON(http.MethodDelete, servicePath(namespace, name), nil, nil); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			return err
		}
	}
	return nil
}

// podsOf answers the pods in the store that carry one label, in one
// namespace.
func (o *operator) podsOf(namespace, key, value string) []podObject {
	var out []podObject
	for _, p := range informer.CachedList[podObject](o.stores.collections["Pod"].View()) {
		if p.Metadata.Namespace == namespace && p.Metadata.Labels[key] == value {
			out = append(out, p)
		}
	}
	return out
}

// reportedPod answers the pod of a workload to report: a ready one when
// one is, or else the first, and nil for none. A Deployment with the
// Recreate strategy runs one pod, and a second only while the first is
// being deleted.
func reportedPod(pods []podObject) *podObject {
	for i := range pods {
		if pods[i].ready() {
			return &pods[i]
		}
	}
	if len(pods) == 0 {
		return nil
	}
	return &pods[0]
}
