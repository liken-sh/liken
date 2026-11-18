package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// eventedControl returns an endpoint controller whose Events the
// returned eventstest.Events records. Call it inside a synctest bubble,
// because the Event recorder writes from its own goroutine, and
// synctest.Wait returns once that goroutine has written its queue.
func eventedControl(t *testing.T, api http.Handler) (*endpointControl, *eventstest.Events) {
	t.Helper()
	recorded := &eventstest.Events{}
	control := controlOver(t, recorded.Around(api), &writeRecord{})
	control.recorder = events.New(t.Context(), control.client, operatorComponent, events.Options{Log: io.Discard})
	return control, recorded
}

type eventLine struct {
	Type, Reason string
	Count        int32
}

func eventLines(posted []events.Event) []eventLine {
	lines := make([]eventLine, 0, len(posted))
	for _, event := range posted {
		lines = append(lines, eventLine{event.Type, event.Reason, event.Count})
	}
	return lines
}

// The LayoutChanged Event names the Sink's UID, so kubectl describe sink
// shows it.
func TestASpecLayoutRewritesTheDeclarationAndPostsLayoutChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, endpoints := layoutReconciler(t)
		analog := endpoints[1]
		quad := []string{"FL", "FR", "RL", "RR"}
		server := &sinkServer{sinks: map[string]Sink{
			analog.Name(): {Metadata: EndpointMeta{Name: analog.Name(), UID: "uid-1"}, Spec: SinkSpec{Layout: quad}},
		}}
		control, recorded := eventedControl(t, server.handler())
		operator.control = control

		states := operator.reconcileLayouts(endpoints, pwGraph{})
		want := channelLayout{Source: layoutFromSpec, Positions: quad}
		if state := states[analog.Name()]; !state.Layout.equal(want) {
			t.Errorf("the analog sink reports %s, want %s", state.Layout, want)
		}
		synctest.Wait()
		posted := recorded.About(SinkKind, analog.Name())
		if len(posted) != 1 {
			t.Fatalf("events = %+v, want one", posted)
		}
		event := posted[0]
		if event.Type != events.TypeNormal || event.Reason != reasonLayoutChanged || event.InvolvedObject.UID != "uid-1" ||
			!strings.Contains(event.Message, "from no positions to FL,FR,RL,RR from Spec") {
			t.Errorf("the event is %+v, want a Normal LayoutChanged on uid-1 that names both layouts", event)
		}
	})
}

// The operator posts one LayoutWriteFailed Warning for a run of passes
// that fail with the same error, not one for each pass.
func TestALayoutThatDoesNotWritePostsOneWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, endpoints := layoutReconciler(t)
		analog := endpoints[1]
		server := &sinkServer{sinks: map[string]Sink{
			analog.Name(): {Metadata: EndpointMeta{Name: analog.Name()}, Spec: SinkSpec{Layout: []string{"FL", "FR", "RL", "RR"}}},
		}}
		control, recorded := eventedControl(t, server.handler())
		operator.control = control
		// A regular file where the directory should be makes every
		// write of the drop-in fail.
		blocked := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(blocked, nil, 0o644); err != nil {
			t.Fatalf("writing a file at the drop-in directory's path: %v", err)
		}
		pipewireConfigDir = blocked

		for range 3 {
			if state := operator.reconcileLayouts(endpoints, pwGraph{})[analog.Name()]; state.Reason != layoutReasonAwaitingIdle {
				t.Fatalf("the analog sink reports %+v, want AwaitingIdle", state)
			}
		}
		synctest.Wait()
		want := []eventLine{{events.TypeWarning, reasonLayoutWriteFailed, 1}}
		if got := eventLines(recorded.About(SinkKind, analog.Name())); !slices.Equal(got, want) {
			t.Errorf("events = %+v, want %+v", got, want)
		}
	})
}
