package main

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
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
		`{"command":["observe_property",5,"current-tracks/audio/lang"]}`,
		`{"command":["observe_property",6,"current-tracks/sub/lang"]}`,
		`{"command":["observe_property",7,"current-tracks/video/codec"]}`,
		`{"command":["observe_property",8,"hwdec-current"]}`,
		`{"command":["observe_property",9,"frame-drop-count"]}`,
		`{"command":["observe_property",10,"avsync"]}`,
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

// The dial tries once a second. A unix socket dial returns at once,
// whether the socket is there or not, so these tests run in a synctest
// bubble and check each wait against the bubble's clock.
func TestDialMPVWaitsForTheSocketMPVHasNotMadeYet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mpv.sock")
		// The first dial nearly always fails in production, because mpv
		// creates its IPC socket a moment after it starts. The delayed
		// listener here is that moment.
		listenAfter(t, path, 2*mpvDialDelay+mpvDialDelay/2)
		began := time.Now()

		connection, err := dialMPV(context.Background(), path)

		mustSucceed(t, err)
		connection.Close()
		mustMatch(t, time.Since(began), 3*mpvDialDelay)
	})
}

// A socket that never arrives ends the dial only when the context does,
// because waiting for mpv is the whole job and the dial has no deadline
// of its own.
func TestDialMPVWaitsForASocketUntilItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		began := time.Now()

		_, err := dialMPV(ctx, filepath.Join(t.TempDir(), "absent.sock"))

		mustFail(t, err)
		mustMatch(t, time.Since(began), time.Minute)
	})
}

func TestDialMPVStopsWithItsContext(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stop()

	_, err := dialMPV(ctx, filepath.Join(t.TempDir(), "absent.sock"))
	mustFail(t, err)
}

// The display asks for the current block by broadcasting a script-message,
// which mpv delivers to the sidecar as a client-message event, and readEvents
// hands its arguments through untouched.
func TestReadEventsDeliversClientMessages(t *testing.T) {
	changes := make(chan propertyChange, 8)
	messages := make(chan clientMessage, 8)
	lines := []string{
		`{"event":"property-change","id":1,"name":"pause","data":true}`,
		`{"event":"client-message","args":["liken-presentation-request"]}`,
		`{"event":"client-message","args":["someone-elses-broadcast"]}`,
	}
	stream := strings.NewReader(strings.Join(lines, "\n") + "\n")
	mustSucceed(t, readEvents(context.Background(), stream, changes, messages, nil))
	close(changes)
	close(messages)

	var got []string
	for message := range messages {
		got = append(got, strings.Join(message.Args, ","))
	}
	mustMatchAll(t, got, []string{"liken-presentation-request", "someone-elses-broadcast"})
	mustMatch(t, len(changes), 1)
}

// An answer to a command that carried a request id reaches the replies
// channel with mpv's error word. An answer under id 0 is to a command the
// sidecar sent for itself, and it is dropped.
func TestReadEventsDeliversTheAnswersToCommandsThatCarriedAnID(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`{"request_id":0,"error":"success"}`,
		`{"request_id":3,"error":"property unavailable","data":null}`,
		`{"event":"property-change","name":"pause","data":true}`,
	}, "\n") + "\n")
	changes := make(chan propertyChange, 4)
	messages := make(chan clientMessage, 4)
	replies := make(chan mpvReply, 4)

	mustSucceed(t, readEvents(context.Background(), stream, changes, messages, replies))
	close(replies)

	var got []mpvReply
	for reply := range replies {
		got = append(got, reply)
	}
	mustMatchAll(t, got, []mpvReply{{ID: 3, Error: "property unavailable"}})
	mustMatch(t, len(changes), 1)
}

// collectChanges runs readEvents over a scripted stream and renders
// each delivered change as name=data. The rendering makes a failure
// message readable, where raw JSON diffs are not.
func collectChanges(t *testing.T, lines []string) []string {
	t.Helper()
	changes := make(chan propertyChange, 64)
	messages := make(chan clientMessage, 64)
	mustSucceed(t, readEvents(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), changes, messages, nil))
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
