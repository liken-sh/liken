package watch

// These tests run the reads and the wake handlers through client-go's
// real reflector, against a scripted API server. The reflector's own
// loop is upstream's to test, and the shared informer package tests
// the watch itself. What these tests prove is that a ready store
// answers reads, including the answer that an object is absent, and
// that each change the API server sends wakes the loop by its rule.
// Each test that runs a watch runs in a synctest bubble, so
// synctest.Wait returns once the reflector has done all it can.

import (
	"context"
	"testing"
	"testing/synctest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

var testSource = informer.Source{Resource: thingResource, LabelSelector: "app=test", FieldSelector: "metadata.name=a"}

func asObject(t *testing.T, item thing) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// mistyped is an object whose spec.size is text, so it does not
// convert to a thing.
func mistyped(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	object := asObject(t, newThing(name, "7", 1))
	if err := unstructured.SetNestedField(object.Object, "two", "spec", "size"); err != nil {
		t.Fatal(err)
	}
	return object
}

// A store that never started cannot answer, so the pass reads the API
// server instead.
func TestAStoreThatNeverStartedCannotAnswer(t *testing.T) {
	var missing *informer.Collection
	_, _, ok := Get[thing](missing.View(), "a")
	_, listed := List[thing](missing.View())
	_, indexed := ByIndex[thing](missing.View(), "app", "test")
	if ok || listed || indexed {
		t.Error("a nil collection answered a read")
	}
}

// Through the reflector: the ready store answers a read by name, an
// absent name, a list, and a lookup by label, and it holds no
// managedFields.
func TestAReadyStoreAnswersReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := newThing("a", "7", 1), newThing("b", "8", 1)
		a.Metadata.Labels = map[string]string{"app": "one"}
		b.Metadata.Labels = map[string]string{"app": "two"}
		a.Metadata.ManagedFields = []any{map[string]any{"manager": "kubectl"}}
		server := newWatchServer([][]thing{{b, a}})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		c := informer.Start(ctx, testWatcher(t, server), testSource, informer.Options{Indexers: cache.Indexers{"app": LabelIndex("app")}})
		awaitReady(t, c)

		got, found, ok := Get[thing](c.View(), "a")
		if !ok || !found || got.Metadata.Name != "a" || got.Metadata.ManagedFields != nil {
			t.Errorf("Get(a) = %+v, found %v, ok %v; want a with no managedFields", got, found, ok)
		}
		if _, found, ok := Get[thing](c.View(), "c"); !ok || found {
			t.Errorf("Get(c) found %v, ok %v; want an answer that c is absent", found, ok)
		}
		if all, ok := List[thing](c.View()); !ok || len(all) != 2 || all[0].Metadata.Name != "a" {
			t.Errorf("List = %+v, ok %v; want a, then b", all, ok)
		}
		if two, ok := ByIndex[thing](c.View(), "app", "two"); !ok || len(two) != 1 || two[0].Metadata.Name != "b" {
			t.Errorf("ByIndex(app=two) = %+v, ok %v; want b", two, ok)
		}
	})
}

// readyStore is a ready view of a store that holds the given objects,
// with no watch behind it.
func readyStore(t *testing.T, objects ...any) informer.View {
	t.Helper()
	store := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{"app": LabelIndex("app")})
	for _, object := range objects {
		if err := store.Add(object); err != nil {
			t.Fatal(err)
		}
	}
	return informer.View{Store: store, Synced: func() bool { return true }}
}

// An object that does not convert makes a read of it, a list, and a
// lookup by label unusable, so the pass reads the API server and never
// judges a collection with an object missing.
func TestAnObjectThatDoesNotConvertSpoilsTheAnswer(t *testing.T) {
	broken := mistyped(t, "b")
	broken.SetLabels(map[string]string{"app": "two"})
	view := readyStore(t, asObject(t, newThing("a", "7", 1)), broken)

	_, _, read := Get[thing](view, "b")
	_, listed := List[thing](view)
	_, indexed := ByIndex[thing](view, "app", "two")
	if read || listed || indexed {
		t.Errorf("read %v, listed %v, indexed %v; want no answer from a store that holds an object that does not convert",
			read, listed, indexed)
	}
}

// A lookup by label needs a store that keeps indexes.
func TestALookupByLabelNeedsAnIndexer(t *testing.T) {
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	view := informer.View{Store: store, Synced: func() bool { return true }}
	if _, ok := ByIndex[thing](view, "app", "two"); ok {
		t.Error("a store with no indexes answered a lookup by label")
	}
	if _, ok := ByIndex[thing](readyStore(t), "size", "two"); ok {
		t.Error("a store answered a lookup by an index it does not keep")
	}
}

