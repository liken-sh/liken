package main

// The playback pods have no API credentials, so they publish their
// status on the bus. The bus handler stores each message here, and the
// reconcile loop reads the newest report of each Play from here and
// writes it to the Play's status.

import (
	"strings"
	"sync"
)

// reports stores the bus messages of each run, keyed by runKey. The bus
// handler and the reconcile loop run on different goroutines, so one
// mutex covers all the maps.
//
//   - latest is the newest report of each run. An offline message
//     deletes it.
//   - seen has every run that sent any bus message. Because offline
//     deletes from latest, only seen can find a deleted Play whose
//     retained topics are still on the broker.
//   - ended has the UID of the pod that reported the end of each run.
//     The sidecar publishes the end and then goes offline, which deletes
//     the report, but the pod keeps running for a few seconds and the
//     Player must show idle during that time. A mark counts only while
//     its pod is still the run's pod (runpod.go).
//   - pods tells the run's current pod from an older pod with the same
//     name.
type reports struct {
	mutex  sync.Mutex
	latest map[string]playReport
	ended  map[string]string
	seen   map[string]bool
	pods   runPods
	wake   chan<- struct{}
}

func newReports(wake chan<- struct{}) *reports {
	return &reports{
		latest: map[string]playReport{},
		ended:  map[string]string{},
		seen:   map[string]bool{},
		pods:   newRunPods(),
		wake:   wake,
	}
}

func runKey(namespace, name string) string {
	return namespace + "/" + name
}

// splitRunKey reverses runKey. Kubernetes names and namespaces contain
// no slash, so the first slash separates them.
func splitRunKey(key string) (namespace, name string) {
	namespace, name, _ = strings.Cut(key, "/")
	return namespace, name
}

// fold stores a run's newest report in place of the previous one.
//
// fold wakes the reconcile loop for a run's first report and for a
// report that changes Ended, Paused, or Item, because a person at the
// screen waits for those. A report that only advances the position
// wakes nothing, so the loop writes the position on its tick and not on
// every report.
//
// A report with Ended false clears the ended mark, so a Play recreated
// under the same name does not start ended.
//
// fold returns true when this report starts the run's end: Ended is set
// and the ended mark does not already hold this pod. The bus handler
// then publishes the Player's status at once. The check reads the ended
// mark and not the previous report, because the offline message that
// follows the end deletes the report, and an end that was handled once
// must not be handled again.
//
// fold drops a report from a pod that is not the run's pod. A report
// with no pod UID counts as the run's.
func (r *reports) fold(namespace, name string, report playReport) (endingBegan bool) {
	key := runKey(namespace, name)
	r.mutex.Lock()
	r.seen[key] = true
	if !r.pods.fromRunPod(namespace, name, report.Pod) {
		r.mutex.Unlock()
		return false
	}
	previous, had := r.latest[key]
	endedBy, wasEnded := r.ended[key]
	endingBegan = report.Ended && !(wasEnded && endedBy == report.Pod)
	r.latest[key] = report
	if report.Ended {
		r.ended[key] = report.Pod
	} else {
		delete(r.ended, key)
	}
	r.mutex.Unlock()
	if !had || report.Ended != previous.Ended ||
		report.Paused != previous.Paused || report.Item != previous.Item {
		signalWake(r.wake)
	}
	return endingBegan
}

// markReplaced marks the run's pod with this UID as gone before the pass
// deletes it to replace it. The kubelet sends SIGTERM as soon as the
// API server accepts the delete, so the old pod's end message can reach
// the bus handler before the delete request returns or the pod store
// shows the delete.
func (r *reports) markReplaced(namespace, name, pod string) {
	r.mutex.Lock()
	r.pods.markGone(runKey(namespace, name), pod)
	r.mutex.Unlock()
}

// unmarkReplaced undoes markReplaced when the delete fails, so the pod's messages
// count again.
func (r *reports) unmarkReplaced(namespace, name, pod string) {
	r.mutex.Lock()
	r.pods.unmarkGone(runKey(namespace, name), pod)
	r.mutex.Unlock()
}

// readPodsFrom sets the pod store that fold and availability read to
// tell the run's pod from an older one. The bus reader starts before
// the watches have listed the cluster, so the store is set later.
//
// The check reads only the store, never the API server, even while the
// API server forbids the pod watch. It runs under the mutex for every
// report, up to once per reportInterval for each playing run, and an API
// request could hold the mutex while it waits out a 429. runpod.go
// handles a store that lags behind the API server.
func (r *reports) readPodsFrom(view *clusterView) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.pods.lookup = view.StoredPod
}

// observe records the run's pod as the pass reads it from the pod store,
// or nil when the store has none. The pass calls it for every Play
// before it reads the ended marks.
func (r *reports) observe(namespace, name string, pod *Pod) {
	r.mutex.Lock()
	r.pods.observe(runKey(namespace, name), pod)
	r.mutex.Unlock()
}

// availability handles an online or offline message for a run. Offline
// deletes the latest report, because it describes a pod that is gone,
// and wakes the loop to rewrite the Play's status.
//
// A message from a pod that is not the run's pod changes only seen. The
// broker publishes a dead pod's Last Will when its keepalive runs out,
// which can be after the run's new pod came online, and that offline
// message must not delete the new pod's report.
func (r *reports) availability(namespace, name string, online bool, pod string) {
	key := runKey(namespace, name)
	r.mutex.Lock()
	r.seen[key] = true
	if !r.pods.fromRunPod(namespace, name, pod) {
		r.mutex.Unlock()
		return
	}
	if !online {
		delete(r.latest, key)
	}
	r.mutex.Unlock()
	if !online {
		signalWake(r.wake)
	}
}

func (r *reports) latestFor(namespace, name string) *playReport {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	report, held := r.latest[runKey(namespace, name)]
	if !held {
		return nil
	}
	return &report
}

// endedFor reports whether the run's pod has reported the end. The Play
// and its pod can still exist while this is true. Only the Player's
// status reads it, to show idle, and the Play's phase still comes from
// the pod.
func (r *reports) endedFor(namespace, name string) bool {
	_, ended := r.endedBy(namespace, name)
	return ended
}

// endedBy returns the UID of the pod that reported the run's end, and
// false when there is no mark or that pod is no longer the run's pod.
// The pod stops being the run's pod when the pod store or replacing
// shows it gone.
func (r *reports) endedBy(namespace, name string) (string, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	pod, ended := r.ended[runKey(namespace, name)]
	if !ended || !r.pods.fromRunPod(namespace, name, pod) {
		return "", false
	}
	return pod, true
}

// retain deletes the latest report and the ended mark of every run that
// is not in live, the runs that still have a Play, so a Play created
// later under the same name does not start ended. seen keeps its
// entries until forget, because stale reads them.
func (r *reports) retain(live map[string]bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for key := range r.latest {
		if !live[key] {
			delete(r.latest, key)
		}
	}
	for key := range r.ended {
		if !live[key] {
			delete(r.ended, key)
		}
	}
	r.pods.retain(live)
}

// stale returns the runs that sent a bus message but are not in live.
// Each is a deleted Play whose retained status and availability topics
// are still on the broker, and the pass clears both.
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

// forget deletes everything stored about a run. The operator calls it
// after it clears the run's retained topics, so stale does not return
// the run again.
func (r *reports) forget(key string) {
	r.mutex.Lock()
	delete(r.latest, key)
	delete(r.ended, key)
	delete(r.seen, key)
	r.pods.forget(key)
	r.mutex.Unlock()
}
