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

	// The topic the panel state stands on, and the publish half of the
	// sidecar's own bus connection.
	panelTopic string
	publish    func(topic string, payload []byte, retained bool)

	// The unit's volume topic, empty for a Player with no sinks.
	// Empty is the speaker gate: the sidecar subscribes to no level,
	// applies none, and answers no volume press.
	volumeTopic string

	// The last state the volume topic delivered, and whether one
	// arrived at all. It takes a lock of its own rather than the
	// fade's, because a press reads it while the bus reader writes
	// it, and neither touches the shade.
	volumeMu   sync.Mutex
	volume     volumeState
	haveVolume bool

	// volumeCaughtUp marks that this bus session has already
	// delivered a level. The first message of a session is the
	// broker's retained catch-up, a restore and not a press, so it
	// applies silently and the idle screen draws no indicator at pod
	// start. Every message after it signals the display.
	volumeCaughtUp bool

	// The delivered control device. A nil wire is a Player that states
	// no control device, and every hardware write below is off.
	wire panelWire

	// The hardware window, clamped to at least the fade, and what the
	// sidecar writes when it runs out. Zero never darkens the panel.
	offAfter time.Duration
	offMode  string

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
	// The second window's timer, armed when the shade comes down and
	// stopped by every wake.
	offTimer *time.Timer
	// The panel state the sidecar last actuated, and the brightness it
	// read from the panel before it wrote zero.
	panel          string
	brightness     uint16
	brightnessRead bool
	// One wake ladder runs at a time, and the generation stops a
	// ladder that a later off window overtook.
	reviving        bool
	panelGeneration uint64

	// Every DDC dialogue takes this lock, so a wake ladder and an off
	// window never interleave their writes on the one wire.
	panelMu sync.Mutex
}

// The wake ladder: twenty tries, a second apart, then the sidecar
// stops and reports the panel unresponsive. The spacing is a variable
// so a test runs the ladder in milliseconds.
const panelWakeAttempts = 20

var panelWakeInterval = time.Second

// The brightness a wake writes when the sidecar never read one.
const defaultPanelBrightness uint16 = 100

// panelReport is the whole of what the sidecar says about the panel.
// The unit it belongs to is named by the topic, not by the body.
type panelReport struct {
	State string `json:"state"`
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

	fadeAfter := idleFadeAfter(os.Getenv(idleFadeAfterSecondsVariable))
	ic := &idleCommander{
		commandsTopic: commandsTopic,
		statusTopic:   statusTopic,
		runCtx:        runCtx,
		panelTopic:    os.Getenv(idlePanelTopicVariable),
		volumeTopic:   os.Getenv(playerVolumeTopicVariable),
		remotes: idleRemoteMap(
			os.Getenv(idleRemoteEventsTopicsVariable),
			os.Getenv(idleRemoteKeymapTopicsVariable)),
		fadeAfter: fadeAfter,
		offAfter:  idleOffAfter(os.Getenv(idleOffAfterSecondsVariable), fadeAfter),
		offMode:   idleOffMode(os.Getenv(idleOffModeVariable)),
		tables:    map[string][]compiledBinding{},
		panel:     panelOn,
	}
	// The delivered wire is the gate. A Player that states no control
	// device holds no i2c node, so this variable is empty and the
	// sidecar runs the fade alone. A node that does not open leaves the
	// fade running as well, because a screen that fades beats a pod
	// that restarts on a hardware fault.
	if path := os.Getenv(displayControlBusVariable); path != "" {
		wire, err := openPanelWire(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "idle-command: open the panel wire %s: %v\n", path, err)
		} else {
			defer wire.Close()
			ic.wire = wire
		}
	}
	bus := newBus(busAddress, idleCommandClientID(commandsTopic), nil, ic.onBusConnect, ic.handle)
	// The panel state publishes on the same connection the sidecar
	// reads its topics on.
	ic.publish = bus.Publish
	// Each subscription is made once. The Bus remembers the filters and
	// re-sends them on every reconnect, so a broker restart does not need
	// the sidecar to subscribe again. The status topic is retained, so the
	// broker delivers the current state on this subscribe and a pod that
	// just started paints live state with no request of its own.
	bus.Subscribe(commandsTopic)
	bus.Subscribe(statusTopic)
	// The volume topic is retained too, so the unit's current level
	// reaches the idle mpv the moment the pod starts, and the display
	// draws from the property. A Player with no sinks names no topic,
	// so this pod subscribes to no level at all.
	if ic.volumeTopic != "" {
		bus.Subscribe(ic.volumeTopic)
	}
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

// idleOffAfter reads the hardware window the way idleFadeAfter reads
// the fade, and clamps it to at least the fade, so the panel never
// goes dark behind a still-lit image. Zero means the panel never goes
// dark on its own.
func idleOffAfter(value string, fade time.Duration) time.Duration {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	off := time.Duration(seconds) * time.Second
	if off < fade {
		return fade
	}
	return off
}

