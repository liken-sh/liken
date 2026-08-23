package main

// These tests cover the command sidecar: a named command on the commands
// topic becomes the right mpv command, and the report side folds mpv's
// property changes and throttles the position.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

// A named command on the commands topic becomes mpv's own command words.
// mpv is a pipe here, so the test reads the line the command sidecar
// writes.
func TestACommandOnTheCommandsTopicBecomesAnMpvCommand(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	c := &commander{mpv: client}
	payload, err := json.Marshal(mediaCommand{Action: actionSeek, Amount: 30})
	if err != nil {
		t.Fatal(err)
	}
	c.handle(c.commandsTopic, payload)

	select {
	case line := <-lines:
		if line != `{"command":["osd-auto","seek",30]}` {
			t.Errorf("mpv command = %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("no command reached mpv")
	}
}

// A payload that does not decode, and an action the sidecar has no case
// for, write nothing to mpv.
func TestACommandThatDoesNotDecodeOrHasNoCaseWritesNothing(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	wrote := make(chan struct{}, 1)
	go func() {
		buffer := make([]byte, 256)
		if _, err := server.Read(buffer); err == nil {
			wrote <- struct{}{}
		}
	}()

	c := &commander{mpv: client}
	c.handle(c.commandsTopic, []byte("not json"))
	newer, err := json.Marshal(mediaCommand{Action: "brightness", Amount: 1})
	if err != nil {
		t.Fatal(err)
	}
	c.handle(c.commandsTopic, newer)

	select {
	case <-wrote:
		t.Error("a command with no mpv translation wrote to the socket")
	case <-time.After(100 * time.Millisecond):
	}
}

// The command sidecar forwards the current item's presentation block to
// the display over the mpv socket: on the first item it knows and again
// on every advance. The block is the one baked for that playlist
// position, and it travels as one string argument.
func TestTheSidecarForwardsThePresentationOnEachItem(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	c := &commander{
		mpv: client,
		presentations: []json.RawMessage{
			json.RawMessage(`{"title":"First"}`),
			json.RawMessage(`{"title":"Second"}`),
		},
	}

	changes := make(chan propertyChange, 8)
	go feedChanges(changes,
		changeOf("playlist-pos", "0"),
		changeOf("time-pos", "1.0"),
		changeOf("playlist-pos", "1"),
	)
	runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present)

	want := []string{
		`{"command":["script-message-to","display","presentation","{\"title\":\"First\"}"]}`,
		`{"command":["script-message-to","display","presentation","{\"title\":\"Second\"}"]}`,
	}
	for _, each := range want {
		select {
		case line := <-lines:
			if line != each {
				t.Errorf("presentation = %q, want %q", line, each)
			}
		case <-time.After(time.Second):
			t.Fatalf("no presentation reached mpv, want %q", each)
		}
	}
}

// An item the sidecar has no baked block for forwards {}, so the display
// always receives a definite value.
func TestTheSidecarForwardsEmptyForAMissingBlock(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	c := &commander{mpv: client}

	changes := make(chan propertyChange, 8)
	go feedChanges(changes, changeOf("playlist-pos", "0"))
	runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present)

	want := `{"command":["script-message-to","display","presentation","{}"]}`
	select {
	case line := <-lines:
		if line != want {
			t.Errorf("presentation = %q, want %q", line, want)
		}
	case <-time.After(time.Second):
		t.Fatal("no presentation reached mpv")
	}
}

func TestFormatPosition(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{seconds: 0, want: "0:00:00"},
		{seconds: 0.9, want: "0:00:00"},
		{seconds: 59.999, want: "0:00:59"},
		{seconds: 60, want: "0:01:00"},
		{seconds: 3599, want: "0:59:59"},
		{seconds: 3600, want: "1:00:00"},
		{seconds: 5550.4, want: "1:32:30"},
		{seconds: 36000, want: "10:00:00"},
		{seconds: -1, want: "0:00:00"},
	}

	for _, each := range cases {
		t.Run(each.want, func(t *testing.T) {
			mustMatch(t, formatPosition(each.seconds), each.want)
		})
	}
}

