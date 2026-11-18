package main

// These tests cover the command sidecar. mpv's socket is a net.Pipe, so a
// test reads each line that the sidecar writes to mpv, and writes the
// events that mpv would send.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// readLines reads n lines from the connection. It sets a deadline, so a
// writer that sends too few lines fails the test and does not hang it.
func readLines(t *testing.T, conn net.Conn, n int) []string {
	t.Helper()
	mustSucceed(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	scanner := bufio.NewScanner(conn)
	var lines []string
	for len(lines) < n && scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func TestACommandOnTheCommandsTopicBecomesAnMpvCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
			t.Fatalf("encoding the seek command: %v", err)
		}
		c.handle(c.commandsTopic, payload)

		// A seek writes two commands: the seek with no-osd, and then the
		// summon that makes the display draw the new position.
		want := []string{
			`{"command":["no-osd","seek",30],"request_id":1}`,
			`{"command":["script-message","summon"]}`,
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
	})
}

func TestACommandThatDoesNotDecodeOrHasNoCaseWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
			t.Fatalf("encoding the brightness command: %v", err)
		}
		c.handle(c.commandsTopic, newer)

		select {
		case <-wrote:
			t.Error("a command with no mpv translation wrote to the socket")
		case <-time.After(100 * time.Millisecond):
		}
	})
}

// The sidecar sends the display the presentation block of each item as
// the playlist reaches it, as one string argument.
func TestTheSidecarForwardsThePresentationOnEachItem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present, nil, io.Discard)

		want := []string{
			`{"command":["script-message","presentation","{\"title\":\"First\"}"]}`,
			`{"command":["script-message","presentation","{\"title\":\"Second\"}"]}`,
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
	})
}

// An item with no block sends {}, so the display always receives a
// value.
func TestTheSidecarForwardsEmptyForAMissingBlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present, nil, io.Discard)

		want := `{"command":["script-message","presentation","{}"]}`
		select {
		case line := <-lines:
			if line != want {
				t.Errorf("presentation = %q, want %q", line, want)
			}
		case <-time.After(time.Second):
			t.Fatal("no presentation reached mpv")
		}
	})
}

// The display broadcasts the exit press, and the sidecar publishes the
// ending before it sends mpv the quit. The test checks that order by
// reading nothing from mpv's socket until the publish reaches the broker.
// net.Pipe blocks a write that nobody reads, so a sidecar that sent the
// quit first would never publish, and the wait for the publish would
// fail.
func TestTheExitPressPublishesTheEndingBeforeTheQuitReachesMpv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// mpv keeps drawing the film for the grace between the ending and the
// quit, so the compositor can fade it out.
func TestTheQuitWaitsOutTheGraceAfterTheEnding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		server, client := net.Pipe()
		t.Cleanup(func() { server.Close() })
		lines := readAsync(server)

		c := &commander{
			statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
			bus:         bus,
			mpv:         client,
			grace:       exitGrace,
			lastReport:  playReport{Item: 1, Position: "0:20:00"},
			haveReport:  true,
		}

		go c.exit()

		ending := waitForPublish(t, brokers[0].pubs)
		mustMatch(t, ending.topic, c.statusTopic)
		mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
		ended := time.Now()
		mustMatch(t, waitForLine(t, lines), `{"command":["quit","0"]}`)
		mustMatch(t, time.Since(ended), exitGrace)
	})
}

// A second exit publishes the same ending and sends the quit at once,
// so a SIGTERM followed by the socket closing does not delay the pod by
// two graces.
func TestASecondExitOnAnEndedRunWaitsNoGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		server, client := net.Pipe()
		t.Cleanup(func() { server.Close() })
		lines := readAsync(server)

		c := &commander{
			statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
			bus:         bus,
			mpv:         client,
			grace:       time.Second,
			lastReport:  playReport{Item: 1, Position: "0:20:00", Ended: true},
			haveReport:  true,
			ended:       true,
		}

		started := time.Now()
		c.exit()
		waited := time.Since(started)

		ending := waitForPublish(t, brokers[0].pubs)
		mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
		mustMatch(t, waitForLine(t, lines), `{"command":["quit","0"]}`)
		mustMatch(t, waited < c.grace, true)
	})
}

