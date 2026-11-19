// Package endedpods holds the rule that an operator follows for a pod
// that the kubelet ended: an eviction for pressure on the node, a
// rejection at admission such as OutOfcpu, a graceful node shutdown, or
// a preemption. No restart policy starts such a pod again, so the
// operator that owns it creates the next pod.
//
// A claim or spec.nodeName can pin the next pod to the node that ended
// the last one, and no taint keeps it away after a rejection at
// admission, so the kubelet can reject it again at once. So the next pod
// waits: FirstWait after the ended pod's creation when no earlier pod
// ended, doubled for each earlier pod that the kubelet ended, to
// MaxWait. The wait counts from the creation, so a pod that ran for
// longer than its wait is replaced at once, and only a pod that ends
// young waits.
//
// The count of earlier ended pods is in an annotation of the pod and
// not in memory, so a restarted operator waits as long as the one before
// it. Each operator names the annotation in its own API group, such as
// library.liken.sh/tries.
//
// Each operator decodes pods into its own types, so the rule reads a Pod
// of plain values that the operator fills from its own type.
package endedpods

import (
	"strconv"
	"strings"
	"time"
)

// The wait before the next pod: FirstWait for a pod with no count,
// doubled for each count, to MaxWait.
const (
	FirstWait = 10 * time.Second
	MaxWait   = 5 * time.Minute
)

// disruptionTarget is the condition that the kubelet and the eviction
// API write on a pod that they end. Its lastTransitionTime is when the
// pod stopped running.
const disruptionTarget = "DisruptionTarget"

// Pod is what the rule reads of a pod. A zero time means that the pod
// does not have the field.
type Pod struct {
	// Phase and Reason are status.phase and status.reason.
	Phase  string
	Reason string
	// Created is metadata.creationTimestamp.
	Created time.Time
	// Tries is the pod's count of earlier pods that the kubelet ended,
	// as Tries reads it.
	Tries int
	// Conditions are status.conditions.
	Conditions []Condition
	// Finished holds the finishedAt of each terminated container state
	// that the operator reads.
	Finished []time.Time
}

// Condition is a pod condition.
type Condition struct {
	Type               string
	Status             string
	LastTransitionTime time.Time
}

// Ended reports whether a pod with restartPolicy Always is in a final
// phase. The kubelet restarts a container of such a pod that exits, so
// only the kubelet puts the pod in Failed or Succeeded. A node shutdown
// leaves it Succeeded when its containers exit zero.
func Ended(phase string) bool {
	return phase == "Failed" || phase == "Succeeded"
}

// EndedByKubelet reports whether the kubelet ended a pod that runs to
// completion, for a cause outside the pod's program. The kubelet leaves
// such a pod Failed with a DisruptionTarget condition, a pod-level
// reason, or both. A container that exits with a status that is not
// zero leaves the pod-level reason empty, and DeadlineExceeded is the
// end of the pod's own activeDeadlineSeconds, so both are failures of
// the pod's program.
func (p Pod) EndedByKubelet() bool {
	if p.Phase != "Failed" {
		return false
	}
	_, disrupted := p.disruption()
	return disrupted || (p.Reason != "" && p.Reason != "DeadlineExceeded")
}

func (p Pod) disruption() (Condition, bool) {
	for _, c := range p.Conditions {
		if c.Type == disruptionTarget && c.Status == "True" {
			return c, true
		}
	}
	return Condition{}, false
}

// Tries returns the count in the annotation key. A pod with no count,
// or with a count that is not a number of zero or more, reads as zero.
func Tries(annotations map[string]string, key string) int {
	count, _ := strconv.Atoi(annotations[key])
	return max(count, 0)
}

// SetTries writes the count tries in the annotation key.
func SetTries(annotations map[string]string, key string, tries int) {
	annotations[key] = strconv.Itoa(tries)
}

// Wait returns the wait after a pod with tries earlier ended pods. The
// loop stops at the limit, so a large count cannot overflow the
// duration.
func Wait(tries int) time.Duration {
	wait := FirstWait
	for range tries {
		if wait >= MaxWait {
			break
		}
		wait *= 2
	}
	return min(wait, MaxWait)
}

// ReplaceAt returns the earliest time at which the next pod starts.
func (p Pod) ReplaceAt() time.Time {
	return p.Created.Add(Wait(p.Tries))
}

// EndedAt returns when the kubelet ended the pod: the lastTransitionTime
// of its DisruptionTarget condition, which an eviction and a node
// shutdown write, or else the latest finishedAt. A pod that the kubelet
// rejected at admission has neither, because it never ran.
func (p Pod) EndedAt() (time.Time, bool) {
	if c, disrupted := p.disruption(); disrupted && !c.LastTransitionTime.IsZero() {
		return c.LastTransitionTime, true
	}
	var last time.Time
	for _, finished := range p.Finished {
		if finished.After(last) {
			last = finished
		}
	}
	return last, !last.IsZero()
}

// RanFor returns how long the pod ran before the kubelet ended it, and
// zero for a pod that never ran. The pod's age when the operator
// replaces it is no measure: a pass runs a little after each wait, and
// the API server cuts each timestamp to whole seconds, so a pod that the
// kubelet rejected at once would read as older than its wait.
func (p Pod) RanFor() time.Duration {
	end, ran := p.EndedAt()
	if !ran {
		return 0
	}
	return max(end.Sub(p.Created), 0)
}

// FirstOfLoop reports whether the end of the pod starts a new loop of
// ends, so the operator posts an Event for it. A retry loop posts its
// first failure and not each retry. The loop starts at a pod with no
// count, and again at a pod that ran for longer than MaxWait, whose end
// has a cause of its own. Once the wait is at its limit, each pod of a
// loop of rejections is older than MaxWait when its turn comes, so the
// rule reads how long the pod ran and not its age.
func (p Pod) FirstOfLoop() bool {
	return p.Tries == 0 || p.RanFor() > MaxWait
}

// Words returns the kubelet's words for why it ended a pod: the reason,
// such as Evicted, and the message, joined by ": ", or the one of them
// that is not empty, or "" when both are empty. The kubelet ends a
// message with a period, and an eviction's message with a period and a
// space. An operator puts the words inside a sentence of its own, so
// Words removes the white space around each part and then one period
// at its end. An operator that writes the reason in a sentence of its
// own passes an empty reason, and gets the message alone.
func Words(reason, message string) string {
	var parts []string
	for _, part := range []string{reason, message} {
		if part = strings.TrimSuffix(strings.TrimSpace(part), "."); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ": ")
}
