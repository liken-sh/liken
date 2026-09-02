package main

// The operator owns two kinds of standing objects: a Player's idle claim
// with its idle client pod, and a Remote's controller claim with its
// reader pod. Each one
// stands whether or not anything plays, and each follows the template the
// current pass would build. This file holds the hash that tells one
// template from another, and the rule every standing reconcile runs.
//
// A Deployment finds a stale pod by stamping a hash of the template it
// built and comparing that hash, never by comparing live specs, because
// the API server defaults fields the builder never set and a live
// comparison would either roll on every pass or grow a field-by-field
// allowlist. The operator does the same with one annotation on each
// object it stands up.

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
)

// templateHashAnnotation carries the hash of the spec the operator built.
// The key is the operator's own group, because the value is this
// operator's record of its own output and no other program reads it.
const templateHashAnnotation = "media.liken.sh/template-hash"

// templateHash reduces one built spec to the string the annotation
// carries. fnv-1a is enough here, and the whole job is to tell one pass's
// output from another's. Nothing signs the value and nothing outside this
// operator reads it, so the hash needs no collision resistance against an
// attacker.
//
// The input is the spec alone and never the metadata, so the annotation
// is not part of what it hashes and a stamped object hashes to the same
// value as the object before the stamp.
func templateHash(spec any) (string, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	sum := fnv.New64a()
	// A hash never fails a write, so the error is the interface's and not
	// a state this code can reach.
	_, _ = sum.Write(body)
	return strconv.FormatUint(sum.Sum64(), 16), nil
}

// stampTemplateHash writes the hash of one built spec onto the object
// that carries it. The caller hands in the metadata and the spec of the
// same object, and the stamp is what a later pass compares against.
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

// standing is what one pass wants for a claim and a pod that
// stand between runs: the namespace and the names to read in the
// cluster, and the objects this pass would build under those names. A
// nil object is one the pass no longer wants, and the name beside it is
// what the pass deletes. An empty claim name is a standing pod that
// holds no claim.
type standing struct {
	namespace string
	claimName string
	claim     *ResourceClaim
	podName   string
	pod       *Pod
}

// standingPair is the standing of a Remote or of the idle screen
// this operator draws itself: one claim and one pod, both built by this
// pass and both wanted.
func standingPair(claim *ResourceClaim, pod *Pod) standing {
	return standing{
		namespace: claim.Metadata.Namespace,
		claimName: claim.Metadata.Name,
		claim:     claim,
		podName:   pod.Metadata.Name,
		pod:       pod,
	}
}

// podsResource is the resource name a claim's status.reservedFor
// carries for a pod. This operator acts on that kind of holder alone.
const podsResource = "pods"

// reconcileStanding brings one standing pair into line with the claim and
// the pod this pass would build. It stamps both with their template
// hashes, reads what the cluster holds, and replaces whichever object no
// longer matches. It creates whatever is missing, which is how a
// replacement comes back: the delete returns, and the next pass finds the
// object absent and creates it.
//
// The two divergences are not the same repair. A claim is immutable and
// the pod holds its allocation, so a changed claim replaces both. A
// changed pod replaces the pod alone, and the claim keeps its
// allocation, so an image-only release does not cost a sleeping
// controller the allocation it holds, and the reader pod comes back
// running instead of Pending.
//
// An object the pass no longer wants is deleted on the same two
// rules: an unwanted claim takes its holders with it, and an unwanted pod
// goes alone.
//
// A 409 on either create means another pass, or another copy of this
// operator, created the object first, which is success.
func (o *operator) reconcileStanding(want standing) error {
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
	namespace := want.namespace

	var liveClaim *ResourceClaim
	claimStands := false
	if want.claimName != "" {
		held, err := GetResourceClaim(o.client, namespace, want.claimName)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		liveClaim, claimStands = held, err == nil
	}

	livePod, err := GetPod(o.client, namespace, want.podName)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	podStands := err == nil

	// An object with a deletionTimestamp counts as still present. The
	// delete this operator sent is in progress, and the pass leaves the
	// whole pair alone until it completes, so one divergence causes one
	// delete and not one delete per pass.
	if claimStands && liveClaim.Metadata.DeletionTimestamp != "" {
		return nil
	}
	if podStands && livePod.Metadata.DeletionTimestamp != "" {
		return nil
	}

	// A live object stamped with a different hash is stale, and so is a
	// live object with no stamp at all, which an older release created.
	// So the first release that carries the stamp rolls every standing
	// pod once, and the pass after that reads matching hashes and deletes
	// nothing.
	if claimStands && (want.claim == nil || !sameTemplate(&liveClaim.Metadata, &want.claim.Metadata)) {
		own := ""
		if podStands {
			own = want.podName
		}
		if err := o.deleteClaimHolders(liveClaim, own); err != nil {
			return err
		}
		return DeleteResourceClaim(o.client, namespace, want.claimName)
	}
	if podStands && (want.pod == nil || !sameTemplate(&livePod.Metadata, &want.pod.Metadata)) {
		return DeletePod(o.client, namespace, want.podName)
	}

	if want.claim != nil && !claimStands {
		if _, err := CreateResourceClaim(o.client, want.claim); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	if want.pod != nil && !podStands {
		if _, err := CreatePod(o.client, want.pod); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}

// deleteClaimHolders deletes every pod that holds one claim,
// before the claim itself goes. A claim in use carries the
// delete-protection finalizer until every holder is gone, so deleting the
// claim under a running pod would leave it Terminating for as long as
// that pod runs. status.reservedFor is the holder list, and it is the
// one place this operator learns of a delegate's pod, which it did not
// create.
//
// own is this operator's own pod for the claim, which goes whether
// or not the list names it: a pod still Pending holds no reservation, and
// leaving it would let it schedule against the replacement claim. An
// absent pod is a holder that already went, which the delete reads as
// success.
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

// sameTemplate reports whether a live object carries the hash the pass
// just stamped on the object it built. An absent annotation reads as an
// empty string, which never equals a hash, so an object from a release
// that stamped nothing counts as diverged.
func sameTemplate(live, desired *ObjectMeta) bool {
	return live.Annotations[templateHashAnnotation] == desired.Annotations[templateHashAnnotation]
}
