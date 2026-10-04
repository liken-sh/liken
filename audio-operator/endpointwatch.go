package main

// A person edits a Sink's or a Source's spec with kubectl, and neither
// the card nor PipeWire reports that edit. So the operator watches both
// kinds, and wakes the loop when an event carries a change the pass
// must act on:
//
//   - a change to the spec, such as a Sink's spec.volume.level,
//   - a deletion request,
//   - a resource that enters this machine's selection, which is a new
//     resource or one whose status.node now names this machine,
//   - a resource that leaves it, which is a removed resource or one
//     whose status.node now names another machine, such as a
//     Bluetooth speaker that moved.
//
// Nothing of the event is read but its arrival: the pass that follows
// reads every endpoint again, the way every other wake in this
// operator works.
//
// The operator writes the status of these resources on many passes.
// metadata.generation is the API server's count of spec changes, and a
// write to the status subresource does not change it, so the watch of
// this operator's own status write does not wake the loop. The handler
// compares the generation, the deletion mark, and the UID and nothing
// else.
//
// Each watch selects the resources whose status.node is this machine.
// A watch on the whole collection would receive every other machine's
// status writes, and each wake is a pass that reads this machine's
// resources again. A resource enters the selection with the status
// write that names this machine, and the API server sends that entry
// as an event, so a spec a person wrote before it is read on the pass
// that follows.
//
// A Sink's status.session is the exception. The media operator writes
// a volume ask there, and a status write changes no generation, so the
// Sinks' handler also compares the time of the ask, and a new one
// calls asked. That path does not wake the pass: the loop applies the
// ask at once, outside the settle window (asks.go).
//
// Each watch also wakes the loop once, when its first read of the
// collection is done. The first pass can read the resources before the
// watch does, and an edit made between the two reads is in the watch's
// read and in no event.

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

// watchEndpoints turns an edit to one of this machine's resources, on
// both collections, into one wake, and a new volume ask on a Sink into
// one call of asked. The two watches run until the context ends. It answers the two stores with a memo each, which the
// pass reads in place of the API server (objectcache.go).
func watchEndpoints(ctx context.Context, client dynamic.Interface, machine string, wake, asked func(), readings *metrics) objectCache {
	watch := func(kind string, resource schema.GroupVersionResource, handler cache.ResourceEventHandler) informer.Held {
		collection := informer.Start(ctx, client, informer.Source{Resource: resource, FieldSelector: machineSelector(machine)},
			informer.Options{Handler: handler, Synced: wake, Reopened: func() { readings.watchRestarted(kind) }})
		return informer.Held{View: collection.View(), Versions: memo.New()}
	}
	return objectCache{
		sinks: watch(SinkKind, sinkResource, sinkHandler{
			edits: editHandler[Sink]{what: "the Sinks of " + machine, wake: wake}, asked: asked,
		}.handler()),
		sources: watch(SourceKind, sourceResource,
			editHandler[Source]{what: "the Sources of " + machine, wake: wake}.handler()),
	}
}

// editMark is what an edit changes on a resource: the generation,
// which counts spec changes, and the deletion mark. The handler
// compares the deletion mark as well, so a deletion request wakes the
// loop whether or not the API server raises the generation when it
// sets deletionTimestamp. It compares the UID too. After a gap in the
// watch, a resource that somebody deleted and created again with the
// same name reaches the handler as an update, and the new resource can
// have the same generation as the old one.
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

// editHandler wakes the loop when a change from one watch carries an
// edit. T is the operator's own struct for the kind, and a resource
// that does not convert to it is logged. The informer hands an update
// both the copy it held and the new copy, so the handler compares
// their marks and keeps no copy of its own. After a gap in the watch,
// the informer reads the collection again and reports each difference
// from what it held as an addition, an update, or a deletion, so an
// edit made during the gap reaches the handler as well.
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

// added takes a resource that entered the selection.
func (h editHandler[T]) added(object any) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.what, err)
		return
	}
	h.wake()
}

// removed takes a resource that left the selection. A resource that
// does not convert, or a tombstone that holds no copy, still wakes the
// loop: the pass reads the collection again, and one extra pass costs
// less than a Sink that is not created again.
func (h editHandler[T]) removed(object any) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.what, err)
	}
	h.wake()
}

// updated takes a change to a resource the informer held. Only a
// change to the mark wakes the loop, so this operator's own status
// write does not.
func (h editHandler[T]) updated(before, after any) {
	if _, err := informer.Convert[T](after); err != nil {
		informer.Report(h.what, err)
		return
	}
	now, _ := unwrap(after)
	// A held copy that is not an object leaves nothing to compare, so
	// the change counts as an edit.
	held, err := unwrap(before)
	if err != nil || markOf(held) != markOf(now) {
		h.wake()
	}
}

// sinkHandler is the Sinks' handler: the edits wake the loop as they
// do for a Source, and a change of status.session.volumeAsk.at calls
// asked. A Sink that enters the selection calls nothing, because an
// ask on a Sink this operator has not read is recorded and not
// applied (asks.go), and the pass the entry wakes records it.
type sinkHandler struct {
	edits editHandler[Sink]
	asked func()
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
	now, err := askAtOf(after)
	if err != nil || now == "" {
		return
	}
	// A held copy whose ask cannot be read counts as a copy with
	// another ask. applyAsks compares the time with its own record, so
	// a call for an ask it already applied writes nothing.
	if held, err := askAtOf(before); err != nil || held != now {
		h.asked()
	}
}

// askAtOf answers the time of the volume ask a delivered Sink holds,
// and an empty string when it holds none.
func askAtOf(object any) (string, error) {
	item, err := unwrap(object)
	if err != nil {
		return "", err
	}
	at, _, err := unstructured.NestedString(item.Object, "status", "session", "volumeAsk", "at")
	if err != nil {
		return "", fmt.Errorf("reading the volume ask of %s: %w", item.GetName(), err)
	}
	return at, nil
}
