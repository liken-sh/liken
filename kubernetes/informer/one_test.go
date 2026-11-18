package informer

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// reports records what a watch of one object delivered, in order: the
// size of each version, and "absent" for each nil.
type reports struct {
	mu        sync.Mutex
	delivered []string
}

func (r *reports) record(object *thing) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if object == nil {
		r.delivered = append(r.delivered, "absent")
		return
	}
	r.delivered = append(r.delivered, fmt.Sprint(object.Spec.Size))
}

func (r *reports) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.delivered)
}

func sized(name, version string, size int) thing {
	item := newThing(name, version, 1)
	item.Spec.Size = size
	return item
}

// unconvertible is a watch event whose spec.size is a string, so it
// does not convert to thing.
func unconvertible(kind, name, version string) string {
	return fmt.Sprintf(`{"type":%q,"object":{"apiVersion":%q,"kind":%q,"metadata":{"name":%q,"resourceVersion":%q},"spec":{"size":"large"}}}`,
		kind, testAPI, testKind, name, version)
}

// Whether firstReadDone delivers nil depends on which events the
// handler has received and on what the store has, in either order of
// the informer's goroutines. Each step is one call: firstReadDone with
// an empty store or with the object in the store, onChange with an
// object that converts or does not, or onDelete.
func TestAWatchOfOneObjectDeliversNilOnlyWhenNoEventArrived(t *testing.T) {
	cases := []struct {
		name  string
		steps []string
		want  []string
	}{
		{"an empty first read", []string{"first read, empty"}, []string{"absent"}},
		{"an object in the first read", []string{"change", "first read, stored"}, []string{"1"}},
		{"an object in the first read that does not convert", []string{"bad change", "first read, stored"}, nil},
		{"an object stored after the read, before its event", []string{"first read, stored", "change"}, []string{"1"}},
		{"an object created after an empty read", []string{"first read, empty", "change"}, []string{"absent", "1"}},
		{"an object deleted before the first read ends", []string{"change", "delete", "first read, empty"}, []string{"1", "absent"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen := &reports{}
			report := &oneReport[thing]{what: "the test thing", deliver: seen.record}
			for _, step := range c.steps {
				switch step {
				case "first read, empty":
					report.firstReadDone(func() bool { return true })
				case "first read, stored":
					report.firstReadDone(func() bool { return false })
				case "change":
					report.onChange(asObject(t, sized("a", "7", 1)))
				case "bad change":
					report.onChange("not an object")
				case "delete":
					report.onDelete()
				}
			}
			if got := seen.all(); !slices.Equal(got, c.want) {
				t.Errorf("reports = %q, want %q", got, c.want)
			}
		})
	}
}

func watchThing(t *testing.T, server *watchServer, seen *reports, synced func()) {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	watch := WatchOne(ctx, testWatcher(t, server), One{Resource: thingResource, Name: "a", What: "the thing a"}, seen.record, synced)
	t.Cleanup(func() {
		stop()
		<-watch.Done()
	})
}

// The first read delivers the object as it is, and each later change is
// delivered in order: an update, a deletion, and a new object. The
// watch selects the object by name, because RBAC checks a list and a
// watch against a Role's resourceNames only through that field
// selector.
func TestAWatchOfOneObjectDeliversEachChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{sized("a", "7", 1)}}, []string{
			event("MODIFIED", sized("a", "8", 2)),
			event("DELETED", sized("a", "9", 2)),
			event("ADDED", sized("a", "10", 3)),
			holdOpen,
		})
		seen := &reports{}
		watchThing(t, server, seen, nil)
		synctest.Wait()

		if got, want := seen.all(), []string{"1", "2", "absent", "3"}; !slices.Equal(got, want) {
			t.Errorf("reports = %q, want %q", got, want)
		}
		for _, query := range server.sent() {
			if query != "labelSelector= fieldSelector=metadata.name=a" {
				t.Errorf("a request used %q, want the field selector metadata.name=a", query)
			}
		}
	})
}

// An object absent from the first read has no event, so the watch
// delivers nil once that read is done, and then runs synced.
func TestAWatchOfOneObjectDeliversNilBeforeSynced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{}})
		seen := &reports{}
		synced := make(chan []string, 1)
		watchThing(t, server, seen, func() { synced <- seen.all() })

		if got, want := <-synced, []string{"absent"}; !slices.Equal(got, want) {
			t.Errorf("reports at synced = %q, want %q", got, want)
		}
	})
}

func TestAWatchOfOneObjectDeliversNilThenTheNewObject(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{}}, []string{awaitGate, event("ADDED", sized("a", "8", 5)), holdOpen})
		seen := &reports{}
		watchThing(t, server, seen, func() { close(server.gate) })
		synctest.Wait()

		if got, want := seen.all(), []string{"absent", "5"}; !slices.Equal(got, want) {
			t.Errorf("reports = %q, want %q", got, want)
		}
	})
}

func TestAWatchOfOneObjectSkipsAVersionThatDoesNotConvert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{sized("a", "7", 1)}}, []string{
			unconvertible("MODIFIED", "a", "8"),
			event("MODIFIED", sized("a", "9", 4)),
			holdOpen,
		})
		seen := &reports{}
		watchThing(t, server, seen, nil)
		synctest.Wait()

		if got, want := seen.all(), []string{"1", "4"}; !slices.Equal(got, want) {
			t.Errorf("reports = %q, want %q", got, want)
		}
	})
}

func TestAWatchOfOneObjectDeliversAnUnstructuredVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newWatchServer(thingsPath, [][]thing{{}}, []string{unconvertible("ADDED", "a", "8"), holdOpen})
		var mu sync.Mutex
		var sizes []any
		ctx, stop := context.WithCancel(t.Context())
		watch := WatchOne(ctx, testWatcher(t, server), One{Resource: thingResource, Name: "a", What: "the thing a"},
			func(object *unstructured.Unstructured) {
				mu.Lock()
				defer mu.Unlock()
				if object == nil {
					sizes = append(sizes, nil)
					return
				}
				size, _, _ := unstructured.NestedFieldNoCopy(object.Object, "spec", "size")
				sizes = append(sizes, size)
			}, nil)
		t.Cleanup(func() {
			stop()
			<-watch.Done()
		})
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		if len(sizes) == 0 || sizes[len(sizes)-1] != "large" {
			t.Errorf("the owner received sizes %v, want the last to be large", sizes)
		}
	})
}