// idleOffMode reads what the window writes. Every value but power,
// an unset one included, takes the backlight, because a panel at zero
// backlight still answers DDC and a wake cannot strand.
func idleOffMode(value string) string {
	if value == offModePower {
		return offModePower
	}
	return offModeBacklight
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
	// The volume topic carries a state and not a named command, so it
	// is read before the command vocabulary below.
	if ic.volumeTopic != "" && topic == ic.volumeTopic {
		ic.applyVolume(payload)
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
	ic.applyShade(message)
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
// draws the shade at once. A press named volume or mute, while the
// unit plays nothing and the screen is awake, publishes the unit's
// next level, and the level reaches mpv on the subscription like any
// other change. The two gates on it are deliberate: a press on a
// sleeping screen is a wake and nothing more, and a unit that plays
// has the film's own pod answering its presses. Every other press
// restarts the quiet window.
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
	binding, named := ic.bindingForLocked(topic, event)
	message := ""
	press := mediaCommand{}
	switch {
	case ic.asleep:
		ic.asleep = false
		message = playerWakeMessage
	case ic.idle && named && binding.Action == actionBack:
		ic.asleep = true
		message = playerSleepMessage
	case ic.idle && named && isVolumeAction(binding.Action):
		press = mediaCommand{Action: binding.Action, Amount: binding.Amount}
	}
	ic.rearmLocked()
	ic.mu.Unlock()
	ic.applyShade(message)
	ic.pressVolume(press)
}

// applyShade sends the fold's script message, pixels first: the shade
// lift needs no hardware, and a lit panel showing black beats a dark
// one. Only then does a wake bring the panel up.
func (ic *idleCommander) applyShade(message string) {
	ic.sendScript(message)
	if message == playerWakeMessage {
		ic.revive()
	}
}

// bindingForLocked reports what this press names on the remote that
// published it. The match runs the translator's own matchBinding
// against that remote's compiled table, so back and a volume step
// mean here exactly what they mean during a film. A remote with no
// keymap, or a press no binding matches, names nothing.
func (ic *idleCommander) bindingForLocked(topic string, event remoteEvent) (compiledBinding, bool) {
	keymap := ic.remotes[topic]
	if keymap == "" {
		return compiledBinding{}, false
	}
	return matchBinding(ic.tables[keymap],
		inputEvent{Type: event.Type, Code: event.Code, Value: event.Value})
}

// applyVolume folds one message off the volume topic and writes it
// to the idle mpv. The idle mpv plays no audio, and it holds the
// level as a property all the same, and that property is what the
// display draws the indicator from.
func (ic *idleCommander) applyVolume(payload []byte) {
	state, ok := parseVolumeState(payload)
	if !ok {
		return
	}
	ic.volumeMu.Lock()
	ic.volume = state
	ic.haveVolume = true
	signal := ic.volumeCaughtUp
	ic.volumeCaughtUp = true
	ic.volumeMu.Unlock()
	ic.withMPV("set the volume", func(d *mpvDialog) error {
		for _, command := range volumeCommands(state) {
			if err := d.call(command); err != nil {
				return err
			}
		}
		if !signal {
			return nil
		}
		return d.call(volumeChangedCommand())
	})
}

// onBusConnect runs at the start of every bus session. A fresh
// session redelivers the retained level, so that message is a
// catch-up again and applies silently again.
func (ic *idleCommander) onBusConnect(*Bus) {
	ic.volumeMu.Lock()
	ic.volumeCaughtUp = false
	ic.volumeMu.Unlock()
}

// pressVolume publishes what a press means, retained, and writes
// nothing to mpv. An empty action is the ordinary case of a press
// that named no level, and it publishes nothing. It computes from
// the last message the topic delivered, or from unity before any
// message arrives.
func (ic *idleCommander) pressVolume(command mediaCommand) {
	if ic.volumeTopic == "" || !isVolumeAction(command.Action) {
		return
	}
	payload, err := marshalVolumeState(nextVolume(ic.heldVolume(), command))
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: volume: %v\n", err)
		return
	}
	if ic.publish == nil {
		return
	}
	ic.publish(ic.volumeTopic, payload, true)
}

// heldVolume is the state the last message left, and unity before
// any message arrives.
func (ic *idleCommander) heldVolume() volumeState {
	ic.volumeMu.Lock()
	defer ic.volumeMu.Unlock()
	if !ic.haveVolume {
		return defaultVolumeState()
	}
	return ic.volume
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
	if ic.offTimer != nil {
		ic.offTimer.Stop()
		ic.offTimer = nil
	}
	if !ic.idle {
		return
	}
	generation := ic.generation
	// The second window runs from the moment the shade came down, so
	// the two windows measure one quiet stretch. It arms only for a
	// Player that holds the wire and states a window, and only while
	// the panel is still lit.
	if ic.asleep {
		if ic.wire == nil || ic.offAfter <= 0 || ic.panel != panelOn {
			return
		}
		ic.offTimer = time.AfterFunc(ic.offAfter-ic.fadeAfter, func() { ic.darken(generation) })
		return
	}
	if ic.fadeAfter <= 0 {
		return
	}
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
	// The shade coming down starts the second window.
	ic.rearmLocked()
	ic.mu.Unlock()
	ic.sendScript(playerSleepMessage)
}

