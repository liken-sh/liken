package main

// Every status this operator writes comes from the one derivation in
// this file, and the derivation reads nothing but its arguments. The
// shape matters because the loop is level-triggered: a pass must reach
// the same status from the same facts, whatever order the events
// arrived in, and a function of its arguments cannot do otherwise.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Everything one pass observed about one Library, gathered so
// the derivation stays one function of its arguments: what the API
// server says about the storage, which Catalog the namespace resolved
// to and how its pod is doing, whether the schedule stands, what the
// namespace's reporter last said about this library, whether that
// reporter is on the bus, and the namespace the operator's own Service
// is in.
type libraryObservation struct {
	bound             binding
	choice            catalogChoice
	cronJob           *CronJob
	report            *libraryReport
	online            bool
	operatorNamespace string
	// What the Library's ordered sources resolved to against the
	// MetadataProviders this pass checked. An empty reason is a Library that
	// names no source, and that Library carries no Sources condition.
	sources sourcesVerdict
}

// deriveLibraryStatus builds the whole status of one Library from one
// pass's observation and nothing else. A nil cronJob is a library whose
// schedule does not stand, and a nil report is one the reporter has
// said nothing about yet.
func deriveLibraryStatus(library *Library, seen libraryObservation, now time.Time) LibraryStatus {
	status := LibraryStatus{Volume: seen.bound.volume}

	// The webhook address names the operator's own Service and this
	// Library, so it holds for the whole life of the Library. It is
	// reported on the same condition the schedule is written on, so a
	// Library that is not being scanned reports no address to send an
	// import to.
	if libraryStands(seen.bound, seen.choice) {
		status.Webhook = webhookURL(seen.operatorNamespace,
			library.Metadata.Namespace, library.Metadata.Name)
	}

	// The counts, the times, and the runs are the reporter's, carried
	// through as it published them. A Library with no report keeps
	// zeroes, and the Ready condition says why.
	if latest := seen.report; latest != nil {
		status.Titles = latest.Titles
		status.Unidentified = latest.Unidentified
		status.Items = latest.Items
		status.Files = latest.Files
		status.RemovedLastSweep = latest.RemovedLastSweep
		status.LastWalk = latest.LastWalk
		status.LastChange = latest.LastChange
		status.Runs = latest.Runs
		// The gap counts and the two identity counts are the reporter's own,
		// carried through as it published them, so the number the operator
		// schedules on is the number a person reads.
		status.Gaps = latest.Gaps
		status.Waiting = latest.Waiting
		status.Unresolved = latest.Unresolved
		status.Fights = latest.Fights
	}

	// The conditions are built on a copy of the ones the Library
	// carries, because SetCondition writes in place and the caller
	// compares this status against the Library's own to decide whether
	// to write at all. Writing through the Library's slice would make
	// every status look unchanged.
	conditions := slices.Clone(library.Status.Conditions)
	generation := library.Metadata.Generation
	ready := readyCondition(seen, generation)
	conditions = SetCondition(conditions, boundCondition(seen.bound, generation), now)
	conditions = SetCondition(conditions, ready, now)
	// A Library that names no source carries no Sources condition at all,
	// because there is nothing to report about a list it does not have.
	if seen.sources.reason != "" {
		conditions = SetCondition(conditions, sourcesCondition(seen.sources, generation), now)
	}
	status.Conditions = conditions
	status.Phase = libraryPhase(ready, seen.report)
	return status
}

// LibraryPhase says what the library is doing, in the word a person reads in
// the status column. It reads the Ready condition this same derivation built,
// so the column and the condition never disagree. The phase is Offline when
// the reporter has left the bus, Pending while any other step of the path is
// missing, Scanning while the report says a walk runs, Enriching while the
// report carries an enrich run that has started and not finished, and Idle
// otherwise.
func libraryPhase(ready Condition, latest *libraryReport) string {
	switch {
	case ready.Reason == reasonOffline:
		return phaseOffline
	case ready.Status != ConditionTrue:
		return phasePending
	case latest != nil && latest.Walking:
		return phaseScanning
	case latest != nil && enrichInFlight(latest.Runs):
		return phaseEnriching
	default:
		return phaseIdle
	}
}

