package main

// These tests cover the command sidecar's lines: one line per press or
// published command, with what it sent and what mpv answered, and no
// line for a repeat, a catch-up, or a message that changes nothing.

import (
	"encoding/json"
	"testing"
)

// loggingCommander is keyTestCommander with its lines collected and the
// controller's mark on this Play's Player.
func loggingCommander(t *testing.T) (*commander, *fakeBroker, <-chan string, *logBuffer) {
	t.Helper()
	c, broker, lines := keyTestCommander(t)
	var log logBuffer
	c.log = &log
	focusHere(c)
	return c, broker, lines, &log
}

// answerLast reads the command the sidecar wrote and answers it the way
// mpv does, under the request id it carried.
func answerLast(t *testing.T, c *commander, lines <-chan string, answer string) {
	t.Helper()
	var sent mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(nextLine(t, lines)), &sent))
	c.answer(mpvReply{ID: sent.RequestID, Error: answer})
}

func TestAPressLogsWhatItSentAndWhatMpvAnswered(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{key: "KEY_PLAYPAUSE", want: `KEY_PLAYPAUSE from remote house/sofa: pause or resume, sent ["no-osd","cycle","pause"] to mpv, mpv answered success`},
		{key: "KEY_FASTFORWARD", want: `KEY_FASTFORWARD from remote house/sofa: seek +10 s, sent ["no-osd","seek",10] to mpv, mpv answered success`},
		{key: "KEY_REWIND", want: `KEY_REWIND from remote house/sofa: seek -10 s, sent ["no-osd","seek",-10] to mpv, mpv answered success`},
		{key: "KEY_NEXTSONG", want: `KEY_NEXTSONG from remote house/sofa: chapter +1, sent ["no-osd","add","chapter",1] to mpv, mpv answered success`},
		{key: "KEY_SUBTITLE", want: `KEY_SUBTITLE from remote house/sofa: next subtitle track, sent ["osd-auto","cycle","sub"] to mpv, mpv answered success`},
		{key: "KEY_AUDIO", want: `KEY_AUDIO from remote house/sofa: next audio track, sent ["osd-auto","cycle","audio"] to mpv, mpv answered success`},
		{key: "KEY_RIGHT", want: `KEY_RIGHT from remote house/sofa: right, sent ["script-message","right"] to mpv, mpv answered success`},
	}
	for _, each := range cases {
		t.Run(each.key, func(t *testing.T) {
			c, _, lines, log := loggingCommander(t)
			events, _ := keyTestTopics()

			c.handle(events, mustEncode(t, keyEvent{Key: each.key, Value: 1}))
			answerLast(t, c, lines, mpvAnswerSuccess)

			mustLogOnce(t, log, "command: "+each.want)
		})
	}
}

// mpv's refusal reaches the line in mpv's own words.
func TestAPressLogsMpvsRefusalWordForWord(t *testing.T) {
	c, _, lines, log := loggingCommander(t)
	events, _ := keyTestTopics()

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_NEXTSONG", Value: 1}))
	answerLast(t, c, lines, "property unavailable")

	mustLogOnce(t, log, "chapter +1", "mpv answered property unavailable")
}

// A press the pod does not act on says why once, and its repeat and its
// release say nothing more.
func TestAnIgnoredPressSaysWhyOnce(t *testing.T) {
	cases := []struct {
		name string
		mark string
		key  string
		want string
	}{
		{
			name: "focus on another player",
			mark: "gaming",
			key:  "KEY_PLAYPAUSE",
			want: "command: KEY_PLAYPAUSE from remote house/sofa ignored, because focus is on player gaming",
		},
		{
			name: "no mark yet",
			key:  "KEY_PLAYPAUSE",
			want: "command: KEY_PLAYPAUSE from remote house/sofa ignored, because no focus mark names a player for this remote",
		},
		{
			name: "a key with no row",
			mark: keyTestPlayer,
			key:  "KEY_A",
			want: "command: KEY_A from remote house/sofa ignored, because the playback table has no row for it",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			c, _, _ := keyTestCommander(t)
			var log logBuffer
			c.log = &log
			events, focus := keyTestTopics()
			c.handle(focus, []byte(each.mark))

			for _, value := range []int32{1, 2, 2, 0} {
				c.handle(events, mustEncode(t, keyEvent{Key: each.key, Value: value}))
			}

			mustLogOnce(t, &log, each.want)
		})
	}
}

