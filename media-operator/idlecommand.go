package main

// The idle command sidecar is the idle screen's one path to the bus. It
// recreates the idle mpv's Wayland surface when a Play on the same Player
// ends, and it forwards the unit's live state into the display script. It
// is a native sidecar beside the idle mpv, subscribing to the Player's
// commands and status topics and driving mpv through the JSON IPC socket
// the two containers share on the pod's ipc volume.
//
// The recreate is the whole fix for a seatless compositor. Weston's
// kiosk-shell reveals a lower surface only along a code path gated on a
// seat, and liken's compositor runs with require-input=false and no
// input devices, so it has no seat. When a Play's surface is destroyed
// the idle clock stays hidden and the screen goes black, though the idle
// mpv still runs. A freshly mapped surface is revealed along a
// seat-independent path, so the sidecar makes the idle mpv destroy and
// recreate its surface, and kiosk shows the fresh one. The idle mpv
// keeps running across the cycle, so the gap is sub-second and no pod
// restarts.
//
// The sidecar holds no API credentials and reaches the control plane
// only through the operator's own place on the bus.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// surfaceTeardownGap is the pause between clearing force-window and
// setting it again. mpv's video output destroys the surface on the first
// set and builds a new one on the second, and the gap lets the surface
// fully destroy before it is recreated, so the two do not race inside the
// video output. 200ms is well above the teardown a local Wayland surface
// needs and far below what a person notices as a gap in the clock.
var surfaceTeardownGap = 200 * time.Millisecond

// idleDialTimeout bounds one re-present's wait for the idle mpv socket.
// The idle mpv has run since the pod started, so the dial connects at
// once in the ordinary case. The timeout only limits the rare case of a
// re-present that arrives while the idle mpv restarts after a crash, so
// the bus reader is not held on a socket that is not coming back.
var idleDialTimeout = 5 * time.Second

// The two script messages the sidecar broadcasts into the idle display
// script. player-status carries one status payload as its single argument,
// and revealed says the idle surface is on screen again, which is the
// frame the display starts the mark's ramp-down from.
const (
	playerStatusMessage = "player-status"
	revealedMessage     = "revealed"
)

// The two script messages that draw and lift the shade over the idle
// screen. The sidecar owns the quiet timer, so the display never
// decides to fade on its own.
const (
	playerSleepMessage = "player-sleep"
	playerWakeMessage  = "player-wake"
)

// idleCommander holds the idle command sidecar's two inputs, the commands
// and status topics it subscribes to, and the run context every write to
// mpv dials under.
//
// It also holds the fade: each remote's events topic paired with the
// keymap topic that names its presses, the resolved quiet window, and
// the shade's current state.
type idleCommander struct {
	commandsTopic string
	statusTopic   string
	runCtx        context.Context

	// remotes maps each remote's events topic to its keymap topic. The
	// keymap is blank for a remote with none.
	remotes map[string]string

	// fadeAfter is the quiet window the operator resolved. Zero never
	// arms the timer.
	fadeAfter time.Duration

	// The fade state below is written by the bus reader and by the
	// fired timer, so one lock covers all of it.
	mu sync.Mutex
	// tables holds the compiled table of each keymap topic, replaced
	// whenever the retained topic delivers a new one.
	tables map[string][]compiledBinding
	// idle says whether the last status named the activity Idle, the
	// only state the timer arms in.
	idle bool
	// asleep says whether the shade is down.
	asleep bool
	// The armed timer and its generation. Each arming takes a new
	// generation, so a timer that fires after a press or a status
	// replaced it reads an old generation and does nothing.
	timer      *time.Timer
	generation uint64
}

