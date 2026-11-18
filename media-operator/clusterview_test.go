package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/tools/cache"
)

// The view reads a client-go store by the informer's keys: namespace/name,
// or the name alone for a cluster-scoped object.
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

// The view sorts a collection by namespace and then by name, which is
// the API server's order, whatever order the store returns.
func TestTheViewReturnsACollectionInTheAPIServersOrder(t *testing.T) {
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

// A Play converts completely, including each item's presentation, and
// an item with no presentation converts to a nil presentation.
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

// An object that the view does not have returns apiclient.ErrNotFound,
// the same error the API client returns.
func TestAnObjectMissingFromTheViewIsNotFound(t *testing.T) {
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
