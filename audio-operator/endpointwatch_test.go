package main

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/tools/cache"
)

// sinkAt returns this machine's Sink at a resource version and a
// generation, in the form the API server sends it.
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

func deleting(sink map[string]any) map[string]any {
	sink["metadata"].(map[string]any)["deletionTimestamp"] = "2026-09-27T12:00:00Z"
	return sink
}

// recreated gives a Sink a new UID, as deleting and creating a Sink of
// the same name does.
func recreated(sink map[string]any) map[string]any {
	sink["metadata"].(map[string]any)["uid"] = "9e4d2b7a-1c3f-4a6e-8b5d-0f2a7c9e1b63"
	return sink
}

func sinkEvent(t *testing.T, kind string, sink map[string]any) string {
	t.Helper()
	return encode(t, map[string]any{"type": kind, "object": sink})
}

// updated, added, and removed each build the call that the informer
// makes on a handler for one kind of event, so a table can list them.
func updated(before, after any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnUpdate(before, after) }
}

func added(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnAdd(item, false) }
}

func removed(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnDelete(item) }
}

// Each update case starts from an old copy at generation 1.
func TestAChangeWakesTheLoopOnlyForAnEdit(t *testing.T) {
	original := asObject(t, sinkAt("1", 1))
	mistyped := asObject(t, sinkAt("2", 2))
	mistyped.Object["spec"] = map[string]any{"volume": "loud"}
	cases := []struct {
		name    string
		deliver func(cache.ResourceEventHandler)
		want    bool
	}{
		{name: "a status write", deliver: updated(original, asObject(t, sinkAt("2", 1))), want: false},
		{name: "a spec edit", deliver: updated(original, asObject(t, sinkAt("2", 2))), want: true},
		{name: "a deletion request", deliver: updated(original, asObject(t, deleting(sinkAt("2", 1)))), want: true},
		{name: "a resource deleted and created again with the same name",
			deliver: updated(original, asObject(t, recreated(sinkAt("2", 1)))), want: true},
		{name: "an old copy that is not an object", deliver: updated("not an object", original), want: true},
		{name: "an edit that does not convert", deliver: updated(original, mistyped), want: false},
		{name: "a resource that entered the selection", deliver: added(original), want: true},
		{name: "an object that does not convert", deliver: added("not an object"), want: false},
		{name: "a resource that left the selection", deliver: removed(original), want: true},
		{name: "a resource removed while the watch was down",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: testSinkName, Obj: original}), want: true},
		{name: "a removal whose tombstone holds no copy",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: testSinkName}), want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			woke := false
			c.deliver(editHandler[Sink]{what: "the Sinks", wake: func() { woke = true }}.handler())
			if woke != c.want {
				t.Errorf("the handler woke the loop: %v, want %v", woke, c.want)
			}
		})
	}
}

// asking sets status.session.volumeAsk on a Sink, with the given time
// in its at field.
func asking(sink map[string]any, at string) map[string]any {
	sink["status"].(map[string]any)["session"] = map[string]any{
		"player":    "media/den",
		"volumeAsk": map[string]any{"level": int64(30), "at": at},
	}
	return sink
}

// A new status.session.volumeAsk.at calls onVolumeAsk and does not wake
// the loop. Any other status write does not call onVolumeAsk.
func TestANewVolumeAskCallsOnVolumeAskAndNothingElse(t *testing.T) {
	const first, second = "2026-10-04T12:15:25.164Z", "2026-10-04T12:15:25.264Z"
	original := asObject(t, asking(sinkAt("1", 1), first))
	cases := []struct {
		name    string
		deliver func(cache.ResourceEventHandler)
		asked   bool
	}{
		{name: "a new request", deliver: updated(original, asObject(t, asking(sinkAt("2", 1), second))), asked: true},
		{name: "a first request", deliver: updated(asObject(t, sinkAt("1", 1)), original), asked: true},
		{name: "a status write that keeps the request", deliver: updated(original, asObject(t, asking(sinkAt("2", 1), first)))},
		{name: "a session with no request", deliver: updated(original, asObject(t, sinkAt("2", 1)))},
		{name: "a Sink that enters the selection with a request", deliver: added(original)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asked, woke := false, false
			c.deliver(sinkHandler{
				edits:       editHandler[Sink]{what: "the Sinks", wake: func() { woke = true }},
				onVolumeAsk: func() { asked = true },
			}.handler())
			if asked != c.asked {
				t.Errorf("the handler called onVolumeAsk: %v, want %v", asked, c.asked)
			}
			if woke && c.asked {
				t.Error("a new request woke the pass, want only a call of onVolumeAsk")
			}
		})
	}
}

// Sources are watched as well as Sinks, because a person sets a
// microphone's resting state the same way as a speaker's.
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

// wakeCount removes the wakes queued on the channel and returns how
// many there were.
func wakeCount(wakes chan struct{}) int {
	count := 0
	for len(wakes) > 0 {
		<-wakes
		count++
	}
	return count
}

// Through a real reflector, each watch wakes the loop when its first
// list finishes, even an empty list. Then a status write does not wake
// the loop and a spec edit does.
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
		// The wakes are the Sink's add and the first list of each watch.
		if got := wakeCount(wakes); got != 3 {
			t.Errorf("the first reads woke the loop %d times, want 3", got)
		}

		sinks.release()
		synctest.Wait()
		if got := wakeCount(wakes); got != 0 {
			t.Errorf("the operator's own status write woke the loop %d times, want 0", got)
		}
		sinks.release()
		synctest.Wait()
		if got := wakeCount(wakes); got != 1 {
			t.Errorf("the spec edit woke the loop %d times, want 1", got)
		}
	})
}

// Through a real reflector, an edit made while the watch was down
// reaches the handler when the informer lists the collection again, and
// a status write made in the same gap does not wake the loop.
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

// Through a real reflector, a Sink that enters the selection and a Sink
// that leaves it each wake the loop.
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
