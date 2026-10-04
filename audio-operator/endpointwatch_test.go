package main

// These tests cover what wakes the loop for a Sink or a Source: a spec
// edit, a deletion request, a resource that enters or leaves this
// machine's selection, and the end of each watch's first read. A
// status write does not.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/tools/cache"
)

// sinkAt is this machine's Sink at a resource version and a generation,
// as the API server sends it.
func sinkAt(version string, generation int64) map[string]any {
	return map[string]any{
		"apiVersion": EndpointAPIVersion,
		"kind":       SinkKind,
		"metadata": map[string]any{
			"name":            testSinkName,
			"uid":             "3b1f6c2e-0d4a-4e8b-9c7f-5a2e1d6b8f40",
			"resourceVersion": version,
			"generation":      generation,
		},
		"status": map[string]any{"node": "liken-1"},
	}
}

// deleting marks a Sink as being deleted.
func deleting(sink map[string]any) map[string]any {
	sink["metadata"].(map[string]any)["deletionTimestamp"] = "2026-09-27T12:00:00Z"
	return sink
}

// recreated gives a Sink another UID, as a delete and a create of the
// same name do.
func recreated(sink map[string]any) map[string]any {
	sink["metadata"].(map[string]any)["uid"] = "9e4d2b7a-1c3f-4a6e-8b5d-0f2a7c9e1b63"
	return sink
}

// sinkEvent is one watch event for a Sink.
func sinkEvent(t *testing.T, kind string, sink map[string]any) string {
	t.Helper()
	return encode(t, map[string]any{"type": kind, "object": sink})
}

// The informer calls a handler in one of three ways. Each builds the
// call for one case of a table.
func updated(before, after any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnUpdate(before, after) }
}

func added(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnAdd(item, false) }
}

func removed(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnDelete(item) }
}

// Each update starts from a held copy at generation 1.
func TestAChangeWakesTheLoopOnlyForAnEdit(t *testing.T) {
	held := asObject(t, sinkAt("1", 1))
	mistyped := asObject(t, sinkAt("2", 2))
	mistyped.Object["spec"] = map[string]any{"volume": "loud"}
	cases := []struct {
		name    string
		deliver func(cache.ResourceEventHandler)
		want    bool
	}{
		{name: "a status write", deliver: updated(held, asObject(t, sinkAt("2", 1))), want: false},
		{name: "a spec edit", deliver: updated(held, asObject(t, sinkAt("2", 2))), want: true},
		{name: "a deletion request", deliver: updated(held, asObject(t, deleting(sinkAt("2", 1)))), want: true},
		{name: "a resource deleted and created again with the same name",
			deliver: updated(held, asObject(t, recreated(sinkAt("2", 1)))), want: true},
		{name: "a held copy that is not an object", deliver: updated("not an object", held), want: true},
		{name: "an edit that does not convert", deliver: updated(held, mistyped), want: false},
		{name: "a resource that entered the selection", deliver: added(held), want: true},
		{name: "an object that does not convert", deliver: added("not an object"), want: false},
		{name: "a resource that left the selection", deliver: removed(held), want: true},
		{name: "a resource removed while the watch was down",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: testSinkName, Obj: held}), want: true},
		{name: "a removal whose tombstone holds no copy",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: testSinkName}), want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			woke := false
			c.deliver(editHandler[Sink]{what: "the Sinks", wake: func() { woke = true }}.handler())
			if woke != c.want {
				t.Errorf("woke = %v, want %v", woke, c.want)
			}
		})
	}
}

// asking gives a Sink a volume ask at a time.
func asking(sink map[string]any, at string) map[string]any {
	sink["status"].(map[string]any)["session"] = map[string]any{
		"player":    "media/den",
		"volumeAsk": map[string]any{"level": int64(30), "at": at},
	}
	return sink
}

// A new volume ask is a status write, which changes no generation, so
// the Sinks' handler compares the time of the ask as well. A new time
// calls asked and wakes no pass. Any other status write calls nothing.
func TestANewVolumeAskCallsAskedAndNothingElse(t *testing.T) {
	const first, second = "2026-10-04T12:15:25.164Z", "2026-10-04T12:15:25.264Z"
	held := asObject(t, asking(sinkAt("1", 1), first))
	cases := []struct {
		name    string
		deliver func(cache.ResourceEventHandler)
		asked   bool
	}{
		{name: "a new ask", deliver: updated(held, asObject(t, asking(sinkAt("2", 1), second))), asked: true},
		{name: "a first ask", deliver: updated(asObject(t, sinkAt("1", 1)), held), asked: true},
		{name: "a status write that keeps the ask", deliver: updated(held, asObject(t, asking(sinkAt("2", 1), first)))},
		{name: "a session with no ask", deliver: updated(held, asObject(t, sinkAt("2", 1)))},
		{name: "a Sink that enters the selection with an ask", deliver: added(held)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asked, woke := false, false
			c.deliver(sinkHandler{
				edits: editHandler[Sink]{what: "the Sinks", wake: func() { woke = true }},
				asked: func() { asked = true },
			}.handler())
			if asked != c.asked {
				t.Errorf("asked = %v, want %v", asked, c.asked)
			}
			if woke && c.asked {
				t.Error("an ask woke the pass")
			}
		})
	}
}

