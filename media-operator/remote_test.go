package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBindingsFromEnvironment(t *testing.T) {
	t.Run("the operator compiled a keymap into the environment", func(t *testing.T) {
		t.Setenv(keymapVariable, `[{"type":1,"code":304,"value":1,"action":"pause"},{"type":3,"code":17,"value":-1,"action":"volume","amount":5}]`)
		bindings, err := bindingsFromEnvironment()
		mustSucceed(t, err)
		mustMatchAll(t, bindings, []compiledBinding{
			{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause},
			{EventType: evAbs, Code: 0x11, Value: -1, Action: actionVolume, Amount: 5},
		})
	})

	cases := []struct {
		name  string
		value string
	}{
		{name: "nothing set the variable", value: ""},
		{name: "a value that is not JSON", value: "BTN_SOUTH=pause"},
		{name: "a JSON object rather than a table", value: `{"type":1}`},
		{name: "a table with no rows", value: `[]`},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Setenv(keymapVariable, each.value)
			_, err := bindingsFromEnvironment()
			mustFail(t, err)
		})
	}
}

func TestCommandFor(t *testing.T) {
	cases := []struct {
		name    string
		binding compiledBinding
		want    []any
	}{
		{
			name:    "pause",
			binding: compiledBinding{Action: actionPause},
			want:    []any{"osd-auto", "cycle", "pause"},
		},
		{
			name:    "mute",
			binding: compiledBinding{Action: actionMute},
			want:    []any{"osd-auto", "cycle", "mute"},
		},
		{
			name:    "seek forward",
			binding: compiledBinding{Action: actionSeek, Amount: 30},
			want:    []any{"osd-auto", "seek", 30},
		},
		{
			name:    "seek back",
			binding: compiledBinding{Action: actionSeek, Amount: -10},
			want:    []any{"osd-auto", "seek", -10},
		},
		{
			name:    "volume",
			binding: compiledBinding{Action: actionVolume, Amount: 5},
			want:    []any{"osd-auto", "add", "volume", 5},
		},
		{
			name:    "chapter",
			binding: compiledBinding{Action: actionChapter, Amount: -1},
			want:    []any{"osd-auto", "add", "chapter", -1},
		},
		{
			name:    "subtitles",
			binding: compiledBinding{Action: actionSubtitles},
			want:    []any{"osd-auto", "cycle", "sub"},
		},
		{
			name:    "audio",
			binding: compiledBinding{Action: actionAudio},
			want:    []any{"osd-auto", "cycle", "audio"},
		},
		{
			name:    "info",
			binding: compiledBinding{Action: actionInfo},
			want:    []any{"expand-properties", "show-text", "${filename}\n${time-pos} / ${duration}", 4000},
		},
		{
			name:    "an action from a newer operator",
			binding: compiledBinding{Action: "brightness", Amount: 1},
			want:    nil,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, commandFor(each.binding), each.want)
		})
	}
}

func TestMatchBinding(t *testing.T) {
	cases := []struct {
		name  string
		event inputEvent
		want  string
	}{
		{name: "the button goes down", event: keyEvent(0x130, 1), want: actionPause},
		{name: "the button repeats while it is held", event: keyEvent(0x130, 2), want: ""},
		{name: "the button comes up", event: keyEvent(0x130, 0), want: ""},
		{name: "a button nothing binds", event: keyEvent(0x131, 1), want: ""},
		{name: "the hat goes up", event: axisEvent(0x11, -1), want: actionVolume},
		{name: "the hat goes down", event: axisEvent(0x11, 1), want: actionVolume},
		{name: "the hat returns to the middle", event: axisEvent(0x11, 0), want: ""},
		{name: "an axis nothing binds", event: axisEvent(0x10, 1), want: ""},
		{name: "the button's code on the wrong event type", event: axisEvent(0x130, 1), want: ""},
		{name: "a synchronization event", event: inputEvent{Type: 0x00, Code: 0x00, Value: 0}, want: ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			binding, matched := matchBinding(testBindings, each.event)
			mustMatch(t, matched, each.want != "")
			mustMatch(t, binding.Action, each.want)
		})
	}
}

func TestTranslateEventsSendsOneCommandPerBoundEvent(t *testing.T) {
	link, lines, _ := standInMPV(t)

	events := make(chan inputEvent, 8)
	go feedEvents(events,
		keyEvent(0x130, 1),
		keyEvent(0x130, 2),
		keyEvent(0x130, 0),
		axisEvent(0x11, -1),
		axisEvent(0x11, 0),
		keyEvent(0x131, 1),
		axisEvent(0x11, 1),
	)

	mustSucceed(t, translateEvents(t.Context(), testBindings, events, link, refuseRedial))

	mustMatchAll(t, nextLines(t, lines, 3), []string{
		`{"command":["osd-auto","cycle","pause"]}`,
		`{"command":["osd-auto","add","volume",5]}`,
		`{"command":["osd-auto","add","volume",-5]}`,
	})
}

func TestTranslateEventsReadsWhatMPVPushesAtIt(t *testing.T) {
	// The stand-in writes its pushes before anything reads, so
	// delivered closes only if the sidecar drains the socket.
	link, _, delivered := standInMPV(t,
		`{"event":"property-change","id":1,"name":"pause","data":false}`,
		`{"event":"file-loaded"}`,
		`{"event":"property-change","id":3,"name":"time-pos","data":5.4}`,
	)

	events := make(chan inputEvent)
	finished := make(chan error, 1)
	go func() {
		finished <- translateEvents(t.Context(), testBindings, events, link, refuseRedial)
	}()

	mustDeliver(t, delivered)
	close(events)
	mustSucceed(t, nextError(t, finished))
}