// A held key acts on every repeat and writes two lines for the whole
// hold: the press, and the release with the count.
func TestAHeldKeyLogsThePressAndTheReleaseAlone(t *testing.T) {
	c, _, lines, log := loggingCommander(t)
	events, _ := keyTestTopics()

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_FASTFORWARD", Value: 1}))
	answerLast(t, c, lines, mpvAnswerSuccess)
	nextLine(t, lines)
	for range 3 {
		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_FASTFORWARD", Value: 2}))
		mustMatch(t, nextLine(t, lines), `{"command":["no-osd","seek",10]}`)
		nextLine(t, lines)
	}
	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_FASTFORWARD", Value: 0}))

	mustMatchAll(t, log.lines(), []string{
		`command: KEY_FASTFORWARD from remote house/sofa: seek +10 s, sent ["no-osd","seek",10] to mpv, mpv answered success`,
		"command: KEY_FASTFORWARD from remote house/sofa released after 3 repeats",
	})
}

func TestAPublishedCommandLogsWhatItSentAndWhatMpvAnswered(t *testing.T) {
	c, _, lines, log := loggingCommander(t)

	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionSeek, Amount: 30}))
	answerLast(t, c, lines, mpvAnswerSuccess)

	mustLogOnce(t, log, `command: a message on `+c.commandsTopic+`: seek +30 s, sent ["no-osd","seek",30] to mpv, mpv answered success`)
}

func TestAPublishedCommandThePodCannotRunSaysWhy(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "a payload that does not decode",
			payload: "not json",
			want:    "ignored, because it does not decode: invalid character 'o' in literal null (expecting 'u')",
		},
		{
			name:    "an action this build has no command for",
			payload: `{"action":"brightness","amount":1}`,
			want:    `ignored, because this build has no command for the action "brightness"`,
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			c, _, _, log := loggingCommander(t)

			c.handle(c.commandsTopic, []byte(each.payload))

			mustLogOnce(t, log, "command: a message on "+c.commandsTopic+" "+each.want)
		})
	}
}

// A volume press says what level it published and where. The level
// reaches mpv when the topic delivers it back, and that has its own
// line.
func TestAVolumePressLogsTheLevelItPublished(t *testing.T) {
	c, broker, _, log := loggingCommander(t)
	events, _ := keyTestTopics()

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_VOLUMEDOWN", Value: 1}))
	waitForPublish(t, broker.pubs)

	mustLogOnce(t, log, "command: KEY_VOLUMEDOWN from remote house/sofa: volume -5, published level 95, not muted to "+c.volumeTopic)
}

func TestAVolumePressOnAUnitWithNoSinksSaysWhy(t *testing.T) {
	c, _, _, log := loggingCommander(t)
	c.volumeTopic = ""
	events, _ := keyTestTopics()

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_MUTE", Value: 1}))

	mustLogOnce(t, log, "command: KEY_MUTE from remote house/sofa ignored, because the player has no sinks")
}

// The first level of a session is the catch-up and writes no line. A
// live level that moves writes one line when mpv has answered both of
// its commands, and a level that repeats the held one writes none.
func TestALiveLevelLogsWhereItWent(t *testing.T) {
	c, _, lines, log := loggingCommander(t)

	c.handle(c.volumeTopic, []byte(`{"level":40,"muted":false}`))
	nextLine(t, lines)
	nextLine(t, lines)
	c.handle(c.volumeTopic, []byte(`{"level":45,"muted":true}`))
	answerLast(t, c, lines, mpvAnswerSuccess)
	answerLast(t, c, lines, mpvAnswerSuccess)
	nextLine(t, lines)
	c.handle(c.volumeTopic, []byte(`{"level":45,"muted":true}`))
	nextLine(t, lines)
	nextLine(t, lines)

	mustLogOnce(t, log, "command: "+c.volumeTopic+` delivered level 45, muted, sent ["no-osd","set","volume","45"] and ["no-osd","set","mute","yes"] to mpv, mpv answered success`)
}

