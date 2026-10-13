package main

// What a promotion Job's report changes in its Target's status. Each
// write is composed from the copy it writes over, so it holds after a
// conflict, and a report is applied once: the Job's number goes into
// status.promotion.applied in the same write.
//
// The counts come from two writers. The runner adds one to a
// sequence's pending and to the outbox's pending after each exposure,
// and the report sets them again from what the Job found:
//
//   - A sequence's archived is the catalog's total for it, and its
//     pending drops by as many frames as archived rose. The catalog's
//     totals are of the whole file, so a report that the operator never
//     read is made up by the next one.
//   - An outbox's pending becomes the frames the Job left, plus the
//     frames counted after the Job was created. A frame that the runner
//     counted after the Job's start and that the Job moved anyway counts
//     once too often, and the next Job, which finds nothing, corrects it.
//     A frame is never counted too few times, so the finalizer on the
//     outbox claim never comes off while a frame is in it.

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

// promotionResult is what a finished promotion Job tells its Target.
type promotionResult struct {
	job    string
	number int32
	claim  string
	// pending is the claim's count of the target's frames when the Job
	// was created.
	pending int32
	// report is nil when the Job ended with no report.
	report *promote.Report
	// failure says why there is no report.
	failure string
}

// resultOf answers the result of a finished promotion Job, with the
// report from the termination message of the pod that succeeded.
//
// The watches of Jobs and pods deliver their changes apart, so a Job
// that succeeded can reach the store before its pod's last status. The
// result is then not ready, and the pod's change wakes the loop again.
// The loop waits only while the store holds the pod: a pod that is gone
// never changes again, so the loop runs the Job again (rerun in
// promotion.go). A Job that failed has no report to wait for.
func resultOf(j jobObject, number int32, succeeded bool, pods []podObject) (promotionResult, bool) {
	pending, _ := strconv.Atoi(j.Metadata.Annotations[annotationPending])
	result := promotionResult{job: j.Metadata.Name, number: number, claim: j.Metadata.Annotations[annotationClaim], pending: int32(pending)}
	if !succeeded {
		failed, _ := condition(j.Status.Conditions, "Failed", "True")
		result.failure = fmt.Sprintf("the promotion Job %s failed: %s", j.Metadata.Name, failed.Message)
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
				result.failure = fmt.Sprintf("the report of the promotion Job %s does not read: %v", j.Metadata.Name, err)
				return result, true
			}
			result.report = &report
			return result, true
		}
	}
	return result, false
}

// applied answers the number of the last Job whose report the status
// holds.
func applied(t *astrophotography.Target) int32 {
	if t.Status.Promotion == nil {
		return 0
	}
	return t.Status.Promotion.Applied
}

// apply composes the status after the Job, and answers the conditions
// that transitioned, or false when the status holds the Job already.
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
		held := &t.Status.Outboxes[entry]
		held.Pending = r.report.Left + max(0, held.Pending-r.pending)
		held.Left = r.report.Left
		if held.Pending == 0 {
			t.Status.Outboxes = slices.Delete(t.Status.Outboxes, entry, entry+1)
		}
	}
	changed = setCondition(t, changed, completeness(t))
	return setCondition(t, changed, r.availability()), true
}

// availability answers ArchiveAvailable after a report: False with the
// reason of the first failure, or True.
func (r promotionResult) availability() conditions.Condition {
	if failures := r.report.Failures; len(failures) > 0 {
		message := fmt.Sprintf("the promotion Job %s left %s in the outbox; the first failure is %s: %s",
			r.job, frames(r.report.Left), failures[0].File, failures[0].Message)
		return archiveAvailable(conditions.False, failures[0].Reason, message)
	}
	message := fmt.Sprintf("the promotion Job %s moved %s to the archive", r.job, frames(r.report.Promoted))
	return archiveAvailable(conditions.True, astrophotography.ReasonPromoted, message)
}

// frames counts frames for a message: "1 frame", "2 frames".
func frames(n int32) string {
	if n == 1 {
		return "1 frame"
	}
	return fmt.Sprintf("%d frames", n)
}

func archiveAvailable(status conditions.Status, reason, message string) conditions.Condition {
	return conditions.Condition{Type: astrophotography.ConditionArchiveAvailable, Status: status, Reason: reason, Message: message}
}

// setCondition sets a condition, and adds it to the list of
// transitions when it transitioned.
func setCondition(t *astrophotography.Target, changed []conditions.Condition, next conditions.Condition) []conditions.Condition {
	next.ObservedGeneration = t.Metadata.Generation
	if conditions.Set(&t.Status.Conditions, next) {
		changed = append(changed, next)
	}
	return changed
}

// completeness answers Complete, and sets the progress of the printer
// column: the frames archived of the frames wanted. A sequence's frames
// beyond its count do not count toward another's.
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

// sequenceProgress answers each sequence of the spec with its counts.
// A sequence keeps the counts of the status's entry with the same
// settings, so an edit of the list or of a count keeps the counts of
// the sequences that stay. Each total counts toward the first sequence it matches.
// When the report holds only the first totals, a sequence that matches
// none of them keeps its archived count.
func sequenceProgress(spec []astrophotography.Sequence, held []astrophotography.SequenceProgress,
	totals []catalog.Total, truncated bool) []astrophotography.SequenceProgress {
	out := make([]astrophotography.SequenceProgress, len(spec))
	for i, s := range spec {
		out[i].Sequence = s
		if j := slices.IndexFunc(held, func(p astrophotography.SequenceProgress) bool { return sameSettings(p.Sequence, s) }); j >= 0 {
			out[i].Archived, out[i].Pending = held[j].Archived, held[j].Pending
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

// countsToward reports whether a frame of these settings counts toward
// a sequence: the same filter, exposure, binning, gain, and offset. A
// field that the sequence leaves out takes the camera's own value, so
// it matches any value.
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

// sameSettings reports whether two sequences take the same frames,
// whatever their counts.
func sameSettings(a, b astrophotography.Sequence) bool {
	return a.Filter == b.Filter && a.Exposure == b.Exposure &&
		sameOptional(a.Binning, b.Binning) && sameOptional(a.Gain, b.Gain) && sameOptional(a.Offset, b.Offset)
}

func sameOptional(a, b *int32) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}
