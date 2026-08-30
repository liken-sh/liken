package main

// Every status this operator writes comes from the one derivation in
// this file, and the derivation reads nothing but its arguments. The
// shape matters because the loop is level-triggered: a pass must reach
// the same status from the same facts, whatever order the events
// arrived in, and a function of its arguments cannot do otherwise.

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// deriveLibraryStatus builds the whole status of one Library. Each
// argument is one authority: the binding is what the API server says
// about the storage, the pod is what the kubelet says about the
// scanner, and the report is what the scanner itself says about the
// volume. A nil pod is a library with no scanner, and a nil report is
// a scanner that has said nothing yet.
func deriveLibraryStatus(library *Library, bound binding, pod *Pod, latest *libraryReport, now time.Time) LibraryStatus {
	status := LibraryStatus{Volume: bound.volume}
	if pod != nil {
		status.Pod = pod.Metadata.Name
	}

	// The counts and the times are the scanner's, carried through as
	// it reported them. A Library with no report keeps zeroes, and the
	// Ready condition says why.
	if latest != nil {
		status.Titles = latest.Titles
		status.Unidentified = latest.Unidentified
		status.LastWalk = latest.LastWalk
		status.LastChange = latest.LastChange
	}

	// The conditions are built on a copy of the ones the Library
	// carries, because SetCondition writes in place and the caller
	// compares this status against the Library's own to decide whether
	// to write at all. Writing through the Library's slice would make
	// every status look unchanged.
	conditions := slices.Clone(library.Status.Conditions)
	generation := library.Metadata.Generation
	conditions = SetCondition(conditions, boundCondition(bound, generation), now)
	conditions = SetCondition(conditions, readyCondition(bound, pod, latest, generation), now)
	status.Conditions = conditions
	return status
}

// boundCondition reports the storage. The volume is the whole verdict:
// the binding carries one only when the claim exists, is bound, and
// its PersistentVolume was read.
func boundCondition(bound binding, generation int64) Condition {
	status := ConditionFalse
	if bound.volume != nil {
		status = ConditionTrue
	}
	return Condition{
		Type:               conditionBound,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             bound.reason,
		Message:            bound.message,
	}
}

// readyCondition reports whether this library is being scanned. Ready
// is the whole path working: the storage is bound, the pod runs with
// every container ready, and a report has arrived over the bus. Each
// reason names the step that has not happened, so the condition says
// where to look.
func readyCondition(bound binding, pod *Pod, latest *libraryReport, generation int64) Condition {
	condition := Condition{
		Type:               conditionReady,
		Status:             ConditionFalse,
		ObservedGeneration: generation,
	}
	switch {
	case bound.volume == nil:
		condition.Reason = reasonNotBound
		condition.Message = "the library's storage is not bound"
	case pod == nil:
		condition.Reason = reasonPodPending
		condition.Message = "there is no scanner pod yet"
	case pod.Status.Phase == podFailed:
		condition.Reason = reasonPodFailed
		condition.Message = podFailureMessage(pod)
	case pod.Status.Phase != podRunning || !everyContainerReady(pod):
		condition.Reason = reasonPodPending
		condition.Message = podPendingMessage(pod)
	case latest == nil:
		condition.Reason = reasonNoReport
		condition.Message = "the scanner has not reported yet"
	default:
		condition.Status = ConditionTrue
		condition.Reason = reasonReady
		condition.Message = fmt.Sprintf("the scanner reports %d titles", latest.Titles)
	}
	return condition
}

// everyContainerReady reports whether the kubelet marks every
// container in the pod ready. A pod the kubelet has said nothing about
// is not ready: an empty list is a pod that is still starting, not a
// pod whose containers all passed.
func everyContainerReady(pod *Pod) bool {
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if !container.Ready {
			return false
		}
	}
	return true
}

// podPendingMessage prefers the kubelet's own words, because the
// ordinary hold is the volume: a pod whose claim no node can mount
// says so, and that sentence is the part a person acts on.
func podPendingMessage(pod *Pod) string {
	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	if pod.Status.Phase == podRunning {
		return "the scanner pod runs and not every container is ready"
	}
	return "the scanner pod has not started"
}

func podFailureMessage(pod *Pod) string {
	if pod.Status.Message != "" {
		return "the scanner pod failed: " + pod.Status.Message
	}
	if pod.Status.Reason != "" {
		return "the scanner pod failed: " + pod.Status.Reason
	}
	return "the scanner pod failed"
}

// writeLibraryStatus writes only a status that differs from the one
// the Library carries. Every write bumps the resourceVersion, and this
// operator watches its own collection, so a write on every pass would
// wake the watch that wakes the pass, and the backstop tick would
// become one write per library every ten seconds.
func writeLibraryStatus(c *Client, library *Library, desired LibraryStatus) error {
	same, err := sameStatus(library.Status, desired)
	if err != nil || same {
		return err
	}

	library.Status = desired
	_, err = PutLibraryStatus(c, library)
	if errors.Is(err, ErrConflict) {
		// Something wrote this Library between the list and this
		// write. That write bumped the resourceVersion, which wakes
		// this operator's own libraries watch, and the pass it wakes
		// reads the fresh copy and derives the status again.
		return nil
	}
	return err
}

// sameStatus compares the marshaled form, because that is what the API
// server stores and what each field's omitempty decides: two statuses
// that marshal alike write alike.
func sameStatus(current, desired LibraryStatus) (bool, error) {
	was, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	wants, err := json.Marshal(desired)
	if err != nil {
		return false, err
	}
	return string(was) == string(wants), nil
}
