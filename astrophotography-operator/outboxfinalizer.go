package main

// The promotion loop puts the finalizer astrophotography.liken.sh/frames
// on an outbox claim while a Target's status.outboxes lists the claim,
// so that deleting the claim waits until its frames are promoted to the
// archive. The loop removes the finalizer when no Target lists the
// claim. While the operator is down, the deletion waits. A person who
// removes the finalizer by hand loses the frames that are still in the
// claim.

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
)

const framesFinalizer = astrophotography.Group + "/frames"

const mergePatchType = "application/merge-patch+json"

// keepFinalizers adds the finalizer to each claim that a Target lists
// and removes it from every other claim. The API server refuses to add
// a finalizer to a claim that is being deleted, so the loop does not
// try.
func (p *promotions) keepFinalizers(targets []astrophotography.Target, claims map[string]claimObject) error {
	listed := map[string]bool{}
	for _, t := range targets {
		for _, entry := range t.Status.Outboxes {
			listed[t.Metadata.Namespace+"/"+entry.ClaimName] = true
		}
	}
	for key, claim := range claims {
		has := slices.Contains(claim.Metadata.Finalizers, framesFinalizer)
		want := listed[key]
		if has == want || (want && claim.Metadata.DeletionTimestamp != nil) {
			continue
		}
		if err := p.patchFinalizer(claim, want); err != nil {
			return err
		}
	}
	return nil
}

// patchFinalizer sends the whole finalizer list in a merge patch, with
// the cached resourceVersion. When another controller changed the
// finalizers since the cache read, the API server returns 409 instead of
// overwriting that change, and the next pass patches the new copy.
func (p *promotions) patchFinalizer(claim claimObject, present bool) error {
	finalizers := slices.DeleteFunc(slices.Clone(claim.Metadata.Finalizers), func(f string) bool { return f == framesFinalizer })
	if present {
		finalizers = append(finalizers, framesFinalizer)
	}
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{
		"resourceVersion": claim.Metadata.ResourceVersion,
		"finalizers":      finalizers,
	}})
	path := "/api/v1/namespaces/" + claim.Metadata.Namespace + "/persistentvolumeclaims/" + claim.Metadata.Name
	return p.client.Request(http.MethodPatch, path, mergePatchType, body, nil)
}
