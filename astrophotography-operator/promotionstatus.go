package main

// How a promotion Job's report changes its Target's status. Each change
// is applied to the copy of the Target that the write replaces, so after
// a conflict it is applied again to the newer copy. The same write sets
// status.promotion.applied to the Job's number, so a report is applied
// once.
//
// The runner adds one to a sequence's pending count and to the outbox's
// pending count after each exposure. The report then sets them again
// from what the Job found:
//
//   - A sequence's archived count becomes the catalog's total for it,
//     and its pending count drops by as much as archived rose. The
//     totals cover the whole catalog, so the next report corrects the
//     counts after a report that the operator never read.
//   - An outbox's pending count becomes the frames the Job left plus
//     the frames the runner counted after the Job was created. When the
//     Job also moved one of those later frames, that frame is counted
//     once too often, and the next Job finds nothing to move and
//     corrects the count. A frame is never counted too few times, so the
//     finalizer on the outbox claim is never removed while a frame is
//     still in the outbox.

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/catalog"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

type promotionResult struct {
	job    string
	number int32
	claim  string
	// pending is the outbox's pending count of the Target's frames when
	// the Job was created.
	pending int32
	// report is nil when the Job ended with no report.
	report *promote.Report
	// failure is the reason there is no report.
	failure string
}

// promotionPending returns the pending count in the Job's annotation, or
// zero when the annotation is missing or is not an int32.
func promotionPending(j jobObject) int32 {
	pending, err := strconv.ParseInt(j.Metadata.Annotations[annotationPending], 10, 32)
	if err != nil {
		return 0
	}
	return int32(pending)
}

// resultOf returns the result of a finished promotion Job, read from the
// termination message of the pod that succeeded. It returns false when
// that report is not in the store yet.
//
// The Job and pod watches deliver events independently, so a Job that
// succeeded can reach the store before its pod's final status. The
// pod's update then wakes the loop again. When the pod is gone, the
// report never arrives, and rerun creates the Job again.
func resultOf(j jobObject, number int32, succeeded bool, pods []podObject) (promotionResult, bool) {
	result := promotionResult{job: j.Metadata.Name, number: number, claim: j.Metadata.Annotations[annotationClaim], pending: promotionPending(j)}
	if !succeeded {
		failed, _ := condition(j.Status.Conditions, "Failed", "True")
		result.failure = fmt.Sprintf("The promotion Job %s failed: %s", j.Metadata.Name, failed.Message)
		return result, true
	}
	for _, pod := range pods {
		if pod.Metadata.Labels[labelPromotionJob] != j.Metadata.Name {
			continue
		}
		for _, status := range pod.Status.ContainerStatuses {
			ended := status.State.Terminated
			if status.Name != promoteContainer || ended == nil || ended.ExitCode != 0 {
				continue
			}
			var report promote.Report
			if err := json.Unmarshal([]byte(ended.Message), &report); err != nil {
				result.failure = fmt.Sprintf("The report of the promotion Job %s does not decode: %v", j.Metadata.Name, err)
				return result, true
			}
			result.report = &report
			return result, true
		}
	}
	return result, false
}

// applied returns the number of the last Job whose report is in the
// status.
func applied(t *astrophotography.Target) int32 {
	if t.Status.Promotion == nil {
		return 0
	}
	return t.Status.Promotion.Applied
}

// apply updates the Target's status from the result. It returns the
// conditions that transitioned, and false when the status already has
// this Job's result.
func (r promotionResult) apply(t *astrophotography.Target) ([]conditions.Condition, bool) {
	if applied(t) >= r.number {
		return nil, false
	}
	t.Status.Promotion = &astrophotography.PromotionStatus{Applied: r.number}
	entry := slices.IndexFunc(t.Status.Outboxes, func(o astrophotography.OutboxFrames) bool { return o.ClaimName == r.claim })
	var changed []conditions.Condition
	if r.report == nil {
		if entry >= 0 {
			t.Status.Outboxes[entry].Left = t.Status.Outboxes[entry].Pending
		}
		return setCondition(t, changed, archiveAvailable(conditions.False, astrophotography.ReasonCopyFailed, r.failure)), true
	}
	t.Status.Sequences = sequenceProgress(t.Spec.Sequences, t.Status.Sequences, r.report.Totals, r.report.Truncated)
	if entry >= 0 {
		outbox := &t.Status.Outboxes[entry]
		outbox.Pending = r.report.Left + max(0, outbox.Pending-r.pending)
		outbox.Left = r.report.Left
		if outbox.Pending == 0 {
			t.Status.Outboxes = slices.Delete(t.Status.Outboxes, entry, entry+1)
		}
	}
	changed = setCondition(t, changed, completeness(t))
	return setCondition(t, changed, r.availability()), true
}

