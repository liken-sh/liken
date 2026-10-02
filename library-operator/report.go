package main

// The report desk is the boundary between the bus and the
// reconcile loop; the operator subscribes to every Library's status
// topic, the bus handler folds each message in here, and the reconcile
// pass reads the newest report per Library and writes it into that
// Library's status. The catalog pod holds no API credentials, so this
// desk is the only path a report takes to the control plane.

import (
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"
)

// What the namespace's reporter says about one library: how many
// titles the catalog holds, how many folders no .nfo file identified, when
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
	// ItemsByKind is the same Items count, broken out by table: movies,
	// series, episodes, and franchises. The operator's library_items
	// metric reads it, and status.items keeps the sum, because a person
	// reading kubectl wants the one number.
	ItemsByKind map[string]int `json:"itemsByKind,omitempty"`
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
	// One count per fact of the rows that fact has left to fill,
	// from gapQueries. The operator creates a Job that fills gaps with
	// the phases whose counts are above zero, so a fact with no key
	// here never runs in one.
	Gaps map[string]int `json:"gaps,omitempty"`
	// The part of each gap that is episodes, for a fact whose episodes only
	// some provider blocks answer. The operator takes it out of the gap for a
	// Library whose sources answer the fact with another block.
	EpisodeGaps map[string]int `json:"episodeGaps,omitempty"`
	// The oldest attempt this library holds for each fact. The operator
	// reads it against the Library's spec.refresh: a refresh later than
	// the oldest attempt is a fact with work left, whatever the gap
	// count says. The reporter counts each gap with the refresh times the
	// operator publishes, and a report built before those times reached
	// the reporter counts with none.
	OldestAttempts map[string]time.Time `json:"oldestAttempts,omitempty"`
	// Waiting is the titles whose identity ended in candidates for a
	// person to choose from, and Unresolved the titles no provider could
	// name. Both are folded into Library status.
	Waiting    int `json:"waiting"`
	Unresolved int `json:"unresolved"`
	// The count of titles a fact left because another writer holds the element
	// group it writes, with the .contributors/ entries a merge left because a
	// person edited one of them. The operator folds it into Library status.
	Fights int `json:"fights"`
	// The counts the Jobs raised inside containers that have exited. The
	// operator turns them into Prometheus counters.
	Tallies []libraryTally `json:"tallies,omitempty"`
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

// fold records the newest report for a Library, and wakes the loop when
// the report changes what the pass acts on. A report is a whole
// observation, so the newest one says everything an older one did.
//
// A report wakes the loop at once when it changes a decision the pass
// makes (actsOn): a run that starts or ends, a walk that starts or ends,
// a fact whose gap opens or closes, and an oldest attempt that moves.
// A report that changes only the counts the pass writes into status, such
// as the titles or a gap that stays open, wakes nothing. The desk holds
// it, and the next pass writes it: the backstop tick runs one within ten
// seconds. While a Job fills gaps, its gap counts change every few
// seconds, and a pass for each change ran the pass about thirty times a
// minute. A report that repeats the held one wakes nothing either: the
// reporter republishes every library it knows each time the catalog
// changes.
//
// It answers with the runs this report ended: a row that carries a finish
// the desk's report did not. A desk that held no report answers none, because
// the first report after a restart replays runs that ended long ago.
func (r *reports) fold(namespace, name string, report libraryReport) []libraryRun {
	key := libraryKey(namespace, name)
	r.mutex.Lock()
	before, held := r.latest[key]
	r.latest[key] = report
	r.mutex.Unlock()
	if !held || actsOn(before, report) {
		r.poke()
	}
	if !held {
		return nil
	}
	return endedRuns(before.Runs, report.Runs)
}

// actsOn reports whether a report changes something the pass decides on,
// and not only a count it writes into status.
func actsOn(before, after libraryReport) bool {
	if before.Walking != after.Walking {
		return true
	}
	if !reflect.DeepEqual(before.Runs, after.Runs) {
		return true
	}
	if !maps.Equal(before.OldestAttempts, after.OldestAttempts) {
		return true
	}
	return !maps.Equal(openGaps(before), openGaps(after))
}

// openGaps is the facts with rows left to fill, counted whole and with
// their episodes taken out, because the schedule reads a gap both ways
// (imdbratinggap.go). A count that stays above zero leaves the set as it
// was.
func openGaps(report libraryReport) map[string]bool {
	open := map[string]bool{}
	for fact, count := range report.Gaps {
		if count > 0 {
			open[fact] = true
		}
		if count-report.EpisodeGaps[fact] > 0 {
			open[fact+"/titles"] = true
		}
	}
	return open
}

// The runs that carry a finish the earlier runs did not, for the same Job. A
// Job writes its finished row twice, the second time with the write that
// made it, and the finish does not move, so the second write ends nothing.
func endedRuns(before, after []libraryRun) []libraryRun {
	var ended []libraryRun
	for _, run := range after {
		if run.Finished.IsZero() {
			continue
		}
		if was, ran := runOf(before, run.Worker); ran && was.Job == run.Job && was.Finished.Equal(run.Finished) {
			continue
		}
		ended = append(ended, run)
	}
	return ended
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