// mpv closing its socket at the end of the playlist and SIGTERM each
// publish the ending with the position of the last report.
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
			synctest.Test(t, func(t *testing.T) {
				// conn is mpv's end of the socket. A pipe has no buffer, so
				// this goroutine reads and discards the observe requests.
				sidecar, conn := net.Pipe()
				t.Cleanup(func() {
					sidecar.Close()
					conn.Close()
				})
				go func() { _, _ = io.Copy(io.Discard, conn) }()

				bus, brokers, connected := startBus(t, 1, nil, nil)
				waitForConnect(t, connected)
				c := &commander{
					statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
					bus:         bus,
				}

				ctx, stop := context.WithCancel(t.Context())
				t.Cleanup(stop)
				done := make(chan struct{})
				go func() {
					defer close(done)
					c.reportOver(ctx, sidecar)
				}()

				// The position sends no report, because mpv has not reported
				// an item yet. The item sends the only report of this run.
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
		})
	}
}

func TestEveryReportAfterTheEndingCarriesTheMark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// Every report, the ending included, carries the UID of the pod that
// sent it, so the operator can refuse the ending of a pod that it has
// already replaced.
func TestEveryReportNamesThePodThatSentIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		c := &commander{
			statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
			podUID:      "pod-one",
			bus:         bus,
		}

		mustSucceed(t, c.send(playReport{Item: 1, Position: "0:20:00"}))
		running := waitForPublish(t, brokers[0].pubs)
		c.endRun()
		ending := waitForPublish(t, brokers[0].pubs)

		mustMatch(t, reportOf(t, running.payload), playReport{Item: 1, Position: "0:20:00", Pod: "pod-one"})
		mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true, Pod: "pod-one"})
	})
}

// The online message and the Last Will carry the pod's UID too, so the
// operator can refuse a late offline message from a replaced pod.
func TestTheAvailabilityNamesThePodThatSentIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		c := &commander{
			availabilityTopic: playAvailabilityTopic(defaultTopicBase, "house", "movie"),
			podUID:            "pod-one",
		}

		c.onConnect(bus)

		mustMatch(t, string(waitForPublish(t, brokers[0].pubs).payload), "online pod-one")
		mustMatch(t, string(c.will().Payload), "offline pod-one")
	})
}

// A run that never reported publishes no ending, because it has no item
// or position to send.
func TestARunThatNeverReportedPublishesNoEnding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		c := &commander{
			statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
			bus:         bus,
		}

		c.endRun()

		mustPublishNothing(t, brokers[0])
	})
}

// A live level on the volume topic reaches the display as one script
// message with the level and the mute state. It changes nothing in mpv,
// which keeps its default volume.
func TestALiveLevelSendsTheDisplayTheLevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		t.Cleanup(func() { server.Close() })
		lines := readAsync(server)

		c := &commander{
			volumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
			mpv:         client,
		}
		c.handle(c.volumeTopic, []byte(`{"level":0.63,"muted":true}`))

		mustMatch(t, waitForLine(t, lines), `{"command":["script-message","volume-changed","0.63","yes","yes"]}`)
	})
}

// The retained level that the broker sends at subscribe time is the
// volume from before the pod started, so it draws no indicator. A
// retained focus mark still reaches the focus check.
func TestARetainedLevelDrawsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events, focus := keyTestTopics()
		server, client := net.Pipe()
		t.Cleanup(func() { server.Close() })
		c := &commander{
			volumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
			mpv:         client,
			remotes:     map[string]playRemote{events: {focus: focus}},
			marks:       map[string]string{},
		}

		c.handleRetained(c.volumeTopic, []byte(`{"level":0.63,"muted":false}`))
		c.handleRetained(focus, []byte(keyTestPlayer))

		mustWriteNothing(t, server)
		mustMatch(t, c.marks[events], keyTestPlayer)
	})
}

