package main

// The operator watches every collection its pass reads, so the pass
// reads them from memory (clusterview.go) instead of from the API
// server. Some watches also wake the pass, and the rest only keep the
// view current:
//
//   - A change to a Play, a Player, a Remote, a Keymap, a
//     MediaPreferences, or a Peripheral wakes the pass. That includes
//     this operator's own status writes on Plays, Players, and
//     Remotes: the pass after a write is how a Finished Play retires
//     at once and a new phase reaches the Player.
//   - A pod of this operator's that goes away wakes the pass, so the
//     pass that follows an eviction, a lost node, or its own delete
//     creates what it owes. A playback pod also wakes it when its
//     status changes, because the Play's status is derived from it.
//   - A claim, a ResourceSlice, a Display, or a Receiver wakes the pass
//     when a change reaches a field the pass reads, and not when this
//     operator writes it. changewake.go holds each rule, the pods'
//     rule too.
//
// A watch is scoped the way the pass reads: the pods by the component
// label this operator stamps on each pod it creates, and every other
// collection in the whole cluster. A claim carries no label of this
// operator's, and a claim created by an older release never will, so
// the claims are watched in the whole cluster.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// resourceOf names one collection by its API version and resource.
func resourceOf(apiVersion, resource string) schema.GroupVersionResource {
	version, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		panic(fmt.Sprintf("the API version %q does not parse: %v", apiVersion, err))
	}
	return version.WithResource(resource)
}

// The collections the operator watches.
var (
	playResource        = resourceOf(mediaAPIVersion, "plays")
	playerResource      = resourceOf(mediaAPIVersion, "players")
	remoteResource      = resourceOf(mediaAPIVersion, "remotes")
	keymapResource      = resourceOf(mediaAPIVersion, "keymaps")
	preferencesResource = resourceOf(mediaAPIVersion, "mediapreferences")
	peripheralResource  = resourceOf(peripheralAPIVersion, "peripherals")
	podResource         = resourceOf(podAPIVersion, "pods")
	claimResource       = resourceOf(claimAPIVersion, "resourceclaims")
	displayResource     = resourceOf(displayAPIVersion, "displays")
	sliceResource       = resourceOf(claimAPIVersion, "resourceslices")
	receiverResource    = resourceOf(receiverAPIVersion, "receivers")
)

// ownPodsSelector selects every pod this operator creates, by the
// component label each builder stamps.
var ownPodsSelector = fmt.Sprintf("%s in (%s,%s,%s)",
	playbackLabelKey, playbackLabelValue, idleLabelValue, remoteLabelValue)

// clusterSyncWait bounds the wait for the first read of every
// collection. The first read is one request per collection, so a
// healthy API server answers well inside it. A collection that is not
// read by then ends the process, and the kubelet restarts it with
// backoff.
const clusterSyncWait = time.Minute

// watchedCollection is one collection of the view: the watch, and
// where the view keeps its store.
type watchedCollection struct {
	kind  string
	watch collectionWatch
	into  func(informer.View)
}

// keepIn keeps a store in one field of the view.
func keepIn(field *objectSource) func(informer.View) {
	return func(view informer.View) { *field = view.Store }
}

// keepWhole keeps a store that holds its whole kind in one field of the
// view, with a memo of the copies this operator wrote or read.
func keepWhole(field *informer.Held) func(informer.View) {
	return func(view informer.View) {
		view.Whole = true
		*field = informer.Held{View: view, Versions: memo.New()}
	}
}

// watchCluster starts one informer for each collection the pass reads,
// and answers the view once every informer has read its collection.
// The informers run until ctx ends. wait bounds the wait for the first
// reads: when it ends first, watchCluster answers an error that names
// each collection still unread.
func watchCluster(ctx, wait context.Context, client dynamic.Interface, wake chan<- struct{},
	metrics *mediaMetrics) (*clusterView, error) {
	view := &clusterView{}
	wakes := wakeOnChange(wake)
	collections := []watchedCollection{
		// The Play and Player watches take every object of their kind,
		// so each store holds the whole collection.
		{kindPlay, collectionWatch{resource: playResource, handler: wakes}, keepWhole(&view.plays)},
		{kindPlayer, collectionWatch{resource: playerResource, handler: wakes}, keepWhole(&view.players)},
		{kindRemote, collectionWatch{resource: remoteResource, handler: wakes}, keepIn(&view.remotes)},
		{kindKeymap, collectionWatch{resource: keymapResource, handler: wakes}, keepIn(&view.keymaps)},
		{kindMediaPreferences, collectionWatch{resource: preferencesResource, handler: wakes}, keepIn(&view.preferences)},
		{kindPeripheral, collectionWatch{resource: peripheralResource, handler: wakes}, keepIn(&view.peripherals)},
		{kindPod, collectionWatch{resource: podResource, labels: ownPodsSelector, handler: podRule.handler(wake)}, keepIn(&view.pods)},
		{kindResourceClaim, collectionWatch{resource: claimResource, handler: claimRule.handler(wake)}, keepIn(&view.claims)},
		{kindResourceSlice, collectionWatch{resource: sliceResource, handler: sliceRule.handler(wake)}, keepIn(&view.slices)},
		// The display-operator and the equipment-operator define these
		// two, and a cluster can run the media operator with neither.
		{kindDisplay, collectionWatch{resource: displayResource, optional: true,
			handler: displayRule.handler(wake)}, keepIn(&view.displays)},
		{kindReceiver, collectionWatch{resource: receiverResource, optional: true,
			handler: receiverRule.handler(wake)}, keepIn(&view.receivers)},
	}
	type read struct {
		kind string
		into func(informer.View)
		view informer.View
	}
	reads := make(chan read, len(collections))
	for _, each := range collections {
		watch := each.watch
		watch.reopened = watchRestartFunc(metrics, each.kind)
		watch.synced = func(view informer.View) { reads <- read{each.kind, each.into, view} }
		go watchCollection(ctx, client, watch)
	}

	pending := map[string]bool{}
	for _, each := range collections {
		pending[each.kind] = true
	}
	for len(pending) > 0 {
		select {
		case done := <-reads:
			done.into(done.view)
			delete(pending, done.kind)
		case <-wait.Done():
			return nil, fmt.Errorf("these collections were not read: %s: %w", sortedKinds(pending), wait.Err())
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return view, nil
}

func sortedKinds(kinds map[string]bool) string {
	names := make([]string, 0, len(kinds))
	for kind := range kinds {
		names = append(names, kind)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// wakeOnChange wakes the pass on every change the informer reports.
// The pass reads the whole collection, so the handler converts nothing.
func wakeOnChange(wake chan<- struct{}) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { poke(wake) },
		UpdateFunc: func(any, any) { poke(wake) },
		DeleteFunc: func(any) { poke(wake) },
	}
}
