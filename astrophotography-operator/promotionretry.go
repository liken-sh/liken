package main

// The retry of the frames that a promotion Job left in an outbox. A
// claim starts a Job when its pending frames are more than the frames
// the last Job left (status.outboxes[].left), so a frame that failed
// starts no Job again by itself, because a NameConflict or a CopyFailed
// needs a person to fix the archive first. The next Job starts when a
// new frame of the target arrives in the claim, or when a person adds
// the annotation astrophotography.liken.sh/retry to the target after
// the fix. The count is in the target's status, so a restart of the
// operator retries nothing. observatory-operator reads the annotation
// observatory.liken.sh/retry on its resources the same way.
//
// The loop acts on the annotation only when no Job of the target runs,
// so a Job that started before the fix does not use up the retry. It
// first sets left to 0 on every outbox entry, and then removes the
// annotation. A restart between the two writes finds the annotation
// again with nothing left to clear, and the Job starts as before. The
// retry asks once: a frame that fails again waits for a new frame or
// the next annotation.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// annotationRetry asks the promotion loop to try again the frames that
// the target's last Jobs left in the outbox. Its value is not read.
const annotationRetry = astrophotography.Group + "/retry"

// retry serves the retry annotation of a target that no Job runs for.
func (p *promotions) retry(t *astrophotography.Target) error {
	if _, asked := t.Metadata.Annotations[annotationRetry]; !asked {
		return nil
	}
	var left int32
	landed, err := p.settle(t, func(held *astrophotography.Target) ([]conditions.Condition, bool) {
		left = 0
		for i := range held.Status.Outboxes {
			left += held.Status.Outboxes[i].Left
			held.Status.Outboxes[i].Left = 0
		}
		return nil, left > 0
	})
	if err != nil {
		return err
	}
	if landed {
		message := fmt.Sprintf("the annotation %s asked for a retry, so the next promotion Job tries the %s that the last Job left in the outbox again",
			annotationRetry, frames(left))
		fmt.Fprintf(p.logs, "%s: Target %s: %s\n", managedBy, targetName(t), message)
		p.recorder.Normal(events.ObjectReference{APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
			Namespace: t.Metadata.Namespace, Name: t.Metadata.Name, UID: t.Metadata.UID}, reasonPromotionRetry, message)
	}
	return p.clearRetry(t)
}

// clearRetry removes the annotation with a merge patch, and keeps the
// API server's answer in t, so the pass's next write of the target
// states its new resourceVersion. The patch states no resourceVersion:
// a person who annotates again in between asks for the Job that starts
// next anyway.
func (p *promotions) clearRetry(t *astrophotography.Target) error {
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{annotationRetry: nil}}})
	path := targetPath(t.Metadata.Namespace, t.Metadata.Name)
	answer, err := memo.Written[astrophotography.Target](p.versions, memo.Key(&t.Metadata), func() (*astrophotography.Target, error) {
		var answer astrophotography.Target
		err := p.client.Request(http.MethodPatch, path, mergePatchType, body, &answer)
		return &answer, err
	})
	if err != nil {
		return err
	}
	*t = *answer
	return nil
}
