package main

// These tests cover the idle sidecar's fade: the quiet window it arms
// only while the unit plays nothing, the press that restarts it and
// lifts the shade, the status that leaves Idle, and the back press a
// person sleeps the screen with by hand.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// fakeMPV is a stand-in for the idle mpv. It answers every dial the
// sidecar makes, replies success to each command, and hands the
// command lines to the test in the order they arrived. The replies
// matter, because the sidecar's dialog waits for each one before its
// next write.
type fakeMPV struct {
	commands chan string
}

// startFakeMPV starts the stand-in on a socket of this test's own and
// points the sidecar at it.
func startFakeMPV(t *testing.T) *fakeMPV {
	t.Helper()
	useDialDelay(t, time.Millisecond)
	path := filepath.Join(t.TempDir(), "mpv.sock")
	useSocket(t, path)
	listener, err := net.Listen("unix", path)
	mustSucceed(t, err)
	t.Cleanup(func() { listener.Close() })
	fake := &fakeMPV{commands: make(chan string, 32)}
	go fake.serve(listener)
	return fake
}

func (f *fakeMPV) serve(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go f.talk(conn)
	}
}

func (f *fakeMPV) talk(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		f.commands <- scanner.Text()
		fmt.Fprintln(conn, `{"error":"success"}`)
	}
}

// next returns the next command the sidecar wrote. The wait is
// bounded, so a sidecar that writes nothing fails the test instead of
// hanging it.
func (f *fakeMPV) next(t *testing.T) string {
	t.Helper()
	return f.nextWithin(t, 2*time.Second)
}

// nextWithin returns the next command, and fails when none arrives
// inside the window. A test that proves a quiet window was not
// restarted reads with an allowance shorter than a restart would need.
func (f *fakeMPV) nextWithin(t *testing.T, window time.Duration) string {
	t.Helper()
	select {
	case line := <-f.commands:
		return line
	case <-time.After(window):
		t.Fatalf("the sidecar wrote no command inside %s", window)
		return ""
	}
}

// quiet fails when the sidecar writes anything at all inside this
// window.
func (f *fakeMPV) quiet(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case line := <-f.commands:
		t.Fatalf("the sidecar wrote %s, and should have written nothing", line)
	case <-time.After(window):
	}
}

// fadeTopics builds the topics of one unit and the one controller
// these tests press.
func fadeTopics() (events, keymap string) {
	return remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		keymapTopic(defaultTopicBase, "gamepad")
}

// fadingCommander builds one idle sidecar for a Player, with its quiet
// window in milliseconds so the fade lands inside a test rather than
// ten minutes after it. A sidecar runs as long as its pod, so nothing
// stops its timer in production and the test stops it here: a window
// still armed when the test ends would otherwise fire into the next
// test's stand-in mpv.
func fadingCommander(t *testing.T, fade time.Duration, remotes map[string]string) *idleCommander {
	t.Helper()
	ic := &idleCommander{
		commandsTopic: playerCommandsTopic(defaultTopicBase, "house", "theater"),
		statusTopic:   playerStatusTopic(defaultTopicBase, "house", "theater"),
		runCtx:        context.Background(),
		remotes:       remotes,
		fadeAfter:     fade,
		tables:        map[string][]compiledBinding{},
		// The panel is lit when a pod starts, the same state the
		// sidecar assumes on the metal.
		panel:   panelOn,
		repeats: map[uint16]context.CancelFunc{},
	}
	t.Cleanup(func() {
		// A repeat still held when the test ends would tick into the
		// next test's stand-in broker, so every cancel runs here.
		ic.repeatMu.Lock()
		for code, cancel := range ic.repeats {
			cancel()
			delete(ic.repeats, code)
		}
		ic.repeatMu.Unlock()
		ic.mu.Lock()
		defer ic.mu.Unlock()
		ic.idle = false
		ic.rearmLocked()
	})
	return ic
}

// sendActivity hands the sidecar one status and reads past the copy it
// forwards to the display, so a test reads only what the fade itself
// wrote.
func sendActivity(t *testing.T, ic *idleCommander, fake *fakeMPV, activity string) {
	t.Helper()
	ic.handle(ic.statusTopic, []byte(`{"activity":"`+activity+`"}`))
	mustMatch(t, fake.next(t),
		`{"command":["script-message","player-status","{\"activity\":\"`+activity+`\"}"]}`)
}

// sendEvent publishes one controller event on the events topic, the
// way the standing remote pod does.
func sendEvent(t *testing.T, ic *idleCommander, events string, event remoteEvent) {
	t.Helper()
	payload, err := json.Marshal(event)
	mustSucceed(t, err)
	ic.handle(events, payload)
}