func TestTranslateEventsRedialsOnceWhenTheSendFails(t *testing.T) {
	replacement, lines, _ := standInMPV(t)
	redials := 0
	redial := func(context.Context) (io.ReadWriteCloser, error) {
		redials++
		return replacement, nil
	}

	events := make(chan inputEvent, 4)
	go feedEvents(events, keyEvent(0x130, 1), axisEvent(0x11, -1))

	mustSucceed(t, translateEvents(t.Context(), testBindings, events, brokenLink{}, redial))

	mustMatch(t, redials, 1)
	mustMatchAll(t, nextLines(t, lines, 2), []string{
		`{"command":["osd-auto","cycle","pause"]}`,
		`{"command":["osd-auto","add","volume",5]}`,
	})
}

func TestTranslateEventsEndsWhenTheRedialFails(t *testing.T) {
	events := make(chan inputEvent, 4)
	go feedEvents(events, keyEvent(0x130, 1))

	mustFail(t, translateEvents(t.Context(), testBindings, events, brokenLink{}, refuseRedial))
}

func TestTranslateEventsStopsWithItsContext(t *testing.T) {
	link, _, _ := standInMPV(t)
	ctx, stop := context.WithCancel(t.Context())
	stop()

	mustSucceed(t, translateEvents(ctx, testBindings, make(chan inputEvent), link, refuseRedial))
}

func TestAwaitNodesGivesUpOnABudget(t *testing.T) {
	useNodeBudget(t, 30*time.Millisecond, 5*time.Millisecond)

	cases := []struct {
		name  string
		files []string
	}{
		{name: "the machine has no input nodes at all", files: nil},
		{name: "a node that answers no evdev request", files: []string{"event0"}},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			directory := t.TempDir()
			for _, file := range each.files {
				mustSucceed(t, os.WriteFile(filepath.Join(directory, file), nil, 0o644))
			}
			useNodePattern(t, filepath.Join(directory, "event*"))

			_, err := awaitNodes(t.Context(), testBindings)
			mustFail(t, err)
		})
	}
}

// One gamepad's compiled table: the south button and both
// directions of the vertical hat axis.
var testBindings = []compiledBinding{
	{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause},
	{EventType: evAbs, Code: 0x11, Value: -1, Action: actionVolume, Amount: 5},
	{EventType: evAbs, Code: 0x11, Value: 1, Action: actionVolume, Amount: -5},
}

func keyEvent(code uint16, value int32) inputEvent {
	return inputEvent{Type: evKey, Code: code, Value: value}
}

func axisEvent(code uint16, value int32) inputEvent {
	return inputEvent{Type: evAbs, Code: code, Value: value}
}

func feedEvents(events chan<- inputEvent, list ...inputEvent) {
	for _, each := range list {
		events <- each
	}
	close(events)
}

// standInMPV is the player's end of the IPC socket: it collects the
// lines the sidecar writes and pushes its own, the way mpv pushes
// events at every client. A net.Pipe write blocks until the peer
// reads, so the delivered channel closes only when the sidecar has
// read every push, which is the proof it keeps draining.
func standInMPV(t *testing.T, pushes ...string) (io.ReadWriteCloser, <-chan string, <-chan struct{}) {
	t.Helper()
	near, far := net.Pipe()
	lines := make(chan string, 16)
	delivered := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(far)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	go func() {
		for _, push := range pushes {
			if _, err := fmt.Fprintln(far, push); err != nil {
				return
			}
		}
		close(delivered)
	}()
	t.Cleanup(func() {
		near.Close()
		far.Close()
	})
	return near, lines, delivered
}

func mustDeliver(t *testing.T, delivered <-chan struct{}) {
	t.Helper()
	select {
	case <-delivered:
	case <-time.After(30 * time.Second):
		t.Fatal("the remote read nothing mpv pushed at it")
	}
}

func nextError(t *testing.T, finished <-chan error) error {
	t.Helper()
	select {
	case err := <-finished:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the remote never stopped")
		return nil
	}
}

func nextLines(t *testing.T, lines <-chan string, count int) []string {
	t.Helper()
	var read []string
	for range count {
		select {
		case line := <-lines:
			read = append(read, line)
		case <-time.After(30 * time.Second):
			t.Fatalf("the remote wrote %d of %d commands", len(read), count)
		}
	}
	return read
}

// brokenLink is the socket of an mpv that exited: every write
// fails, every read is at its end.
type brokenLink struct{}

func (brokenLink) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (brokenLink) Write([]byte) (int, error) {
	return 0, errors.New("write: broken pipe")
}

func (brokenLink) Close() error {
	return nil
}

func refuseRedial(context.Context) (io.ReadWriteCloser, error) {
	return nil, errors.New("no socket answered")
}

func useNodeBudget(t *testing.T, budget, delay time.Duration) {
	t.Helper()
	budgetWas, delayWas := nodeWaitBudget, nodePollDelay
	t.Cleanup(func() { nodeWaitBudget, nodePollDelay = budgetWas, delayWas })
	nodeWaitBudget, nodePollDelay = budget, delay
}

func useNodePattern(t *testing.T, pattern string) {
	t.Helper()
	was := inputNodePattern
	t.Cleanup(func() { inputNodePattern = was })
	inputNodePattern = pattern
}
