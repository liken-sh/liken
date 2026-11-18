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
			name:  "a property the sidecar observes",
			lines: []string{`{"event":"property-change","id":1,"name":"pause","data":true}`},
			want:  []string{"pause=true"},
		},
		{
			name:  "a property the sidecar does not observe",
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

// The test runs in a synctest bubble, so the wait of mpvDialDelay between
// attempts passes on the bubble's fake clock.
func TestDialMPVRetriesUntilTheSocketExists(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mpv.sock")
		listenAfter(t, path, 2*mpvDialDelay+mpvDialDelay/2)
		began := time.Now()

		connection, err := dialMPV(context.Background(), path)

		mustSucceed(t, err)
		connection.Close()
		mustMatch(t, time.Since(began), 3*mpvDialDelay)
	})
}

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

// mpv delivers a script-message that a client broadcast as a
// client-message event, and readEvents passes its arguments on unchanged,
// including those of messages from other scripts.
func TestReadEventsDeliversClientMessages(t *testing.T) {
	changes := make(chan propertyChange, 8)
	messages := make(chan clientMessage, 8)
	lines := []string{
		`{"event":"property-change","id":1,"name":"pause","data":true}`,
		`{"event":"client-message","args":["liken-presentation-request"]}`,
		`{"event":"client-message","args":["someone-elses-broadcast"]}`,
	}
	stream := strings.NewReader(strings.Join(lines, "\n") + "\n")
	mustSucceed(t, readEvents(context.Background(), stream, changes, messages, nil, nil))
	close(changes)
	close(messages)

	var got []string
	for message := range messages {
		got = append(got, strings.Join(message.Args, ","))
	}
	mustMatchAll(t, got, []string{"liken-presentation-request", "someone-elses-broadcast"})
	mustMatch(t, len(changes), 1)
}

// readEvents drops a reply with request id 0, because it belongs to a
// command that was sent without a request id.
func TestReadEventsDeliversRepliesToCommandsWithARequestID(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`{"request_id":0,"error":"success"}`,
		`{"request_id":3,"error":"property unavailable","data":null}`,
		`{"event":"property-change","name":"pause","data":true}`,
	}, "\n") + "\n")
	changes := make(chan propertyChange, 4)
	messages := make(chan clientMessage, 4)
	replies := make(chan mpvReply, 4)

	mustSucceed(t, readEvents(context.Background(), stream, changes, messages, replies, nil))
	close(replies)

	var got []mpvReply
	for reply := range replies {
		got = append(got, reply)
	}
	mustMatchAll(t, got, []mpvReply{{ID: 3, Error: "property unavailable"}})
	mustMatch(t, len(changes), 1)
}

func TestReadEventsDeliversGetPropertyRepliesAndFileLoads(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`{"request_id":4,"error":"success","data":[{"id":1,"type":"sub"}]}`,
		`{"event":"file-loaded"}`,
		`{"event":"playback-restart"}`,
	}, "\n") + "\n")
	replies := make(chan mpvReply, 4)
	loads := make(chan struct{}, 4)

	mustSucceed(t, readEvents(context.Background(), stream, nil, nil, replies, loads))

	mustMatch(t, <-replies, mpvReply{ID: 4, Error: mpvReplySuccess, Data: `[{"id":1,"type":"sub"}]`})
	mustMatch(t, len(loads), 1)
}

// collectChanges runs readEvents over the given lines and returns each
// property change as name=data, which reads better in a failure message
// than raw JSON.
func collectChanges(t *testing.T, lines []string) []string {
	t.Helper()
	changes := make(chan propertyChange, 64)
	messages := make(chan clientMessage, 64)
	mustSucceed(t, readEvents(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), changes, messages, nil, nil))
	close(changes)

	var rendered []string
	for change := range changes {
		rendered = append(rendered, change.Name+"="+string(change.Data))
	}
	return rendered
}

// changeOf builds the propertyChange that readEvents sends for a name and
// its JSON data.
func changeOf(name, data string) propertyChange {
	return propertyChange{Name: name, Data: json.RawMessage(data)}
}

// listenAfter creates a unix socket at path after delay. The listener stays
// open until the test ends.
func listenAfter(t *testing.T, path string, delay time.Duration) {
	t.Helper()
	go func() {
		time.Sleep(delay)
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Errorf("listening on the socket %s: %v", path, err)
			return
		}
		defer listener.Close()
		<-t.Context().Done()
	}()
}