// sendPress presses the east button down.
func sendPress(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, remoteEvent{Type: evKey, Code: buttonCodes["BTN_EAST"], Value: 1})
}

// sendRelease lets the east button back up. The standing remote pod
// publishes this edge too, so the fade reads it and must do nothing
// with it.
func sendRelease(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, remoteEvent{Type: evKey, Code: buttonCodes["BTN_EAST"], Value: 0})
}

// bindBack hands the sidecar the compiled table that binds the east
// button to back, the way the operator publishes it on the retained
// keymap topic.
func bindBack(t *testing.T, ic *idleCommander, keymap string) {
	t.Helper()
	payload, err := json.Marshal([]compiledBinding{{
		EventType: evKey,
		Code:      buttonCodes["BTN_EAST"],
		Value:     1,
		Action:    actionBack,
	}})
	mustSucceed(t, err)
	ic.handle(keymap, payload)
}

const sleepCommand = `{"command":["script-message","player-sleep"]}`
const wakeCommand = `{"command":["script-message","player-wake"]}`

// A unit that plays nothing arms the quiet window, and the window running
// out draws the shade.
func TestIdleFadeSleepsAfterTheQuietWindow(t *testing.T) {
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, nil)

	sendActivity(t, ic, fake, playerIdle)

	mustMatch(t, fake.next(t), sleepCommand)
}

// A Play never sleeps the screen, so the window stays off while one runs.
func TestIdleFadeNeverArmsWhileAPlayRuns(t *testing.T) {
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, nil)

	sendActivity(t, ic, fake, playerPlaying)

	fake.quiet(t, 100*time.Millisecond)
}

// A quiet window of zero is the cluster stating that this screen never dims
// on its own, so the window never arms whatever the unit does.
func TestIdleFadeNeverArmsAtZero(t *testing.T) {
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 0, nil)

	sendActivity(t, ic, fake, playerIdle)

	fake.quiet(t, 100*time.Millisecond)
}

// A press restarts the window from the moment of the press, so a screen a
// person touches keeps the whole window again rather than the remainder of
// the last one.
func TestIdleFadeAPressResetsTheWindow(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 100*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, fake, playerIdle)
	time.Sleep(60 * time.Millisecond)
	sendPress(t, ic, events)

	// The first window ran out at 100ms. Nothing arrives through 130ms, so the
	// press moved it.
	fake.quiet(t, 70*time.Millisecond)
	mustMatch(t, fake.next(t), sleepCommand)
}

// A press on a sleeping screen lifts the shade, whether or not the
// controller has a keymap. The press is the person, so it is the wake
// signal.
func TestIdleFadeAPressWakesASleepingScreen(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	sendPress(t, ic, events)

	mustMatch(t, fake.next(t), wakeCommand)
}

// A status that leaves Idle lifts the shade, so a Play started from another
// room shows the film and not a black screen.
func TestIdleFadeAStatusThatLeavesIdleWakes(t *testing.T) {
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, nil)

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	sendActivity(t, ic, fake, playerStarting)

	mustMatch(t, fake.next(t), wakeCommand)
	fake.quiet(t, 100*time.Millisecond)
}

// A press named back, on a unit that plays nothing, draws the shade at once
// rather than waiting out the window.
func TestIdleBackSleepsTheScreenAtOnce(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, time.Hour, map[string]string{events: keymap})
	bindBack(t, ic, keymap)

	sendActivity(t, ic, fake, playerIdle)
	sendPress(t, ic, events)

	mustMatch(t, fake.next(t), sleepCommand)
}

// The same back press lifts the shade again, because any press wakes a
// sleeping screen. So one button works the screen from either side.
func TestIdleBackWakesTheScreenItSlept(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, time.Hour, map[string]string{events: keymap})
	bindBack(t, ic, keymap)

	sendActivity(t, ic, fake, playerIdle)
	sendPress(t, ic, events)
	mustMatch(t, fake.next(t), sleepCommand)
	sendPress(t, ic, events)

	mustMatch(t, fake.next(t), wakeCommand)
}

// Back sleeps nothing while a Play runs. The film owns the screen, and back
// means what the display makes of it there.
func TestIdleBackSleepsNothingWhileAPlayRuns(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, map[string]string{events: keymap})
	bindBack(t, ic, keymap)

	sendActivity(t, ic, fake, playerPlaying)
	sendPress(t, ic, events)

	fake.quiet(t, 100*time.Millisecond)
}

// A controller with no keymap names no action, so its presses reset the
// window and never sleep the screen by hand.
func TestIdleBackNeedsAKeymapToNameThePress(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, time.Hour, map[string]string{events: ""})

	sendActivity(t, ic, fake, playerIdle)
	sendPress(t, ic, events)

	fake.quiet(t, 100*time.Millisecond)
}

