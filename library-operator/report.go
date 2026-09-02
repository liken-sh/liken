package main

// The report desk is the boundary between the bus and the
// reconcile loop; the operator subscribes to every Library's status
// topic, the bus handler folds each message in here, and the reconcile
// pass reads the newest report per Library and writes it into that
// Library's status. The catalog pod holds no API credentials, so this
// desk is the only path a report takes to the control plane.

import (
	"slices"
	"sync"
	"time"
)

// What the namespace's reporter says about one library: how many
// titles the catalog holds, how many folders no sidecar identified, when
// the last walk ended and the last change landed, and the run of every
// worker. The reporter publishes it retained, so the broker holds the
// current counts for a subscriber that arrives later.
type libraryReport struct {
	Titles       int       `json:"titles"`
	Unidentified int       `json:"unidentified"`
	LastWalk     time.Time `json:"lastWalk"`
	LastChange   time.Time `json:"lastChange"`
	// Items and Files are the catalog's own counts after the last walk
	// pruned: the item rows and the file rows it holds for this library.
	// The operator folds them into Library status.
	Items int `json:"items"`
	Files int `json:"files"`
	// True while a scan Job runs, which the reporter reads off the
	// scan run whose start is later than its finish, so the operator's
	// phase follows the walk.
	Walking bool `json:"walking"`
	// The count of rows the last full sweep removed, so a mass delete that a
	// partial walk caused is visible on the bus without a shell. The operator
	// folds it into Library status.
	RemovedLastSweep int `json:"removedLastSweep"`
	// One entry per worker that has run against this library, sorted
	// by worker, each naming the Job that ran and what it left. A Job waits
	// for its own entry here before it exits.
	Runs []libraryRun `json:"runs,omitempty"`
}

// reports holds the newest report per Library and the wake the loop
// reads. One mutex covers the map, because the bus handler runs on
// the bus reader's goroutine and the loop runs on its own.
type reports struct {
	mutex  sync.Mutex
	latest map[string]libraryReport
	wake   chan<- struct{}
}

func newReports(wake chan<- struct{}) *reports {
	return &reports{
		latest: map[string]libraryReport{},
		wake:   wake,
	}
}

// libraryKey is the one key shape for a Library. Namespace and name
// identify a Library everywhere in this operator, and one shape keeps
// the desk and the reconcile pass in step.
func libraryKey(namespace, name string) string {
	return namespace + "/" + name
}

// fold records the newest report for a Library and wakes the loop. A
// report is a whole observation, so the newest one says everything an
// older one did.
//
// Every fold wakes the loop. A scanner publishes when it finishes a
// walk and when it applies a change, and neither happens often, so
// there is nothing here to throttle: a report that arrives is a report
// worth writing into the resource at once.
func (r *reports) fold(namespace, name string, report libraryReport) {
	key := libraryKey(namespace, name)
	r.mutex.Lock()
	r.latest[key] = report
	r.mutex.Unlock()
	r.poke()
}

// latestFor returns the newest report, the only one kept, or nil when
// the desk holds none. A Library with no report is one whose scanner
// has not finished a walk yet, and the operator says so in the
// Library's conditions.
func (r *reports) latestFor(namespace, name string) *libraryReport {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	report, held := r.latest[libraryKey(namespace, name)]
	if !held {
		return nil
	}
	return &report
}

// retain drops everything the desk holds for a deleted Library. The
// pass hands over the set of Libraries that still exist, and the map
// shrinks to match, so the desk never serves a report for a Library the
// collection no longer holds and a Library created later under the same
// name starts with none.
//
// retain answers with the keys it dropped, because desk state for a
// Library the collection does not hold is a retained message still
// standing on the bus, and the pass is the only reader that holds
// the whole Library list, so it is the one that clears those topics.
// The keys come back sorted, so a pass clears them in one order and
// a broker log reads the same way every time.
func (r *reports) retain(live map[string]bool) []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	keys := []string{}
	for key := range r.latest {
		if !live[key] {
			delete(r.latest, key)
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// poke never blocks, and the wake channel buffers exactly one. A wake
// already queued says everything a second one would say, because the
// pass that answers it reads the whole collection.
func (r *reports) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
