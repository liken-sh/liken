package main

import (
	"testing"
	"testing/synctest"
)

// When the API server returns 403 for a collection's watch, the store
// never becomes ready, so the view reads the collection from the API
// server. A Remote created after the view started is then still found.
func TestAForbiddenWatchReadsTheAPIServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// fold checks a report's pod against the pod store only, even while the
// pod watch is forbidden. readPodsFrom in report.go gives the reason.
func TestAReportWhileThePodWatchIsForbiddenSendsNoRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		cluster.pods["movie-playback"] = housePlaybackPod()
		server := servedCluster(t, cluster)
		pods := server.collections[collectionPath(podResource)]
		server.mu.Lock()
		pods.forbidWatch = true
		server.mu.Unlock()
		view := watchedView(t, server, make(chan struct{}, 1))
		playReports := newReports(make(chan struct{}, 1))
		playReports.readPodsFrom(view)
		before := server.objectReads()

		playReports.fold("house", "movie", playReport{Item: 1, Position: "0:00:10", Pod: "some-pod"})

		mustMatch(t, server.objectReads(), before)
	})
}

// While the store is not ready, a collection that the API server does
// not serve lists as empty and has no objects, the same as the watch of
// an optional resource reads it.
func TestAnUnservedCollectionReadsAsEmptyWhileTheStoreIsNotReady(t *testing.T) {
	server := newCollectionServer()
	source := watchedSource{client: viewClient(t, server), resource: receiverResource}

	listed, err := source.List()
	mustSucceed(t, err)
	mustMatch(t, len(listed), 0)
	_, found, err := source.GetByKey("theater")
	mustSucceed(t, err)
	mustMatch(t, found, false)
}
