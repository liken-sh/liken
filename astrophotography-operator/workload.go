package main

// A workload is a Deployment of one pod and the Service in front of
// it, which the operator runs for a Solver and for each reservation's
// worker. The operator creates each one that is missing and replaces a
// Deployment whose template it would now write differently, such as
// after an upgrade that changes the image's tag. It compares a hash of
// its own template, stamped on the Deployment, and not the API
// server's copy, which holds every default the API server filled in.
//
// A watch delivers each of the operator's writes some time after the
// API server answers it, and a pass that runs in that time reads a
// store without the write. A pass that acted on that copy would create
// the Deployment again, or replace it again at the older copy's
// resourceVersion, which the API server refuses with 409, and the
// failed pass would turn the Solver's Ready False. So every write and
// read of a workload goes through the memo of the operator's versions
// (informer.ReadSelected), and a store's copy that is older than the
// operator's last write is read from the API server. The watch selects
// every object with the operator's label, and each object the operator
// creates has it, so a ready store's absence of an object the memo has
// not noted answers that it does not exist.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
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
	key := memo.Key(&d.Metadata.ObjectMeta)
	deployments := o.held("Deployment", o.deploymentVersions)
	held, err := informer.ReadSelected[deployment](o.client, deployments, key, deploymentPath(d.Metadata.Namespace, d.Metadata.Name))
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		if err := create(o.client, deployments.Versions, "/apis/apps/v1/namespaces/"+d.Metadata.Namespace+"/deployments", d); err != nil {
			return err
		}
	case err != nil:
		return err
	case held.Metadata.Annotations[templateAnnotation] != stamp:
		d.Metadata.ResourceVersion = held.Metadata.ResourceVersion
		body, _ := json.Marshal(d)
		if _, err := memo.Written(deployments.Versions, key, func() (*deployment, error) {
			var replaced deployment
			err := o.client.RequestJSON(http.MethodPut, deploymentPath(d.Metadata.Namespace, d.Metadata.Name), body, &replaced)
			return &replaced, err
		}); err != nil {
			return err
		}
	}
	s := w.service
	services := o.held("Service", o.serviceVersions)
	_, err = informer.ReadSelected[service](o.client, services, memo.Key(&s.Metadata.ObjectMeta), servicePath(s.Metadata.Namespace, s.Metadata.Name))
	if !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}
	return create(o.client, services.Versions, "/api/v1/namespaces/"+s.Metadata.Namespace+"/services", s)
}

// held answers the store of one kind the operator creates, and the
// memo of its writes.
func (o *operator) held(kind string, versions *memo.Versions) informer.Held {
	return informer.Held{View: o.stores.collections[kind].View(), Versions: versions}
}

// create posts an object and notes the version the API server answered.
// An object that exists already is no error: a write whose answer was
// lost, or the lead before this one, created it. The memo then holds no
// current copy, so the next read asks the API server.
func create[T any, P informer.Object[T]](c *apiclient.Client, versions *memo.Versions, collection string, object P) error {
	body, _ := json.Marshal(object)
	_, err := memo.Written[T, P](versions, memo.Key(object.GetObjectMeta()), func() (*T, error) {
		var created T
		err := c.RequestJSON(http.MethodPost, collection, body, &created)
		return &created, err
	})
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// remove deletes a workload's Deployment and Service when the stores
// hold them. An object that is gone already is no error. The memo then
// forgets each one, so it keeps no record of a removed worker. The
// store holds the deleted copy until the watch delivers the delete, and
// that delete wakes the next pass.
func (o *operator) remove(namespace, name string) error {
	key := namespace + "/" + name
	if _, found := informer.Cached[deployment](o.stores.collections["Deployment"].View(), key); found {
		if err := o.client.RequestJSON(http.MethodDelete, deploymentPath(namespace, name), nil, nil); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			return err
		}
		o.deploymentVersions.Forget(key)
	}
	if _, found := informer.Cached[service](o.stores.collections["Service"].View(), key); found {
		if err := o.client.RequestJSON(http.MethodDelete, servicePath(namespace, name), nil, nil); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			return err
		}
		o.serviceVersions.Forget(key)
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
