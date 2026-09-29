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
//
// ended holds, for each run that reported the ending, the UID of the pod
// that reported it. It is kept beside latest rather than read out of it,
// because the sidecar publishes the ending and then goes offline, and
// offline drops the latest report. The mark has to outlive that drop: the
// pod lives on for seconds after the film is over, and the Player is idle
// for every one of them. The mark counts only while its pod is the run's
// pod (runpod.go), so the ending of a pod that crashed or was deleted
// stops counting once the run moves on to a new pod.
//
// pods is what tells the run's current pod from an older one under the
// same name. runpod.go says how.
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
// The ending, a pause, or an item change is what a person waits to see,
// so each wakes the loop at once. The ending is the one a person is
// looking at the screen for: the pass it wakes is what turns the idle
// screen back on. A position that only advances updates the desk and
// wakes nothing: the reconcile pass reads the current position off the
// desk on its next tick, so a steadily playing film writes its
// resource on that interval and not on every report. This is the
// throttle that keeps a one-second bus cadence from becoming a
// one-second write to etcd.
//
// The mark follows the report rather than latching, so a Play recreated
// under the same name reports a run of its own and starts not ended.
//
// fold answers whether this report is where the run's ending began, which
// the caller reads to publish the unit's idle state at once. It is the
// mark and not the previous report that decides that, because a sidecar
// that goes offline drops the previous report while the mark stands, and a
// run whose ending was already answered must not be answered again.
//
// A report from a pod that is not the run's pod says nothing about the
// run, so the desk drops it and wakes nothing. A report with no pod UID
// names no pod, and the desk takes it as the run's.
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
		poke(r.wake)
	}
	return endingBegan
}

// replacing records that the operator is about to delete the run's pod
// with this UID to replace it. The pass calls it before it sends the
// delete, because the delete is what makes the kubelet send the SIGTERM,
// and the old pod's ending can reach the bus reader before the delete
// request returns or the pod store shows the delete.
func (r *reports) replacing(namespace, name, pod string) {
	r.mutex.Lock()
	r.pods.markGone(runKey(namespace, name), pod)
	r.mutex.Unlock()
}

// keeping takes back the record of a run whose delete failed. That pod
// still runs as the run's pod, so its messages must count again, and the
// next pass that replaces it records it again.
func (r *reports) keeping(namespace, name, pod string) {
	r.mutex.Lock()
	r.pods.back(runKey(namespace, name), pod)
	r.mutex.Unlock()
}

// readPodsFrom points the desk at the pod store. The bus reader runs
// before the watches have read the cluster, so until this call the desk
// knows only the pods it recorded, and the mutex hands the store to the
// bus reader's goroutine.
//
// The desk reads the store alone, even while the API server forbids the
// pod watch. It reads a pod for each report, about one a second for each
// playing run, under its mutex, so a read of the API server would cost a
// request for each report and could hold the mutex through a 429. The
// desk already takes a store that lags behind the API server
// (runpod.go).
func (r *reports) readPodsFrom(view *clusterView) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.pods.lookup = view.StoredPod
}

// observe records the run's pod as the pass reads it from the pod store,
// nil for none. The pass calls it for every Play before it reads the
// ending marks. runpod.go says what it records.
func (r *reports) observe(namespace, name string, pod *Pod) {
	r.mutex.Lock()
	r.pods.observe(runKey(namespace, name), pod)
	r.mutex.Unlock()
}

// availability marks a Play online or offline. Either way the run is one
// the desk has now seen, so it joins seen. Offline also drops the latest
// report, because the retained status the broker still holds describes a
// pod that is gone, and a stale report must not read as a live Play; the
// wake lets the pass rewrite the status the drop changed.
//
// An availability from a pod that is not the run's pod changes nothing.
// The broker publishes a dead pod's Last Will when the pod's keepalive
// runs out, which can be after the run's new pod came online, and that
// offline would drop the new pod's report.
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

// endedFor reports whether the run's sidecar has said the run is over.
// The Play and its pod still exist while this is true, and the Play's
// phase still reads from the pod; only the Player's presentable state
// reads the mark.
func (r *reports) endedFor(namespace, name string) bool {
	_, ended := r.endedBy(namespace, name)
	return ended
}

// endedBy answers the UID of the pod whose ending marks the run over, and
// whether a mark counts. A mark counts only while its pod is still the
// run's pod: once the pod store or the operator's own delete shows that
// pod gone, the run goes on in another pod and the mark says nothing.
func (r *reports) endedBy(namespace, name string) (string, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	pod, ended := r.ended[runKey(namespace, name)]
	if !ended || !r.pods.fromRunPod(namespace, name, pod) {
		return "", false
	}
	return pod, true
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
	// The ending mark goes with the report it came on. A Play that no
	// longer exists names nothing, and a Play later created under the same
	// name must not start life ended.
	for key := range r.ended {
		if !live[key] {
			delete(r.ended, key)
		}
	}
	r.pods.retain(live)
}

// stale returns the runs the desk has seen a bus message for that the
// pass's list of Plays does not hold. Each is a Play the API server no
// longer holds whose retained status and availability the broker still
// holds, and the operator clears both on the pass that reads it.
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
	delete(r.ended, key)
	delete(r.seen, key)
	r.pods.forget(key)
	r.mutex.Unlock()
}
