package main

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObservePropertiesAsksForEachPropertyOnce(t *testing.T) {
	var sent strings.Builder
	mustSucceed(t, observeProperties(&sent, observedProperties))

	mustMatchAll(t, strings.Split(strings.TrimSuffix(sent.String(), "\n"), "\n"), []string{
		`{"command":["observe_property",1,"pause"]}`,
		`{"command":["observe_property",2,"playlist-pos"]}`,
		`{"command":["observe_property",3,"time-pos"]}`,
		`{"command":["observe_property",4,"duration"]}`,
	})
}

func TestReadEventsDeliversOnlyWhatWasObserved(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name:  "a property the supervisor observes",
			lines: []string{`{"event":"property-change","id":1,"name":"pause","data":true}`},
			want:  []string{"pause=true"},
		},
		{
			name:  "a property the supervisor did not ask for",
			lines: []string{`{"event":"property-change","id":9,"name":"volume","data":70}`},
			want:  nil,
		},
		{
			name:  "an event that is not a property change",
			lines: []string{`{"event":"file-loaded"}`, `{"event":"playback-restart"}`},
			want:  nil,
		},
		{
			name:  "a reply to a command",
			lines: []string{`{"error":"success","request_id":1}`},
			want:  nil,
		},
		{
			name:  "a line that is not JSON",
			lines: []string{`{`, ``, `not json at all`},
			want:  nil,
		},
		{
			name: "a run of events in order",
			lines: []string{
				`{"event":"property-change","id":2,"name":"playlist-pos","data":0}`,
				`{"event":"idle"}`,
				`{"event":"property-change","id":3,"name":"time-pos","data":12.5}`,
				`{"event":"property-change","id":4,"name":"duration","data":null}`,
			},
			want: []string{"playlist-pos=0", "time-pos=12.5", "duration=null"},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, collectChanges(t, each.lines), each.want)
		})
	}
}

func TestDialMPVWaitsForTheSocketMPVHasNotMadeYet(t *testing.T) {
	useDialDelay(t, 10*time.Millisecond)
	path := filepath.Join(t.TempDir(), "mpv.sock")

	// The first dial nearly always fails in production, because mpv
	// creates its IPC socket a moment after it starts. The delayed
	// listener here is that moment.
	listenAfter(t, path, 50*time.Millisecond)

	connection, err := dialMPV(context.Background(), path)
	mustSucceed(t, err)
	connection.Close()
}

// A socket that never arrives ends the dial only when the context does,
// because waiting for mpv is the whole job and the dial has no deadline
// of its own.
func TestDialMPVWaitsForASocketUntilItsContextEnds(t *testing.T) {
	useDialDelay(t, time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := dialMPV(ctx, filepath.Join(t.TempDir(), "absent.sock"))
	mustFail(t, err)
}

func TestDialMPVStopsWithItsContext(t *testing.T) {
	useDialDelay(t, 10*time.Millisecond)
	ctx, stop := context.WithCancel(context.Background())
	stop()

	_, err := dialMPV(ctx, filepath.Join(t.TempDir(), "absent.sock"))
	mustFail(t, err)
}

// The display asks for a logo by broadcasting a script-message, which
// mpv delivers to the bridge as a client-message event, and readEvents hands
// its arguments through untouched.
func TestReadEventsDeliversClientMessages(t *testing.T) {
	changes := make(chan propertyChange, 8)
	messages := make(chan clientMessage, 8)
	lines := []string{
		`{"event":"property-change","id":1,"name":"pause","data":true}`,
		`{"event":"client-message","args":["liken-art-request","logo","280","96"]}`,
		`{"event":"client-message","args":["someone-elses-broadcast"]}`,
	}
	stream := strings.NewReader(strings.Join(lines, "\n") + "\n")
	mustSucceed(t, readEvents(context.Background(), stream, changes, messages))
	close(changes)
	close(messages)

	var got []string
	for message := range messages {
		got = append(got, strings.Join(message.Args, ","))
	}
	mustMatchAll(t, got, []string{"liken-art-request,logo,280,96", "someone-elses-broadcast"})
	mustMatch(t, len(changes), 1)
}

// collectChanges runs readEvents over a scripted stream and renders
// each delivered change as name=data. The rendering makes a failure
// message readable, where raw JSON diffs are not.
func collectChanges(t *testing.T, lines []string) []string {
	t.Helper()
	changes := make(chan propertyChange, 64)
	messages := make(chan clientMessage, 64)
	mustSucceed(t, readEvents(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), changes, messages))
	close(changes)

	var rendered []string
	for change := range changes {
		rendered = append(rendered, change.Name+"="+string(change.Data))
	}
	return rendered
}

// changeOf is one property change as readEvents would deliver it.
func changeOf(name, data string) propertyChange {
	return propertyChange{Name: name, Data: json.RawMessage(data)}
}

// listenAfter is the socket mpv would serve, arriving late the way
// mpv's does. It holds the listener open until the test ends, so a
// dial that lands during cleanup finds a socket and not a flake.
func listenAfter(t *testing.T, path string, delay time.Duration) {
	t.Helper()
	go func() {
		time.Sleep(delay)
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Errorf("listen on %s: %v", path, err)
			return
		}
		defer listener.Close()
		<-t.Context().Done()
	}()
}

func useDialDelay(t *testing.T, delay time.Duration) {
	t.Helper()
	was := mpvDialDelay
	t.Cleanup(func() { mpvDialDelay = was })
	mpvDialDelay = delay
}
