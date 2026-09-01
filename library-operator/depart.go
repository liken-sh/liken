package main

// This file is plan 21: a deleted Library takes its rows with it.
// The catalog replicates to every agent in the namespace, and each
// scanner prunes only its own library, so a deleted Library's items,
// files, links, and aliases would stay in every surviving copy
// forever. The operator holds a finalizer on every Library, and the
// deletion window the finalizer opens is where a cleanup pod deletes
// those rows and replication carries the deletes to every peer.
//
// The departure is a ladder, read from the top on every pass. A
// namespace with no survivor releases at once, because no copy of
// the catalog outlives the Library. The scanner stops before the
// sweep, so nothing rewrites the rows while they go. The finalizer
// goes only when every survivor's own report says the departed
// library's rows are out of its catalog.
//
// A finalizer's classic cost is an object stuck deleting forever.
// The operator never gives up on a timer: while something blocks the
// departure, it retries and reports the blocker in the Departing
// condition. The two states where a cleanup pod could never run, the
// last Library in a namespace and a namespace that is itself
// deleting, both land in the no-survivor rule and release at once.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// departure is what one pass decided about a deleting Library:
// whether the finalizer may go, and while it may not, the reason and
// message the Departing condition reports.
type departure struct {
	clear   bool
	reason  string
	message string
}

// depart runs one pass over a deleting Library. A Library that does
// not hold this operator's finalizer is the API server's to remove,
// and the pass leaves it alone.
func (o *operator) depart(ctx context.Context, library *Library, survivors []string) error {
	if !library.Metadata.holds(libraryFinalizer) {
		return nil
	}

	stage, err := o.departureStage(ctx, library, survivors)
	if err != nil {
		return err
	}
	if stage.clear {
		return o.releaseLibrary(ctx, library)
	}
	return writeLibraryStatus(ctx, o.client, library,
		departingStatus(library, stage, time.Now().UTC()))
}

// departureStage reads every release rule before it acts, so a
// departure that is already complete stands nothing, and a release
// that failed repeats on the next pass without more churn.
func (o *operator) departureStage(ctx context.Context, library *Library, survivors []string) (departure, error) {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name

	// No survivor means no copy of the catalog outlives this Library,
	// so there is nothing to sweep. This one rule also answers a
	// namespace under deletion, where every Library is deleting and
	// no new pod can start.
	if len(survivors) == 0 {
		return departure{clear: true}, nil
	}

	// A Library with no catalog claim never stood an agent, so it
	// wrote no rows and there is nothing to sweep.
	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, scannerCatalogClaimName(name))
	if errors.Is(err, ErrNotFound) {
		return departure{clear: true}, nil
	}
	if err != nil {
		return departure{}, err
	}

	// The release rule: every survivor is online and no survivor's
	// report names this library. An offline survivor holds the
	// release, and that is correct, because its copy of the catalog
	// still carries the rows and sheds them only after it returns
	// and syncs.
	holder := o.survivorHoldingRows(namespace, name, survivors)
	if holder == "" {
		return departure{clear: true}, nil
	}

	// The scanner goes before the sweep, because it rewrites the rows
	// the sweep deletes, and because it holds the ReadWriteOnce
	// catalog claim the cleanup pod needs.
	stopped, err := o.scannerStopped(ctx, library)
	if err != nil {
		return departure{}, err
	}
	if !stopped {
		return departure{
			reason:  reasonStoppingScanner,
			message: "the scanner pod is stopping, so nothing writes the catalog during the sweep",
		}, nil
	}

	pod, err := o.standCleanupPod(ctx, library)
	if err != nil {
		return departure{}, err
	}
	if blocker := cleanupBlocker(pod); blocker != "" {
		return departure{reason: reasonBlocked, message: blocker}, nil
	}
	if pod == nil || !everyContainerReady(pod) {
		return departure{
			reason:  reasonSweeping,
			message: "the cleanup pod is deleting this library's rows from the catalog",
		}, nil
	}
	return departure{reason: reasonAwaitingSurvivor, message: holder}, nil
}

// survivorHoldingRows names the first survivor whose catalog still
// holds the departed library's rows, and why, or answers empty when
// none does. The survivors arrive sorted, so a blocked departure
// names the same survivor on every pass and the status write settles
// instead of flapping between messages.
func (o *operator) survivorHoldingRows(namespace, departed string, survivors []string) string {
	key := libraryKey(namespace, departed)
	for _, survivor := range survivors {
		if !o.reports.onlineFor(namespace, survivor) {
			return fmt.Sprintf("the scanner of %s is offline, so its catalog still holds %s", survivor, key)
		}
		report := o.reports.latestFor(namespace, survivor)
		if report == nil {
			return fmt.Sprintf("%s has not reported which libraries its catalog holds", survivor)
		}
		if slices.Contains(report.CatalogLibraries, key) {
			return fmt.Sprintf("the catalog of %s still holds %s", survivor, key)
		}
	}
	return ""
}

