package main

// These tests cover the watch of an optional collection: a resource
// another operator defines, which a cluster may not have installed.
// They run in a synctest bubble, so the recheck of an absent collection
// waits its full five minutes on the bubble's clock.

import (
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// absentReceivers serves every collection but the Receivers, whose
// resource the cluster has not installed, and answers the view the
// operator watches it through.
func absentReceivers(t *testing.T) (*collectionServer, *clusterView) {
	t.Helper()
	server := servedCluster(t, newFakeCluster())
	server.collections[collectionPath(receiverResource)].status = http.StatusNotFound
	return server, watchedView(t, server, make(chan struct{}, 1))
}

// heldReceivers answers how many Receivers the view holds, or -1 for a
// read that fails.
func heldReceivers(view *clusterView) int {
	receivers, err := view.Receivers()
	if err != nil {
		return -1
	}
	return len(receivers)
}

// A collection that a cluster has not installed reads as empty, so the
// operator starts on a cluster with no equipment-operator or
// display-operator. When the resource arrives, the watch finds it after
// its next recheck and the reflector's backoff, and not before the
// recheck.
func TestAnAbsentOptionalCollectionReadsAsEmptyUntilItArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, view := absentReceivers(t)
		mustMatch(t, heldReceivers(view), 0)

		server.serve(t, receiverResource, "Receiver", houseReceiver())
		time.Sleep(optionalRecheck - time.Second)
		synctest.Wait()
		mustMatch(t, heldReceivers(view), 0)
		time.Sleep(time.Minute)
		synctest.Wait()

		mustMatch(t, heldReceivers(view), 1)
	})
}

// A Receiver deleted after its resource arrives leaves the view, even
// when the deletion lands while no watch is open. The API server sends
// such a deletion only to a watch that starts from a version, so the
// watch of a collection that arrived must start from the version of a
// read that found it.
func TestAnOptionalCollectionThatArrivedReportsADeletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, view := absentReceivers(t)
		receivers := server.serve(t, receiverResource, "Receiver", houseReceiver())
		time.Sleep(optionalRecheck + time.Minute)
		synctest.Wait()
		mustMatch(t, heldReceivers(view), 1)

		// The stream runs past the reflector's one-second limit before it
		// closes. The reflector counts a watch that closes sooner with no
		// event as a failure and reads the collection again, so the longer
		// stream shows that it resumes from its version instead.
		time.Sleep(2 * time.Second)
		server.mu.Lock()
		receivers.items = nil
		receivers.missed = append(receivers.missed, watchLine("DELETED", stamped(t, receivers, houseReceiver())))
		server.mu.Unlock()
		receivers.live <- closeStream
		time.Sleep(time.Minute)
		synctest.Wait()

		mustMatch(t, heldReceivers(view), 0)
		mustMatch(t, watchedFromVersion(server.requested(receiverResource)), true)
	})
}

// watchedFromVersion reports whether a watch opened from the version of
// a read that found the collection.
func watchedFromVersion(requests []string) bool {
	for _, request := range requests {
		if strings.Contains(request, "watch=true") && strings.Contains(request, "resourceVersion=100") &&
			!strings.Contains(request, "sendInitialEvents=true") {
			return true
		}
	}
	return false
}
