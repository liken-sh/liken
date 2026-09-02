package main

// A deleted Library takes its rows with it. The catalog
// replicates to every agent in the namespace, and each scan prunes only
// its own library, so a deleted Library's items, files, links, and
// aliases would stay in the standing catalog forever. The operator
// holds a finalizer on every Library, and the deletion window the
// finalizer opens is where a cleanup Job deletes those rows and
// replication carries the deletes to the catalog pod.
//
// The departure is a ladder, read from the top on every pass. The
// schedule goes first, then the departure waits out any scan that is
// still running, because a scan rewrites the rows the sweep deletes and
// holds the ReadWriteOnce claim the cleanup Job needs. The finalizer
// goes only when the cleanup Job exited zero and the reporter echoed
// that same Job back over the bus.
//
// A finalizer's classic cost is an object stuck deleting forever. The
// operator never gives up on a timer: while something blocks the
// departure, it retries and reports the blocker in the Departing
// condition. A namespace with no Catalog releases at once, because
// nothing there holds the rows any more, and that one rule also answers
// a namespace that is itself being deleted.

import (
	"context"
	"errors"
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
func (o *operator) depart(ctx context.Context, library *Library, choice catalogChoice, jobs []Job) error {
	if !library.Metadata.holds(libraryFinalizer) && !library.Metadata.holds(formerLibraryFinalizer) {
		return nil
	}

	stage, err := o.departureStage(ctx, library, choice, jobs)
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
func (o *operator) departureStage(ctx context.Context, library *Library, choice catalogChoice, jobs []Job) (departure, error) {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name

	// A namespace with no Catalog holds no catalog to sweep, so there is
	// nothing to delete and nothing to wait for. This one rule also
	// answers a namespace under deletion, where the Catalog goes with
	// everything else and no new Job can start.
	if choice.catalog == nil {
		if choice.reason == reasonNoCatalog {
			return departure{clear: true}, nil
		}
		// More than one Catalog: the sweep cannot tell which cluster it
		// would be deleting from, so the departure waits for a person.
		return departure{reason: reasonBlocked, message: choice.message}, nil
	}

	// The schedule goes first, so no new walk starts behind the sweep.
	if err := o.stopScanCronJob(ctx, library); err != nil {
		return departure{}, err
	}

	// A scan that is still running rewrites the rows the sweep deletes,
	// and it holds the ReadWriteOnce claim the cleanup Job needs.
	if scanRunning(jobs, namespace, name) {
		return departure{
			reason:  reasonScanRunning,
			message: "a scan job of this library is still running",
		}, nil
	}

	blocker, err := o.standDepartureClaim(ctx, library, choice)
	if err != nil {
		return departure{}, err
	}
	if blocker != "" {
		return departure{reason: reasonBlocked, message: blocker}, nil
	}

	job, err := o.standCleanupJob(ctx, library, jobs)
	if err != nil {
		return departure{}, err
	}
	if blocker := cleanupBlocker(job); blocker != "" {
		return departure{reason: reasonBlocked, message: blocker}, nil
	}
	if cleanupComplete(job, o.reports.latestFor(namespace, name)) {
		return departure{clear: true}, nil
	}
	if job != nil && job.Status.Succeeded > 0 {
		return departure{
			reason:  reasonAwaitingEcho,
			message: "the sweep is done and the namespace's reporter has not echoed it yet",
		}, nil
	}
	return departure{
		reason:  reasonSweeping,
		message: "the cleanup job is deleting this library's rows from the catalog",
	}, nil
}

// standDepartureClaim gives the cleanup Job a volume to mount, and
// answers with the sentence a person has to act on, or empty when
// the claim stands. A fresh empty claim is enough: the agent joins
// the namespace's cluster, the rows arrive over gossip, and the sweep
// deletes what has arrived, so the release still waits on the
// reporter's own echo.
func (o *operator) standDepartureClaim(ctx context.Context, library *Library, choice catalogChoice) (string, error) {
	namespace := library.Metadata.Namespace
	name := scannerCatalogClaimName(library.Metadata.Name)

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	return "", o.standCatalogClaim(ctx, library, choice.catalog)
}

// releaseLibrary lets a swept Library go: it retires the cleanup Job,
// drops the library's retained messages from the bus, and takes the
// finalizer off last, so the act that releases the object is the final
// one. The garbage collector then takes the catalog claim and the
// CronJob with the Library.
func (o *operator) releaseLibrary(ctx context.Context, library *Library) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name

	if err := o.retireCleanupJob(ctx, namespace, name); err != nil {
		return err
	}
	o.clearLibraryTopics(namespace, name)

	_, err := PatchLibraryFinalizers(ctx, o.client, namespace, name,
		library.Metadata.ResourceVersion,
		library.Metadata.without(libraryFinalizer, formerLibraryFinalizer))
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