// scannerStopped deletes the scanner pod and reports whether it is
// gone. A pod with a deletion timestamp counts as still present, the
// rule the reconcile pass follows, so one departure sends one delete
// and not one delete per pass.
func (o *operator) scannerStopped(ctx context.Context, library *Library) (bool, error) {
	namespace, name := library.Metadata.Namespace, scannerPodName(library.Metadata.Name)

	live, err := GetPod(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if live.Metadata.DeletionTimestamp != "" {
		return false, nil
	}
	return false, DeletePod(ctx, o.client, namespace, name)
}

// cleanupBlocker answers with the sentence a person has to act on: a
// cleanup pod that failed, or one the scheduler cannot place. Both
// answers quote the cluster's own words, because the remedy is out
// there and not in this operator.
func cleanupBlocker(pod *Pod) string {
	if pod == nil {
		return ""
	}
	if pod.Status.Phase == podFailed {
		return "the cleanup pod failed: " + podFailureReason(pod)
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == podScheduled && condition.Status == conditionIsFalse && condition.Message != "" {
			return "the cleanup pod cannot be scheduled: " + condition.Message
		}
	}
	return ""
}

// podFailureReason prefers the kubelet's sentence, then its one-word
// reason, then the bare fact that the pod failed with no reason
// given.
func podFailureReason(pod *Pod) string {
	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	if pod.Status.Reason != "" {
		return pod.Status.Reason
	}
	return "the kubelet gave no reason"
}

// releaseLibrary lets a swept Library go: it retires the cleanup
// pod, drops the library's retained messages from the bus, and takes
// the finalizer off last, so the act that releases the object is the
// final one. The garbage collector then takes the catalog claim and
// the webhook Service with the Library.
func (o *operator) releaseLibrary(ctx context.Context, library *Library) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name

	if err := DeletePod(ctx, o.client, namespace, cleanupPodName(name)); err != nil {
		return err
	}
	o.clearLibraryTopics(namespace, name)

	_, err := PatchLibraryFinalizers(ctx, o.client, namespace, name,
		library.Metadata.ResourceVersion, library.Metadata.without(libraryFinalizer))
	if errors.Is(err, ErrNotFound) {
		// An object that is already gone is the state this release
		// was for.
		return nil
	}
	if errors.Is(err, ErrConflict) {
		// A write between the list and this patch wakes the libraries
		// watch, and the next pass releases again.
		return nil
	}
	if err != nil {
		return err
	}
	delete(o.cleanupStands, libraryKey(namespace, name))
	return nil
}

// clearLibraryTopics publishes an empty retained payload on the
// departed library's two topics, which is how MQTT drops a retained
// message, so a subscriber that arrives later reads nothing for a
// Library that is gone.
func (o *operator) clearLibraryTopics(namespace, name string) {
	o.bus.Publish(libraryStatusTopic(o.topicBase, namespace, name), nil, true)
	o.bus.Publish(libraryAvailabilityTopic(o.topicBase, namespace, name), nil, true)
}

// departingStatus keeps the counts and the volume as the last true
// observation of the library. Only the phase and the Departing
// condition change, and they say how far the teardown reached.
func departingStatus(library *Library, stage departure, now time.Time) LibraryStatus {
	status := library.Status
	status.Phase = phaseDeparting
	status.Conditions = SetCondition(slices.Clone(library.Status.Conditions), Condition{
		Type:               conditionDeparting,
		Status:             ConditionTrue,
		ObservedGeneration: library.Metadata.Generation,
		Reason:             stage.reason,
		Message:            stage.message,
	}, now)
	return status
}

// survivingLibraries groups the Libraries that are not deleting by
// namespace, because a catalog is one namespace's and no other
// namespace holds its rows. The names are sorted so every reader of
// one namespace's survivors reads them in one order.
func survivingLibraries(libraries []Library) map[string][]string {
	byNamespace := map[string][]string{}
	for i := range libraries {
		library := &libraries[i]
		if library.Metadata.deleting() {
			continue
		}
		namespace := library.Metadata.Namespace
		byNamespace[namespace] = append(byNamespace[namespace], library.Metadata.Name)
	}
	for namespace := range byNamespace {
		slices.Sort(byNamespace[namespace])
	}
	return byNamespace
}