// runIdleCommand connects to the bus, subscribes to the Player's commands
// and status topics, recreates the idle surface on each re-present, and
// forwards each status into the display script. It returns on the
// kubelet's grace signal. Both topics are pre-built and carry the Player's
// identity, so the sidecar subscribes to two exact topics and parses
// nothing.
func runIdleCommand() {
	busAddress := os.Getenv(busAddressVariable)
	commandsTopic := os.Getenv(playerCommandsTopicVariable)
	statusTopic := os.Getenv(playerStatusTopicVariable)

	// The idle command sidecar is its container's PID 1, so the signal
	// context ends the run on the kubelet's SIGTERM. Every re-present dials
	// mpv under it, so a dial in progress ends when the run does.
	runCtx, stopRun := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopRun()

	ic := &idleCommander{
		commandsTopic: commandsTopic,
		statusTopic:   statusTopic,
		runCtx:        runCtx,
		remotes: idleRemoteMap(
			os.Getenv(idleRemoteEventsTopicsVariable),
			os.Getenv(idleRemoteKeymapTopicsVariable)),
		fadeAfter: idleFadeAfter(os.Getenv(idleFadeAfterSecondsVariable)),
		tables:    map[string][]compiledBinding{},
	}
	bus := newBus(busAddress, idleCommandClientID(commandsTopic), nil, nil, ic.handle)
	// Each subscription is made once. The Bus remembers the filters and
	// re-sends them on every reconnect, so a broker restart does not need
	// the sidecar to subscribe again. The status topic is retained, so the
	// broker delivers the current state on this subscribe and a pod that
	// just started paints live state with no request of its own.
	bus.Subscribe(commandsTopic)
	bus.Subscribe(statusTopic)
	// A press on any of the unit's remotes reaches the fade, so the
	// sidecar reads every events topic. The keymap topics are retained,
	// so each table arrives on subscribe and a Keymap edit reaches the
	// fade with no pod restart. Two remotes that share a Keymap
	// subscribe once, because the Bus holds its filters in a set.
	for events, keymap := range ic.remotes {
		bus.Subscribe(events)
		if keymap != "" {
			bus.Subscribe(keymap)
		}
	}
	bus.Run(runCtx)
}

// idleRemoteMap pairs each remote's events topic with the keymap topic
// on the same line of the second list. A list shorter than the other,
// or a blank line in it, leaves that remote with no keymap.
func idleRemoteMap(events, keymaps string) map[string]string {
	remotes := map[string]string{}
	keymapList := splitIdleLines(keymaps)
	for index, topic := range splitIdleLines(events) {
		if topic == "" {
			continue
		}
		keymap := ""
		if index < len(keymapList) {
			keymap = keymapList[index]
		}
		remotes[topic] = keymap
	}
	return remotes
}

// splitIdleLines splits a newline-joined variable, keeping blank lines
// so the two remote lists stay aligned by position.
func splitIdleLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

// idleFadeAfter reads the resolved quiet window. An unset or unreadable
// value fades nothing, because the operator settles this field for
// every Player, and a guessed default here would dim a screen the
// cluster never asked to dim.
func idleFadeAfter(value string) time.Duration {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// idleCommandClientID builds the sidecar's bus identity from its commands
// topic. The topic carries the Player's namespace and name, so one
// identity per Player falls out with no extra environment, and two idle
// sidecars never collide on the broker.
func idleCommandClientID(commandsTopic string) string {
	return "idle-command-" + strings.ReplaceAll(commandsTopic, "/", "-")
}

// handle folds one message from either subscription. The two topics carry
// different messages, so the topic says which this is: the status topic
// carries the unit's state, and the commands topic carries a named
// command, of which only the re-present acts. A payload that does not
// decode, or any other action, does nothing, so a newer command on the
// topic has no effect rather than a crash.
//
// The Bus calls this on its reader goroutine, so the two topics are served
// in the order the broker delivered them. The operator publishes a Player's
// status before the re-present that follows it, so the display reads the
// Idle status before the reveal and animates the return.
func (ic *idleCommander) handle(topic string, payload []byte) {
	if topic == ic.statusTopic {
		ic.forwardStatus(payload)
		ic.onStatus(payload)
		return
	}
	// A controller's presses reach the fade, and the keymap that names
	// them arrives on its own retained topic. Both are checked before
	// the commands topic, because neither carries the operator's
	// command vocabulary.
	if _, ok := ic.remotes[topic]; ok {
		ic.onRemoteEvent(topic, payload)
		return
	}
	if ic.holdsKeymapTopic(topic) {
		ic.setTable(topic, payload)
		return
	}
	var command mediaCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		return
	}
	if command.Action != actionRePresent {
		return
	}
	ic.represent()
}