// The Play's commands topic has no volume or mute actions. A program that
// sets a level publishes on the Player's volume/commands topic, so a
// volume action on the Play's topic writes nothing to mpv and publishes
// nothing.
func TestAVolumeActionOnThePlaysCommandsTopicDoesNothing(t *testing.T) {
	for _, action := range []string{"volume", "mute", "unmute"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bus, brokers, connected := startBus(t, 1, nil, nil)
				waitForConnect(t, connected)
				server, client := net.Pipe()
				t.Cleanup(func() { server.Close() })

				c := &commander{
					commandsTopic: playCommandsTopic(defaultTopicBase, "house", "movie"),
					volumeTopic:   playerVolumeTopic(defaultTopicBase, "house", "theater"),
					bus:           bus,
					mpv:           client,
					log:           io.Discard,
				}
				c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: action, Amount: 5}))

				mustPublishNothing(t, brokers[0])
				mustWriteNothing(t, server)
			})
		})
	}
}

// bridgeToMPV returns a commander connected to a pipe, and a channel of
// the lines that mpv would receive from it.
func bridgeToMPV(t *testing.T) (*commander, <-chan string) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })

	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	return &commander{mpv: client}, lines
}

func waitForLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(time.Second):
		t.Fatal("nothing reached mpv")
		return ""
	}
}

// mustNoLine fails when the sidecar has written anything to mpv once
// every goroutine in the synctest bubble is blocked.
func mustNoLine(t *testing.T, lines <-chan string) {
	t.Helper()
	synctest.Wait()
	select {
	case line := <-lines:
		t.Fatalf("the sidecar wrote %s, and should have written nothing", line)
	default:
	}
}

func mustEncode(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	mustSucceed(t, err)
	return payload
}

// readAsync sends each line that the sidecar writes to mpv's socket to
// the returned channel, in order.
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

// mustWriteNothing fails when the sidecar writes to mpv's socket within
// 100 ms.
func mustWriteNothing(t *testing.T, conn net.Conn) {
	t.Helper()
	mustSucceed(t, conn.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	buffer := make([]byte, 256)
	read, err := conn.Read(buffer)
	if err == nil {
		t.Fatalf("the sidecar wrote %q to mpv, and should have written nothing", buffer[:read])
	}
}

// writeEvent writes one event to the socket as a line of JSON, as mpv
// does.
func writeEvent(t *testing.T, conn net.Conn, line string) {
	t.Helper()
	_, err := conn.Write([]byte(line + "\n"))
	mustSucceed(t, err)
}

func reportOf(t *testing.T, payload []byte) playReport {
	t.Helper()
	var report playReport
	mustSucceed(t, json.Unmarshal(payload, &report))
	return report
}

// endedReport decodes a published payload and fails unless it has
// "ended":true.
func endedReport(t *testing.T, payload []byte) playReport {
	t.Helper()
	mustMatch(t, bytes.Contains(payload, []byte(`"ended":true`)), true)
	return reportOf(t, payload)
}

func TestFormatPositionWritesHoursMinutesSecondsAndFloorsTheFraction(t *testing.T) {
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

func TestApplyUpdatesTheStateAndReportsWhetherTheChangeNeedsAReportAtOnce(t *testing.T) {
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
			name:   "a property that no report contains",
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
		synctest.Test(t, func(t *testing.T) {
			send, sent := recordReports()

			changes := make(chan propertyChange, 8)
			go feedChanges(changes,
				changeOf("playlist-pos", "0"),
				changeOf("time-pos", "1.0"),
				changeOf("pause", "true"),
				changeOf("time-pos", "1.0"),
				changeOf("playlist-pos", "1"),
			)
			runReporter(t.Context(), changes, send, func(int) {}, nil, io.Discard)

			mustMatchAll(t, itemsAndPauses(sent()), []string{"1 playing", "1 paused", "2 paused"})
		})
	})

	t.Run("the position waits for the interval", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			send, sent := recordReports()

			changes := make(chan propertyChange)
			go func() {
				changes <- changeOf("playlist-pos", "0")
				changes <- changeOf("time-pos", "1.0")
				time.Sleep(reportInterval - time.Millisecond)
				changes <- changeOf("time-pos", "1.9")
				time.Sleep(time.Millisecond)
				changes <- changeOf("time-pos", "2.0")
				close(changes)
			}()
			runReporter(t.Context(), changes, send, func(int) {}, nil, io.Discard)

			mustMatchAll(t, positions(sent()), []string{"", "0:00:02"})
		})
	})

	t.Run("nothing is reported before an item is loaded", func(t *testing.T) {
		send, sent := recordReports()

		changes := make(chan propertyChange, 8)
		go feedChanges(changes,
			changeOf("pause", "false"),
			changeOf("time-pos", "1.0"),
			changeOf("duration", "60.0"),
		)
		runReporter(t.Context(), changes, send, func(int) {}, nil, io.Discard)

		mustMatch(t, len(sent()), 0)
	})

	t.Run("a report the bus drops does not stop the run", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			attempts := 0
			send := func(playReport) error {
				attempts++
				return errors.New("the broker is unreachable")
			}

			changes := make(chan propertyChange)
			go func() {
				changes <- changeOf("playlist-pos", "0")
				time.Sleep(reportInterval)
				changes <- changeOf("time-pos", "1.0")
				time.Sleep(reportInterval)
				changes <- changeOf("time-pos", "2.0")
				close(changes)
			}()
			runReporter(t.Context(), changes, send, func(int) {}, nil, io.Discard)

			mustMatch(t, attempts, 3)
		})
	})
}

