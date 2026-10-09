package watch

// A liken operator runs one full pass for each wake, and the pass reads
// every object it judges again, from the copies. So a watch's handler
// does one thing: it decides whether a change needs a pass, and wakes
// the loop when it does. The loop's channel has one slot, so a burst of
// changes makes one wake.
//
// Each handler converts the object into the operator's struct before
// it wakes the loop, and logs an object that does not convert. The pass
// that follows cannot use that object either, and the log line is the
// only place a person learns why.

import (
	"maps"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// Signal returns a wake function for a channel with one slot. A send
// that finds the slot full is dropped, because the wake already waiting
// starts a pass that reads everything this change did.
func Signal(wake chan<- struct{}) func() {
	return func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// WakeOnChange wakes the loop for every change to an object: a new
// object, a removed object, and any write to an object. It is the
// handler for a collection whose status the pass reads, such as a
// Machine that another writer reports on. An update that the informer
// delivers after it reads the collection again, for an object whose
// resourceVersion did not move, is no change and does not wake.
func WakeOnChange[T any](source informer.Source, wake func()) cache.ResourceEventHandler {
	h := wakeHandler[T]{source: source, wake: wake, changed: func(before, after *unstructured.Unstructured) bool {
		return before.GetResourceVersion() != after.GetResourceVersion()
	}}
	return h.handler()
}

// WakeOnEdit wakes the loop only for an edit: a new object, a removed
// object, and a write that changed the spec, the deletion mark, or the
// object's identity. It is the handler for a collection whose spec the
// pass acts on, where the writes to its status, such as this
// operator's own, must not wake a pass.
//
// metadata.generation is the API server's count of spec changes, and a
// write to the status subresource does not raise it. The deletion mark
// is compared as well, so a deletion request wakes the loop whether or
// not the API server raised the generation when it set
// deletionTimestamp. The UID is compared because, after a gap in the
// watch, an object that somebody deleted and created again with the
// same name reaches the handler as an update, and the new object can
// have the generation the old one had.
func WakeOnEdit[T any](source informer.Source, wake func()) cache.ResourceEventHandler {
	h := wakeHandler[T]{source: source, wake: wake, changed: func(before, after *unstructured.Unstructured) bool {
		return before.GetUID() != after.GetUID() ||
			before.GetGeneration() != after.GetGeneration() ||
			(before.GetDeletionTimestamp() == nil) != (after.GetDeletionTimestamp() == nil)
	}}
	return h.handler()
}

// WakeOnContent wakes the loop when an object's content changes: a new
// object, a removed object, and a write that changed anything but the
// resourceVersion. It is the handler for a collection whose informer
// trims each object to the fields the pass reads. The kubelet writes a
// pod's status every few seconds while a container restarts or a probe
// runs, and each write moves the resourceVersion. After the trim, a
// write that changed no field the pass reads leaves the two copies
// equal except for the version, and wakes no pass.
func WakeOnContent[T any](source informer.Source, wake func()) cache.ResourceEventHandler {
	h := wakeHandler[T]{source: source, wake: wake, changed: func(before, after *unstructured.Unstructured) bool {
		return !reflect.DeepEqual(withoutVersion(before), withoutVersion(after))
	}}
	return h.handler()
}

// withoutVersion answers a shallow copy of an object with no
// resourceVersion, and leaves the informer's copy as it is.
func withoutVersion(object *unstructured.Unstructured) map[string]any {
	fields := maps.Clone(object.Object)
	if metadata, ok := fields["metadata"].(map[string]any); ok {
		metadata = maps.Clone(metadata)
		delete(metadata, "resourceVersion")
		fields["metadata"] = metadata
	}
	return fields
}

// wakeHandler is the shared half of the three handlers above. changed
// compares the copy the informer held with the new copy. The informer
// hands an update both copies, so the handler keeps no copy of its own.
// After a gap in the watch, the informer reads the collection again
// and reports each difference from what it held as an addition, an
// update, or a deletion, so a change made during the gap reaches the
// handler too.
type wakeHandler[T any] struct {
	source  informer.Source
	wake    func()
	changed func(before, after *unstructured.Unstructured) bool
}

func (h wakeHandler[T]) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    h.added,
		UpdateFunc: h.updated,
		DeleteFunc: h.removed,
	}
}

func (h wakeHandler[T]) added(object any) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.source.String(), err)
		return
	}
	h.wake()
}

// removed wakes the loop even for an object that does not convert. A
// tombstone can hold no copy of the object at all, and one extra pass
// costs less than a removal the pass never judged.
func (h wakeHandler[T]) removed(object any) {
	if _, err := informer.Convert[T](object); err != nil {
		informer.Report(h.source.String(), err)
	}
	h.wake()
}

// updated wakes the loop when changed says so. A held copy that is not
// an object says nothing about what changed, so the update counts as a
// change.
func (h wakeHandler[T]) updated(before, after any) {
	if _, err := informer.Convert[T](after); err != nil {
		informer.Report(h.source.String(), err)
		return
	}
	held, heldOK := before.(*unstructured.Unstructured)
	fresh, freshOK := after.(*unstructured.Unstructured)
	if !heldOK || !freshOK || h.changed(held, fresh) {
		h.wake()
	}
}
