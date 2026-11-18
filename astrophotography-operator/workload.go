package main

// A workload is a Deployment with one pod and the Service in front of
// it. The operator runs one for each Solver and one for each
// Reservation's worker. It creates the objects that are missing, and
// replaces a Deployment when the spec it would write differs from the
// one it wrote before, for example after an upgrade changes the image
// tag. It compares a hash of its own spec, stored in an annotation,
// because the API server's copy of the spec also has every default
// that the API server filled in.
//
// The watch delivers the operator's own writes after a delay. A pass
// that read the store during that delay would create the Deployment a
// second time, or replace it with an old resourceVersion and get a 409,
// and the failed pass would set the Solver's Ready condition to False.
// So every read goes through informer.ReadSelected with the memo of the
// resourceVersions the operator wrote. When the store's copy is older
// than the memo's version, ReadSelected reads the object from the API
// server. The watch selects every object with the
// operator's label, and the operator labels every object it creates, so
// an object that is missing from a synced store and from the memo does
// not exist.

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

// stamp returns the hash of the Deployment's spec, which the operator
// stores in templateAnnotation.
func (w workload) stamp() string {
	spec, _ := json.Marshal(w.deployment.Spec)
	sum := sha256.Sum256(spec)
	return hex.EncodeToString(sum[:8])
}

// ensure creates the workload's objects that are missing, and replaces
// the Deployment when its stored hash differs from the current one.
func (o *operator) ensure(w workload) error {
	d := w.deployment
	stamp := w.stamp()
	d.Metadata.Annotations = map[string]string{templateAnnotation: stamp}
	key := memo.Key(&d.Metadata.ObjectMeta)
	deployments := o.storeOf("Deployment", o.deploymentVersions)
	existing, err := informer.ReadSelected[deployment](o.client, deployments, key, deploymentPath(d.Metadata.Namespace, d.Metadata.Name))
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		if err := create(o.client, deployments.Versions, "/apis/apps/v1/namespaces/"+d.Metadata.Namespace+"/deployments", d); err != nil {
			return err
		}
	case err != nil:
		return err
	case existing.Metadata.Annotations[templateAnnotation] != stamp:
		d.Metadata.ResourceVersion = existing.Metadata.ResourceVersion
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
	services := o.storeOf("Service", o.serviceVersions)
	_, err = informer.ReadSelected[service](o.client, services, memo.Key(&s.Metadata.ObjectMeta), servicePath(s.Metadata.Namespace, s.Metadata.Name))
	if !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}
	return create(o.client, services.Versions, "/api/v1/namespaces/"+s.Metadata.Namespace+"/services", s)
}

// storeOf returns the store of one kind of object that the operator
// creates, with the memo of the resourceVersions it wrote for that kind.
func (o *operator) storeOf(kind string, versions *memo.Versions) informer.Held {
	return informer.Held{View: o.stores.collections[kind].View(), Versions: versions}
}

// create sends a POST for an object and records the version that the
// API server returns. An object that already exists is not an error,
// because an earlier request whose response was lost, or the previous
// leader, may have created it. The memo then has no current copy, so
// the next read goes to the API server.
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
// contain them, and drops the memo's record of each. An object that is
// already gone is not an error. The store keeps the deleted copy until
// the watch delivers the delete event, and that event triggers the next
// pass.
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

// podsOf returns the pods in the store that have the given label value
// in the given namespace.
func (o *operator) podsOf(namespace, key, value string) []podObject {
	var out []podObject
	for _, p := range informer.CachedList[podObject](o.stores.collections["Pod"].View()) {
		if p.Metadata.Namespace == namespace && p.Metadata.Labels[key] == value {
			out = append(out, p)
		}
	}
	return out
}

// reportedPod picks the pod to report on for a workload. It returns the
// first ready pod, or the first pod when none is ready, or nil when
// there are no pods. A Deployment with the Recreate strategy runs one
// pod, and a second only while the first is being deleted.
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
