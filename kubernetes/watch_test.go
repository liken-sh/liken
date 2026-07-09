package kubernetes

// The watch loop against a real streaming server: events arrive as
// the server sends them, bookmarks advance the resume point
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
// resume point. Every watch request's URL lands on the paths
// channel, which is how a test observes the resume points without
// racing the handler.
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
	go WatchMachines(client, "", "1", events)

	// The MODIFIED event arrives; the BOOKMARK does not (it only
	// advances the resume point).
	first := <-events
	if first.Metadata.ResourceVersion != "7" {
		t.Errorf("got %s", first.Metadata.ResourceVersion)
	}

	// The stream then ends, an ordinary drop, and the loop recovers
	// with a fresh list; the list's items land as events so the
	// caller's working copies are refreshed.
	second := <-events
	if second.Metadata.Name != "node-1" || second.Metadata.ResourceVersion != "50" {
		t.Errorf("the recovery list's items should arrive: %s@%s",
			second.Metadata.Name, second.Metadata.ResourceVersion)
	}
	third := <-events
	if third.Metadata.Name != "node-2" {
		t.Errorf("every item recovers, not just one machine's: %s", third.Metadata.Name)
	}
}

func TestWatchWithoutASelectorSpansTheCollection(t *testing.T) {
	// The cluster operator derives the Cluster's status from every
	// Machine, so its watch carries no fieldSelector and another
	// machine's event is delivered like any other.
	api := &watchAPI{stream: []string{
		watchEvent(t, "MODIFIED", "node-2", "7"),
	}}
	client := testClient(t, api.handler())

	events := make(chan *machine.Machine, 4)
	go WatchMachines(client, "", "1", events)

	if path := <-api.paths; strings.Contains(path, "fieldSelector") {
		t.Errorf("no selector was asked for: %s", path)
	}
	first := <-events
	if first.Metadata.Name != "node-2" {
		t.Errorf("another machine's event should be delivered: %s", first.Metadata.Name)
	}
}

func TestWatchCarriesTheFieldSelector(t *testing.T) {
	// The machine operator narrows its watch to its own object; the
	// server does the filtering, so all the client sends is the
	// selector, escaped into the query string.
	api := &watchAPI{stream: []string{
		watchEvent(t, "MODIFIED", "node-1", "7"),
	}}
	client := testClient(t, api.handler())

	events := make(chan *machine.Machine, 4)
	go WatchMachines(client, "metadata.name=node-1", "1", events)

	if path := <-api.paths; !strings.Contains(path, "fieldSelector=metadata.name%3Dnode-1") {
		t.Errorf("the watch should carry the selector: %s", path)
	}
	<-events
}

func TestWatchResumesFromTheListsVersion(t *testing.T) {
	api := &watchAPI{stream: []string{
		watchEvent(t, "BOOKMARK", "node-1", "42"),
	}}
	client := testClient(t, api.handler())

	events := make(chan *machine.Machine, 4)
	go WatchMachines(client, "", "1", events)

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

// TestMain silences RetryPause for the whole test binary, exactly
// once: WatchMachines loops forever by design (a crash-only daemon
// has no shutdown path), so the goroutines these tests start outlive
// them, and any later write to the seam would race a live loop
// reading it. No test wants the real five-second pause anyway.
func TestMain(m *testing.M) {
	RetryPause = func() {}
	os.Exit(m.Run())
}
