package main

// The watch loop against a real streaming server: events arrive,
// bookmarks advance the resume point silently, and a dropped stream
// recovers through a fresh GET.

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
// hangs up on later reconnects, and answers the recovery GET that
// follows a drop. Every watch request's URL lands on the paths
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
		_ = json.NewEncoder(w).Encode(&machine.Machine{
			Kind:     "Machine",
			Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "99"},
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
	go watchMachine(client, "node-1", "1", events)

	// The MODIFIED event arrives; the BOOKMARK does not (it only
	// advances the resume point).
	first := <-events
	if first.Metadata.ResourceVersion != "7" {
		t.Errorf("got %s", first.Metadata.ResourceVersion)
	}

	// The stream then ends, an ordinary drop, and the loop recovers
	// with a fresh GET, whose answer also lands as an event.
	second := <-events
	if second.Metadata.ResourceVersion != "99" {
		t.Errorf("the recovery GET's copy should arrive: %s", second.Metadata.ResourceVersion)
	}
}

func TestWatchResumesFromTheFreshestVersion(t *testing.T) {
	api := &watchAPI{stream: []string{
		watchEvent(t, "BOOKMARK", "node-1", "42"),
	}}
	client := testClient(t, api.handler())

	events := make(chan *machine.Machine, 4)
	go watchMachine(client, "node-1", "1", events)

	if first := <-api.paths; !strings.Contains(first, "resourceVersion=1") {
		t.Errorf("the first watch starts where the caller said: %s", first)
	}
	// The bookmark advanced the resume point, the stream dropped, and
	// the recovery GET answered with version 99: the reconnect must
	// carry the freshest of those, not the version it started with.
	if second := <-api.paths; !strings.Contains(second, "resourceVersion=99") {
		t.Errorf("the reconnect should resume from the freshest version: %s", second)
	}
}

// TestMain silences retryPause for the whole test binary, exactly
// once: watchMachine loops forever by design (a crash-only daemon
// has no shutdown path), so the goroutines these tests start outlive
// them, and any later write to the seam would race a live loop
// reading it. No test wants the real five-second pause anyway.
func TestMain(m *testing.M) {
	retryPause = func() {}
	os.Exit(m.Run())
}