// Whether an enricher of this library is in flight, which the runs say: a row
// for the enrich worker with a start and no finish. The reporter derives
// Walking the same way from the scan row.
func enrichInFlight(runs []libraryRun) bool {
	run, held := runOf(runs, workerEnrich)
	return held && !run.Started.IsZero() && run.Finished.IsZero()
}

// The Sources condition reports the providers a Library names: True when
// every one exists and one of them serves the facts this library needs,
// and False with the reason that names what is wrong.
func sourcesCondition(verdict sourcesVerdict, generation int64) Condition {
	status := ConditionFalse
	if verdict.reason == reasonSourcesReady {
		status = ConditionTrue
	}
	return Condition{
		Type:               conditionSources,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             verdict.reason,
		Message:            verdict.message,
	}
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

// ReadyCondition reports whether this library is being scanned.
// Ready is the whole path working: the storage is bound, the namespace
// holds one Catalog whose pod runs with every container ready, the
// schedule stands, the reporter is on the bus, and it has reported this
// library. Each reason names the step that has not happened, so the
// condition says where to look.
func readyCondition(seen libraryObservation, generation int64) Condition {
	condition := Condition{
		Type:               conditionReady,
		Status:             ConditionFalse,
		ObservedGeneration: generation,
	}
	_, blocker := catalogPodBlocker(seen.choice.pod)
	switch {
	case seen.bound.volume == nil:
		condition.Reason = reasonNotBound
		condition.Message = "the library's storage is not bound"
	case seen.choice.catalog == nil:
		// The namespace has no single Catalog, so the Library waits. The
		// reason and message are the catalog choice's own, which name whether
		// the namespace has none or several.
		condition.Reason = seen.choice.reason
		condition.Message = seen.choice.message
	case blocker != "":
		condition.Reason = reasonCatalogPending
		condition.Message = blocker
	case seen.cronJob == nil:
		condition.Reason = reasonScanPending
		condition.Message = "the scan schedule does not stand yet"
	case !seen.online:
		condition.Reason = reasonOffline
		condition.Message = "the namespace's reporter is not on the bus"
	case seen.report == nil:
		condition.Reason = reasonNoReport
		condition.Message = "the reporter has not reported this library yet"
	default:
		condition.Status = ConditionTrue
		condition.Reason = reasonReady
		condition.Message = fmt.Sprintf("the reporter reports %d titles", seen.report.Titles)
	}
	return condition
}

// everyContainerReady reports whether the kubelet marks every
// container in the pod ready. A pod the kubelet has said nothing about
// is not ready: an empty list is a pod that is still starting, not a
// pod whose containers all passed.
//
// The catalog agent counts here as much as the container beside it, and
// the kubelet reports it under initContainerStatuses because it is a
// native sidecar. A pod whose agent has not opened its API is not up.
func everyContainerReady(pod *Pod) bool {
	if !containerReady(pod.Status.InitContainerStatuses, catalogContainer) {
		return false
	}
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

// containerReady reads one named container's readiness out of a status
// list. A container the kubelet has not reported is not ready.
func containerReady(statuses []ContainerStatus, name string) bool {
	for _, status := range statuses {
		if status.Name == name {
			return status.Ready
		}
	}
	return false
}

// podPendingMessage prefers the kubelet's own words, because the
// ordinary hold is the volume: a pod whose claim no node can mount
// says so, and that sentence is the part a person acts on.
func podPendingMessage(pod *Pod) string {
	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	if pod.Status.Phase == podRunning {
		return "the catalog pod runs and not every container is ready"
	}
	return "the catalog pod has not started"
}

func podFailureMessage(pod *Pod) string {
	if pod.Status.Message != "" {
		return "the catalog pod failed: " + pod.Status.Message
	}
	if pod.Status.Reason != "" {
		return "the catalog pod failed: " + pod.Status.Reason
	}
	return "the catalog pod failed"
}

// writeLibraryStatus writes only a status that differs from the one
// the Library carries. Every write bumps the resourceVersion, and this
// operator watches its own collection, so a write on every pass would
// wake the watch that wakes the pass, and the backstop tick would
// become one write per library every ten seconds.
func writeLibraryStatus(ctx context.Context, c *Client, library *Library, desired LibraryStatus) error {
	same, err := sameStatus(library.Status, desired)
	if err != nil || same {
		return err
	}

	library.Status = desired
	_, err = PutLibraryStatus(ctx, c, library)
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
func sameStatus[T any](current, desired T) (bool, error) {
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
