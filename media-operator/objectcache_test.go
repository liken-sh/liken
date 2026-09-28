package main

// These tests cover what the Play store adds to the object cache: the
// memo forgets a Play once the API server and the store both no longer
// hold it, and keeps every other record.

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

func TestTheMemoForgetsAPlayOnlyOnceItIsGoneFromTheStore(t *testing.T) {
	cases := []struct {
		name     string
		listed   bool
		held     bool
		wantMemo bool
	}{
		{name: "a Play the list answered keeps its record", listed: true, held: true, wantMemo: true},
		{name: "a Play the store still holds keeps its record", held: true, wantMemo: true},
		{name: "a Play gone from both loses its record"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := cache.NewStore(cache.MetaNamespaceKeyFunc)
			if test.held {
				held := &unstructured.Unstructured{}
				held.SetNamespace("house")
				held.SetName("movie")
				mustSucceed(t, store.Add(held))
			}
			memo := newVersionMemo()
			memo.note("house/movie", "")

			memo.forgetGone(store, map[string]bool{"house/movie": test.listed})

			_, noted := memo.seen["house/movie"]
			mustMatch(t, noted, test.wantMemo)
		})
	}
}

// A Play the API server no longer holds is left out of the list, though
// the store still holds its copy, once the operator's own delete has
// noted it.
func TestAPlayTheOperatorDeletedIsNotListedFromTheStore(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media := testOperator(t, cluster, make(chan struct{}, 1))
	// The watch has not delivered the delete, so the store keeps its copy.
	frozen := newFakeCluster()
	frozen.plays["movie"] = cluster.plays["movie"]
	media.view.plays.view.store = frozen.view().plays.view.store

	mustSucceed(t, media.deletePlay("house", "movie"))
	plays, err := media.view.Plays(media.client)

	mustSucceed(t, err)
	mustMatch(t, len(plays), 0)
	mustMatch(t, countPathRequests(cluster.requests, "GET "+playPath("house", "movie")), 1)
}
