package main

// A collection whose watch the API server forbids is read from the API
// server, because no watch keeps its store current.

import (
	"testing"
)

// A Remote created after the first read of a collection whose watch
// the API server forbids reaches no store. The view reads the
// collection and the object from the API server, and answers it.
func TestAForbiddenWatchReadsTheAPIServer(t *testing.T) {
	server := servedCluster(t, newFakeCluster())
	remotes := server.collections[collectionPath(remoteResource)]
	server.mu.Lock()
	remotes.forbidWatch = true
	server.mu.Unlock()
	view := watchedView(t, server, make(chan struct{}, 1))

	server.mu.Lock()
	remotes.items = append(remotes.items, stamped(t, remotes, Remote{Metadata: ObjectMeta{Name: "wand", Namespace: "house"}}))
	server.mu.Unlock()

	listed, err := view.Remotes()
	mustSucceed(t, err)
	mustMatch(t, len(listed), 1)
	one, err := view.Remote("house", "wand")
	mustSucceed(t, err)
	mustMatch(t, one.Metadata.Name, "wand")
}

// The report desk tells the run's pod from an older one by the pod
// store, and it runs on the bus reader's goroutine for each report,
// about one a second for each playing run. It reads only the store,
// even while the API server forbids the pod watch, so a report sends
// the API server no request and never waits on a 429 under the desk's
// lock. The desk already reads a store that lags behind the API server.
func TestAReportWhileThePodWatchIsForbiddenSendsNoRequest(t *testing.T) {
	cluster := newFakeCluster()
	cluster.pods["movie-playback"] = housePlaybackPod()
	server := servedCluster(t, cluster)
	pods := server.collections[collectionPath(podResource)]
	server.mu.Lock()
	pods.forbidWatch = true
	server.mu.Unlock()
	view := watchedView(t, server, make(chan struct{}, 1))
	desk := newReports(make(chan struct{}, 1))
	desk.readPodsFrom(view)
	before := server.objectReads()

	desk.fold("house", "movie", playReport{Item: 1, Position: "0:00:10", Pod: "some-pod"})

	mustMatch(t, server.objectReads(), before)
}

// A collection the API server does not serve lists as empty while the
// store is not ready, the way the watch of an optional collection reads
// it, and a read of one of its objects answers that it does not exist.
func TestAnUnservedCollectionReadsAsEmptyWhileTheStoreIsNotReady(t *testing.T) {
	server := newCollectionServer()
	source := watchedSource{client: viewClient(t, server), resource: receiverResource}

	listed, err := source.List()
	mustSucceed(t, err)
	mustMatch(t, len(listed), 0)
	_, held, err := source.GetByKey("theater")
	mustSucceed(t, err)
	mustMatch(t, held, false)
}
