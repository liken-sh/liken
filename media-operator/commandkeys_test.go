package main

// These tests cover the command sidecar's controller half: the focus
// gate, the key that asks for a cycle, and the press that reaches mpv.

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// The one controller these tests press, and the Player its mark must
// name for a press to act.
const (
	keyTestPlayer  = "theater"
	keyTestRemote  = "sofa"
	keyTestPlayNS  = "house"
	keyTestPlayrun = "movie"
)

func keyTestTopics() (events, focus string) {
	return remoteEventsTopic(defaultTopicBase, keyTestPlayNS, keyTestRemote),
		remoteFocusTopic(defaultTopicBase, keyTestPlayNS, keyTestRemote)
}

// A command sidecar wired to a fake broker and to a pipe that stands
// in for mpv's IPC socket, so a test reads both what the pod publishes
// and what it writes to the player.
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
		commandsTopic: playCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun),
		volumeTopic:   playerVolumeTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer),
		playerName:    keyTestPlayer,
		remotes:       map[string]playRemote{events: {focus: focus}},
		marks:         map[string]string{},
	}, brokers[0], lines
}

// focusHere marks the controller on this Play's own Player, the state
// a running pod holds once the retained mark arrives.
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
		t.Fatal("no command reached mpv")
		return ""
	}
}

// A key press on the focused controller reaches mpv as the command
// this pod binds it to.
func TestAFocusedKeyPressReachesMpv(t *testing.T) {
	c, _, lines := keyTestCommander(t)
	events, _ := keyTestTopics()
	focusHere(c)

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1}))

	mustMatch(t, nextLine(t, lines), `{"command":["no-osd","cycle","pause"]}`)
}

// A held arrow reaches the display on every repeat, because an arrow
// is one of the four kinds a person holds.
func TestAHeldArrowReachesTheDisplayOnTheRepeat(t *testing.T) {
	c, _, lines := keyTestCommander(t)
	events, _ := keyTestTopics()
	focusHere(c)

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_RIGHT", Value: 2}))

	mustMatch(t, nextLine(t, lines), `{"command":["script-message-to","display","right"]}`)
}

// A press whose mark names another Player reaches mpv not at all, so a
// controller pointed at another room does not drive this film.
func TestAPressFromAnUnfocusedControllerReachesMpvNotAtAll(t *testing.T) {
	c, _, lines := keyTestCommander(t)
	events, focus := keyTestTopics()
	c.handle(focus, []byte("gaming"))

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1}))
	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionSubtitles}))

	mustMatch(t, nextLine(t, lines), `{"command":["osd-auto","cycle","sub"]}`)
}

// A press that arrives before any mark reaches mpv not at all, because
// the gate is closed until the operator marks the controller.
func TestAPressBeforeAnyMarkReachesMpvNotAtAll(t *testing.T) {
	c, _, lines := keyTestCommander(t)
	events, _ := keyTestTopics()

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1}))
	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionSubtitles}))

	mustMatch(t, nextLine(t, lines), `{"command":["osd-auto","cycle","sub"]}`)
}

// The cycle key asks the operator to move the mark, on the remote's own
// cycle topic, and reaches mpv not at all.
func TestTheCycleKeyPublishesTheCycleRequest(t *testing.T) {
	c, broker, _ := keyTestCommander(t)
	events, focus := keyTestTopics()
	focusHere(c)

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_CYCLEWINDOWS", Value: 1}))

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, focus+focusCycleSuffix)
	mustMatch(t, published.retained, false)
}

// A volume key publishes the unit's next level, retained, and writes
// nothing to mpv: the subscription on the level is what applies it.
func TestAVolumeKeyPublishesTheUnitsNextLevel(t *testing.T) {
	c, broker, _ := keyTestCommander(t)
	events, _ := keyTestTopics()
	focusHere(c)

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_VOLUMEDOWN", Value: 1}))

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, c.volumeTopic)
	mustMatch(t, published.retained, true)
	state, decoded := parseVolumeState(published.payload)
	mustMatch(t, decoded, true)
	mustMatch(t, state.Level, defaultVolumeState().Level-volumeStep)
}

// The two topic lists the operator sets pair by position, so each
// controller's presses are gated on that controller's own mark.
func TestThePlayRemoteListsPairByPosition(t *testing.T) {
	remotes := playRemoteMap("events/sofa\nevents/armchair", "focus/sofa\nfocus/armchair")

	mustMatch(t, len(remotes), 2)
	mustMatch(t, remotes["events/sofa"], playRemote{focus: "focus/sofa"})
	mustMatch(t, remotes["events/armchair"], playRemote{focus: "focus/armchair"})
}

// A Play on a Player that names no controller subscribes to none.
func TestAPlayWithNoRemotesReadsNoController(t *testing.T) {
	mustMatch(t, len(playRemoteMap("", "")), 0)
}
