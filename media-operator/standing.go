package main

// The operator owns two standing pairs: a Player's idle claim with its
// idle pod, and a Remote's controller claim with its reader pod. Both
// stand whether or not anything plays, and both follow the template the
// current pass would build. This file holds the hash that tells one from
// the other, and the rule both reconciles run.
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
// A 409 on either create means another pass, or another copy of this
// operator, created the object first, which is success.
func (o *operator) reconcileStanding(claim *ResourceClaim, pod *Pod) error {
	if err := stampTemplateHash(&claim.Metadata, claim.Spec); err != nil {
		return err
	}
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		return err
	}
	namespace := claim.Metadata.Namespace

	liveClaim, err := GetResourceClaim(o.client, namespace, claim.Metadata.Name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	claimStands := err == nil

	livePod, err := GetPod(o.client, namespace, pod.Metadata.Name)
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
	if claimStands && !sameTemplate(&liveClaim.Metadata, &claim.Metadata) {
		// The pod goes first. A claim in use carries a finalizer until
		// every pod that holds it is gone, so deleting the claim under a
		// running pod would leave the claim terminating for as long as the
		// pod runs.
		if podStands {
			if err := DeletePod(o.client, namespace, pod.Metadata.Name); err != nil {
				return err
			}
		}
		return DeleteResourceClaim(o.client, namespace, claim.Metadata.Name)
	}
	if podStands && !sameTemplate(&livePod.Metadata, &pod.Metadata) {
		return DeletePod(o.client, namespace, pod.Metadata.Name)
	}

	if !claimStands {
		if _, err := CreateResourceClaim(o.client, claim); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	if !podStands {
		if _, err := CreatePod(o.client, pod); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}

// sameTemplate reports whether a live object carries the hash the pass
// just stamped on the object it built. An absent annotation reads as an
// empty string, which never equals a hash, so an object from a release
// that stamped nothing counts as diverged.
func sameTemplate(live, desired *ObjectMeta) bool {
	return live.Annotations[templateHashAnnotation] == desired.Annotations[templateHashAnnotation]
}
