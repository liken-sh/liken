package main

// The finalizer astrophotography.liken.sh/frames holds an outbox claim
// while a Target's status.outboxes names it, so a delete of the claim
// waits while frames of a target are in it and not yet in the archive.
// The loop removes the finalizer when no target names the claim. While
// the operator is down, a delete waits for it; a person can remove the
// finalizer by hand, and the frames in the claim go with it.

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
)

const framesFinalizer = astrophotography.Group + "/frames"

// mergePatchType is the content type of a JSON merge patch.
const mergePatchType = "application/merge-patch+json"

// keepFinalizers adds the finalizer to each claim that a target names,
// and removes it from every other claim. The API server refuses a new
// finalizer on a claim that is being deleted, so the loop adds none to
// one.
func (p *promotions) keepFinalizers(targets []astrophotography.Target, claims map[string]claimObject) error {
	named := map[string]bool{}
	for _, t := range targets {
		for _, entry := range t.Status.Outboxes {
			named[t.Metadata.Namespace+"/"+entry.ClaimName] = true
		}
	}
	for key, claim := range claims {
		holds := slices.Contains(claim.Metadata.Finalizers, framesFinalizer)
		want := named[key]
		if holds == want || (want && claim.Metadata.DeletionTimestamp != nil) {
			continue
		}
		if err := p.patchFinalizer(claim, want); err != nil {
			return err
		}
	}
	return nil
}

// patchFinalizer adds or removes the finalizer with a merge patch that
// states the store's resourceVersion, so a write of another
// controller's finalizer in between answers 409 and is not lost. The
// next pass reads the claim's new copy and patches again.
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