// The label index holds an object under its label's value, and holds
// an object with no such label, or no object at all, under nothing.
func TestTheLabelIndexReadsOneLabel(t *testing.T) {
	labeled := asObject(t, newThing("a", "7", 1))
	labeled.SetLabels(map[string]string{"app": "one"})
	cases := []struct {
		name   string
		object any
		want   []string
	}{
		{"a labeled object", labeled, []string{"one"}},
		{"an object with no such label", asObject(t, newThing("b", "7", 1)), nil},
		{"something that is not an object", "b", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := LabelIndex("app")(c.object)
			if err != nil || len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) {
				t.Errorf("LabelIndex = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

// The three wake rules, through the reflector. WakeOnEdit ignores a
// status write and wakes for a spec edit, a deletion mark, a new
// object, and a removed object. WakeOnChange wakes for the status
// write too. WakeOnContent wakes for the status write, and ignores a
// write that moved only the resourceVersion.
func TestEachHandlerWakesTheLoopForItsChanges(t *testing.T) {
	statusWrite := newThing("a", "110", 1)
	statusWrite.Status.Phase = "Ready"
	specEdit := newThing("a", "120", 2)
	deleting := newThing("a", "130", 2)
	deleting.Metadata.DeletionTimestamp = "2026-09-27T12:00:00Z"
	cases := []struct {
		name    string
		handler func(wake func()) cache.ResourceEventHandler
		line    string
		wakes   bool
	}{
		{"an edit handler, a status write", editHandler, event("MODIFIED", statusWrite), false},
		{"an edit handler, a spec edit", editHandler, event("MODIFIED", specEdit), true},
		{"an edit handler, a deletion mark", editHandler, event("MODIFIED", deleting), true},
		{"an edit handler, a new object", editHandler, event("ADDED", newThing("b", "140", 1)), true},
		{"an edit handler, a removed object", editHandler, event("DELETED", newThing("a", "150", 1)), true},
		{"a change handler, a status write", changeHandler, event("MODIFIED", statusWrite), true},
		{"a content handler, a status write", contentHandler, event("MODIFIED", statusWrite), true},
		{"a content handler, a new version only", contentHandler, event("MODIFIED", newThing("a", "160", 1)), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newWatchServer([][]thing{{newThing("a", "7", 1)}}, []string{pause, c.line, holdOpen})
				wakes := make(chan struct{}, 1)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				informer.Start(ctx, testWatcher(t, server), testSource, informer.Options{Handler: c.handler(Signal(wakes))})
				if !woke(wakes) {
					t.Fatal("the first read did not wake the loop")
				}

				server.release()

				if got := woke(wakes); got != c.wakes {
					t.Errorf("woke = %v, want %v", got, c.wakes)
				}
			})
		})
	}
}

func editHandler(wake func()) cache.ResourceEventHandler { return WakeOnEdit[thing](testSource, wake) }
func changeHandler(wake func()) cache.ResourceEventHandler {
	return WakeOnChange[thing](testSource, wake)
}
func contentHandler(wake func()) cache.ResourceEventHandler {
	return WakeOnContent[thing](testSource, wake)
}

// A content handler with an ignore function wakes for no write that
// changes only the fields it removes.
func TestAContentHandlerIgnoresTheFieldsItIsTold(t *testing.T) {
	stamped := func(phase, version string) *unstructured.Unstructured {
		item := newThing("a", version, 1)
		item.Status.Phase = phase
		return asObject(t, item)
	}
	ignorePhase := func(fields map[string]any) { unstructured.RemoveNestedField(fields, "status", "phase") }
	woke := false
	h := WakeOnContent[thing](testSource, func() { woke = true }, ignorePhase)

	h.OnUpdate(stamped("Ready", "7"), stamped("Busy", "8"))

	if woke {
		t.Error("a write that changed only an ignored field woke the loop")
	}
}

// After a gap in the watch, the informer reads the collection again
// and reports the difference. An update whose resourceVersion did not
// move is no change, and a change handler does not wake for it.
func TestARereadWithNoChangeDoesNotWake(t *testing.T) {
	a := newThing("a", "7", 1)
	h := WakeOnChange[thing](testSource, func() { t.Error("an unchanged object woke the loop") })
	h.OnUpdate(asObject(t, a), asObject(t, a))
}

// A removal wakes the loop even when its tombstone holds no object,
// because nothing says what was removed.
func TestARemovalWithNoCopyWakes(t *testing.T) {
	woke := false
	h := WakeOnEdit[thing](testSource, func() { woke = true })
	h.OnDelete(cache.DeletedFinalStateUnknown{Key: "a"})
	if !woke {
		t.Error("a removal with no copy did not wake the loop")
	}
}

// An object that does not convert does not wake the loop, because the
// pass cannot use it either. The handler logs it.
func TestAnObjectThatDoesNotConvertDoesNotWake(t *testing.T) {
	h := WakeOnChange[thing](testSource, func() { t.Error("an object that does not convert woke the loop") })
	h.OnAdd(mistyped(t, "a"), false)
	h.OnUpdate(asObject(t, newThing("a", "7", 1)), mistyped(t, "a"))
}