// darken is the off window running out. The backlight mode reads the
// panel's brightness, remembers it, and writes zero, so the wake puts
// back what a person set. The power mode writes DPM off, which is
// deeper, and which some panels never answer DDC from again.
func (ic *idleCommander) darken(generation uint64) {
	ic.mu.Lock()
	if generation != ic.generation || !ic.asleep || ic.wire == nil {
		ic.mu.Unlock()
		return
	}
	ic.offTimer = nil
	mode := ic.offMode
	ic.mu.Unlock()

	ic.panelMu.Lock()
	defer ic.panelMu.Unlock()
	// A wake that landed while this waited for the wire owns the
	// panel, so a screen that is awake again stays lit.
	if !ic.sleeping() {
		return
	}
	// This window's writes take the panel from here, so a wake ladder
	// still running from the last one stops.
	ic.startPanelWrite()
	if mode == offModePower {
		ic.setPanel(ic.writePanel(vcpPowerMode, powerModeOff, panelOff))
		return
	}
	if value, err := ic.wire.GetVCP(vcpBrightness); err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: read the panel brightness: %v\n", err)
	} else {
		ic.remember(value)
	}
	ic.setPanel(ic.writePanel(vcpBrightness, 0, panelBacklightOff))
}

// writePanel is one set and the state it leaves the panel in. A write
// that fails leaves the state Unresponsive and the fault on stderr.
func (ic *idleCommander) writePanel(code byte, value uint16, state string) string {
	if err := ic.wire.SetVCP(code, value); err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: write VCP %#04x: %v\n", code, err)
		return panelUnresponsive
	}
	return state
}

// revive is the wake's hardware half, run on its own goroutine so the
// bus reader is never held by a ladder that can run twenty seconds.
func (ic *idleCommander) revive() {
	ic.mu.Lock()
	if ic.wire == nil || ic.panel == panelOn || ic.reviving {
		ic.mu.Unlock()
		return
	}
	ic.reviving = true
	generation := ic.panelGeneration
	code, value := vcpBrightness, ic.rememberedLocked()
	if ic.offMode == offModePower {
		code, value = vcpPowerMode, powerModeOn
	}
	ic.mu.Unlock()
	go ic.reviveLadder(generation, code, value)
}

// reviveLadder is the bounded wake: at most panelWakeAttempts writes,
// panelWakeInterval apart, then the sidecar stops and reports the
// panel unresponsive. It ends early when the run ends, and when a
// later off window took the panel, because that window owns it from
// there.
func (ic *idleCommander) reviveLadder(generation uint64, code byte, value uint16) {
	defer ic.stopReviving()
	for attempt := range panelWakeAttempts {
		if attempt > 0 {
			select {
			case <-ic.runCtx.Done():
				return
			case <-time.After(panelWakeInterval):
			}
		}
		if ic.overtaken(generation) {
			return
		}
		ic.panelMu.Lock()
		err := ic.wire.SetVCP(code, value)
		ic.panelMu.Unlock()
		if err == nil {
			ic.setPanel(panelOn)
			return
		}
		fmt.Fprintf(os.Stderr, "idle-command: wake the panel: %v\n", err)
	}
	ic.setPanel(panelUnresponsive)
}

// sleeping reports whether the shade is down right now.
func (ic *idleCommander) sleeping() bool {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return ic.asleep
}

// startPanelWrite marks an off window taking the panel, which stops
// every ladder that started before it.
func (ic *idleCommander) startPanelWrite() {
	ic.mu.Lock()
	ic.panelGeneration++
	ic.mu.Unlock()
}

// overtaken reports whether an off window took the panel after this
// ladder began.
func (ic *idleCommander) overtaken(generation uint64) bool {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return generation != ic.panelGeneration
}

// stopReviving ends the ladder, so the next wake may start one.
func (ic *idleCommander) stopReviving() {
	ic.mu.Lock()
	ic.reviving = false
	ic.mu.Unlock()
}

// remember keeps the brightness the panel held before the sidecar
// wrote zero.
func (ic *idleCommander) remember(value uint16) {
	ic.mu.Lock()
	ic.brightness = value
	ic.brightnessRead = true
	ic.mu.Unlock()
}

// rememberedLocked is the brightness a wake writes back. A panel the
// sidecar never read comes back at full, because a lit screen is what
// the person pressed a button for.
func (ic *idleCommander) rememberedLocked() uint16 {
	if !ic.brightnessRead {
		return defaultPanelBrightness
	}
	return ic.brightness
}

// setPanel publishes the panel state, retained on its own topic, so
// the operator folds the current state into the Player's status the
// moment it subscribes. An unchanged state publishes nothing.
func (ic *idleCommander) setPanel(state string) {
	ic.mu.Lock()
	if ic.panel == state {
		ic.mu.Unlock()
		return
	}
	ic.panel = state
	topic, publish := ic.panelTopic, ic.publish
	ic.mu.Unlock()
	if publish == nil || topic == "" {
		return
	}
	payload, err := json.Marshal(panelReport{State: state})
	if err != nil {
		return
	}
	publish(topic, payload, true)
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
