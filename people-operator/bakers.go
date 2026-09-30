package main

// The operator keeps no record of a bake in memory. The baker pod is
// the record: its annotations name the Person, the source, and the
// check request, and its phase says whether the bake is done. So an
// operator that restarts during a bake finds the pod and reads its
// result, and a pod whose Person is gone, or whose source changed, is
// deleted.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// bakeDeadline bounds one baker pod, from its creation. An NFS server
// that does not answer holds the mount, and so does a claim bound to a
// node the pod cannot reach, and the pod never runs. The pod's own
// activeDeadlineSeconds counts only from the start of a pod that
// runs, so the operator measures the deadline itself.
const bakeDeadline = 2 * time.Minute

// bakers starts and follows baker pods.
type bakers struct {
	client *apiclient.Client

	// pods is the watch of every baker pod, in every namespace.
	pods informer.View

	// image is the operator's own image, which a baker pod runs.
	image string

	// namespace is the operator's own, where an nfs:// pod runs.
	namespace string
}

// byPerson answers each baker pod by the Person it bakes for. It
// answers false while the watch does not hold every pod, because a
// pass that sees no pod for a Person starts one.
func (b *bakers) byPerson() (map[string]pod, bool) {
	if !b.pods.Ready() {
		return nil, false
	}
	held := map[string]pod{}
	for _, item := range informer.CachedList[pod](b.pods) {
		held[item.Metadata.Annotations[bakerPersonKey]] = item
	}
	return held, true
}

// start creates the Person's baker pod. A pod that already exists
// under the name answers 409, and the watch brings it to the next pass.
func (b *bakers) start(p *person, mount bakerMount) error {
	baker := buildBakerPod(p, mount, b.image)
	err := b.client.RequestJSON(http.MethodPost, "/api/v1/namespaces/"+mount.namespace+"/pods", mustJSON(baker), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// remove deletes a baker pod. A pod that is gone already is the
// answer the delete wants.
func (b *bakers) remove(baker pod) error {
	err := b.client.RequestJSON(http.MethodDelete, podPath(baker.Metadata.Namespace, baker.Metadata.Name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// follow reads a Person's baker pod. It answers the outcome of a pod
// that finished or ran out of time, and the caller writes the status
// and then deletes the pod, so an operator that stops between the two
// reads the same pod again. It answers no outcome for a pod that still
// runs, with the time of its deadline, or for a pod it deleted because
// the Person's source changed.
func (b *bakers) follow(p *person, baker pod, now time.Time) (*outcome, time.Time, error) {
	if baker.Metadata.DeletionTimestamp != "" {
		return nil, time.Time{}, nil
	}
	if baker.Metadata.Annotations[bakerSourceKey] != p.Spec.Avatar {
		return nil, time.Time{}, b.remove(baker)
	}
	out := &outcome{source: p.Spec.Avatar, checked: baker.Metadata.Annotations[bakerCheckedKey], reason: reasonBaked}
	created, _ := time.Parse(time.RFC3339, baker.Metadata.CreationTimestamp)
	deadline := created.Add(bakeDeadline)
	switch {
	case baker.finished():
		out.reading, out.err = b.result(baker, knownVersion(p))
	case !now.Before(deadline):
		out.err = failure(reasonBakeFailed, fmt.Errorf("the baker pod %s/%s did not finish within %s",
			baker.Metadata.Namespace, baker.Metadata.Name, bakeDeadline))
	default:
		return nil, deadline, nil
	}
	return out, time.Time{}, nil
}

// result reads the line a finished baker pod wrote to its log. The
// line is JSON, so the client decodes the log as the answer to the
// request. limitBytes bounds the read at the largest line a thumbnail
// makes, with room to spare.
func (b *bakers) result(baker pod, known pictureVersion) (reading, error) {
	var line bakeResult
	logPath := podPath(baker.Metadata.Namespace, baker.Metadata.Name) + "/log?container=" + bakerContainer + "&limitBytes=1048576"
	if err := b.client.RequestJSON(http.MethodGet, logPath, nil, &line); err != nil {
		return reading{}, failure(reasonBakeFailed, fmt.Errorf("the baker pod %s/%s wrote no result: %w",
			baker.Metadata.Namespace, baker.Metadata.Name, err))
	}
	if line.Error != "" || line.Thumbnail == "" {
		return reading{}, &sourceError{reason: bakeReason(line.Reason), err: errors.New(line.Error)}
	}
	version := pictureVersion{LastModified: line.ModTime, Size: line.Size}
	if version == known {
		return reading{version: version, unchanged: true}, nil
	}
	return reading{thumbnail: line.Thumbnail, version: version}, nil
}

// bakeReason keeps the pod's reason when it is one a bake reports.
func bakeReason(reason string) string {
	if reason == reasonDecodeFailed {
		return reason
	}
	return reasonBakeFailed
}

// ownImage reads the image of the operator's own container from its
// pod. A baker pod runs the same image, so a kustomization that pins
// the operator's tag pins the baker's too.
func ownImage(client *apiclient.Client, namespace, name string) (string, error) {
	own, err := apiclient.Get[pod](client, podPath(namespace, name))
	if err != nil {
		return "", fmt.Errorf("reading the operator's own pod %s/%s: %w", namespace, name, err)
	}
	for _, held := range own.Spec.Containers {
		if held.Name == operatorContainer {
			return held.Image, nil
		}
	}
	return "", fmt.Errorf("the pod %s/%s has no container named %s", namespace, name, operatorContainer)
}

const operatorContainer = "operator"

// bakedScheme answers the mount of a reference that a baker pod reads,
// and false for a scheme the operator reads itself.
func bakedScheme(ref *url.URL, namespace string) (bakerMount, bool, error) {
	mounter, baked := mounts[ref.Scheme]
	if !baked {
		return bakerMount{}, false, nil
	}
	mount, err := mounter(ref, namespace)
	return mount, true, err
}
