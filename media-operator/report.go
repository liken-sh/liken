package main

// The report desk is the boundary between the bus and the reconcile
// loop. The operator subscribes to every Play's status and availability
// topic, and the bus handler folds each message in here. The reconcile
// pass reads the newest report per Play and writes it into the Play's
// status. The playback pod holds no API credentials, so this desk is
// the only path a report takes to the control plane.

import (
	"strings"
	"sync"
)

// reports holds the newest report per run and the wake the loop reads.
// One mutex covers the maps, because the bus handler runs on the bus
// reader's goroutine and the loop runs on its own.
//
// seen holds every run the desk has taken any bus message for, online or
// offline. latest drops a run the moment it goes offline, so it cannot
// answer which runs still have a retained topic on the broker; seen can,
// and it is how the operator finds a deleted Play's gravestone to clear.
type reports struct {
	mutex  sync.Mutex
	latest map[string]playReport
	seen   map[string]bool
	wake   chan<- struct{}
}

func newReports(wake chan<- struct{}) *reports {
	return &reports{
		latest: map[string]playReport{},
		seen:   map[string]bool{},
		wake:   wake,
	}
}

// runKey is the one key shape for a run. Namespace and name identify a
// run everywhere in this operator, and one shape keeps the map and the
// pass in step.
func runKey(namespace, name string) string {
	return namespace + "/" + name
}

// splitRunKey reverses runKey. A namespace and a name hold no slash, so
// the first one separates the two.
func splitRunKey(key string) (namespace, name string) {
	namespace, name, _ = strings.Cut(key, "/")
	return namespace, name
}

// fold records the newest report for a run. A report is a whole
// observation, so the newest one says everything an older one did.
//
// A pause or an item change is what a person waits to see, so it wakes
// the loop at once. A position that only advances updates the desk and
// wakes nothing: the reconcile pass reads the current position off the
// desk on its next backstop tick, so a steadily playing film writes its
// resource on that interval and not on every report. This is the
// throttle that keeps a one-second bus cadence from becoming a
// one-second write to etcd.
func (r *reports) fold(namespace, name string, report playReport) {
	key := runKey(namespace, name)
	r.mutex.Lock()
	previous, had := r.latest[key]
	r.latest[key] = report
	r.seen[key] = true
	r.mutex.Unlock()
	if !had || report.Paused != previous.Paused || report.Item != previous.Item {
		poke(r.wake)
	}
}

// availability marks a Play online or offline. Either way the run is one
// the desk has now seen, so it joins seen. Offline also drops the latest
// report, because the retained status the broker still holds describes a
// pod that is gone, and a stale report must not read as a live Play; the
// wake lets the pass rewrite the status the drop changed.
func (r *reports) availability(namespace, name string, online bool) {
	key := runKey(namespace, name)
	r.mutex.Lock()
	r.seen[key] = true
	if !online {
		delete(r.latest, key)
	}
	r.mutex.Unlock()
	if !online {
		poke(r.wake)
	}
}

// latestFor returns the newest report, the only one kept, or nil when
// the desk holds none.
func (r *reports) latestFor(namespace, name string) *playReport {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	report, held := r.latest[runKey(namespace, name)]
	if !held {
		return nil
	}
	return &report
}

// retain drops the latest report of a deleted Play. The pass hands over
// the set of runs that still exist, and the map shrinks to match, so the
// desk never serves a report for a run the collection no longer holds.
// seen is left alone: the operator still needs it to find and clear the
// deleted Play's retained topics.
func (r *reports) retain(live map[string]bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for key := range r.latest {
		if !live[key] {
			delete(r.latest, key)
		}
	}
}

// stale returns the runs the desk has seen a bus message for that no
// longer exist. Each is a deleted Play whose retained status and
// availability the broker still holds, and which the operator clears
// after a grace period.
func (r *reports) stale(live map[string]bool) []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var gone []string
	for key := range r.seen {
		if !live[key] {
			gone = append(gone, key)
		}
	}
	return gone
}

// forget drops every trace of one run, after the operator has cleared
// its retained topics, so the desk does not offer it as stale again.
func (r *reports) forget(key string) {
	r.mutex.Lock()
	delete(r.latest, key)
	delete(r.seen, key)
	r.mutex.Unlock()
}
