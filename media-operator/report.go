package main

// The report desk is the boundary between the bus and the reconcile
// loop. The operator subscribes to every Play's status and availability
// topic, and the bus handler folds each message in here. The reconcile
// pass reads the newest report per Play and writes it into the Play's
// status. The playback pod holds no API credentials, so this desk is
// the only path a report takes to the control plane.

import "sync"

// reports holds the newest report per run and the wake the loop reads.
// One mutex covers the map, because the bus handler runs on the bus
// reader's goroutine and the loop runs on its own.
type reports struct {
	mutex  sync.Mutex
	latest map[string]playReport
	wake   chan<- struct{}
}

func newReports(wake chan<- struct{}) *reports {
	return &reports{
		latest: map[string]playReport{},
		wake:   wake,
	}
}

// runKey is the one key shape for a run. Namespace and name identify a
// run everywhere in this operator, and one shape keeps the map and the
// pass in step.
func runKey(namespace, name string) string {
	return namespace + "/" + name
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
	r.mutex.Unlock()
	if !had || report.Paused != previous.Paused || report.Item != previous.Item {
		poke(r.wake)
	}
}

// availability marks a Play online or offline. A Play marked offline
// drops its latest report, because the retained status the broker still
// holds describes a pod that is gone, and a stale report must not read
// as a live Play. The wake lets the pass rewrite the status the drop
// changed.
func (r *reports) availability(namespace, name string, online bool) {
	if online {
		return
	}
	r.mutex.Lock()
	delete(r.latest, runKey(namespace, name))
	r.mutex.Unlock()
	poke(r.wake)
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

// retain drops what a deleted Play left behind. The pass hands over the
// set of runs that still exist, and the map shrinks to match, so the
// desk never grows past the collection it serves.
func (r *reports) retain(live map[string]bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for key := range r.latest {
		if !live[key] {
			delete(r.latest, key)
		}
	}
}
