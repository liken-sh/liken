package informer

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// reports records what a watch of one object reported, in order: the
// size of each copy, and "absent" for a report that the object does
// not exist.
type reports struct {
	mu   sync.Mutex
	said []string
}

func (r *reports) see(held *thing) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if held == nil {
		r.said = append(r.said, "absent")
		return
	}
	r.said = append(r.said, fmt.Sprint(held.Spec.Size))
}

func (r *reports) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.said)
}

func sized(name, version string, size int) thing {
	item := newThing(name, version, 1)
	item.Spec.Size = size
	return item
}

// unconvertible is a watch event whose object has text where the struct
// holds a number.
func unconvertible(kind, name, version string) string {
	return fmt.Sprintf(`{"type":%q,"object":{"apiVersion":%q,"kind":%q,"metadata":{"name":%q,"resourceVersion":%q},"spec":{"size":"large"}}}`,
		kind, testAPI, testKind, name, version)
}

// The report after the first read depends on what the handler took and
// on what the store holds, in whatever order the informer's two
// goroutines run. Each step is one call: "read empty" and "read held"
// are the end of the first read with an empty store or a store that
// holds the object, "take 1" and "take bad" are an add or an update
// with a copy that converts or does not, and "gone" is a deletion.
func TestAWatchOfOneObjectReportsAbsenceOnlyWhenNothingArrived(t *testing.T) {
	cases := []struct {
		name  string
		steps []string
		want  []string
	}{
		{"an empty first read", []string{"read empty"}, []string{"absent"}},
		{"an object in the first read", []string{"take 1", "read held"}, []string{"1"}},
		{"an object in the first read that does not convert", []string{"take bad", "read held"}, nil},
		{"an object stored after the read, before its event", []string{"read held", "take 1"}, []string{"1"}},
		{"an object that arrives after an empty read", []string{"read empty", "take 1"}, []string{"absent", "1"}},
		{"an object deleted before the read's report", []string{"take 1", "gone", "read empty"}, []string{"1", "absent"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen := &reports{}
			report := &oneReport[thing]{what: "the test thing", seen: seen.see}
			for _, step := range c.steps {
				switch step {
				case "read empty":
					report.firstRead(func() bool { return true })
				case "read held":
					report.firstRead(func() bool { return false })
				case "take 1":
					report.take(asObject(t, sized("a", "7", 1)))
				case "take bad":
					report.take("not an object")
				case "gone":
					report.gone()
				}
			}
			if got := seen.all(); !slices.Equal(got, c.want) {
				t.Errorf("reports = %q, want %q", got, c.want)
			}
		})
	}
}

// watchThing watches the thing named a until the test ends.
func watchThing(t *testing.T, server *watchServer, seen *reports, synced func()) {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	watch := WatchOne(ctx, testWatcher(t, server), One{Resource: thingResource, Name: "a", What: "the thing a"}, seen.see, synced)
	t.Cleanup(func() {
		stop()
		<-watch.Done()
	})
}

// The first read reports the object as it stands, and each change after
// it reaches the owner in order: an update, a deletion, and a new
// object. The watch asks for the one object by name, because RBAC
// matches a list and a watch against a Role's resourceNames only
// through that field selector.
func TestAWatchOfOneObjectReportsEachChange(t *testing.T) {
	server := newWatchServer(thingsPath, [][]thing{{sized("a", "7", 1)}}, []string{
		event("MODIFIED", sized("a", "8", 2)),
		event("DELETED", sized("a", "9", 2)),
		event("ADDED", sized("a", "10", 3)),
		holdOpen,
	})
	seen := &reports{}
	watchThing(t, server, seen, nil)

	eventually(t, "the owner takes each change", func() bool { return len(seen.all()) == 4 })
	if got, want := seen.all(), []string{"1", "2", "absent", "3"}; !slices.Equal(got, want) {
		t.Errorf("reports = %q, want %q", got, want)
	}
	for _, query := range server.sent() {
		if query != "labelSelector= fieldSelector=metadata.name=a" {
			t.Errorf("a request asked for %q, want the field selector metadata.name=a", query)
		}
	}
}

// An object absent from the first read has no event, so the watch
// reports it absent once that read is done, and then runs synced.
func TestAWatchOfOneObjectReportsAnAbsentObjectBeforeSynced(t *testing.T) {
	server := newWatchServer(thingsPath, [][]thing{{}})
	seen := &reports{}
	synced := make(chan []string, 1)
	watchThing(t, server, seen, func() { synced <- seen.all() })

	if got, want := <-synced, []string{"absent"}; !slices.Equal(got, want) {
		t.Errorf("reports at synced = %q, want %q", got, want)
	}
}

// An object absent from the first read is reported absent, and when
// it is created after that read, the owner takes it.
func TestAWatchOfOneObjectReportsAnAbsentObjectAndItsArrival(t *testing.T) {
	server := newWatchServer(thingsPath, [][]thing{{}}, []string{awaitGate, event("ADDED", sized("a", "8", 5)), holdOpen})
	seen := &reports{}
	watchThing(t, server, seen, func() { close(server.gate) })

	eventually(t, "the owner takes the new object", func() bool { return len(seen.all()) == 2 })
	if got, want := seen.all(), []string{"absent", "5"}; !slices.Equal(got, want) {
		t.Errorf("reports = %q, want %q", got, want)
	}
}

// A copy that does not convert is logged, and the owner keeps the copy
// it holds, because the next version of the object can be valid.
func TestAWatchOfOneObjectSkipsACopyThatDoesNotConvert(t *testing.T) {
	server := newWatchServer(thingsPath, [][]thing{{sized("a", "7", 1)}}, []string{
		unconvertible("MODIFIED", "a", "8"),
		event("MODIFIED", sized("a", "9", 4)),
		holdOpen,
	})
	seen := &reports{}
	watchThing(t, server, seen, nil)

	eventually(t, "the owner takes the valid copy", func() bool { return len(seen.all()) == 2 })
	if got, want := seen.all(), []string{"1", "4"}; !slices.Equal(got, want) {
		t.Errorf("reports = %q, want %q", got, want)
	}
}

// An owner that reads the object itself takes it as Unstructured, which
// every copy converts to.
func TestAWatchOfOneObjectHandsOverAnUnstructuredCopy(t *testing.T) {
	server := newWatchServer(thingsPath, [][]thing{{}}, []string{unconvertible("ADDED", "a", "8"), holdOpen})
	var mu sync.Mutex
	var sizes []any
	ctx, stop := context.WithCancel(t.Context())
	watch := WatchOne(ctx, testWatcher(t, server), One{Resource: thingResource, Name: "a", What: "the thing a"},
		func(held *unstructured.Unstructured) {
			mu.Lock()
			defer mu.Unlock()
			if held == nil {
				sizes = append(sizes, nil)
				return
			}
			size, _, _ := unstructured.NestedFieldNoCopy(held.Object, "spec", "size")
			sizes = append(sizes, size)
		}, nil)
	t.Cleanup(func() {
		stop()
		<-watch.Done()
	})

	eventually(t, "the owner takes the copy", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sizes) > 0 && sizes[len(sizes)-1] == "large"
	})
}