// recordReports returns a reportSender that records every report, so a
// test can read the reports with no broker.
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

// itemsAndPauses reduces each report to its item and paused fields,
// which are all that the throttling tests check.
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

// The display requests the current block when its script loads, because
// a block that the sidecar sent before then reached no one.
func TestTheSidecarAnswersThePresentationRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, lines := bridgeToMPV(t)
		bridge.presentations = []json.RawMessage{
			json.RawMessage(`{"title":"First"}`),
			json.RawMessage(`{"type":"music","hint":"album"}`),
		}
		bridge.item = 2

		go bridge.serveMessages(requestFor(presentationRequestMessage))

		want := `{"command":["script-message","presentation","{\"type\":\"music\",\"hint\":\"album\"}"]}`
		select {
		case line := <-lines:
			mustMatch(t, line, want)
		case <-time.After(time.Second):
			t.Fatal("the sidecar answered nothing")
		}
	})
}

// Before mpv reports an item, the sidecar ignores the request, because
// it sends the first block when mpv reports the item.
func TestThePresentationRequestBeforeTheFirstItem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, lines := bridgeToMPV(t)
		bridge.presentations = []json.RawMessage{json.RawMessage(`{"title":"First"}`)}

		go bridge.serveMessages(requestFor(presentationRequestMessage))

		select {
		case line := <-lines:
			t.Errorf("the sidecar sent %q, want nothing", line)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

// requestFor returns a closed channel that holds one display message
// whose only argument is name, as the reader passes to serveMessages.
func requestFor(name string) <-chan clientMessage {
	messages := make(chan clientMessage, 1)
	messages <- clientMessage{Args: []string{name}}
	close(messages)
	return messages
}

// mpv keeps its default volume, so the sidecar sends it no level when
// the socket opens. It writes only its observe requests.
func TestASocketThatOpensGetsNoLevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		t.Cleanup(func() { server.Close() })
		lines := readAsync(server)

		c := &commander{volumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"), log: io.Discard}

		driveInBackground(t, c, client)

		skipObserves(t, lines)
		mustNoLine(t, lines)
	})
}

// driveInBackground runs the socket loop on a connection that the test
// reads, and stops it when the test ends.
func driveInBackground(t *testing.T, c *commander, conn net.Conn) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(func() {
		stop()
		conn.Close()
	})
	go c.drive(ctx, conn, func(playReport) error { return nil })
}

// skipObserves reads the observe requests that attach writes, so the
// next read returns the first command that the sidecar sends after them.
func skipObserves(t *testing.T, lines <-chan string) {
	t.Helper()
	for index, name := range observedProperties {
		mustMatch(t, waitForLine(t, lines),
			fmt.Sprintf(`{"command":["observe_property",%d,%q]}`, index+1, name))
	}
}