// availability returns ArchiveAvailable False with the reason of the
// report's first failure, or True when there is no failure.
func (r promotionResult) availability() conditions.Condition {
	if failures := r.report.Failures; len(failures) > 0 {
		message := fmt.Sprintf("The promotion Job %s left %s in the outbox, and the first failure is %s: %s",
			r.job, frames(r.report.Left), failures[0].File, failures[0].Message)
		return archiveAvailable(conditions.False, failures[0].Reason, message)
	}
	message := fmt.Sprintf("The promotion Job %s moved %s to the archive", r.job, frames(r.report.Promoted))
	return archiveAvailable(conditions.True, astrophotography.ReasonPromoted, message)
}

func frames(n int32) string {
	if n == 1 {
		return "1 frame"
	}
	return fmt.Sprintf("%d frames", n)
}

func archiveAvailable(status conditions.Status, reason, message string) conditions.Condition {
	return conditions.Condition{Type: astrophotography.ConditionArchiveAvailable, Status: status, Reason: reason, Message: message}
}

// setCondition sets a condition on the Target and appends it to changed
// when it transitioned.
func setCondition(t *astrophotography.Target, changed []conditions.Condition, next conditions.Condition) []conditions.Condition {
	next.ObservedGeneration = t.Metadata.Generation
	if conditions.Set(&t.Status.Conditions, next) {
		changed = append(changed, next)
	}
	return changed
}

// completeness returns the Complete condition, and sets status.progress,
// a printer column, to the archived frames over the wanted frames. A
// sequence's archived frames beyond its count do not count toward
// another sequence.
func completeness(t *astrophotography.Target) conditions.Condition {
	var archived, wanted int32
	for _, s := range t.Status.Sequences {
		archived += min(s.Archived, s.Count)
		wanted += s.Count
	}
	t.Status.Progress = fmt.Sprintf("%d/%d", archived, wanted)
	verb := "are"
	if archived == 1 {
		verb = "is"
	}
	message := fmt.Sprintf("%s of %d %s in the archive", frames(archived), wanted, verb)
	if archived < wanted || wanted == 0 {
		return conditions.Condition{Type: astrophotography.ConditionComplete, Status: conditions.False,
			Reason: astrophotography.ReasonWorkLeft, Message: message}
	}
	return conditions.Condition{Type: astrophotography.ConditionComplete, Status: conditions.True,
		Reason: astrophotography.ReasonArchived, Message: message}
}

// sequenceProgress returns the progress of each sequence in the spec.
// A sequence starts with the counts of the status entry that has the
// same settings, so an edit of the list or of a count keeps the counts
// of the sequences that remain. Each total counts toward the first
// sequence that it matches. A truncated report can leave out a
// sequence's total, so after a truncated report, a sequence that no
// total matches keeps its archived count.
func sequenceProgress(spec []astrophotography.Sequence, stored []astrophotography.SequenceProgress,
	totals []catalog.Total, truncated bool) []astrophotography.SequenceProgress {
	out := make([]astrophotography.SequenceProgress, len(spec))
	for i, s := range spec {
		out[i].Sequence = s
		if j := slices.IndexFunc(stored, func(p astrophotography.SequenceProgress) bool { return sameSettings(p.Sequence, s) }); j >= 0 {
			out[i].Archived, out[i].Pending = stored[j].Archived, stored[j].Pending
		}
	}
	counted := make([]int32, len(spec))
	matched := make([]bool, len(spec))
	for _, total := range totals {
		if i := slices.IndexFunc(spec, func(s astrophotography.Sequence) bool { return countsToward(s, total.Settings) }); i >= 0 {
			counted[i] += total.Frames
			matched[i] = true
		}
	}
	for i := range out {
		if truncated && !matched[i] {
			continue
		}
		out[i].Pending = max(0, out[i].Pending-max(0, counted[i]-out[i].Archived))
		out[i].Archived = counted[i]
	}
	return out
}

// countsToward reports whether a frame with these settings counts
// toward the sequence: it has the same filter, exposure, binning, gain,
// and offset. A field that the sequence leaves out matches any value,
// because the camera used its own value for it.
func countsToward(s astrophotography.Sequence, frame catalog.Settings) bool {
	exposure, err := s.ExposureDuration()
	return err == nil &&
		(s.Filter == "" || s.Filter == frame.Filter) &&
		math.Abs(exposure.Seconds()-frame.ExposureSeconds) < 0.0005 &&
		(s.Binning == nil || *s.Binning == frame.Binning) &&
		optionalMatches(s.Gain, frame.Gain) && optionalMatches(s.Offset, frame.Offset)
}

func optionalMatches(want *int32, got *float64) bool {
	return want == nil || (got != nil && float64(*want) == *got)
}

// sameSettings compares two sequences without their counts.
func sameSettings(a, b astrophotography.Sequence) bool {
	return a.Filter == b.Filter && a.Exposure == b.Exposure &&
		sameOptional(a.Binning, b.Binning) && sameOptional(a.Gain, b.Gain) && sameOptional(a.Offset, b.Offset)
}

func sameOptional(a, b *int32) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}