// While the mark stands the level goes to the owner, and the line says
// so. The mark itself writes a line when it changes hands and none when
// a reconnect delivers it again.
func TestTheOwnerMarkLogsWhoAppliesTheLevel(t *testing.T) {
	c, _, lines, log := loggingCommander(t)
	c.volumeOwnerTopic = playerVolumeOwnerTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
	drain := func(count int) {
		for range count {
			nextLine(t, lines)
		}
	}

	c.handle(c.volumeOwnerTopic, []byte(`{"owner":"receiver/den-receiver"}`))
	drain(2)
	c.handle(c.volumeOwnerTopic, []byte(`{"owner":"receiver/den-receiver"}`))
	drain(2)
	c.handle(c.volumeTopic, []byte(`{"level":30,"muted":false}`))
	drain(2)
	c.handle(c.volumeTopic, []byte(`{"level":35,"muted":false}`))
	drain(2)
	c.handle(c.volumeOwnerTopic, nil)
	drain(2)

	mustMatchAll(t, log.lines(), []string{
		"command: " + c.volumeOwnerTopic + " names receiver/den-receiver, so receiver/den-receiver applies the level and mpv stays at unity",
		"command: " + c.volumeTopic + " delivered level 35, not muted, applied by receiver/den-receiver, mpv stays at unity",
		"command: " + c.volumeOwnerTopic + " cleared, so mpv applies level 35, not muted",
	})
}

func TestAClearedMarkBeforeAnyLevelSaysSo(t *testing.T) {
	c, _, _, log := loggingCommander(t)
	c.volumeOwnerTopic = playerVolumeOwnerTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
	c.volumeOwned, c.volumeOwner = true, "receiver/den-receiver"

	c.handle(c.volumeOwnerTopic, nil)

	mustLogOnce(t, log, "command: "+c.volumeOwnerTopic+" cleared, and no level has arrived yet")
}

func TestTheCycleKeyLogsTheRequest(t *testing.T) {
	c, broker, _, log := loggingCommander(t)
	events, focus := keyTestTopics()

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_CYCLEWINDOWS", Value: 1}))
	waitForPublish(t, broker.pubs)

	mustLogOnce(t, log, "command: KEY_CYCLEWINDOWS from remote house/sofa: cycle focus, published the cycle request to "+focus+focusCycleSuffix)
}

func TestTheCycleKeyWithNoFocusTopicSaysWhy(t *testing.T) {
	c, _, _, log := loggingCommander(t)

	c.publishCycle("KEY_CYCLEWINDOWS from remote house/sofa", playRemote{})

	mustLogOnce(t, log, "command: KEY_CYCLEWINDOWS from remote house/sofa ignored, because the remote has no focus topic")
}

func TestTheHomeKeyLogsTheAskAndTheEnding(t *testing.T) {
	cases := []struct {
		name   string
		player string
		want   string
	}{
		{
			name:   "a pod that read its player",
			player: keyTestPlayer,
			want: "command: KEY_HOMEPAGE from remote house/sofa: home, published the home ask to " +
				playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer) + ", so the run ends",
		},
		{
			name: "a pod that read no player",
			want: "command: KEY_HOMEPAGE from remote house/sofa: home, with no player commands topic to ask on, so the run ends",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			c, _, _, log := loggingCommander(t)
			if each.player != "" {
				c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, each.player)
			}

			events, _ := keyTestTopics()

			c.handle(events, mustEncode(t, keyEvent{Key: "KEY_HOMEPAGE", Value: 1}))

			mustLogOnce(t, log, each.want)
		})
	}
}

