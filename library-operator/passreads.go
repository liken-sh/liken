package main

// A pass stands many claims and pods, and it reads each one first to
// learn whether it stands. So a pass reads each collection once when it
// starts, from the watches' stores (watch.go), and every check during
// the pass reads that copy. A read by name per object, or a list per
// pass, would cost the API server one request per claim and per pod on
// every pass, and most passes find everything standing.
//
// A write that raced the watch is safe. A create of an object the store
// does not hold yet meets a conflict, which every stand reads as success,
// and the next pass reads the object. An object deleted after the read is
// created again on the next pass.

import "context"

// The claims of every namespace, which the watch of the claims lists and
// watches. A Library names a claim a person made, which carries no label
// of this operator's, so the watch takes no selector.
const claimsAllPath = "/api/v1/persistentvolumeclaims"

// The selector that reaches every pod this operator stands by name: the
// copies of both durable stores, the screens, and the Jellyfin pod. A
// pod of another writer that took one of those names carries no such
// label, so the pass never sees it, and its create of that name meets a
// conflict and leaves the pod alone.
const stoodPodsSelector = scannerLabelKey + " in (" +
	catalogLabelValue + "," + progressLabelValue + "," + screenLabelValue + "," + jellyfinLabelValue + ")"

// The objects one pass read, keyed by namespace and name, or by name
// alone for a volume, which has no namespace. The pods are kept in
// namespace and name order as well, for the screens the pass walks.
type passReads struct {
	claims  map[string]*PersistentVolumeClaim
	volumes map[string]*PersistentVolume
	pods    map[string]*Pod
	podList []Pod
}

// readPass reads the three collections. A read that fails ends the pass,
// because without it the pass cannot tell a claim or a pod that stands
// from one it must create.
func readPass(watched collections) (*passReads, error) {
	claims, err := watched.readClaims()
	if err != nil {
		return nil, err
	}
	volumes, err := watched.readVolumes()
	if err != nil {
		return nil, err
	}
	pods, err := watched.readStoodPods()
	if err != nil {
		return nil, err
	}
	reads := &passReads{
		claims:  map[string]*PersistentVolumeClaim{},
		volumes: map[string]*PersistentVolume{},
		pods:    map[string]*Pod{},
		podList: pods.Items,
	}
	for index := range claims.Items {
		claim := &claims.Items[index]
		reads.claims[libraryKey(claim.Metadata.Namespace, claim.Metadata.Name)] = claim
	}
	for index := range volumes.Items {
		reads.volumes[volumes.Items[index].Metadata.Name] = &volumes.Items[index]
	}
	for index := range pods.Items {
		pod := &pods.Items[index]
		reads.pods[libraryKey(pod.Metadata.Namespace, pod.Metadata.Name)] = pod
	}
	return reads, nil
}

// PersistentVolumeClaimList is the claims one read answers.
type PersistentVolumeClaimList struct {
	Metadata ListMeta                `json:"metadata"`
	Items    []PersistentVolumeClaim `json:"items"`
}

// readClaim answers one claim from the pass's read, and reads it by name
// from the API server where no pass has read the claims, which is a
// caller outside a pass.
// An absent claim is ErrNotFound in both cases.
func (o *operator) readClaim(ctx context.Context, namespace, name string) (*PersistentVolumeClaim, error) {
	if o.reads == nil {
		return GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	}
	claim, held := o.reads.claims[libraryKey(namespace, name)]
	if !held {
		return nil, ErrNotFound
	}
	return claim, nil
}

// readVolume answers one volume the way readClaim answers a claim.
func (o *operator) readVolume(ctx context.Context, name string) (*PersistentVolume, error) {
	if o.reads == nil {
		return GetPersistentVolume(ctx, o.client, name)
	}
	volume, held := o.reads.volumes[name]
	if !held {
		return nil, ErrNotFound
	}
	return volume, nil
}

// readPod answers one pod the way readClaim answers a claim.
func (o *operator) readPod(ctx context.Context, namespace, name string) (*Pod, error) {
	if o.reads == nil {
		return GetPod(ctx, o.client, namespace, name)
	}
	pod, held := o.reads.pods[libraryKey(namespace, name)]
	if !held {
		return nil, ErrNotFound
	}
	return pod, nil
}

// forgetPod drops a pod the pass deleted, so a later read in the same pass
// does not find it standing.
func (o *operator) forgetPod(namespace, name string) {
	if o.reads != nil {
		delete(o.reads.pods, libraryKey(namespace, name))
	}
}

// forgetClaim drops a claim the pass deleted, the way forgetPod drops a pod.
func (o *operator) forgetClaim(namespace, name string) {
	if o.reads != nil {
		delete(o.reads.claims, libraryKey(namespace, name))
	}
}

// The screen pods among the pods the pass read, in namespace and name
// order.
func (r *passReads) screenPods() []Pod {
	var screens []Pod
	for _, pod := range r.podList {
		if pod.Metadata.Labels[scannerLabelKey] == screenLabelValue {
			screens = append(screens, pod)
		}
	}
	return screens
}
