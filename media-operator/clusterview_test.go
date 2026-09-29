package main

// These tests cover the view a pass reads: the order of a collection,
// the conversion into the operator's own structs, and the answer for an
// object the view does not hold. The first test reads a store of
// client-go's own, the kind an informer fills.

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/tools/cache"
)

// The view reads an informer's store by the keys the informer files
// each object under: namespace/name, and name alone for a
// cluster-scoped object.
func TestTheViewReadsAnInformersStore(t *testing.T) {
	pods := cache.NewStore(cache.DeletionHandlingMetaNamespaceKeyFunc)
	mustSucceed(t, pods.Add(asObject(t, housePlaybackPod())))
	displays := cache.NewStore(cache.DeletionHandlingMetaNamespaceKeyFunc)
	mustSucceed(t, displays.Add(asObject(t, litDisplay())))
	ready := func() bool { return true }
	view := &clusterView{
		pods:     watchedSource{view: informer.View{Store: pods, Synced: ready}},
		displays: watchedSource{view: informer.View{Store: displays, Synced: ready}},
	}

	pod, err := view.Pod("house", "movie-playback")
	mustSucceed(t, err)
	mustMatch(t, pod.Status.Phase, podRunning)
	display, err := view.Display(testMonitor)
	mustSucceed(t, err)
	mustMatch(t, display.Status.Observed.Power, "on")
}

// The view answers a whole collection in the API server's order, by
// namespace and then by name, whatever order the store holds it in.
func TestTheViewAnswersInTheAPIServersOrder(t *testing.T) {
	cluster := newFakeCluster()
	for _, key := range []string{"den/b", "attic/z", "den/a"} {
		namespace, name, _ := strings.Cut(key, "/")
		cluster.plays[name] = &Play{Metadata: ObjectMeta{Namespace: namespace, Name: name}}
	}

	plays, err := cluster.view().Plays(nil)

	mustSucceed(t, err)
	var keys []string
	for _, play := range plays {
		keys = append(keys, play.Metadata.Namespace+"/"+play.Metadata.Name)
	}
	mustMatch(t, strings.Join(keys, ","), "attic/z,den/a,den/b")
}

// A Play converts whole, each item's presentation included, and an item
// with no presentation converts to none.
func TestTheViewConvertsAPlayWithItsPresentations(t *testing.T) {
	full := &Presentation{
		Type:         "video",
		Hint:         "series",
		Role:         "trailer",
		Title:        "The Pilot",
		Series:       "Example Series",
		Season:       2,
		Episode:      5,
		EpisodeTitle: "The Pilot",
		Year:         2017,
		Date:         "2017-03-05",
		Logo:         "nfs://nas/export/s02/logo.png",
	}
	cluster := newFakeCluster()
	cluster.plays["season"] = &Play{
		Metadata: ObjectMeta{Name: "season", Namespace: "house"},
		Spec: PlaySpec{
			Players: []string{"theater"},
			Items: []PlayItem{
				{URI: "nfs://nas/export/s02e05.mkv", Presentation: full},
				{URI: "https://nas/loose.mkv"},
			},
		},
	}

	plays, err := cluster.view().Plays(nil)

	mustSucceed(t, err)
	items := plays[0].Spec.Items
	mustMatch(t, len(items), 2)
	mustMatch(t, reflect.DeepEqual(items[0].Presentation, full), true)
	mustMatch(t, items[1].Presentation == nil, true)
}

// A Player's device parameters are raw JSON the driver reads, and they
// convert byte for byte.
func TestTheViewConvertsAPlayersDeviceParameters(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = brightPlayer()

	player, err := cluster.view().Player(nil, "house", "theater")

	mustSucceed(t, err)
	mustMatch(t, string(player.Spec.Display.Parameters.Values), `{"brightness":80}`)
}

// An object the view does not hold answers apiclient.ErrNotFound, the answer the
// API client gives, so a caller treats the two the same.
func TestAnObjectTheViewDoesNotHoldIsNotFound(t *testing.T) {
	_, err := newFakeCluster().view().ResourceClaim("house", "movie-devices")

	mustMatch(t, errors.Is(err, apiclient.ErrNotFound), true)
}

// An object that does not convert fails the read of its whole
// collection, and the error names it.
func TestAnObjectThatDoesNotConvertFailsItsCollection(t *testing.T) {
	cluster := newFakeCluster()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.fails[remotesAllPath] = true

	_, err := cluster.view().Remotes()

	mustFail(t, err)
	mustMatch(t, strings.Contains(err.Error(), "house/sofa does not convert"), true)
}
