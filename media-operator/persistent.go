package main

// The operator keeps two claim-and-pod pairs that exist whether or not
// anything plays: a Player's idle claim with its idle client pod, and a
// Remote's controller claim with its reader pod. Each pass builds the
// pair again from the current spec, as a persistentPair, and this file
// creates, replaces, or deletes objects so the cluster matches it.
//
// An object is stale when the hash of its spec, stored in an annotation
// when the operator built it, differs from the hash of the spec this
// pass built. Comparing live specs does not work, because the API server
// sets defaults on fields the builder leaves empty, so a live spec never
// equals the built one without a list of every defaulted field.

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// templateHashAnnotation holds the hash of the spec the operator built.
const templateHashAnnotation = "media.liken.sh/template-hash"

// templateHash hashes the spec alone, so writing the annotation into the
// metadata does not change the hash. FNV-1a is enough because the hash
// only tells one build of a spec from another, and no attacker controls
// the input.
func templateHash(spec any) (string, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	sum := fnv.New64a()
	// hash.Hash.Write never returns an error.
	_, _ = sum.Write(body)
	return strconv.FormatUint(sum.Sum64(), 16), nil
}

// stampTemplateHash sets templateHashAnnotation on metadata to the hash
// of spec. Both must belong to the same object.
func stampTemplateHash(metadata *ObjectMeta, spec any) error {
	hash, err := templateHash(spec)
	if err != nil {
		return err
	}
	if metadata.Annotations == nil {
		metadata.Annotations = map[string]string{}
	}
	metadata.Annotations[templateHashAnnotation] = hash
	return nil
}

// persistentPair is one claim and pod as this pass built them. A nil claim or
// pod means the pass wants none, and the object with that name is
// deleted. An empty claimName means the pod has no claim.
type persistentPair struct {
	// subject is the owner, such as "player den/tv", for the log lines
	// of a create or a delete. absent is the reason the pass wants no
	// pod, for the log line of its delete.
	subject string
	absent  string

	namespace string
	claimName string
	claim     *ResourceClaim
	podName   string
	pod       *Pod
}

// newPersistentPair returns a persistentPair with both a claim and a pod, for a
// Remote or for an idle screen that this operator draws itself.
func newPersistentPair(subject string, claim *ResourceClaim, pod *Pod) persistentPair {
	return persistentPair{
		subject:   subject,
		namespace: claim.Metadata.Namespace,
		claimName: claim.Metadata.Name,
		claim:     claim,
		podName:   pod.Metadata.Name,
		pod:       pod,
	}
}

// podsResource is the resource that status.reservedFor lists for a pod.
// deleteClaimHolders deletes only holders of this resource.
const podsResource = "pods"

// claimRead is a claim that the pass already read. When read is true, a
// nil claim means the claim does not exist. The zero value means no read
// was made.
type claimRead struct {
	claim *ResourceClaim
	read  bool
}

// readClaim reads one claim from the view. A missing claim is not an
// error, because the reconcile creates it.
func (o *operator) readClaim(namespace, name string) (claimRead, error) {
	claim, err := o.view.ResourceClaim(namespace, name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return claimRead{read: true}, nil
	}
	if err != nil {
		return claimRead{}, err
	}
	return claimRead{claim: claim, read: true}, nil
}

// persistentObjects is the claim and pod that one read found. A nil field
// means that object does not exist.
type persistentObjects struct {
	claim *ResourceClaim
	pod   *Pod
}

