package main

// These tests run the watch on one named object through client-go's
// real reflector, against the core API stand-in. The reflector's own
// recovery is upstream's to test; these prove that each version of the
// object reaches its owner, that an absent object is reported, and that
// an object that does not convert leaves the owner as it was.

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// heldValue is what a test owner holds: the data value of the last
// ConfigMap the watch handed it, "" after the watch said the object is
// absent, and every value in the order it arrived.
type heldValue struct {
	mu     sync.Mutex
	value  string
	seen   []string
	synced chan struct{}
}

func newHeldValue() *heldValue { return &heldValue{synced: make(chan struct{})} }

func (h *heldValue) take(configMap *ConfigMap) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.value = ""
	if configMap != nil {
		h.value = configMap.Data["value"]
	}
	h.seen = append(h.seen, h.value)
}

func (h *heldValue) is(value string) func() bool {
	return func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.value == value
	}
}

func (h *heldValue) history() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.seen...)
}

func valueConfigMap(value string) *ConfigMap {
	return &ConfigMap{
		Metadata: ObjectMeta{Name: "anchor", Namespace: "liken-system"},
		Data:     map[string]string{"value": value},
	}
}

// follow starts the watch on the ConfigMap named anchor, and stops it
// when the test ends.
func (h *heldValue) follow(t *testing.T, api *coreAPI) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		stop()
		<-done
	})
	go func() {
		defer close(done)
		watchNamed(ctx, testWatcher(t, api.handler()), configMapResource, "liken-system", "anchor",
			"configmap liken-system/anchor", h.take, h.synced)
	}()
}

// The first read hands the object to its owner, and each change after
// it arrives as the API server makes it, a deletion as nil.
func TestANamedWatchHandsEachVersionToItsOwner(t *testing.T) {
	api := newCoreAPI()
	api.seed(t, "configmaps/anchor", valueConfigMap("first"))
	held := newHeldValue()

	held.follow(t, api)
	mustMatch(t, closedWithin(held.synced, watchTimeout), true)
	mustMatch(t, held.is("first")(), true)

	api.seed(t, "configmaps/anchor", valueConfigMap("second"))
	until(t, "the owner never took the change", held.is("second"))
	api.remove("configmaps/anchor")
	until(t, "the owner never heard of the deletion", held.is(""))

	mustMatchAll(t, held.history(), []string{"first", "second", ""})
}

// An object that is absent at the first read is reported as absent
// once, and one that appears later reaches the owner as it is created.
func TestANamedWatchReportsAnAbsentObjectAndItsArrival(t *testing.T) {
	api := newCoreAPI()
	held := newHeldValue()

	held.follow(t, api)
	mustMatch(t, closedWithin(held.synced, watchTimeout), true)
	api.seed(t, "configmaps/anchor", valueConfigMap("arrived"))
	until(t, "the owner never took the object that appeared", held.is("arrived"))

	mustMatchAll(t, held.history(), []string{"", "arrived"})
}

// A version of the object that does not convert is reported, and the
// owner keeps the version it holds, because the next version can be
// valid.
func TestANamedWatchKeepsTheOwnerOnAnObjectThatDoesNotConvert(t *testing.T) {
	api := newCoreAPI()
	api.seed(t, "configmaps/anchor", valueConfigMap("good"))
	held := newHeldValue()
	held.follow(t, api)
	mustMatch(t, closedWithin(held.synced, watchTimeout), true)

	api.seed(t, "configmaps/anchor", map[string]any{
		"metadata": map[string]any{"name": "anchor", "namespace": "liken-system"},
		"data":     "not a map",
	})
	api.seed(t, "configmaps/anchor", valueConfigMap("fixed"))
	until(t, "the owner never took the valid version", held.is("fixed"))

	mustMatchAll(t, held.history(), []string{"good", "fixed"})
}

// The watch names its one object in a field selector, because that is
// the request RBAC authorizes against a Role's resourceNames.
func TestANamedWatchSelectsItsOneObject(t *testing.T) {
	api := newCoreAPI()
	held := newHeldValue()

	held.follow(t, api)
	mustMatch(t, closedWithin(held.synced, watchTimeout), true)

	for _, request := range api.requested() {
		mustMatch(t, strings.HasSuffix(request, "fieldSelector=metadata.name=anchor"), true)
	}
	mustMatch(t, len(api.requested()) > 0, true)
}