// Both collections are watched, because a person declares how a
// microphone rests as much as a speaker. The list and the watch select
// the resources whose status.node is this machine: an unselected watch
// wakes every machine's operator for a write to any Sink in the
// cluster.
func TestTheWatchSelectsThisMachinesResourcesInBothCollections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sinks := newWatchServer(SinksPath, EndpointAPIVersion, SinkKind, []string{`[]`})
		sources := newWatchServer(SourcesPath, EndpointAPIVersion, SourceKind, []string{`[]`})
		watchEndpoints(t.Context(), testWatcher(t, serveCollections(t, nil, sinks, sources)),
			"liken-1", func() {}, func() {}, nil)
		synctest.Wait()

		for _, server := range []*watchServer{sinks, sources} {
			if len(server.requests()) == 0 {
				t.Errorf("the watch sent no request for %s", server.collection)
			}
			for _, query := range server.requests() {
				if !strings.Contains(query, "fieldSelector=status.node%3Dliken-1") {
					t.Errorf("the request %s?%s does not select this machine's resources", server.collection, query)
				}
			}
		}
	})
}

// wakeCount drains the wakes that have arrived, and answers how many
// there were.
func wakeCount(wakes chan struct{}) int {
	count := 0
	for len(wakes) > 0 {
		<-wakes
		count++
	}
	return count
}

// Through the reflector: each watch wakes the loop when its first read
// is done, even an empty one, and then a spec edit wakes it while the
// operator's own status write before it does not.
func TestASpecEditWakesTheLoopAndAStatusWriteDoesNot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sinks := newWatchServer(SinksPath, EndpointAPIVersion, SinkKind,
			[]string{"[" + encode(t, sinkAt("1", 1)) + "]"},
			[]string{pause, sinkEvent(t, "MODIFIED", sinkAt("2", 1)), pause,
				sinkEvent(t, "MODIFIED", sinkAt("3", 2)), holdOpen})
		sources := newWatchServer(SourcesPath, EndpointAPIVersion, SourceKind, []string{`[]`})
		wakes := make(chan struct{}, 8)
		watchEndpoints(t.Context(), testWatcher(t, serveCollections(t, nil, sinks, sources)),
			"liken-1", func() { wakes <- struct{}{} }, func() {}, nil)
		synctest.Wait()
		// The Sink's add and the two syncs.
		if got := wakeCount(wakes); got != 3 {
			t.Errorf("the first reads woke the loop %d times, want 3", got)
		}

		sinks.release()
		synctest.Wait()
		if got := wakeCount(wakes); got != 0 {
			t.Errorf("the operator's own status write woke the loop %d times", got)
		}
		sinks.release()
		synctest.Wait()
		if got := wakeCount(wakes); got != 1 {
			t.Errorf("the spec edit woke the loop %d times, want 1", got)
		}
	})
}

// Through the reflector: an edit made while the watch was down reaches
// the handler when the informer reads the collection again, and a
// status write made in the same gap does not wake the loop.
func TestAChangeWhileTheWatchWasDownWakesTheLoopOnlyForAnEdit(t *testing.T) {
	for _, c := range []struct {
		name  string
		again map[string]any
		want  int
	}{
		{"a status write", sinkAt("150", 1), 0},
		{"a spec edit", sinkAt("150", 2), 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// The first watch ends with a 410, so the reflector reads
				// the collection again.
				expired := `{"type":"ERROR","object":{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Expired","code":410,"message":"too old resource version"}}`
				sinks := newWatchServer(SinksPath, EndpointAPIVersion, SinkKind,
					[]string{"[" + encode(t, sinkAt("1", 1)) + "]", "[" + encode(t, c.again) + "]"},
					[]string{pause, expired})
				sources := newWatchServer(SourcesPath, EndpointAPIVersion, SourceKind, []string{`[]`})
				wakes := make(chan struct{}, 8)
				watchEndpoints(t.Context(), testWatcher(t, serveCollections(t, nil, sinks, sources)),
					"liken-1", func() { wakes <- struct{}{} }, func() {}, nil)
				synctest.Wait()
				if got := wakeCount(wakes); got != 3 {
					t.Errorf("the first reads woke the loop %d times, want 3", got)
				}

				sinks.release()
				time.Sleep(time.Minute)
				synctest.Wait()
				if got := wakeCount(wakes); got != c.want {
					t.Errorf("the second read woke the loop %d times, want %d", got, c.want)
				}
			})
		})
	}
}

// Through the reflector: a Sink that enters the selection, and one
// that leaves it, each wake the loop.
func TestASinkThatEntersOrLeavesTheSelectionWakesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sinks := newWatchServer(SinksPath, EndpointAPIVersion, SinkKind, []string{`[]`},
			[]string{pause, sinkEvent(t, "ADDED", sinkAt("2", 1)), pause,
				sinkEvent(t, "DELETED", sinkAt("3", 1)), holdOpen})
		sources := newWatchServer(SourcesPath, EndpointAPIVersion, SourceKind, []string{`[]`})
		wakes := make(chan struct{}, 8)
		watchEndpoints(t.Context(), testWatcher(t, serveCollections(t, nil, sinks, sources)),
			"liken-1", func() { wakes <- struct{}{} }, func() {}, nil)
		synctest.Wait()
		if got := wakeCount(wakes); got != 2 {
			t.Errorf("the first reads woke the loop %d times, want 2", got)
		}

		sinks.release()
		synctest.Wait()
		if got := wakeCount(wakes); got != 1 {
			t.Errorf("the Sink that entered woke the loop %d times, want 1", got)
		}
		sinks.release()
		synctest.Wait()
		if got := wakeCount(wakes); got != 1 {
			t.Errorf("the Sink that left woke the loop %d times, want 1", got)
		}

		if slices.ContainsFunc(sinks.requests(), func(query string) bool { return !strings.Contains(query, "status.node") }) {
			t.Errorf("a request carried no selector: %v", sinks.requests())
		}
	})
}
