package main

import (
	"testing"
)

// After the operator deletes a Play, the list leaves it out even though
// the store still has its copy.
func TestAPlayTheOperatorDeletedIsNotListedFromTheStore(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))
	// The watch has not delivered the delete yet.
	frozen := newFakeCluster()
	frozen.plays["movie"] = cluster.plays["movie"]
	media.view.plays.View.Store = frozen.view().plays.View.Store

	mustSucceed(t, media.deletePlay("house", "movie"))
	plays, err := media.view.Plays(media.client)

	mustSucceed(t, err)
	mustMatch(t, len(plays), 0)
	mustMatch(t, countPathRequests(cluster.requests, "GET "+playPath("house", "movie")), 1)
}