// reconcilePersistent deletes each object of the cluster whose hash
// differs from want, or that want does not have, and creates each object
// of want that the cluster lacks. A stale object is deleted on one pass
// and created again on the next.
//
// It decides from the view, which costs no request when the pair is
// current. When something needs a create or a delete, it reads both
// objects again from the API server and acts on that read, because the
// view can still have an object this operator just deleted, or lack one
// it just created.
//
// The pods watch selects on the component label (ownPodsSelector), and a
// pod that an older release created can lack that label, so the view
// does not have it. So when want has no pod and the view has none,
// reconcilePersistent reads the pod from the API server, once per process
// for each pod: absenceChecked records each pod that a read found
// absent.
//
// A changed claim replaces the claim and its pod, because a claim's spec
// is immutable and the pod holds its allocation. A changed pod replaces
// the pod alone and keeps the claim. So a release that changes only the
// reader's image does not take the allocation from a sleeping
// controller, which would leave the new reader pod Pending. An unwanted
// claim is deleted with every pod that holds it, and an unwanted pod is
// deleted alone.
//
// A 409 on a create means another pass or another copy of this operator
// created the object first, which counts as success.
//
// known is a claim read that the caller already made: the pass reads
// each Remote's claim to resolve the controller's Peripheral. When known
// is the zero value, reconcilePersistent reads the claim itself.
func (o *operator) reconcilePersistent(want persistentPair, known claimRead) error {
	if want.claim != nil {
		if err := stampTemplateHash(&want.claim.Metadata, want.claim.Spec); err != nil {
			return err
		}
	}
	if want.pod != nil {
		if err := stampTemplateHash(&want.pod.Metadata, want.pod.Spec); err != nil {
			return err
		}
	}
	viewed, err := o.viewPersistent(want, known)
	if err != nil {
		return err
	}
	absence := namespacedKey(want.namespace, want.podName)
	unconfirmed := want.pod == nil && viewed.pod == nil && !o.absenceChecked[absence]
	if persistentSettled(want, viewed) && !unconfirmed {
		return nil
	}
	live, err := o.livePersistent(want)
	if err != nil {
		return err
	}
	if err := o.settlePersistent(want, live); err != nil {
		return err
	}
	// Record only a read that found no pod. A pod that existed is being
	// deleted, and the next pass must read again until it is gone.
	if want.pod == nil && live.pod == nil {
		if o.absenceChecked == nil {
			o.absenceChecked = map[string]bool{}
		}
		o.absenceChecked[absence] = true
	}
	return nil
}

// viewPersistent reads the claim and pod from the view.
func (o *operator) viewPersistent(want persistentPair, known claimRead) (persistentObjects, error) {
	var found persistentObjects
	if want.claimName != "" {
		if !known.read {
			var err error
			if known, err = o.readClaim(want.namespace, want.claimName); err != nil {
				return found, err
			}
		}
		found.claim = known.claim
	}
	pod, err := o.view.Pod(want.namespace, want.podName)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		return found, err
	}
	found.pod = pod
	return found, nil
}

// livePersistent reads the claim and pod from the API server.
func (o *operator) livePersistent(want persistentPair) (persistentObjects, error) {
	var found persistentObjects
	if want.claimName != "" {
		claim, err := GetResourceClaim(o.client, want.namespace, want.claimName)
		if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			return found, err
		}
		found.claim = claim
	}
	pod, err := GetPod(o.client, want.namespace, want.podName)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		return found, err
	}
	found.pod = pod
	return found, nil
}

// persistentSettled reports whether found needs no create or delete: each
// object exists with the hash of want's object or is absent from both,
// or either object is already being deleted.
func persistentSettled(want persistentPair, found persistentObjects) bool {
	if found.claim != nil && found.claim.Metadata.deleting() {
		return true
	}
	if found.pod != nil && found.pod.Metadata.deleting() {
		return true
	}
	if want.claimName != "" && !claimSettled(want.claim, found.claim) {
		return false
	}
	return podSettled(want.pod, found.pod)
}

func claimSettled(wanted, found *ResourceClaim) bool {
	if wanted == nil || found == nil {
		return wanted == nil && found == nil
	}
	return sameTemplate(&found.Metadata, &wanted.Metadata)
}

func podSettled(wanted, found *Pod) bool {
	if wanted == nil || found == nil {
		return wanted == nil && found == nil
	}
	return sameTemplate(&found.Metadata, &wanted.Metadata)
}

