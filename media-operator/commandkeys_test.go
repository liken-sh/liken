package main

import (
	"bufio"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

// A press has an effect only when the focus mark of keyTestRemote is
// keyTestPlayer.
const (
	keyTestPlayer = "theater"
	keyTestRemote = "sofa"
	keyTestPlayNS = "house"
	keyTestPlay   = "movie"
)

func keyTestTopics() (events, focus string) {
	return remoteEventsTopic(defaultTopicBase, keyTestPlayNS, keyTestRemote),
		remoteFocusTopic(defaultTopicBase, keyTestPlayNS, keyTestRemote)
}

// keyTestCommander returns a command sidecar connected to a fake broker,
// and the lines that it writes to a pipe in place of mpv's IPC socket.
func keyTestCommander(t *testing.T) (*commander, *fakeBroker, <-chan string) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)

	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	events, focus := keyTestTopics()
	return &commander{
		bus:           bus,
		mpv:           client,
		commandsTopic: playCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlay),
		volumeTopic:   playerVolumeTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer),
		playerName:    keyTestPlayer,
		remotes:       map[string]playRemote{events: {focus: focus}},
		marks:         map[string]string{},
	}, brokers[0], lines
}

// focusHere delivers a focus mark of keyTestPlayer, as the broker
// delivers the retained mark after the sidecar subscribes.
func focusHere(c *commander) {
	_, focus := keyTestTopics()
	c.handle(focus, []byte(keyTestPlayer))
}

func nextLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(time.Second):
		t.Fatal("no command reached mpv within one second")
		return ""
	}
}

func TestAFocusedKeyPressReachesMpv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, lines := keyTestCommander(t)
		events, _ := keyTestTopics()
		focusHere(c)

		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1}))

		mustMatch(t, nextLine(t, lines), `{"command":["no-osd","cycle","pause"],"request_id":1}`)
	})
}

func TestARepeatOfAHeldArrowKeyReachesMpv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, lines := keyTestCommander(t)
		events, _ := keyTestTopics()
		focusHere(c)

		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_RIGHT", Value: 2}))

		mustMatch(t, nextLine(t, lines), `{"command":["script-message","right","repeat"]}`)
	})
}

// The subtitles command is the first line that reaches mpv, so the press
// before it wrote nothing.
func TestAPressFromAControllerFocusedElsewhereDoesNotReachMpv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, lines := keyTestCommander(t)
		events, focus := keyTestTopics()
		c.handle(focus, []byte("gaming"))

		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1}))
		c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionSubtitles}))

		mustMatch(t, nextLine(t, lines), `{"command":["osd-auto","cycle","sub"],"request_id":1}`)
	})
}

// As in the test above, the first line that reaches mpv is the
// subtitles command, so the press wrote nothing.
func TestAPressBeforeAnyFocusMarkDoesNotReachMpv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, lines := keyTestCommander(t)
		events, _ := keyTestTopics()

		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1}))
		c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionSubtitles}))

		mustMatch(t, nextLine(t, lines), `{"command":["osd-auto","cycle","sub"],"request_id":1}`)
	})
}

func TestTheCycleKeyPublishesTheCycleRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, broker, _ := keyTestCommander(t)
		events, focus := keyTestTopics()
		focusHere(c)

		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_CYCLEWINDOWS", Value: 1}))

		published := waitForPublish(t, broker.pubs)
		mustMatch(t, published.topic, focus+focusCycleSuffix)
		mustMatch(t, published.retained, false)
	})
}

// The operator handles volume keys from the same events topic, so the
// sidecar publishes nothing, writes nothing to mpv, and logs nothing.
func TestTheSidecarIgnoresVolumeKeys(t *testing.T) {
	for _, key := range []string{"KEY_VOLUMEUP", "KEY_VOLUMEDOWN", "KEY_MUTE", "KEY_UNMUTE"} {
		t.Run(key, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, broker, lines := keyTestCommander(t)
				var log logBuffer
				c.log = &log
				events, _ := keyTestTopics()
				focusHere(c)

				for _, value := range []int32{1, 2, 0} {
					c.handle(events, mustEncode(t, keyEvent{Key: key, Value: value}))
				}

				mustPublishNothing(t, broker)
				mustNoLine(t, lines)
				mustMatch(t, len(log.lines()), 0)
			})
		})
	}
}

func TestPlayRemoteMapPairsTopicsByPosition(t *testing.T) {
	remotes := playRemoteMap("events/sofa\nevents/armchair", "focus/sofa\nfocus/armchair")

	mustMatch(t, len(remotes), 2)
	mustMatch(t, remotes["events/sofa"], playRemote{focus: "focus/sofa"})
	mustMatch(t, remotes["events/armchair"], playRemote{focus: "focus/armchair"})
}

func TestPlayRemoteMapIsEmptyWithNoRemotes(t *testing.T) {
	mustMatch(t, len(playRemoteMap("", "")), 0)
}
