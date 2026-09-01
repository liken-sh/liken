package main

// The report desk is the boundary between the bus and the reconcile
// loop. The operator subscribes to every Library's status and
// availability topic, and the bus handler folds each message in here.
// The reconcile pass reads the newest report per Library and writes it
// into that Library's status. A scanner pod holds no API credentials,
// so this desk is the only path a report takes to the control plane.

import (
	"sync"
	"time"
)

// libraryReport is what one scanner says about the volume it walks:
// how many titles it holds, how many folders the scanner could not
// identify, when it last walked the whole volume, and when it last
// applied a change. The scanner publishes it retained, so the broker
// holds the current counts for a subscriber that arrives later.
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
	// Walking is true while a walk runs. The scanner publishes the report
	// with it set as the walk starts and clear as the walk ends, so the
	// operator's phase follows the walk.
	Walking bool `json:"walking"`
	// The count of rows the last full sweep removed, so a mass delete that a
	// partial walk caused is visible on the bus without a shell. The operator
	// folds it into Library status.
	RemovedLastSweep int `json:"removedLastSweep"`
	// CatalogLibraries is the sorted set of libraries this scanner's
	// own catalog holds rows for, which is more than the one library
	// it walks: every agent in a namespace holds the whole
	// namespace's catalog. The operator reads this field to tell
	// when a departed library's rows have left every survivor's
	// copy, so this is the field that releases a finalizer.
	CatalogLibraries []string `json:"catalogLibraries,omitempty"`
}

// reports holds the newest report per Library and the wake the loop
// reads. One mutex covers the maps, because the bus handler runs on
// the bus reader's goroutine and the loop runs on its own.
type reports struct {
	mutex  sync.Mutex
	latest map[string]libraryReport
	online map[string]bool
	wake   chan<- struct{}
}

func newReports(wake chan<- struct{}) *reports {
	return &reports{
		latest: map[string]libraryReport{},
		online: map[string]bool{},
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

// availability marks a scanner online or offline. The report it
// published stands either way, because the counts describe the volume
// and not the pod that walked it: a library outlives its scanner, and
// the last walk's report is what the volume holds until the next walk
// replaces it.
//
// A change of the flag wakes the loop, so the pass that answers it
// reads the Library beside the pod that just arrived or left. A repeat
// of the flag wakes nothing, because a reconnecting scanner republishes
// online on every session and the pass has already run.
func (r *reports) availability(namespace, name string, online bool) {
	key := libraryKey(namespace, name)
	r.mutex.Lock()
	previous, had := r.online[key]
	r.online[key] = online
	r.mutex.Unlock()
	if !had || previous != online {
		r.poke()
	}
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

// onlineFor reports the last availability the bus carried for a Library.
// A Library the desk holds no availability for reads offline, which is
// the state before its scanner first connects.
func (r *reports) onlineFor(namespace, name string) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.online[libraryKey(namespace, name)]
}

// retain drops everything the desk holds for a deleted Library. The
// pass hands over the set of Libraries that still exist, and the maps
// shrink to match, so the desk never serves a report for a Library the
// collection no longer holds and a Library created later under the same
// name starts with none.
func (r *reports) retain(live map[string]bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for key := range r.latest {
		if !live[key] {
			delete(r.latest, key)
		}
	}
	for key := range r.online {
		if !live[key] {
			delete(r.online, key)
		}
	}
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