func TestApplyOnePropertyChange(t *testing.T) {
	playing := playbackState{item: 1, position: "0:00:10", duration: "0:30:00"}

	cases := []struct {
		name   string
		start  playbackState
		change propertyChange
		want   playbackState
		atOnce bool
	}{
		{
			name:   "a pause the operator must see at once",
			start:  playing,
			change: changeOf("pause", "true"),
			want:   playbackState{paused: true, item: 1, position: "0:00:10", duration: "0:30:00"},
			atOnce: true,
		},
		{
			name:   "the first pause event repeats what is already true",
			start:  playing,
			change: changeOf("pause", "false"),
			want:   playing,
		},
		{
			name:   "the first item of a playlist counts from one",
			change: changeOf("playlist-pos", "0"),
			want:   playbackState{item: 1},
			atOnce: true,
		},
		{
			name:   "the next item",
			start:  playing,
			change: changeOf("playlist-pos", "2"),
			want:   playbackState{item: 3, position: "0:00:10", duration: "0:30:00"},
			atOnce: true,
		},
		{
			name:   "no item is loaded",
			start:  playing,
			change: changeOf("playlist-pos", "-1"),
			want:   playing,
		},
		{
			name:   "the position advances",
			start:  playing,
			change: changeOf("time-pos", "65.9"),
			want:   playbackState{item: 1, position: "0:01:05", duration: "0:30:00"},
		},
		{
			name:   "mpv has no position yet",
			start:  playbackState{item: 1},
			change: changeOf("time-pos", "null"),
			want:   playbackState{item: 1},
		},
		{
			name:   "the item's length arrives with its header",
			start:  playbackState{item: 1},
			change: changeOf("duration", "3600"),
			want:   playbackState{item: 1, duration: "1:00:00"},
		},
		{
			name:   "a stream of unknown length",
			start:  playbackState{item: 1},
			change: changeOf("duration", "null"),
			want:   playbackState{item: 1},
		},
		{
			name:   "the chosen audio track's language the operator must see",
			start:  playing,
			change: changeOf("current-tracks/audio/lang", `"eng"`),
			want:   playbackState{item: 1, position: "0:00:10", duration: "0:30:00", audioLanguage: "eng"},
			atOnce: true,
		},
		{
			name:   "the chosen subtitle track's language",
			start:  playing,
			change: changeOf("current-tracks/sub/lang", `"jpn"`),
			want:   playbackState{item: 1, position: "0:00:10", duration: "0:30:00", subtitleLanguage: "jpn"},
			atOnce: true,
		},
		{
			name:   "the same audio language repeats what is already known",
			start:  playbackState{item: 1, audioLanguage: "eng"},
			change: changeOf("current-tracks/audio/lang", `"eng"`),
			want:   playbackState{item: 1, audioLanguage: "eng"},
		},
		{
			name:   "a track with no language leaves the last one",
			start:  playbackState{item: 1, audioLanguage: "eng"},
			change: changeOf("current-tracks/audio/lang", "null"),
			want:   playbackState{item: 1, audioLanguage: "eng"},
		},
		{
			name:   "a property the command sidecar does not fold",
			start:  playing,
			change: changeOf("volume", "70"),
			want:   playing,
		},
		{
			name:   "a datum of the wrong type",
			start:  playing,
			change: changeOf("pause", `"yes"`),
			want:   playing,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			state := each.start
			atOnce := state.apply(each.change)
			mustMatch(t, state, each.want)
			mustMatch(t, atOnce, each.atOnce)
		})
	}
}

func TestReporterSendsChangesAtOnceAndThrottlesThePosition(t *testing.T) {
	t.Run("a pause and an item change do not wait", func(t *testing.T) {
		useReportInterval(t, time.Hour)
		send, sent := recordReports()

		changes := make(chan propertyChange, 8)
		go feedChanges(changes,
			changeOf("playlist-pos", "0"),
			changeOf("time-pos", "1.0"),
			changeOf("pause", "true"),
			changeOf("time-pos", "1.0"),
			changeOf("playlist-pos", "1"),
		)
		runReporter(t.Context(), changes, send, func(int) {})

		mustMatchAll(t, itemsAndPauses(sent()), []string{"1 playing", "1 paused", "2 paused"})
	})

	t.Run("the position waits for the interval", func(t *testing.T) {
		useReportInterval(t, 20*time.Millisecond)
		send, sent := recordReports()

		changes := make(chan propertyChange)
		go func() {
			changes <- changeOf("playlist-pos", "0")
			changes <- changeOf("time-pos", "1.0")
			time.Sleep(60 * time.Millisecond)
			changes <- changeOf("time-pos", "2.0")
			close(changes)
		}()
		runReporter(t.Context(), changes, send, func(int) {})

		mustMatchAll(t, positions(sent()), []string{"", "0:00:02"})
	})

	t.Run("nothing is reported before an item is loaded", func(t *testing.T) {
		useReportInterval(t, 0)
		send, sent := recordReports()

		changes := make(chan propertyChange, 8)
		go feedChanges(changes,
			changeOf("pause", "false"),
			changeOf("time-pos", "1.0"),
			changeOf("duration", "60.0"),
		)
		runReporter(t.Context(), changes, send, func(int) {})

		mustMatch(t, len(sent()), 0)
	})

	t.Run("a report the bus drops does not stop the run", func(t *testing.T) {
		useReportInterval(t, 0)
		attempts := 0
		send := func(playReport) error {
			attempts++
			return errors.New("the broker is unreachable")
		}

		changes := make(chan propertyChange, 8)
		go feedChanges(changes,
			changeOf("playlist-pos", "0"),
			changeOf("time-pos", "1.0"),
			changeOf("time-pos", "2.0"),
		)
		runReporter(t.Context(), changes, send, func(int) {})

		mustMatch(t, attempts, 3)
	})
}

// useReportInterval moves the throttle for the length of one test, so a
// test drives the loop in milliseconds or turns the throttle off.
func useReportInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	was := reportInterval
	t.Cleanup(func() { reportInterval = was })
	reportInterval = interval
}

// recordReports is a reportSender that keeps every report, so a test
// reads back which reports the loop sent with no broker at all.
func recordReports() (reportSender, func() []playReport) {
	var reports []playReport
	send := func(report playReport) error {
		reports = append(reports, report)
		return nil
	}
	return send, func() []playReport { return reports }
}

func feedChanges(changes chan<- propertyChange, list ...propertyChange) {
	for _, each := range list {
		changes <- each
	}
	close(changes)
}

// itemsAndPauses renders each report to the two fields the throttling
// tests are about, which reports were sent, not every field in them.
func itemsAndPauses(reports []playReport) []string {
	rendered := make([]string, 0, len(reports))
	for _, report := range reports {
		rendered = append(rendered, fmt.Sprintf("%d %s", report.Item, pausedOrPlaying(report.Paused)))
	}
	return rendered
}

func pausedOrPlaying(paused bool) string {
	if paused {
		return "paused"
	}
	return "playing"
}

func positions(reports []playReport) []string {
	rendered := make([]string, 0, len(reports))
	for _, report := range reports {
		rendered = append(rendered, report.Position)
	}
	return rendered
}