// forwardStatus hands one status message to the display script as the
// player-status script message. The payload travels as one string
// argument, so the Lua decodes the same JSON the operator published and
// this sidecar reads none of it: a field the display starts drawing needs
// no change here.
func (ic *idleCommander) forwardStatus(payload []byte) {
	ic.withMPV("forward the player status", func(d *mpvDialog) error {
		return d.call([]any{"script-message", playerStatusMessage, string(payload)})
	})
}

// idleStatus is the one field of the status the sidecar reads for
// itself. Everything else travels opaque to the display, so a field
// the display starts drawing needs no change here.
type idleStatus struct {
	Activity string `json:"activity"`
}

// onStatus folds one status into the fade. Idle is the only activity
// the timer arms in, so a status that leaves Idle disarms it. The same
// status lifts the shade if the screen sleeps, so a Play started from
// another room shows its film and not a black screen.
func (ic *idleCommander) onStatus(payload []byte) {
	var status idleStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return
	}
	ic.mu.Lock()
	ic.idle = status.Activity == playerIdle
	message := ""
	if !ic.idle && ic.asleep {
		ic.asleep = false
		message = playerWakeMessage
	}
	ic.rearmLocked()
	ic.mu.Unlock()
	ic.sendScript(message)
}

// isPressEdge reports whether this event is a control pressed down. The
// standing remote pod publishes both edges of every control: a button's
// release arrives as value 0, a held key's autorepeat as value 2, and a
// hat's return to center as value 0. Only the down edge is a person's
// act, so only the down edge reaches the fade. A release that counted
// would wake the screen its own press just put to sleep.
func isPressEdge(event remoteEvent) bool {
	switch event.Type {
	case evKey:
		return event.Value == 1
	case evAbs:
		return event.Value != 0
	}
	return false
}

// onRemoteEvent folds one press into the fade. A sleeping screen wakes
// on any press, so a person gets the screen back with whatever control
// they touched. A press named back, while the unit plays nothing,
// draws the shade at once. Every other press restarts the quiet
// window.
//
// An event that is not a down edge, or does not decode, changes
// nothing: it neither wakes, nor sleeps, nor restarts the window.
func (ic *idleCommander) onRemoteEvent(topic string, payload []byte) {
	var event remoteEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	if !isPressEdge(event) {
		return
	}
	ic.mu.Lock()
	message := ""
	switch {
	case ic.asleep:
		ic.asleep = false
		message = playerWakeMessage
	case ic.idle && ic.namesBackLocked(topic, event):
		ic.asleep = true
		message = playerSleepMessage
	}
	ic.rearmLocked()
	ic.mu.Unlock()
	ic.sendScript(message)
}

// namesBackLocked reports whether this press is the back action on the
// remote that published it. The match runs the translator's own
// matchBinding against that remote's compiled table, so back means
// here exactly what it means during a film. A remote with no keymap,
// or a press no binding matches, names nothing.
func (ic *idleCommander) namesBackLocked(topic string, event remoteEvent) bool {
	keymap := ic.remotes[topic]
	if keymap == "" {
		return false
	}
	binding, ok := matchBinding(ic.tables[keymap],
		inputEvent{Type: event.Type, Code: event.Code, Value: event.Value})
	return ok && binding.Action == actionBack
}

// holdsKeymapTopic reports whether this topic is the keymap topic of
// one of the unit's remotes.
func (ic *idleCommander) holdsKeymapTopic(topic string) bool {
	for _, keymap := range ic.remotes {
		if keymap == topic {
			return true
		}
	}
	return false
}

// setTable replaces one keymap's held table. A payload that does not
// decode leaves the last good table in place, the same way the
// playback translator holds its own.
func (ic *idleCommander) setTable(topic string, payload []byte) {
	var table []compiledBinding
	if err := json.Unmarshal(payload, &table); err != nil {
		return
	}
	ic.mu.Lock()
	ic.tables[topic] = table
	ic.mu.Unlock()
}

// rearmLocked restarts the quiet window from now. The timer runs only
// while the screen is awake, the unit plays nothing, and the policy is
// above zero; every other state leaves it stopped. Each arming takes a
// new generation, so a timer that fires while this call replaces it is
// stale and draws nothing.
func (ic *idleCommander) rearmLocked() {
	ic.generation++
	if ic.timer != nil {
		ic.timer.Stop()
		ic.timer = nil
	}
	if !ic.idle || ic.asleep || ic.fadeAfter <= 0 {
		return
	}
	generation := ic.generation
	ic.timer = time.AfterFunc(ic.fadeAfter, func() { ic.fade(generation) })
}

