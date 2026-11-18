package main

// The operator watches every collection that a pass reads, so the pass
// reads from memory (clusterview.go) and not from the API server. Each
// watch also decides which changes trigger a pass:
//
//   - Any change to a Play, a Player, a Remote, a Keymap, a
//     MediaPreferences, or a Peripheral triggers a pass. That includes
//     this operator's own status writes, so the pass after a write
//     retires a Finished Play at once and reports the Player's new
//     phase.
//   - Deleting one of this operator's pods triggers a pass, which
//     creates the pod again after an eviction, a lost node, or the
//     operator's own delete. A change to a playback pod's status also
//     triggers a pass, because the Play's status comes from it.
//   - A claim, a ResourceSlice, a Display, a Receiver, a Sink, or a
//     Television triggers a pass only when a field that the pass reads
//     changes, and not when this operator made the change. changewake.go
//     has each of these rules, and the rule for pods.
//
// Pods are watched by the component label that this operator sets on
// each pod it creates. Every other collection is watched across the
// whole cluster. That includes the claims, because this operator sets
// no label on them, and claimRule ignores a claim that this operator
// does not own.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

func resourceOf(apiVersion, resource string) schema.GroupVersionResource {
	version, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		panic(fmt.Sprintf("the API version %q does not parse: %v", apiVersion, err))
	}
	return version.WithResource(resource)
}

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
	sinkResource        = resourceOf(sinkAPIVersion, "sinks")
	televisionResource  = resourceOf(televisionAPIVersion, "televisions")
)

// ownPodsSelector matches every pod this operator creates, by the
// component label that each pod builder sets.
var ownPodsSelector = fmt.Sprintf("%s in (%s,%s,%s)",
	playbackLabelKey, playbackLabelValue, idleLabelValue, remoteLabelValue)

// clusterSyncWait bounds the wait for the first list of every
// collection. That is one request per collection, so a healthy API
// server finishes well inside it. When a collection is not listed by
// then, the process exits and the kubelet restarts it with backoff.
const clusterSyncWait = time.Minute

// watchedCollection is one collection of the view: its watch, and the
// function that stores its source in the view.
type watchedCollection struct {
	kind  string
	watch collectionWatch
	into  func(watchedSource)
}

func keepIn(field *objectSource) func(watchedSource) {
	return func(source watchedSource) { *field = source }
}

// keepWhole is keepIn for a collection whose watch has no selector and
// that is read through the object cache. Whole tells the cache that the
// store has the whole collection, so a list also returns an object that
// this operator just created and the store does not have yet.
func keepWhole(field *informer.Held) func(watchedSource) {
	return func(source watchedSource) {
		view := source.view
		view.Whole = true
		*field = informer.Held{View: view, Versions: memo.New()}
	}
}

// watchCluster starts one informer for each collection the pass reads,
// and returns the view once every informer has listed its collection.
// The informers run until ctx ends. When wait ends before every
// collection is listed, watchCluster returns an error that names the
// collections still unlisted. reader lists a collection from the API
// server while the API server forbids its watch (watchedsource.go).
func watchCluster(ctx, wait context.Context, client dynamic.Interface, reader *apiclient.Client,
	wake chan<- struct{}, metrics *mediaMetrics) (*clusterView, error) {
	view := &clusterView{}
	wakes := wakeOnChange(wake)
	collections := []watchedCollection{
		{kindPlay, collectionWatch{resource: playResource, handler: wakes}, keepWhole(&view.plays)},
		{kindPlayer, collectionWatch{resource: playerResource, handler: wakes}, keepWhole(&view.players)},
		{kindRemote, collectionWatch{resource: remoteResource, handler: wakes}, keepIn(&view.remotes)},
		{kindKeymap, collectionWatch{resource: keymapResource, handler: wakes}, keepIn(&view.keymaps)},
		{kindMediaPreferences, collectionWatch{resource: preferencesResource, handler: wakes}, keepIn(&view.preferences)},
		{kindPeripheral, collectionWatch{resource: peripheralResource, handler: wakes}, keepIn(&view.peripherals)},
		{kindPod, collectionWatch{resource: podResource, labels: ownPodsSelector, handler: podRule.handler(wake)}, keepIn(&view.pods)},
		{kindResourceClaim, collectionWatch{resource: claimResource, handler: claimRule.handler(wake)}, keepIn(&view.claims)},
		{kindResourceSlice, collectionWatch{resource: sliceResource, handler: sliceRule.handler(wake)}, keepIn(&view.slices)},
		// These four kinds are defined by display-operator,
		// equipment-operator, and audio-operator. A cluster can run
		// media-operator without them, so their watches are optional.
		{kindDisplay, collectionWatch{resource: displayResource, optional: true,
			handler: displayRule.handler(wake)}, keepIn(&view.displays)},
		{kindReceiver, collectionWatch{resource: receiverResource, optional: true,
			handler: receiverRule.handler(wake)}, keepIn(&view.receivers)},
		{kindSink, collectionWatch{resource: sinkResource, optional: true,
			handler: sinkRule.handler(wake)}, keepIn(&view.sinks)},
		{kindTelevision, collectionWatch{resource: televisionResource, optional: true,
			handler: televisionRule.handler(wake)}, keepIn(&view.televisions)},
	}
	type read struct {
		kind   string
		into   func(watchedSource)
		source watchedSource
	}
	reads := make(chan read, len(collections))
	for _, each := range collections {
		watch := each.watch
		watch.reopened = watchRestartFunc(metrics, each.kind)
		watch.synced = func(view informer.View) {
			source := watchedSource{view: view, client: reader, resource: watch.resource, labels: watch.labels}
			reads <- read{each.kind, each.into, source}
		}
		go watchCollection(ctx, client, watch)
	}

	pending := map[string]bool{}
	for _, each := range collections {
		pending[each.kind] = true
	}
	for len(pending) > 0 {
		select {
		case done := <-reads:
			done.into(done.source)
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

// wakeOnChange returns a handler that triggers a pass on every add,
// update, and delete. The pass reads the whole collection again, so the
// handler does not look at the object.
func wakeOnChange(wake chan<- struct{}) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { signalWake(wake) },
		UpdateFunc: func(any, any) { signalWake(wake) },
		DeleteFunc: func(any) { signalWake(wake) },
	}
}
