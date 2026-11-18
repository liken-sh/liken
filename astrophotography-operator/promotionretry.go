package main

// Retrying the frames that a promotion Job left in an outbox. The
// operator starts a Job for a claim only when the claim has more
// pending frames than the last Job left (status.outboxes[].left). So a
// frame that failed starts no Job on its own, because a NameConflict
// or a CopyFailed needs a person to fix the archive first. The next Job
// starts when a new frame arrives in the claim, or when the person adds
// the annotation astrophotography.liken.sh/retry to the Target after
// the fix. The count of frames left is in the Target's status, so a
// restart of the operator retries nothing. observatory-operator handles
// observatory.liken.sh/retry on its resources the same way.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// annotationRetry is the retry annotation. The loop ignores its value.
const annotationRetry = astrophotography.Group + "/retry"

// retry handles the retry annotation. The loop calls it only after it
// applied the report of the target's last Job, so a Job that started
// before the person's fix does not use up the retry. retry sets left to
// 0 on every outbox entry, then removes the annotation. If the operator
// restarts between the two writes, it finds the annotation again with
// nothing left to clear, and the Job starts as before. Each annotation
// causes one retry: a frame that fails again waits for a new frame or
// the next annotation.
func (p *promotions) retry(t *astrophotography.Target) error {
	if _, annotated := t.Metadata.Annotations[annotationRetry]; !annotated {
		return nil
	}
	var left int32
	written, err := p.settle(t, func(current *astrophotography.Target) ([]conditions.Condition, bool) {
		left = 0
		for i := range current.Status.Outboxes {
			left += current.Status.Outboxes[i].Left
			current.Status.Outboxes[i].Left = 0
		}
		return nil, left > 0
	})
	if err != nil {
		return err
	}
	if written {
		message := fmt.Sprintf("Cleared status.outboxes[].left because of the annotation %s, so the next promotion Job copies the %s that the last Job left in the outbox again",
			annotationRetry, frames(left))
		fmt.Fprintf(p.logs, "%s: Target %s: %s\n", managedBy, targetName(t), message)
		p.recorder.Normal(events.ObjectReference{APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
			Namespace: t.Metadata.Namespace, Name: t.Metadata.Name, UID: t.Metadata.UID}, reasonPromotionRetry, message)
	}
	return p.clearRetry(t)
}

// clearRetry removes the annotation with a merge patch and stores the
// API server's response in t, so the next write of the Target in this
// pass uses its new resourceVersion. The patch has no resourceVersion
// precondition, so it also removes an annotation that a person added
// again after the status write. That loses nothing, because left is
// already 0 and the next Job starts anyway.
func (p *promotions) clearRetry(t *astrophotography.Target) error {
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{annotationRetry: nil}}})
	path := targetPath(t.Metadata.Namespace, t.Metadata.Name)
	patched, err := memo.Written[astrophotography.Target](p.versions, memo.Key(&t.Metadata), func() (*astrophotography.Target, error) {
		var patched astrophotography.Target
		err := p.client.Request(http.MethodPatch, path, mergePatchType, body, &patched)
		return &patched, err
	})
	if err != nil {
		return err
	}
	*t = *patched
	return nil
}
