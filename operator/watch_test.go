package main

// The watch loop against a real streaming server: events arrive for
// every machine in the fleet, bookmarks advance the resume point
// silently, and a dropped stream recovers through a fresh list.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// watchAPI serves one watch stream (each entry is one event line),
// hangs up on later reconnects, and answers the recovery list that
// follows a drop: a MachineList whose own resourceVersion is the
// resume point, carrying node-1 among its items. Every watch
// request's URL lands on the paths channel, which is how a test
// observes the resume points without racing the handler.
type watchAPI struct {
	stream  []string
	watches atomic.Int32
	paths   chan string
}

func (api *watchAPI) handler() http.Handler {
	api.paths = make(chan string, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			api.paths <- r.URL.String()
			if api.watches.Add(1) > 1 {
				return
			}
			for _, event := range api.stream {
				_, _ = w.Write([]byte(event + "\n"))
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":     "MachineList",
			"metadata": map[string]any{"resourceVersion": "99"},
			"items": []machine.Machine{
				{Kind: "Machine", Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "50"}},
				{Kind: "Machine", Metadata: machine.ObjectMeta{Name: "node-2", ResourceVersion: "51"}},
			},
		})
	})
}

func watchEvent(t *testing.T, eventType, name, resourceVersion string) string {
	t.Helper()
	event := map[string]any{
		"type": eventType,
		"object": &machine.Machine{
			Kind:     "Machine",
			Metadata: machine.ObjectMeta{Name: name, ResourceVersion: resourceVersion},
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestWatchDeliversEventsAndRecoversFromADrop(t *testing.T) {
	api := &watchAPI{stream: []string{
		watchEvent(t, "MODIFIED", "node-1", "7"),
		watchEvent(t, "BOOKMARK", "node-1", "8"),
	}}
	client := testClient(t, api.handler())

	events := make(chan *machine.Machine, 4)
	go watchMachines(client, "node-1", "1", events)

	// The MODIFIED event arrives; the BOOKMARK does not (it only
	// advances the resume point).
	first := <-events
	if first.Metadata.ResourceVersion != "7" {
		t.Errorf("got %s", first.Metadata.ResourceVersion)
	}

	// The stream then ends, an ordinary drop, and the loop recovers
	// with a fresh list; this machine's own copy from that list lands
	// as an event so the caller's copy is refreshed.
	second := <-events
	if second.Metadata.Name != "node-1" || second.Metadata.ResourceVersion != "50" {
		t.Errorf("the recovery list's copy of this machine should arrive: %s@%s",
			second.Metadata.Name, second.Metadata.ResourceVersion)
	}
}

func TestWatchSpansTheWholeFleet(t *testing.T) {
	// The sweeping leader derives the Cluster's status from every
	// Machine, so the loop must wake when any machine changes, not
	// just its own: the watch carries no fieldSelector, and another
	// machine's event is delivered like any other.
	api := &watchAPI{stream: []string{
		watchEvent(t, "MODIFIED", "node-2", "7"),
	}}
	client := testClient(t, api.handler())

	events := make(chan *machine.Machine, 4)
	go watchMachines(client, "node-1", "1", events)

	if path := <-api.paths; strings.Contains(path, "fieldSelector") {
		t.Errorf("the watch should span the collection: %s", path)
	}
	first := <-events
	if first.Metadata.Name != "node-2" {
		t.Errorf("another machine's event should be delivered: %s", first.Metadata.Name)
	}
}

func TestWatchResumesFromTheListsVersion(t *testing.T) {
	api := &watchAPI{stream: []string{
		watchEvent(t, "BOOKMARK", "node-1", "42"),
	}}
	client := testClient(t, api.handler())

	events := make(chan *machine.Machine, 4)
	go watchMachines(client, "node-1", "1", events)

	if first := <-api.paths; !strings.Contains(first, "resourceVersion=1") {
		t.Errorf("the first watch starts where the caller said: %s", first)
	}
	// The bookmark advanced the resume point, the stream dropped, and
	// the recovery list answered at version 99. The reconnect must
	// resume from the list's version, not any single object's: an
	// object's resourceVersion is the revision of its own last write,
	// which on a quiet object can be old enough to have been
	// compacted away, and a watch from a compacted version is refused
	// with 410 Gone.
	if second := <-api.paths; !strings.Contains(second, "resourceVersion=99") {
		t.Errorf("the reconnect should resume from the list's version: %s", second)
	}
}

func TestDrainEventsCoalescesABurstAndKeepsTheNewestOwnCopy(t *testing.T) {
	// A burst of fleet events queued during a pass collapses into the
	// single pass that follows, and only this machine's own newest
	// copy replaces the working copy.
	events := make(chan *machine.Machine, 4)
	events <- &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-2", ResourceVersion: "5"}}
	events <- &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "6"}}
	events <- &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-3", ResourceVersion: "7"}}

	current := &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "2"}}
	current = drainEvents(events, "node-1", current)
	if current.Metadata.ResourceVersion != "6" {
		t.Errorf("the newest own copy wins: %s", current.Metadata.ResourceVersion)
	}
	if len(events) != 0 {
		t.Errorf("the burst is fully drained: %d left", len(events))
	}
}

func TestDrainEventsLeavesTheWorkingCopyWhenOnlyOthersChanged(t *testing.T) {
	events := make(chan *machine.Machine, 4)
	events <- &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-2", ResourceVersion: "5"}}

	current := &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "2"}}
	current = drainEvents(events, "node-1", current)
	if current.Metadata.ResourceVersion != "2" {
		t.Errorf("another machine's event must not replace the working copy: %s", current.Metadata.ResourceVersion)
	}
}

// TestMain silences retryPause for the whole test binary, exactly
// once: watchMachines loops forever by design (a crash-only daemon
// has no shutdown path), so the goroutines these tests start outlive
// them, and any later write to the seam would race a live loop
// reading it. No test wants the real five-second pause anyway.
func TestMain(m *testing.M) {
	retryPause = func() {}
	os.Exit(m.Run())
}
