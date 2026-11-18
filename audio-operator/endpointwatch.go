package main

// A person edits a Sink or a Source with kubectl, and neither the card
// nor PipeWire reports the edit. So the operator watches both kinds and
// wakes the main loop for each event that can change what the next pass
// does:
//
//   - a spec change, such as a Sink's spec.volume.level,
//   - a deletion request,
//   - a resource that enters this machine's selection: a new resource,
//     or one whose status.node changed to this machine,
//   - a resource that leaves it: a deleted resource, or one whose
//     status.node changed to another machine, such as a Bluetooth
//     speaker that moved.
//
// A wake carries no data, and the pass that follows reads every
// endpoint again.
//
// The operator writes the status of these resources on many passes.
// The API server increments metadata.generation only on a spec change,
// so the handler compares the generation, the deletion timestamp, and
// the UID, and the operator's own status writes do not wake the loop.
//
// Each watch selects the resources whose status.node is this machine,
// because a watch on the whole collection would deliver the status
// writes of every other machine. When a status write sets a resource's
// status.node to this machine, the watch delivers an added event, and
// the pass that it wakes reads the spec that a person wrote earlier.
//
// A Sink's status.session.volumeAsk is the exception. The media operator
// writes it, and a status write does not change the generation, so the
// Sinks' handler also compares status.session.volumeAsk.at and calls
// onVolumeAsk when it changes. That call does not wake a pass, so the
// loop applies the volume request without waiting for the settle window
// (asks.go).
//
// Each watch also wakes the loop once when its first list finishes. The
// first pass can read the resources before the watch has listed them,
// and an edit made between those two reads is in the list and in no
// event.

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// watchEndpoints watches this machine's Sinks and Sources until ctx ends,
// and calls wake for each edit and onVolumeAsk for each new volume
// request on a Sink. The pass reads the returned objectCache in place of
// the API server (objectcache.go).
func watchEndpoints(ctx context.Context, client dynamic.Interface, machine string, wake, onVolumeAsk func(), readings *metrics) objectCache {
	watch := func(kind string, resource schema.GroupVersionResource, handler cache.ResourceEventHandler) informer.Held {
		collection := informer.Start(ctx, client, informer.Source{Resource: resource, FieldSelector: machineSelector(machine)},
			informer.Options{Handler: handler, Synced: wake, Reopened: func() { readings.watchRestarted(kind) }})
		return informer.Held{View: collection.View(), Versions: memo.New()}
	}
	return objectCache{
		sinks: watch(SinkKind, sinkResource, sinkHandler{
			edits: editHandler[Sink]{what: "the Sinks of " + machine, wake: wake}, onVolumeAsk: onVolumeAsk,
		}.handler()),
		sources: watch(SourceKind, sourceResource,
			editHandler[Source]{what: "the Sources of " + machine, wake: wake}.handler()),
	}
}

// editMark is the part of a resource whose change counts as an edit.
// deleting wakes the loop on a deletion request whether or not the API
// server increments the generation when it sets deletionTimestamp. uid
// distinguishes a resource that was deleted and created again with the
// same name, because after a gap in the watch that arrives as an update,
// and the new resource can have the old one's generation.
type editMark struct {
	uid        string
	generation int64
	deleting   bool
}

func markOf(item *unstructured.Unstructured) editMark {
	return editMark{
		uid:        string(item.GetUID()),
		generation: item.GetGeneration(),
		deleting:   item.GetDeletionTimestamp() != nil,
	}
}

// editHandler wakes the loop for each change that is an edit. T is the
// operator's struct for the kind, and the handler logs an object that
// does not convert to it. The informer passes an update both the old and
// the new copy, so the handler keeps no state. After a gap in the watch
// the informer lists the collection again and reports each difference
// as an add, an update, or a delete, so an edit made during the gap also
// wakes the loop.
type editHandler[T any] struct {
	what string
	wake func()
}

func (h editHandler[T]) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    h.added,
		UpdateFunc: h.updated,
		DeleteFunc: h.removed,
	}
}

func (h editHandler[T]) added(object any) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.what, err)
		return
	}
	h.wake()
}

// removed wakes the loop even for an object that does not convert or a
// tombstone with no copy in it. An extra pass costs less than a missed
// deletion, because the pass creates the Sink or Source again for each
// endpoint that still exists on this machine.
func (h editHandler[T]) removed(object any) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.what, err)
	}
	h.wake()
}

func (h editHandler[T]) updated(before, after any) {
	if _, err := informer.Convert[T](after); err != nil {
		informer.Report(h.what, err)
		return
	}
	current, _ := unwrap(after)
	// With no readable old copy, the change counts as an edit.
	previous, err := unwrap(before)
	if err != nil || markOf(previous) != markOf(current) {
		h.wake()
	}
}

// sinkHandler wakes the loop for edits, as editHandler does, and calls
// onVolumeAsk when status.session.volumeAsk.at changes. A Sink that
// enters the selection does not call onVolumeAsk, because the operator
// records and does not apply the volume request that it finds on its
// first read of a Sink (asks.go), and the pass that the add wakes makes
// that read.
type sinkHandler struct {
	edits       editHandler[Sink]
	onVolumeAsk func()
}

func (h sinkHandler) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    h.edits.added,
		UpdateFunc: h.updated,
		DeleteFunc: h.edits.removed,
	}
}

func (h sinkHandler) updated(before, after any) {
	h.edits.updated(before, after)
	currentAt, err := askAtOf(after)
	if err != nil || currentAt == "" {
		return
	}
	// An old copy whose request cannot be read counts as one with a
	// different request. That call is safe, because applyAsks compares
	// the time with the one it recorded and writes nothing for a request
	// that it already applied.
	if previousAt, err := askAtOf(before); err != nil || previousAt != currentAt {
		h.onVolumeAsk()
	}
}

// askAtOf returns status.session.volumeAsk.at of a Sink, or an empty
// string when the status has none.
func askAtOf(object any) (string, error) {
	item, err := unwrap(object)
	if err != nil {
		return "", err
	}
	at, _, err := unstructured.NestedString(item.Object, "status", "session", "volumeAsk", "at")
	if err != nil {
		return "", fmt.Errorf("reading the volume request of %s: %w", item.GetName(), err)
	}
	return at, nil
}
