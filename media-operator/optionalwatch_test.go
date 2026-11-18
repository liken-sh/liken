package main

// An optional collection is a resource that another operator defines
// and a cluster may not have installed.

import (
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// absentReceivers starts a fake API server that returns 404 for the
// Receiver collection, as a cluster without the CRD does.
func absentReceivers(t *testing.T) (*collectionServer, *clusterView) {
	t.Helper()
	server := servedCluster(t, newFakeCluster())
	server.collections[collectionPath(receiverResource)].status = http.StatusNotFound
	return server, watchedView(t, server, make(chan struct{}, 1))
}

// receiverCount returns -1 when the read fails.
func receiverCount(view *clusterView) int {
	receivers, err := view.Receivers()
	if err != nil {
		return -1
	}
	return len(receivers)
}

// The operator starts on a cluster without equipment-operator or
// display-operator, and finds the resource at the first recheck after
// it is installed.
func TestAnAbsentOptionalCollectionReadsAsEmptyUntilItArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, view := absentReceivers(t)
		mustMatch(t, receiverCount(view), 0)

		server.serve(t, receiverResource, "Receiver", houseReceiver())
		time.Sleep(optionalRecheck - time.Second)
		synctest.Wait()
		mustMatch(t, receiverCount(view), 0)
		time.Sleep(time.Minute)
		synctest.Wait()

		mustMatch(t, receiverCount(view), 1)
	})
}

// The API server sends a deletion made while no watch was open only to
// a watch that starts from a resourceVersion, so the watch must start
// from the version of the list that found the collection.
func TestAnOptionalCollectionThatArrivedReportsADeletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, view := absentReceivers(t)
		receivers := server.serve(t, receiverResource, "Receiver", houseReceiver())
		time.Sleep(optionalRecheck + time.Minute)
		synctest.Wait()
		mustMatch(t, receiverCount(view), 1)

		// The reflector lists again when a watch closes within one second
		// with no event. A longer stream shows that it resumes from its
		// version.
		time.Sleep(2 * time.Second)
		server.mu.Lock()
		receivers.items = nil
		receivers.missed = append(receivers.missed, watchLine("DELETED", stamped(t, receivers, houseReceiver())))
		server.mu.Unlock()
		receivers.live <- closeStream
		time.Sleep(time.Minute)
		synctest.Wait()

		mustMatch(t, receiverCount(view), 0)
		mustMatch(t, watchedFromVersion(server.requested(receiverResource)), true)
	})
}

// watchedFromVersion reports whether a request opened a watch at the
// list's resourceVersion, 100, without sendInitialEvents.
func watchedFromVersion(requests []string) bool {
	for _, request := range requests {
		if strings.Contains(request, "watch=true") && strings.Contains(request, "resourceVersion=100") &&
			!strings.Contains(request, "sendInitialEvents=true") {
			return true
		}
	}
	return false
}
