package main

// These tests cover what the Play store adds to the shared object
// cache.

import (
	"testing"
)

// A Play the API server no longer holds is left out of the list, though
// the store still holds its copy, once the operator's own delete has
// noted it.
func TestAPlayTheOperatorDeletedIsNotListedFromTheStore(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))
	// The watch has not delivered the delete, so the store keeps its copy.
	frozen := newFakeCluster()
	frozen.plays["movie"] = cluster.plays["movie"]
	media.view.plays.View.Store = frozen.view().plays.View.Store

	mustSucceed(t, media.deletePlay("house", "movie"))
	plays, err := media.view.Plays(media.client)

	mustSucceed(t, err)
	mustMatch(t, len(plays), 0)
	mustMatch(t, countPathRequests(cluster.requests, "GET "+playPath("house", "movie")), 1)
}
