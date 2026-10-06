package main

// The Events this operator posts, for a person who runs kubectl describe
// on a Library, a Catalog, or a MetadataProvider. A condition holds only
// its last transition, so without an Event the history of a Job that
// failed, a provider that refused its key, or a copy the heal deleted is
// only in the operator's log.
//
// Each condition transition posts one Event with the condition's reason
// and message, so kubectl get -o yaml and kubectl describe show the same
// words. The condition reasons are declared beside the conditions
// (api.go, namespacecatalog.go, metadataprovider.go, providercheck.go,
// imdbcheck.go), and warningReasons below names the ones that post a
// Warning. Three actions that change no condition post an Event of
// their own, with the reasons declared here: a Job created, a walk that
// ended, and a stranded copy deleted. Nothing else posts one: a pass, a
// provider call, and a webhook go to the log and the metrics.
//
// Only the operator binary posts Events. The pod build creates no
// recorder, so its operator field stays nil, and a nil recorder posts
// nothing.

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// The component name every Event carries in source.component and
// reportingComponent.
const eventComponent = "library-operator"

// The reasons of the actions that change no condition.
const (
	// The pass created a Job of a Library: a walk, a walk of the folders a
	// webhook named, or the phases after a walk. Normal.
	reasonJobCreated = "JobCreated"
	// The reporter published a walk that finished. Normal.
	reasonScanCompleted = "ScanCompleted"
	// The reporter published a walk that ended with a failure. Warning.
	reasonScanFailed = "ScanFailed"
	// The heal deleted a durable copy of a store, and its claim, from a
	// node that stayed NotReady. Warning, because a node is down.
	reasonStoreCopyHealed = "StoreCopyHealed"
)

// The condition reasons that post a Warning, because a person must act:
// edit a spec, create an object, or fix a node, a claim, or a key. Every
// other reason is Normal, because it is a healthy verdict or a state the
// operator waits through on its own, such as a claim that is not bound
// yet or a reporter that has not reported yet.
var warningReasons = map[string]bool{
	// Library: Bound.
	reasonClaimNotFound:  true,
	reasonVolumeNotFound: true,
	// Library and Catalog: Ready.
	reasonManyCatalogs:    true,
	reasonScheduleInvalid: true,
	reasonJobNotStarted:   true,
	reasonJobFailed:       true,
	// Library: Sources and GPUClaimTemplates.
	reasonProviderNotFound:      true,
	reasonProviderNotReady:      true,
	reasonFactNotServed:         true,
	reasonClaimTemplateNotFound: true,
	// Library: Departing.
	reasonBlocked: true,
	// Catalog: Ready.
	catalogReasonClassNotPerNode: true,
	catalogReasonPodFailed:       true,
	// MetadataProvider: Ready and Cached.
	reasonNoSecret:     true,
	reasonRefused:      true,
	reasonLimitReached: true,
	reasonUnreachable:  true,
	reasonUnavailable:  true,
	reasonClaimFailed:  true,
}

// badStatus answers the status of a condition that posts a Warning, in
// the form events.Recorder.Transition reads: the condition's own status
// for a Warning reason, and none for a Normal one.
func badStatus(c Condition) conditions.Status {
	if warningReasons[c.Reason] {
		return c.Status
	}
	return ""
}

// postTransitions posts one Event for each condition of after that is new
// beside before, or whose status or reason changed. The caller calls it
// after the API server takes the status that holds after, so a refused
// write posts nothing, and the next pass finds the same transition again.
// A condition that after leaves out posts nothing.
func postTransitions(recorder *events.Recorder, object events.ObjectReference, before, after []Condition) {
	for _, next := range after {
		held, found := conditions.Find(before, next.Type)
		if found && held.Status == next.Status && held.Reason == next.Reason {
			continue
		}
		recorder.Transition(object, next, badStatus(next))
	}
}

// postWalkEnd posts the end of a Library's walk once: on the status write
// whose scan run has a finish time that the status before it did not
// hold. The walk runs in a Job and writes its end to the catalog, and the
// operator reads it from the reporter's run, so this write is the first
// point at which the operator holds it.
func postWalkEnd(recorder *events.Recorder, object events.ObjectReference, before, after LibraryStatus) {
	run, held := statusRunOf(after.Runs, workerScan)
	if !held || run.Finished.IsZero() {
		return
	}
	if earlier, held := statusRunOf(before.Runs, workerScan); held &&
		earlier.Job == run.Job && earlier.Finished.Equal(run.Finished) {
		return
	}
	if run.Failure != "" {
		recorder.Warning(object, reasonScanFailed,
			fmt.Sprintf("the walk of the Job %s failed: %s", run.Job, run.Failure))
		return
	}
	recorder.Normal(object, reasonScanCompleted,
		fmt.Sprintf("the walk of the Job %s found %d titles in %d files", run.Job, after.Titles, after.Files))
}

// statusRunOf answers the run of one worker in a status, and false when
// the status holds none.
func statusRunOf(runs []libraryStatusRun, worker string) (libraryStatusRun, bool) {
	for _, run := range runs {
		if run.Worker == worker {
			return run, true
		}
	}
	return libraryStatusRun{}, false
}

// postJobCreated posts the Job the pass created for a Library, with what
// it does and why, the words of the log line.
func postJobCreated(recorder *events.Recorder, library *Library, job string, plan libraryJob) {
	recorder.Normal(libraryReference(library), reasonJobCreated,
		fmt.Sprintf("created the Job %s, %s, because %s", job, plan.described(), plan.cause))
}

// postStoreCopyHealed posts the heal of a stranded copy on the Catalog
// that owns the copy. The pod is gone once the heal deletes it, and the
// Catalog is the object a person reads for the namespace's stores. A pod
// with no Catalog owner posts nothing; the log line still names it.
func postStoreCopyHealed(recorder *events.Recorder, pod *Pod, message string) {
	for _, owner := range pod.Metadata.OwnerReferences {
		if owner.Kind != "Catalog" {
			continue
		}
		recorder.Warning(events.ObjectReference{
			APIVersion: owner.APIVersion,
			Kind:       owner.Kind,
			Namespace:  pod.Metadata.Namespace,
			Name:       owner.Name,
			UID:        owner.UID,
		}, reasonStoreCopyHealed, message)
		return
	}
}

// The references the Events of the three kinds name.

func libraryReference(library *Library) events.ObjectReference {
	return objectReference("Library", library.Metadata)
}

func catalogReference(catalog *NamespaceCatalog) events.ObjectReference {
	return objectReference("Catalog", catalog.Metadata)
}

func metadataProviderReference(provider *MetadataProvider) events.ObjectReference {
	return objectReference(kindMetadataProvider, provider.Metadata)
}

func objectReference(kind string, meta ObjectMeta) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: libraryAPIVersion,
		Kind:       kind,
		Namespace:  meta.Namespace,
		Name:       meta.Name,
		UID:        meta.UID,
	}
}
