package main

// These tests cover the command sidecar: a named command on the commands
// topic becomes the right mpv command, and the report side folds mpv's
// property changes and throttles the position.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
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

	// A seek writes two commands: the no-osd seek, then the summon that
	// makes the display draw the new position.
	want := []string{
		`{"command":["no-osd","seek",30]}`,
		`{"command":["script-message-to","display","summon"]}`,
	}
	for _, expected := range want {
		select {
		case line := <-lines:
			if line != expected {
				t.Errorf("mpv command = %q, want %q", line, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("no command reached mpv")
		}
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
	runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present, func(json.RawMessage) {})

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
	runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present, func(json.RawMessage) {})

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

// The display broadcasts the exit press, and the sidecar publishes the
// ending before it quits mpv. The order is the whole point of the path, so
// the test proves it: nothing reads mpv's socket until the publish reaches
// the broker, and net.Pipe blocks a write nobody reads, so a sidecar that
// quit first would publish nothing and the wait would fail.
func TestTheExitPressPublishesTheEndingBeforeTheQuitReachesMpv(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })

	c := &commander{
		statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
		bus:         bus,
		mpv:         client,
		lastReport:  playReport{Item: 1, Position: "0:20:00"},
		haveReport:  true,
	}

	messages := make(chan clientMessage, 1)
	messages <- clientMessage{Args: []string{exitMessage}}
	close(messages)
	go c.serveMessages(messages)

	ending := waitForPublish(t, brokers[0].pubs)
	mustMatch(t, ending.topic, c.statusTopic)
	mustMatch(t, ending.retained, true)
	mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})

	mustMatchAll(t, readLines(t, server, 1), []string{`{"command":["quit","0"]}`})
}

// The two endings the sidecar observes on mpv's socket: mpv reaches the end
// of the last item and closes it, and the kubelet's SIGTERM ends the run's
// context. Each publishes the ending with the position the last report
// carried, before runCommand clears the retained status.
func TestTheSidecarPublishesTheEndingWhenTheRunEnds(t *testing.T) {
	cases := []struct {
		name string
		end  func(conn net.Conn, stop context.CancelFunc)
	}{
		{
			name: "mpv reaches the end of the last item",
			end:  func(conn net.Conn, _ context.CancelFunc) { conn.Close() },
		},
		{
			name: "the kubelet sends SIGTERM",
			end:  func(_ net.Conn, stop context.CancelFunc) { stop() },
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			useDialDelay(t, time.Millisecond)
			path := filepath.Join(t.TempDir(), "mpv.sock")
			useSocket(t, path)
			listener, err := net.Listen("unix", path)
			mustSucceed(t, err)
			t.Cleanup(func() { listener.Close() })

			bus, brokers, connected := startBus(t, 1, nil, nil)
			waitForConnect(t, connected)
			c := &commander{
				statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
				bus:         bus,
			}

			ctx, stop := context.WithCancel(context.Background())
			t.Cleanup(stop)
			done := make(chan struct{})
			go func() {
				defer close(done)
				c.report(ctx)
			}()

			conn, err := listener.Accept()
			mustSucceed(t, err)
			t.Cleanup(func() { conn.Close() })

			// The position arrives first, which reports nothing because mpv
			// has not named an item, and the item arrives second and sends
			// the one report of this run.
			writeEvent(t, conn, `{"event":"property-change","name":"time-pos","data":1200.0}`)
			writeEvent(t, conn, `{"event":"property-change","name":"playlist-pos","data":0}`)
			running := waitForPublish(t, brokers[0].pubs)
			mustMatch(t, reportOf(t, running.payload), playReport{Item: 1, Position: "0:20:00"})

			one.end(conn, stop)

			ending := waitForPublish(t, brokers[0].pubs)
			mustMatch(t, ending.topic, c.statusTopic)
			mustMatch(t, ending.retained, true)
			mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
			<-done
		})
	}
}