// settlePersistent creates or deletes objects to match want, from a read
// of the API server.
func (o *operator) settlePersistent(want persistentPair, live persistentObjects) error {
	namespace := want.namespace
	liveClaim, claimExists := live.claim, live.claim != nil
	livePod, podExists := live.pod, live.pod != nil

	// While either object has a deletionTimestamp, wait for that delete
	// to finish, so a stale object gets one delete and not one per pass.
	if claimExists && liveClaim.Metadata.DeletionTimestamp != "" {
		return nil
	}
	if podExists && livePod.Metadata.DeletionTimestamp != "" {
		return nil
	}

	// An object with no hash, which an older release created, counts as
	// stale too, so it is replaced once.
	if claimExists && (want.claim == nil || !sameTemplate(&liveClaim.Metadata, &want.claim.Metadata)) {
		own := ""
		if podExists {
			own = want.podName
		}
		if err := o.deleteClaimHolders(liveClaim, own); err != nil {
			return err
		}
		if err := DeleteResourceClaim(o.client, namespace, want.claimName); err != nil {
			return err
		}
		o.logPersistent(want, "deleted claim "+want.claimName+" and the pods that hold it", want.claim == nil)
		return nil
	}
	if podExists && (want.pod == nil || !sameTemplate(&livePod.Metadata, &want.pod.Metadata)) {
		if err := DeletePod(o.client, namespace, want.podName); err != nil {
			return err
		}
		o.logPersistent(want, "deleted pod "+want.podName, want.pod == nil)
		return nil
	}
	// A pod from an older release can have the current hash and no
	// component label. The view never has such a pod, so every pass would
	// read it here. Adding the label in place puts the pod in the view
	// without restarting it.
	if podExists && want.pod != nil {
		key := playbackLabelKey
		if value := want.pod.Metadata.Labels[key]; value != "" && livePod.Metadata.Labels[key] != value {
			if err := PatchPodLabels(o.client, namespace, want.podName, map[string]string{key: value}); err != nil {
				return err
			}
		}
	}

	if want.claim != nil && !claimExists {
		if _, err := CreateResourceClaim(o.client, want.claim); err != nil && !errors.Is(err, apiclient.ErrConflict) {
			return err
		}
		logLine(o.log, "%s: created claim %s", want.subject, want.claimName)
	}
	if want.pod != nil && !podExists {
		if _, err := CreatePod(o.client, want.pod); err != nil && !errors.Is(err, apiclient.ErrConflict) {
			return err
		}
		logLine(o.log, "%s: created pod %s", want.subject, want.podName)
	}
	return nil
}

// logPersistent logs one delete with its reason: want.absent when the pass
// no longer wants the object, and a changed hash otherwise.
func (o *operator) logPersistent(want persistentPair, deleted string, unwanted bool) {
	reason := "its template changed with a spec edit or a new release"
	if unwanted {
		reason = want.absent
	}
	logLine(o.log, "%s: %s, because %s", want.subject, deleted, reason)
}

// deleteClaimHolders deletes every pod that holds claim, before the
// caller deletes the claim. A claim in use has a finalizer until its
// holders are gone, so deleting the claim first would leave it
// Terminating for as long as a pod runs. status.reservedFor is the only
// record of a pod that another operator created for the claim.
//
// own is this operator's pod for the claim. It is deleted even when
// status.reservedFor does not list it, because a Pending pod has no
// reservation and could otherwise schedule against the replacement
// claim. DeletePod treats a pod that is already gone as deleted.
func (o *operator) deleteClaimHolders(claim *ResourceClaim, own string) error {
	namespace := claim.Metadata.Namespace
	deleted := map[string]bool{}
	if claim.Status != nil {
		for _, holder := range claim.Status.ReservedFor {
			if holder.Resource != podsResource || deleted[holder.Name] {
				continue
			}
			if err := DeletePod(o.client, namespace, holder.Name); err != nil {
				return err
			}
			deleted[holder.Name] = true
		}
	}
	if own == "" || deleted[own] {
		return nil
	}
	return DeletePod(o.client, namespace, own)
}

// sameTemplate reports whether live has the hash of desired. An object
// with no annotation reads as an empty string, which never equals a
// hash.
func sameTemplate(live, desired *ObjectMeta) bool {
	return live.Annotations[templateHashAnnotation] == desired.Annotations[templateHashAnnotation]
}