func TestTheUpNextSelectLogsTheAsk(t *testing.T) {
	cases := []struct {
		name   string
		next   string
		player string
		want   string
	}{
		{
			name:   "a Play with a next block",
			next:   `{"title":"Next","request":{"id":"b"}}`,
			player: keyTestPlayer,
			want: "command: the display took the up-next offer, published play-next to " +
				playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer),
		},
		{
			name:   "a Play with no next block",
			player: keyTestPlayer,
			want:   "command: the display took the up-next offer, ignored, because the Play has no next block",
		},
		{
			name: "a pod that read no player",
			next: `{"title":"Next"}`,
			want: "command: the display took the up-next offer, ignored, because the pod has no player commands topic to ask on",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			c, _, _, log := loggingCommander(t)
			c.next = parseNext(each.next)
			if each.player != "" {
				c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, each.player)
			}

			c.serveMessage([]string{nextRequestMessage})

			mustLogOnce(t, log, each.want)
		})
	}
}

// A command with no socket under it logs at once, because no answer
// will come.
func TestACommandWithNoSocketSaysSo(t *testing.T) {
	var log logBuffer
	c := &commander{log: &log}

	c.apply("KEY_PLAYPAUSE from remote house/sofa", mediaCommand{Action: actionPause}, false)

	mustLogOnce(t, &log, `sent ["no-osd","cycle","pause"] to mpv, not delivered, because mpv's socket is not open`)
}

// A command whose write fails logs the write's own error.
func TestACommandWhoseWriteFailsSaysWhy(t *testing.T) {
	c, _, _, log := loggingCommander(t)
	c.mpv.Close()

	c.apply("a message on the commands topic", mediaCommand{Action: actionPause}, false)

	mustLogOnce(t, log, "not delivered: io: read/write on closed pipe")
}

// A line still waiting when the socket closes is logged then, because
// mpv's answer will not come.
func TestALineWaitingOnAClosedSocketIsLoggedOnce(t *testing.T) {
	c, _, lines, log := loggingCommander(t)
	events, _ := keyTestTopics()
	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1}))
	nextLine(t, lines)

	c.detach()
	c.answer(mpvReply{ID: 1, Error: mpvAnswerSuccess})

	mustLogOnce(t, log, "pause or resume", "mpv closed its socket before it answered")
}

// The display's exit press ends the run with two lines: why it ended,
// and the ending the sidecar published.
func TestTheExitPressLogsWhyAndTheEnding(t *testing.T) {
	c, _, lines, log := loggingCommander(t)
	c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun)
	c.lastReport, c.haveReport = playReport{Item: 2, Position: "0:41:07"}, true

	c.serveMessage([]string{exitMessage})
	nextLine(t, lines)
	c.exit()
	nextLine(t, lines)

	mustMatchAll(t, log.lines(), []string{
		"command: the display asked to end the run, because back was pressed at the bare film",
		"command: run ended at item 2, 0:41:07, published the ending to " + c.statusTopic,
	})
}

// The reporter logs what a person sees change and nothing for a
// position, a decode property, or a value that repeats.
func TestTheReporterLogsWhatAPersonSeesChange(t *testing.T) {
	var log logBuffer
	changes := make(chan propertyChange, 16)
	go feedChanges(changes,
		changeOf(audioLanguageProperty, `"eng"`),
		changeOf("playlist-pos", "0"),
		changeOf("time-pos", "61.5"),
		changeOf(codecProperty, `"h264"`),
		changeOf("pause", "true"),
		changeOf("pause", "true"),
		changeOf("pause", "false"),
		changeOf(subtitleLanguageProperty, `"fra"`),
		changeOf(audioLanguageProperty, `""`),
		changeOf("playlist-pos", "1"),
	)

	runReporter(t.Context(), changes, func(playReport) error { return nil }, func(int) {}, nil, &log)

	mustMatchAll(t, log.lines(), []string{
		"command: mpv reports the run started at item 1",
		"command: mpv reports paused at 0:01:01",
		"command: mpv reports playing at 0:01:01",
		`command: mpv reports subtitle language "fra"`,
		"command: mpv reports audio language not stated",
		"command: mpv reports item 2 plays, after item 1",
	})
}