// The mark stays set for the rest of the run, so a subscriber that reads
// any later report reads the same ending.
func TestEveryReportAfterTheEndingCarriesTheMark(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	c := &commander{
		statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
		bus:         bus,
		lastReport:  playReport{Item: 1, Position: "0:20:00"},
		haveReport:  true,
	}

	c.endRun()
	waitForPublish(t, brokers[0].pubs)
	mustSucceed(t, c.send(playReport{Item: 1, Position: "0:20:01"}))

	later := waitForPublish(t, brokers[0].pubs)
	mustMatch(t, endedReport(t, later.payload), playReport{Item: 1, Position: "0:20:01", Ended: true})
}

// A run that never reported publishes no ending, the rule the reporter
// follows: mpv never named an item, so there are no numbers to carry and
// the pod's own death is what ends such a run.
func TestARunThatNeverReportedPublishesNoEnding(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	c := &commander{
		statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
		bus:         bus,
	}

	c.endRun()

	mustPublishNothing(t, brokers[0])
}

// A volume press writes nothing to mpv. It publishes the unit's
// next level, retained, and the subscription is what applies it.
func TestAVolumePressPublishesTheNextLevelAndWritesNoMpvCommand(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })

	c := &commander{
		commandsTopic: playCommandsTopic(defaultTopicBase, "house", "movie"),
		volumeTopic:   playerVolumeTopic(defaultTopicBase, "house", "theater"),
		bus:           bus,
		mpv:           client,
		volume:        volumeState{Level: 40},
		haveVolume:    true,
	}
	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionVolume, Amount: 5}))

	published := waitForPublish(t, brokers[0].pubs)
	mustMatch(t, published.topic, c.volumeTopic)
	mustMatch(t, published.retained, true)
	mustMatch(t, string(published.payload), `{"level":45,"muted":false}`)
	mustWriteNothing(t, server)
}

// A mute press toggles the flag the topic holds, and it too writes
// nothing to mpv.
func TestAMutePressPublishesTheToggledFlag(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)

	c := &commander{
		commandsTopic: playCommandsTopic(defaultTopicBase, "house", "movie"),
		volumeTopic:   playerVolumeTopic(defaultTopicBase, "house", "theater"),
		bus:           bus,
	}
	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionMute}))

	published := waitForPublish(t, brokers[0].pubs)
	mustMatch(t, string(published.payload), `{"level":100,"muted":true}`)
}

// The message on the volume topic is what reaches mpv, so the pod
// that pressed and every pod that only listened run one apply path.
func TestAMessageOnTheVolumeTopicReachesMpv(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })
	lines := readAsync(server)

	c := &commander{
		volumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
		mpv:         client,
	}
	c.handle(c.volumeTopic, []byte(`{"level":45,"muted":true}`))

	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","volume","45"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","mute","yes"]}`)
	mustMatch(t, c.heldVolume(), volumeState{Level: 45, Muted: true})
}

// The first level of a bus session is the broker's retained
// catch-up, so it applies with no signal and the display pops no indicator
// at pod start. Every level after it applies and then signals the display to
// draw. A fresh session redelivers the retained level, so the first message
// after a reconnect is silent again.
func TestTheFirstLevelOfASessionAppliesSilently(t *testing.T) {
	bus, _, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })
	lines := readAsync(server)

	c := &commander{
		volumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
		bus:         bus,
		mpv:         client,
	}

	c.handle(c.volumeTopic, []byte(`{"level":40,"muted":false}`))
	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","volume","40"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","mute","no"]}`)
	mustNoLine(t, lines, 100*time.Millisecond)

	c.handle(c.volumeTopic, []byte(`{"level":45,"muted":false}`))
	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","volume","45"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","mute","no"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["script-message","volume-changed"]}`)

	c.onConnect(bus)

	c.handle(c.volumeTopic, []byte(`{"level":50,"muted":false}`))
	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","volume","50"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","mute","no"]}`)
	mustNoLine(t, lines, 100*time.Millisecond)
}

// mustNoLine fails when the sidecar writes anything to mpv inside this
// window.
func mustNoLine(t *testing.T, lines <-chan string, window time.Duration) {
	t.Helper()
	select {
	case line := <-lines:
		t.Fatalf("the sidecar wrote %s, and should have written nothing", line)
	case <-time.After(window):
	}
}

