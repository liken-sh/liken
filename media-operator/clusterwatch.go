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
//   - A playback pod that goes away or turns Failed wakes the pass, so
//     an eviction or a crash reaches the reconcile at once. Every other
//     change to a pod does not, because a routine update to a running
//     pod needs no pass.
//   - The claims, the Displays, the ResourceSlices, the Receivers, and
//     the standing idle and reader pods wake nothing. The pass reads
//     them on the backstop tick, and the watch only makes that read
//     free. operate.go names what the tick covers.
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
	into  *objectSource
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
		{kindPlay, collectionWatch{resource: playResource, handler: wakes}, &view.plays},
		{kindPlayer, collectionWatch{resource: playerResource, handler: wakes}, &view.players},
		{kindRemote, collectionWatch{resource: remoteResource, handler: wakes}, &view.remotes},
		{kindKeymap, collectionWatch{resource: keymapResource, handler: wakes}, &view.keymaps},
		{kindMediaPreferences, collectionWatch{resource: preferencesResource, handler: wakes}, &view.preferences},
		{kindPeripheral, collectionWatch{resource: peripheralResource, handler: wakes}, &view.peripherals},
		{kindPod, collectionWatch{resource: podResource, labels: ownPodsSelector, handler: wakeOnPlaybackEnd(wake)}, &view.pods},
		{kindResourceClaim, collectionWatch{resource: claimResource}, &view.claims},
		{kindResourceSlice, collectionWatch{resource: sliceResource}, &view.slices},
		// The display-operator and the equipment-operator define these
		// two, and a cluster can run the media operator with neither.
		{kindDisplay, collectionWatch{resource: displayResource, optional: true}, &view.displays},
		{kindReceiver, collectionWatch{resource: receiverResource, optional: true}, &view.receivers},
	}
	type read struct {
		kind  string
		into  *objectSource
		store cache.Store
	}
	reads := make(chan read, len(collections))
	for _, each := range collections {
		watch := each.watch
		watch.reopened = watchRestartFunc(metrics, each.kind)
		watch.synced = func(store cache.Store) { reads <- read{each.kind, each.into, store} }
		go watchCollection(ctx, client, watch)
	}

	pending := map[string]bool{}
	for _, each := range collections {
		pending[each.kind] = true
	}
	for len(pending) > 0 {
		select {
		case done := <-reads:
			*done.into = done.store
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

// wakeOnPlaybackEnd wakes the pass when a playback pod goes away or
// turns Failed, and on nothing else. An added pod that is already
// Failed wakes it too: after a gap in the watch, the informer reports a
// pod it did not hold as an addition, whatever happened to it in the
// gap.
//
// A removal whose tombstone holds no copy of the pod wakes the pass,
// because nothing says whose pod it was, and one extra pass costs less
// than a crash the reconcile never reads.
func wakeOnPlaybackEnd(wake chan<- struct{}) cache.ResourceEventHandler {
	ended := func(object any) {
		pod, err := convert[Pod](object)
		if err != nil {
			reportUnconverted("the pods", err)
			return
		}
		if pod.Metadata.Labels[playbackLabelKey] == playbackLabelValue && pod.Status.Phase == podFailed {
			poke(wake)
		}
	}
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    ended,
		UpdateFunc: func(_, object any) { ended(object) },
		DeleteFunc: func(object any) {
			pod, err := convert[Pod](object)
			if err != nil {
				reportUnconverted("the pods", err)
				poke(wake)
				return
			}
			if pod.Metadata.Labels[playbackLabelKey] == playbackLabelValue {
				poke(wake)
			}
		},
	}
}
