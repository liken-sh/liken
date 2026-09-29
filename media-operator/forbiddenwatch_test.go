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
