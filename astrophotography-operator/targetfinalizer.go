package main

// The finalizer astrophotography.liken.sh/frames keeps a Target from
// being deleted while frames of it are waiting in an outbox claim.
// Promotion learns which frames are waiting, and in which claim, only
// from the Target's status.outboxes. If the Target were deleted with
// frames waiting, they would stay in the outbox with nothing to move
// them to the archive, and the person who deleted the Target would get
// no message about it.
//
// The promotion loop adds the finalizer to every Target it reads. Once
// a delete is requested, no runner selects the Target (selection.go),
// the runner named in status.reservation releases it after the current
// frame, and promotion keeps moving its frames. The loop removes the
// finalizer when status.reservation is empty and no frame of the Target
// is waiting in an outbox claim that still exists. Promotion cannot
// read the frames of a deleted claim, so they do not hold up the
// delete. While promotion
// cannot move a frame, the ArchiveAvailable condition gives the reason.
// A person can remove the finalizer by hand to delete the Target and
// leave the frames in the outbox.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// deletingMessage is the message of the Imaging condition when its
// status is False and its reason is Deleting.
const deletingMessage = "The target is being deleted, and its finalizer " + framesFinalizer +
	" keeps it until promotion moves the frames that status.outboxes lists to the archive"

// keepTargetFinalizer adds the finalizer to a Target that is not being
// deleted. For a Target that is being deleted, it either sets the
// Imaging condition to say why the delete waits, or removes the
// finalizer.
func (p *promotions) keepTargetFinalizer(t *astrophotography.Target, w world) error {
	hasFinalizer := slices.Contains(t.Metadata.Finalizers, framesFinalizer)
	switch {
	case t.Metadata.DeletionTimestamp == nil && !hasFinalizer:
		return p.patchTargetFinalizer(t, true)
	case t.Metadata.DeletionTimestamp == nil || !hasFinalizer:
		return nil
	case t.Status.Reservation != "":
		// The runner releases the Target after the current frame, and
		// that write triggers another pass of the loop.
		return nil
	case framesWait(t, w):
		return p.setCondition(t, conditions.Condition{Type: astrophotography.ConditionImaging, Status: conditions.False,
			Reason: astrophotography.ReasonDeleting, Message: deletingMessage})
	}
	for _, entry := range t.Status.Outboxes {
		if entry.Pending > 0 {
			fmt.Fprintf(p.logs, "%s: Target %s: the outbox claim %s is gone with %s, so the delete goes on\n",
				managedBy, targetName(t), entry.ClaimName, frames(entry.Pending))
		}
	}
	return p.patchTargetFinalizer(t, false)
}

// framesWait reports whether a frame of the Target is waiting in an
// outbox claim that still exists.
func framesWait(t *astrophotography.Target, w world) bool {
	return slices.ContainsFunc(t.Status.Outboxes, func(entry astrophotography.OutboxFrames) bool {
		_, exists := w.claims[t.Metadata.Namespace+"/"+entry.ClaimName]
		return entry.Pending > 0 && exists
	})
}

// patchTargetFinalizer adds or removes the finalizer with a merge patch
// that includes the resourceVersion of the Target it read. If another
// controller changed the finalizers in the meantime, the API server
// returns 409 and the other controller's finalizer is not overwritten.
// The memo records the version in the response, so the next pass reads
// the Target at its new version.
func (p *promotions) patchTargetFinalizer(t *astrophotography.Target, present bool) error {
	finalizers := slices.DeleteFunc(slices.Clone(t.Metadata.Finalizers), func(f string) bool { return f == framesFinalizer })
	if present {
		finalizers = append(finalizers, framesFinalizer)
	}
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{
		"resourceVersion": t.Metadata.ResourceVersion,
		"finalizers":      finalizers,
	}})
	path := targetPath(t.Metadata.Namespace, t.Metadata.Name)
	_, err := memo.Written[astrophotography.Target](p.versions, memo.Key(&t.Metadata), func() (*astrophotography.Target, error) {
		var patched astrophotography.Target
		err := p.client.Request(http.MethodPatch, path, mergePatchType, body, &patched)
		return &patched, err
	})
	return err
}