// The two remote lists travel one per line and stay aligned by position, so
// each events topic reads the keymap of its own controller.
func TestIdleRemoteMapPairsTheTwoLists(t *testing.T) {
	remotes := idleRemoteMap("events/sofa\nevents/armchair", "keymaps/gamepad\nkeymaps/pad")

	mustMatch(t, len(remotes), 2)
	mustMatch(t, remotes["events/sofa"], "keymaps/gamepad")
	mustMatch(t, remotes["events/armchair"], "keymaps/pad")
}

// A blank line, and a keymap list shorter than the events list, leave that
// controller with no keymap rather than shifting the pairing.
func TestIdleRemoteMapLeavesAMissingKeymapBlank(t *testing.T) {
	remotes := idleRemoteMap("events/sofa\nevents/armchair", "\nkeymaps/pad")

	mustMatch(t, remotes["events/sofa"], "")
	mustMatch(t, remotes["events/armchair"], "keymaps/pad")
}

// An unset or unreadable quiet window fades nothing, because the operator
// settles this field for every Player and a sidecar that guessed would dim
// a screen the cluster never asked it to.
func TestIdleFadeAfterReadsTheSeconds(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "the resolved default", value: "600", want: 600 * time.Second},
		{name: "a minute", value: "60", want: time.Minute},
		{name: "the fade turned off", value: "0", want: 0},
		{name: "nothing at all", value: "", want: 0},
		{name: "a word", value: "soon", want: 0},
		{name: "a negative", value: "-5", want: 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, idleFadeAfter(one.value), one.want)
		})
	}
}

// A button press and the release that follows it are one act by one person.
// The standing remote pod publishes both edges, so the release must leave
// the shade the press drew exactly where it is. Otherwise back sleeps the
// screen and its own release wakes it a tenth of a second later.
func TestIdleBackHoldsTheScreenAsleepThroughTheRelease(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, time.Hour, map[string]string{events: keymap})
	bindBack(t, ic, keymap)

	sendActivity(t, ic, fake, playerIdle)
	sendPress(t, ic, events)
	mustMatch(t, fake.next(t), sleepCommand)
	sendRelease(t, ic, events)

	fake.quiet(t, 100*time.Millisecond)
}

// A release on a sleeping screen is a control coming back up, not a person
// reaching for one, so the screen stays dark.
func TestIdleFadeAReleaseDoesNotWakeASleepingScreen(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	sendRelease(t, ic, events)

	fake.quiet(t, 100*time.Millisecond)
}

// A release restarts nothing, so the shade falls on the schedule the last
// press set. The window here runs 200ms and the release lands at 120ms: the
// read allows 140ms, which the remaining 80ms fits and a restarted 200ms
// window does not.
func TestIdleFadeAReleaseDoesNotRestartTheWindow(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 200*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, fake, playerIdle)
	time.Sleep(120 * time.Millisecond)
	sendRelease(t, ic, events)

	mustMatch(t, fake.nextWithin(t, 140*time.Millisecond), sleepCommand)
}

// A d-pad returning to center reads as value 0 on the hat axis, the same
// shape as a button release, so it is not a press either.
func TestIdleFadeAHatReturningToCenterDoesNotWake(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	sendEvent(t, ic, events, remoteEvent{Type: evAbs, Code: axisCodes["ABS_HAT0X"], Value: 0})

	fake.quiet(t, 100*time.Millisecond)
}

// The down edge is the person. A button reports it as value 1, and a hat as
// either of its two directions, while a release, a held button's
// autorepeat, and a hat at rest are not presses.
func TestIsPressEdgeReadsTheDownEdgeAlone(t *testing.T) {
	cases := []struct {
		name  string
		event remoteEvent
		want  bool
	}{
		{name: "a button down", event: remoteEvent{Type: evKey, Value: 1}, want: true},
		{name: "a button up", event: remoteEvent{Type: evKey, Value: 0}},
		{name: "a held button repeating", event: remoteEvent{Type: evKey, Value: 2}},
		{name: "a hat one way", event: remoteEvent{Type: evAbs, Value: 1}, want: true},
		{name: "a hat the other way", event: remoteEvent{Type: evAbs, Value: -1}, want: true},
		{name: "a hat at rest", event: remoteEvent{Type: evAbs, Value: 0}},
		{name: "an event of no bindable type", event: remoteEvent{Type: 0x04, Value: 1}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, isPressEdge(one.event), one.want)
		})
	}
}

// A payload that does not decode is not a person, so it neither wakes the
// screen nor restarts the quiet window.
func TestIdleFadeIgnoresAnEventThatDoesNotDecode(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	ic := fadingCommander(t, 20*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	ic.handle(events, []byte("not json"))

	fake.quiet(t, 100*time.Millisecond)
}
