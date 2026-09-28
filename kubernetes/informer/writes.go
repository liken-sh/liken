package informer

// A copy lags the API server by the time the watch takes to deliver a
// change, usually a few milliseconds. That lag matters for a write
// this process made itself. A pass that runs right after its own write
// can read the copy before the watch delivered the write, and decide
// again from a view that lacks it. For most writes the cost is one
// request that conflicts. For the cluster operator's reboot grants it
// is worse: a sweep that does not see the grant it just wrote counts
// one fewer machine in flight, and can grant another turn beyond the
// disruption budget.
//
// So a caller records each write it makes with Wrote, and the copy
// cannot answer for that object, or answer a List, until the watch
// delivers the version the write returned. The pass then reads the API
// server, which already holds the write. Kubernetes' own controllers
// solve the same problem with what they call expectations.

import (
	"strconv"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// writes holds, by object key, the resourceVersion of each write that
// the watch has not delivered yet. An empty version marks a write
// whose outcome is unknown, such as one that timed out.
type writes struct {
	mu       sync.Mutex
	versions map[string]string
}

// Wrote records a write this process made to the object with key, and
// the resourceVersion the API server answered. An empty version is a
// write whose outcome is unknown: the request failed, and the object
// may hold the write or not. The next change the watch delivers for
// the object, or the next direct read of it (Observed), ends that
// state. A write that conflicted changed nothing, and needs no record.
func (c *Collection) Wrote(key, version string) {
	if c == nil {
		return
	}
	c.written.mu.Lock()
	defer c.written.mu.Unlock()
	// The watch can deliver the write, and even a later change,
	// before the write's own answer arrives. The copy then already
	// holds it.
	if version != "" && atLeast(c.heldVersion(key), version) {
		return
	}
	if c.written.versions == nil {
		c.written.versions = map[string]string{}
	}
	c.written.versions[key] = version
}

// Observed records what a direct read of the API server answered for
// an object, after the copy could not answer. It settles a write whose
// outcome was unknown: the direct read shows the object's version now,
// and the copy can answer once it holds that version.
func (c *Collection) Observed(key, version string) {
	if c == nil || version == "" {
		return
	}
	c.written.mu.Lock()
	defer c.written.mu.Unlock()
	if expected, ok := c.written.versions[key]; !ok || expected != "" {
		return
	}
	if atLeast(c.heldVersion(key), version) {
		delete(c.written.versions, key)
		return
	}
	c.written.versions[key] = version
}

// heldVersion is the resourceVersion of the object the copy holds, or
// "" when it holds none.
func (c *Collection) heldVersion(key string) string {
	object, exists, err := c.store.GetByKey(key)
	if err != nil || !exists {
		return ""
	}
	if item, ok := object.(*unstructured.Unstructured); ok {
		return item.GetResourceVersion()
	}
	return ""
}

func (w *writes) pending(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.versions[key]
	return ok
}

func (w *writes) any() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.versions) > 0
}

// delivered clears the record of a write once the watch delivers it,
// or any later version of the object. A later version can arrive in
// its place: a read of the whole collection after a gap in the watch
// reports only the object's newest version. A write whose outcome is
// unknown clears at the object's next change. A removed object clears
// every record of it.
func (w *writes) delivered(object any, removed bool) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(object)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	expected, ok := w.versions[key]
	if !ok {
		return
	}
	item, isObject := object.(*unstructured.Unstructured)
	if removed || !isObject || expected == "" || atLeast(item.GetResourceVersion(), expected) {
		delete(w.versions, key)
	}
}

// atLeast answers whether version held is the same as version want,
// or later. Kubernetes calls a resourceVersion opaque, but the API
// server that liken runs derives it from its datastore's revision
// counter, so two versions of one object compare as numbers. A version
// that is not a number compares only by equality, and a record that
// never matches keeps the copy from answering, which is the safe side.
func atLeast(held, want string) bool {
	if held == "" || want == "" {
		return false
	}
	h, herr := strconv.ParseUint(held, 10, 64)
	w, werr := strconv.ParseUint(want, 10, 64)
	if herr != nil || werr != nil {
		return held == want
	}
	return h >= w
}

// handler returns a handler that clears the records of delivered
// writes, and then passes each change on to next.
func (w *writes) handler(next cache.ResourceEventHandler) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(object any, initial bool) {
			w.delivered(object, false)
			if next != nil {
				next.OnAdd(object, initial)
			}
		},
		UpdateFunc: func(before, after any) {
			w.delivered(after, false)
			if next != nil {
				next.OnUpdate(before, after)
			}
		},
		DeleteFunc: func(object any) {
			w.delivered(object, true)
			if next != nil {
				next.OnDelete(object)
			}
		},
	}
}
