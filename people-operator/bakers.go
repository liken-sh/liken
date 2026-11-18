package main

// The operator keeps no state about a bake in memory. The baker pod's
// annotations record the Person, the source, and the check request, and
// its phase shows whether the bake is done. So an operator that
// restarts during a bake finds the pod and reads its result. A pod
// whose Person is gone, or whose Person's avatar changed, is deleted.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// bakeDeadline is how long a baker pod may take from its creation. A
// pod can stay Pending with no end, for example when no node can
// schedule it. activeDeadlineSeconds counts from the pod's
// status.startTime, which an unscheduled pod never gets, so the
// operator enforces the deadline itself.
const bakeDeadline = 2 * time.Minute

// bakers creates baker pods and reads their results.
type bakers struct {
	client *apiclient.Client

	// pods watches the baker pods in every namespace.
	pods informer.View

	// image is the operator's own image, which baker pods also run.
	image string

	// namespace is the operator's own namespace, where baker pods for
	// nfs:// sources run.
	namespace string
}

// byPerson returns the baker pods keyed by Person name. It returns
// false until the informer has listed every pod, because a pass that
// finds no pod for a Person starts one.
func (b *bakers) byPerson() (map[string]pod, bool) {
	if !b.pods.Ready() {
		return nil, false
	}
	byName := map[string]pod{}
	for _, item := range informer.CachedList[pod](b.pods) {
		byName[item.Metadata.Annotations[bakerPersonKey]] = item
	}
	return byName, true
}

// start creates the Person's baker pod. A 409 means the pod exists
// already (see bakerPodName), and the next pass finds it.
func (b *bakers) start(p *person, mount bakerMount) error {
	baker := buildBakerPod(p, mount, b.image)
	err := b.client.RequestJSON(http.MethodPost, "/api/v1/namespaces/"+mount.namespace+"/pods", mustJSON(baker), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

func (b *bakers) remove(baker pod) error {
	err := b.client.RequestJSON(http.MethodDelete, podPath(baker.Metadata.Namespace, baker.Metadata.Name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// inspect checks a Person's baker pod and returns one of these:
//
//   - the outcome, when the pod has finished or passed bakeDeadline
//   - the pod's deadline, while it runs
//   - neither, when the pod is being deleted, or when inspect deletes it
//     because the Person's spec.avatar changed
//
// The caller writes the status and then deletes the pod, so an operator
// that stops between the two reads the same pod again.
func (b *bakers) inspect(p *person, baker pod, now time.Time) (*outcome, time.Time, error) {
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

// result reads the JSON line that a finished baker pod wrote to its
// log. limitBytes is well above the size of the largest thumbnail line.
func (b *bakers) result(baker pod, known pictureVersion) (reading, error) {
	var line bakeResult
	logPath := podPath(baker.Metadata.Namespace, baker.Metadata.Name) + "/log?container=" + bakerContainer + "&limitBytes=1048576"
	if err := b.client.RequestJSON(http.MethodGet, logPath, nil, &line); err != nil {
		return reading{}, failure(reasonBakeFailed, fmt.Errorf("reading the result in the log of the baker pod %s/%s: %w",
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

// bakeReason keeps DecodeFailed from the pod and turns any other
// reason into BakeFailed.
func bakeReason(reason string) string {
	if reason == reasonDecodeFailed {
		return reason
	}
	return reasonBakeFailed
}

// ownImage reads the image of the operator's own container from its
// pod. Baker pods run the same image, so pinning the operator's tag in
// a kustomization pins the baker's too.
func ownImage(client *apiclient.Client, namespace, name string) (string, error) {
	own, err := apiclient.Get[pod](client, podPath(namespace, name))
	if err != nil {
		return "", fmt.Errorf("reading the operator's own pod %s/%s: %w", namespace, name, err)
	}
	for _, c := range own.Spec.Containers {
		if c.Name == operatorContainer {
			return c.Image, nil
		}
	}
	return "", fmt.Errorf("the pod %s/%s has no container named %s", namespace, name, operatorContainer)
}

const operatorContainer = "operator"

// bakerMountFor returns the mount for a reference whose scheme a baker
// pod reads, and false for a scheme the operator reads in its own
// process.
func bakerMountFor(ref *url.URL, namespace string) (bakerMount, bool, error) {
	mounter, baked := mounts[ref.Scheme]
	if !baked {
		return bakerMount{}, false, nil
	}
	mount, err := mounter(ref, namespace)
	return mount, true, err
}
