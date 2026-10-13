package main

// The finalizer astrophotography.liken.sh/frames on a Target holds a
// delete of the target while frames of it are pending in an outbox
// claim. Promotion learns which frames wait, and where, only from the
// target's status.outboxes, so a target that went with frames pending
// would leave them in the outbox with nothing to move them, and with no
// word to the person who deleted it.
//
// The promotion loop puts the finalizer on every target it reads.
// While a delete waits, no reservation claims the target (selection.go),
// a runner that holds it releases it after the frame in flight, and
// promotion goes on moving its frames. The loop removes the finalizer
// when no reservation holds the target and no frame of it is pending
// in an outbox claim that exists. A claim that is gone took its frames
// with it, so nothing could move them and the delete goes on. While
// promotion cannot move a frame, ArchiveAvailable says why, and a
// person can remove the finalizer by hand to delete the target and
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

// deletingMessage is the message of Imaging False with Deleting.
const deletingMessage = "The target is being deleted. Its finalizer " + framesFinalizer +
	" keeps it until promotion moves the frames that status.outboxes lists to the archive."

// keepTargetFinalizer adds the finalizer to a target that is not being
// deleted, and for one that is, either reports why the delete waits or
// removes the finalizer.
func (p *promotions) keepTargetFinalizer(t *astrophotography.Target, w world) error {
	holds := slices.Contains(t.Metadata.Finalizers, framesFinalizer)
	switch {
	case t.Metadata.DeletionTimestamp == nil && !holds:
		return p.patchTargetFinalizer(t, true)
	case t.Metadata.DeletionTimestamp == nil || !holds:
		return nil
	case t.Status.Reservation != "":
		// The runner releases the target after the frame in flight, and
		// that write wakes the loop again.
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

// framesWait reports whether a frame of the target is pending in an
// outbox claim that exists.
func framesWait(t *astrophotography.Target, w world) bool {
	return slices.ContainsFunc(t.Status.Outboxes, func(entry astrophotography.OutboxFrames) bool {
		_, exists := w.claims[t.Metadata.Namespace+"/"+entry.ClaimName]
		return entry.Pending > 0 && exists
	})
}

// patchTargetFinalizer adds or removes the finalizer with a merge patch
// that states the resourceVersion of the copy held, so a write of
// another controller's finalizer in between answers 409 and is not
// lost. The memo notes the answer, so the next pass reads the target
// at its new version.
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
		var answer astrophotography.Target
		err := p.client.Request(http.MethodPatch, path, mergePatchType, body, &answer)
		return &answer, err
	})
	return err
}