// A Player with no sinks hands its sidecar no volume topic. That
// sidecar publishes nothing on a press and writes nothing to mpv, because
// a unit with nothing to hear has no level to mean anything.
func TestASidecarWithNoSpeakersIgnoresTheVolume(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })

	c := &commander{
		commandsTopic: playCommandsTopic(defaultTopicBase, "house", "movie"),
		bus:           bus,
		mpv:           client,
	}
	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionVolume, Amount: 5}))
	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionMute}))

	mustPublishNothing(t, brokers[0])
	mustWriteNothing(t, server)
}

// mustEncode marshals one message the way a program on the bus publishes
// it.
func mustEncode(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	mustSucceed(t, err)
	return payload
}

// readAsync hands each line the sidecar writes to mpv's socket to the
// channel, so a test reads them in order.
func readAsync(conn net.Conn) chan string {
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	return lines
}

// mustWriteNothing proves the sidecar wrote nothing to mpv's socket in the
// window a write would have taken.
func mustWriteNothing(t *testing.T, conn net.Conn) {
	t.Helper()
	mustSucceed(t, conn.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	buffer := make([]byte, 256)
	read, err := conn.Read(buffer)
	if err == nil {
		t.Fatalf("the sidecar wrote %q to mpv, and should have written nothing", buffer[:read])
	}
}

// writeEvent hands mpv's socket one event line, the newline-delimited JSON
// the reader reads.
func writeEvent(t *testing.T, conn net.Conn, line string) {
	t.Helper()
	_, err := conn.Write([]byte(line + "\n"))
	mustSucceed(t, err)
}

// reportOf decodes one published payload as a report.
func reportOf(t *testing.T, payload []byte) playReport {
	t.Helper()
	var report playReport
	mustSucceed(t, json.Unmarshal(payload, &report))
	return report
}

// endedReport decodes a published payload and proves the mark travels as
// the field it is, so a subscriber reads the ending and not an empty
// status.
func endedReport(t *testing.T, payload []byte) playReport {
	t.Helper()
	mustMatch(t, bytes.Contains(payload, []byte(`"ended":true`)), true)
	return reportOf(t, payload)
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
		runReporter(t.Context(), changes, send, func(int) {}, func(json.RawMessage) {})

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
		runReporter(t.Context(), changes, send, func(int) {}, func(json.RawMessage) {})

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
		runReporter(t.Context(), changes, send, func(int) {}, func(json.RawMessage) {})

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
		runReporter(t.Context(), changes, send, func(int) {}, func(json.RawMessage) {})

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

// The display broadcasts one request for the current block when it loads,
// because the sidecar's own send races the script's registration: a block sent
// before the script registered reaches nobody, and the display would then
// never learn the item's type or ask for its art.
func TestTheSidecarAnswersThePresentationRequest(t *testing.T) {
	bridge, lines := bridgeToMPV(t)
	bridge.presentations = []json.RawMessage{
		json.RawMessage(`{"title":"First"}`),
		json.RawMessage(`{"type":"music","hint":"album"}`),
	}
	bridge.artItem = 2

	go bridge.serveMessages(requestFor(presentationRequestMessage))

	want := `{"command":["script-message-to","display","presentation","{\"type\":\"music\",\"hint\":\"album\"}"]}`
	select {
	case line := <-lines:
		mustMatch(t, line, want)
	case <-time.After(time.Second):
		t.Fatal("the sidecar answered nothing")
	}
}

// Before mpv says which item plays there is no block to answer with, and the
// reporter presents the first item the moment it does, so the request goes
// unanswered rather than forwarding a block for no item.
func TestThePresentationRequestBeforeTheFirstItem(t *testing.T) {
	bridge, lines := bridgeToMPV(t)
	bridge.presentations = []json.RawMessage{json.RawMessage(`{"title":"First"}`)}

	go bridge.serveMessages(requestFor(presentationRequestMessage))

	select {
	case line := <-lines:
		t.Errorf("the sidecar sent %q, want nothing", line)
	case <-time.After(100 * time.Millisecond):
	}
}

// requestFor is one broadcast on a closed channel, the shape the reader hands
// the message goroutine for a display message with no arguments.
func requestFor(name string) <-chan clientMessage {
	messages := make(chan clientMessage, 1)
	messages <- clientMessage{Args: []string{name}}
	close(messages)
	return messages
}