// fade is the quiet window running out. A timer that fired while a
// press or a status replaced it carries an old generation and draws
// nothing.
func (ic *idleCommander) fade(generation uint64) {
	ic.mu.Lock()
	if generation != ic.generation || ic.asleep {
		ic.mu.Unlock()
		return
	}
	ic.asleep = true
	ic.timer = nil
	ic.mu.Unlock()
	ic.sendScript(playerSleepMessage)
}

// sendScript sends one bare script message to the idle display. An
// empty name is the ordinary case of a fold that changed no state, and
// it sends nothing.
func (ic *idleCommander) sendScript(message string) {
	if message == "" {
		return
	}
	ic.withMPV("send "+message, func(d *mpvDialog) error {
		return d.call([]any{"script-message", message})
	})
}

// represent recreates the idle surface and then reports that it is on
// screen. The first set clears the window, so mpv's video output tears
// the surface down; the second sets it again, so mpv builds a fresh
// surface that kiosk reveals. The pause between them is
// surfaceTeardownGap, so the teardown finishes before the rebuild
// starts. The reveal goes out on the same connection after the second
// set's reply, so the display starts the mark in motion on the frame
// the surface came back into view.
func (ic *idleCommander) represent() {
	ic.withMPV("recreate the idle surface", func(d *mpvDialog) error {
		if err := d.call([]any{"set", "force-window", "no"}); err != nil {
			return err
		}
		time.Sleep(surfaceTeardownGap)
		if err := d.call([]any{"set", "force-window", "yes"}); err != nil {
			return err
		}
		return d.call([]any{"script-message", revealedMessage})
	})
}

// withMPV dials the idle mpv and runs one dialog against it. A dial that
// does not connect within idleDialTimeout writes nothing, so a message
// that lands while the idle mpv restarts does not hold the bus reader.
// The what argument names the dialog in the failure line, because every
// caller reaches mpv the same way and only the commands differ.
func (ic *idleCommander) withMPV(what string, run func(d *mpvDialog) error) {
	ctx, cancel := context.WithTimeout(ic.runCtx, idleDialTimeout)
	defer cancel()
	conn, err := dialMPV(ctx, mpvSocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: reach mpv: %v\n", err)
		return
	}
	defer conn.Close()
	if err := run(&mpvDialog{conn: conn, reader: bufio.NewReader(conn)}); err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: %s: %v\n", what, err)
	}
}

// mpvDialog is one connection to the idle mpv: the socket and the reader
// that takes mpv's replies off it. mpv answers every command on the same
// socket, and this sidecar must read those answers. A client that writes
// its last command and closes at once races mpv's reply writes, and mpv
// abandons a connection whose reply write fails, buffered input unread.
// The reply to `set force-window yes` arrives only after the video
// output rebuilds, so that race window is long, and the abandoned
// command was the revealed that starts the mark's arrival motion.
type mpvDialog struct {
	conn   net.Conn
	reader *bufio.Reader
}

// mpvReply is the slice of one reply line this dialog reads: the event
// name that marks a line as an event and not a reply, and the error word
// every reply carries, which is "success" for a command that ran.
type mpvReply struct {
	Event string `json:"event"`
	Error string `json:"error"`
}

// call writes one command and waits for its reply, so the command is
// proven to have run before the next one goes out and before the caller
// closes the connection. Event lines share the socket and arrive
// unasked, so the wait skips them. The read deadline bounds the wait,
// so an mpv that stops answering does not hold the bus reader.
func (d *mpvDialog) call(command []any) error {
	if err := sendCommand(d.conn, command); err != nil {
		return err
	}
	if err := d.conn.SetReadDeadline(time.Now().Add(idleDialTimeout)); err != nil {
		return err
	}
	for {
		line, err := d.reader.ReadBytes('\n')
		if err != nil {
			return err
		}
		var reply mpvReply
		if err := json.Unmarshal(line, &reply); err != nil {
			continue
		}
		if reply.Event != "" {
			continue
		}
		if reply.Error != "" && reply.Error != "success" {
			return fmt.Errorf("mpv: %s", reply.Error)
		}
		return nil
	}
}
