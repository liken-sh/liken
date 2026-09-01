package main

// The idle command pod holds the idle screen's timers, keymaps,
// and focus gate. It states each moment the idle client draws on the
// Player's screen topic, as one JSON object each, and the client reads
// them there. It runs in a pod of its own, one per unit whatever draws
// that unit's screen, and it subscribes to the Player's commands and
// status topics.
//
// The re-present is the whole fix for a seatless compositor. Weston's
// kiosk-shell reveals a lower surface only along a code path gated on a
// seat, and liken's compositor runs with require-input=false and no
// input devices, so it has no seat. When a Play's surface is destroyed
// the idle clock stays hidden and the screen goes black, though the
// client still runs. A freshly mapped surface is revealed along a
// seat-independent path, so the sidecar states the moment, the client
// maps a new surface, and kiosk reveals that one.
//
// The sidecar holds no API credentials and reaches the control plane
// only through the operator's own place on the bus.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// focusCycleSuffix turns a remote's focus topic into its cycle topic, the
// same path remoteFocusCycleTopic builds, so the sidecar needs no second
// topic list.
const focusCycleSuffix = "/cycle"

// The four moments the sidecar states on the Player's screen topic. The
// shade comes down when the quiet window runs out and goes up on a press
// or a starting Play.
//
// The two shade moments travel retained, because the shade is a
// state a client that restarts has to read; the other two do not.
//
// A focus names the controller a live mark landed on, by its position in
// spec.remotes. The parts list the client draws holds the display and
// the sinks before the controllers, so the client reads this index among
// the controllers alone.
//
// A present says a Play ended and the screen is the idle client's again.
// The client destroys its own surface and maps a fresh one on that
// moment, and kiosk reveals the fresh one. The moment is a request and
// not a report, because the client alone reads the frame its new surface
// is up on, and that frame is where the arrival motion starts.
const (
	screenSleepEvent   = "sleep"
	screenWakeEvent    = "wake"
	screenFocusEvent   = "focus"
	screenPresentEvent = "present"
)

// screenMessage is one moment as it travels on the screen topic. Remote
// is the controller's index in spec.remotes, and only a focus carries
// it, so a pointer keeps index zero on the wire and the field off every
// other moment.
type screenMessage struct {
	Event  string `json:"event"`
	Remote *int   `json:"remote,omitempty"`
}

// idleCommander holds the idle command pod's two inputs, the commands
// and status topics it subscribes to, and the run context every held
// control's repeat runs under.
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
	//
	// RunIdleCommand sets publish before the bus runs and nothing
	// clears it, so no publisher here guards against a nil one.
	panelTopic string
	publish    func(topic string, payload []byte, retained bool)

	// The topic the sidecar states each screen moment on. The operator
	// names it on every idle pod, so publishScreen needs no guard.
	screenTopic string

	// The unit's volume topic, empty for a Player with no sinks.
	// Empty is the speaker gate: the sidecar subscribes to no level and
	// answers no volume press.
	volumeTopic string

	// The last state the volume topic delivered, and whether one
	// arrived at all. It takes a lock of its own rather than the
	// fade's, because a press reads it while the bus reader writes
	// it, and neither touches the shade.
	volumeMu   sync.Mutex
	volume     volumeState
	haveVolume bool

	// repeats holds one cancel per held control that repeats, keyed by
	// its evdev code, the same shape the translator holds during a
	// film, so a held volume button steps continuously on the idle
	// screen too and the release stops the repeat the press started.
	repeatMu sync.Mutex
	repeats  map[uint16]context.CancelFunc

	// The off window, clamped to at least the fade. Zero leaves
	// the desire on the panel topic at on forever.
	offAfter time.Duration

	// remotes maps each remote's events topic to the rest of its record:
	// the keymap topic that names its presses, the focus topic that
	// carries its mark, and its position in spec.remotes, which the focus
	// pulse carries.
	remotes map[string]idleRemote

	// The Player's own object name, the value a focus mark holds when it
	// names this unit. An empty name matches no mark, so a sidecar that
	// reads none answers no press.
	playerName string

	// The last mark each remote's focus topic delivered, keyed by that
	// remote's events topic. It has a lock of its own the way the level
	// does, because a press reads it while the bus reader writes it, and
	// neither touches the shade.
	focusMu sync.Mutex
	marks   map[string]focusMark

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
	// The desire: the panel state the sidecar last stated, on or off.
	// The sidecar writes no hardware; the operator reads the desire
	// from the bus and overrides the screen's Display.
	desire string
}

// idleRemote is one of the unit's controllers as the sidecar reads it: the
// keymap topic that names its presses, blank for a remote with none, the
// retained focus topic the sidecar gates on, and its position in
// spec.remotes.
type idleRemote struct {
	keymap string
	focus  string
	index  int
}

// focusMark is one remote's mark and whether this bus session already
// delivered one. The first message of a session is the broker's retained
// catch-up, a restore and not a person, so it sets the gate and pulses
// nothing.
type focusMark struct {
	player   string
	caughtUp bool
}

// The retained panel topic carries a desire and not a report.
// The unit it belongs to is named by the topic, not by the body.
type panelDesire struct {
	Desire string `json:"desire"`
}

// runIdleCommand connects to the bus, subscribes to the Player's commands
// and status topics, and states each moment the idle client draws on the
// Player's screen topic. It returns on the kubelet's grace signal. Both
// topics are pre-built and carry the Player's identity, so the sidecar
// subscribes to two exact topics and parses nothing.
func runIdleCommand() {
	busAddress := os.Getenv(busAddressVariable)
	commandsTopic := os.Getenv(playerCommandsTopicVariable)
	statusTopic := os.Getenv(playerStatusTopicVariable)

	// The idle command pod is its container's PID 1, so the signal
	// context ends the run on the kubelet's SIGTERM. Every held control's
	// repeat runs under it, so a repeat in progress ends when the run does.
	runCtx, stopRun := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopRun()

	fadeAfter := idleFadeAfter(os.Getenv(idleFadeAfterSecondsVariable))
	ic := &idleCommander{
		commandsTopic: commandsTopic,
		statusTopic:   statusTopic,
		runCtx:        runCtx,
		panelTopic:    os.Getenv(idlePanelTopicVariable),
		screenTopic:   os.Getenv(playerScreenTopicVariable),
		volumeTopic:   os.Getenv(playerVolumeTopicVariable),
		playerName:    os.Getenv(playerNameVariable),
		remotes: idleRemoteMap(
			os.Getenv(idleRemoteEventsTopicsVariable),
			os.Getenv(idleRemoteKeymapTopicsVariable),
			os.Getenv(idleRemoteFocusTopicsVariable)),
		marks:     map[string]focusMark{},
		fadeAfter: fadeAfter,
		offAfter:  idleOffAfter(os.Getenv(idleOffAfterSecondsVariable), fadeAfter),
		tables:    map[string][]compiledBinding{},
		desire:    panelDesireOn,
		repeats:   map[uint16]context.CancelFunc{},
	}
	bus := newBus(busAddress, idleCommandClientID(commandsTopic), nil, ic.onBusConnect, ic.handle)
	// The panel desire publishes on the same connection the sidecar
	// reads its topics on.
	ic.publish = bus.Publish
	// Each subscription is made once. The Bus remembers the filters and
	// re-sends them on every reconnect, so a broker restart does not need
	// the sidecar to subscribe again. The status topic is retained, so the
	// broker delivers the current state on this subscribe and a pod that
	// just started paints live state with no request of its own.
	bus.Subscribe(commandsTopic)
	bus.Subscribe(statusTopic)
	// The volume topic is retained too, so the level a press steps from
	// is the unit's own from the moment the pod starts. A Player with no
	// sinks names no topic, so this sidecar subscribes to no level at
	// all.
	if ic.volumeTopic != "" {
		bus.Subscribe(ic.volumeTopic)
	}
	// A press on any of the unit's remotes reaches the fade, so the
	// sidecar reads every events topic. The keymap topics are retained,
	// so each table arrives on subscribe and a Keymap edit reaches the
	// fade with no pod restart. Two remotes that share a Keymap
	// subscribe once, because the Bus holds its filters in a set.
	//
	// The focus topic is retained too, so each mark arrives on subscribe
	// and the gate stands before the first press.
	for events, remote := range ic.remotes {
		bus.Subscribe(events)
		if remote.keymap != "" {
			bus.Subscribe(remote.keymap)
		}
		if remote.focus != "" {
			bus.Subscribe(remote.focus)
		}
	}
	bus.Run(runCtx)
}

// idleRemoteMap pairs each remote's events topic with the keymap topic
// on the same line of the second list. A list shorter than the other,
// or a blank line in it, leaves that remote with no keymap.
//
// The focus topic comes off the third list the same way, and the line
// number is the remote's index, which the focus pulse carries.
func idleRemoteMap(events, keymaps, focuses string) map[string]idleRemote {
	remotes := map[string]idleRemote{}
	keymapList := splitIdleLines(keymaps)
	focusList := splitIdleLines(focuses)
	for index, topic := range splitIdleLines(events) {
		if topic == "" {
			continue
		}
		remote := idleRemote{index: index}
		if index < len(keymapList) {
			remote.keymap = keymapList[index]
		}
		if index < len(focusList) {
			remote.focus = focusList[index]
		}
		remotes[topic] = remote
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

// idleOffAfter reads the off window the way idleFadeAfter reads the
// fade, and clamps it to at least the fade, so the panel never goes
// dark behind a still-lit image. Zero means the panel never goes dark
// on its own.
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
// The Bus calls this on its reader goroutine, so the topics are served in
// the order the broker delivered them. The operator publishes a Player's
// status before it publishes the re-present, and the sidecar states the
// present only after it has read that status, so the client reads the Idle
// status first and starts the arrival motion from it.
//
// The re-present acts only while the unit plays nothing, so a
// stray one during a film never maps the clock over it.
func (ic *idleCommander) handle(topic string, payload []byte) {
	if topic == ic.statusTopic {
		ic.onStatus(payload)
		return
	}
	// The volume topic carries a state and not a named command, so it
	// is read before the command vocabulary below.
	if ic.volumeTopic != "" && topic == ic.volumeTopic {
		ic.holdVolume(payload)
		return
	}
	// A controller's presses reach the fade, and the keymap that names
	// them arrives on its own retained topic. Both are checked before
	// the commands topic, because neither carries the operator's
	// command vocabulary.
	if remote, ok := ic.remotes[topic]; ok {
		ic.onRemoteEvent(topic, remote, payload)
		return
	}
	// The mark is a state on its own retained topic, read before the
	// command vocabulary for the same reason the level is.
	if events, remote, ok := ic.remoteForFocus(topic); ok {
		ic.onFocus(events, remote, payload)
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
	// The present states the screen back to the idle client, so it
	// acts only while the unit plays nothing, the same gate back, volume,
	// and cycle read.
	ic.mu.Lock()
	if ic.idle {
		ic.publishScreen(screenMessage{Event: screenPresentEvent})
	}
	ic.mu.Unlock()
}

// idleStatus is the one field of the status the sidecar reads for
// itself. The client subscribes to the same retained topic, so a field
// the screen starts drawing needs no change here.
type idleStatus struct {
	Activity string `json:"activity"`
}

// onStatus folds one status into the fade. Idle is the only activity
// the timer arms in, so a status that leaves Idle disarms it. The same
// status lifts the shade if the screen sleeps, so a Play started from
// another room shows its film and not a black screen.
//
// The operator republishes the status on any change to the
// payload, a controller's Connected flap included, so only a status that
// moved the unit into or out of Idle restarts the quiet window; a
// republish of the same activity leaves the window where it stands.
func (ic *idleCommander) onStatus(payload []byte) {
	var status idleStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return
	}
	idle := status.Activity == playerIdle
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if idle == ic.idle {
		return
	}
	ic.idle = idle
	moment := ""
	if !ic.idle && ic.asleep {
		ic.asleep = false
		moment = screenWakeEvent
	}
	ic.rearmLocked()
	ic.applyShadeLocked(moment)
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
// states sleep at once. A press named volume or mute, while the
// unit plays nothing and the screen is awake, publishes the unit's
// next level, and the level reaches the client on the subscription
// like any other change. The two gates on it are deliberate: a press on a
// sleeping screen is a wake and nothing more, and a unit that plays
// has the film's own pod answering its presses. A binding whose
// keymap repeats it steps again while the control is held, on the
// same clock the translator ticks during a film. Every other press
// restarts the quiet window.
//
// An event that is not a down edge, or does not decode, changes
// nothing: it neither wakes, nor sleeps, nor restarts the window.
//
// A press acts only while the remote's mark names this Player. A pad
// pointed at another room touches nothing here, not the shade and not the
// level. The release keeps its own path, focused or not, so a control held
// as the mark moves away still stops its repeat.
//
// A press named cycle-focus asks the operator to move the mark and does
// nothing else: no wake, no level, no shade. It acts only while the unit
// plays nothing, because a Play's own translator publishes the cycle while
// one runs.
func (ic *idleCommander) onRemoteEvent(topic string, remote idleRemote, payload []byte) {
	var event remoteEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	// A release stops the repeat its press started, the same first move
	// the translator makes, so a held volume button and its release pair
	// up whether or not anything else below acts.
	if event.Value == 0 {
		ic.stopRepeat(event.Code)
	}
	if !ic.holdsFocus(topic) {
		return
	}
	if !isPressEdge(event) {
		return
	}
	ic.mu.Lock()
	binding, named := ic.bindingForLocked(topic, event)
	moment := ""
	press := mediaCommand{}
	repeat := false
	cycle := false
	switch {
	case ic.idle && named && binding.Action == actionCycleFocus:
		cycle = true
	case ic.asleep:
		ic.asleep = false
		moment = screenWakeEvent
	case ic.idle && named && binding.Action == actionBack:
		ic.asleep = true
		moment = screenSleepEvent
	case ic.idle && named && isVolumeAction(binding.Action):
		press = mediaCommand{Action: binding.Action, Amount: binding.Amount}
		repeat = binding.RepeatInterval > 0
	}
	ic.rearmLocked()
	ic.applyShadeLocked(moment)
	ic.mu.Unlock()
	if cycle {
		ic.publishCycle(remote)
		return
	}
	ic.pressVolume(press)
	if repeat {
		ic.startRepeat(event.Code, press, binding.RepeatDelay, binding.RepeatInterval)
	}
}

// publishCycle sends the cycle request the operator arbitrates, on the
// remote's own cycle topic, not retained, because a cycle is an event and
// not a state. It is the same message the translator publishes from a
// Play.
func (ic *idleCommander) publishCycle(remote idleRemote) {
	if remote.focus == "" {
		return
	}
	ic.publish(remote.focus+focusCycleSuffix, nil, false)
}

// onFocus folds one mark off a remote's focus topic. It sets the gate
// every time. A live message that names this Player is a person pointing
// the controller here: it lifts the shade, restarts the quiet window, and
// pulses the display with the remote's index. The session's first message
// is the broker's retained catch-up, so it sets the gate and does nothing
// else. A mark that names another Player, or a Play name left from an
// older operator, gates closed and pulses nothing.
//
// A mark that does not name this Player also stops every repeat, the same
// move the translator's setFocus makes, so a volume button held as focus
// cycles away stops stepping this unit at once instead of at the release.
// The repeats are keyed by the control's code and not by the controller,
// so the stop covers all of them.
func (ic *idleCommander) onFocus(events string, remote idleRemote, payload []byte) {
	mark := string(payload)
	ic.focusMu.Lock()
	live := ic.marks[events].caughtUp
	ic.marks[events] = focusMark{player: mark, caughtUp: true}
	ic.focusMu.Unlock()
	if !ic.namesThisPlayer(mark) {
		ic.stopAllRepeats()
		return
	}
	if !live {
		return
	}
	ic.mu.Lock()
	moment := ""
	if ic.asleep {
		ic.asleep = false
		moment = screenWakeEvent
	}
	ic.rearmLocked()
	ic.applyShadeLocked(moment)
	ic.publishScreen(screenMessage{Event: screenFocusEvent, Remote: &remote.index})
	ic.mu.Unlock()
}

// holdsFocus reports whether this remote's mark names this Player right
// now.
func (ic *idleCommander) holdsFocus(events string) bool {
	ic.focusMu.Lock()
	defer ic.focusMu.Unlock()
	return ic.namesThisPlayer(ic.marks[events].player)
}

// namesThisPlayer compares one mark against the Player's own name. The
// name is read once at start and never changes, so this takes no lock. A
// sidecar that read no name matches no mark and answers no press.
func (ic *idleCommander) namesThisPlayer(mark string) bool {
	return ic.playerName != "" && mark == ic.playerName
}

// remoteForFocus reports which remote a focus topic marks. The list is the
// unit's own controllers, so the scan is the same shape holdsKeymapTopic
// runs.
func (ic *idleCommander) remoteForFocus(topic string) (string, idleRemote, bool) {
	if topic == "" {
		return "", idleRemote{}, false
	}
	for events, remote := range ic.remotes {
		if remote.focus == topic {
			return events, remote, true
		}
	}
	return "", idleRemote{}, false
}

// applyShadeLocked states one fold's moment on the screen topic. A wake
// also states the on desire, which is what lifts the override. An empty
// moment is the ordinary case of a fold that changed no state, and it
// states nothing.
//
// The caller holds ic.mu across both the state change and this
// publish, so the order two goroutines moved the shade in is the order
// the client reads; the publish never waits, because a bus publish
// enqueues or drops.
func (ic *idleCommander) applyShadeLocked(moment string) {
	if moment == "" {
		return
	}
	ic.publishScreen(screenMessage{Event: moment})
	if moment == screenWakeEvent {
		ic.setDesireLocked(panelDesireOn)
	}
}

// publishScreen states one moment on the Player's screen topic.
// The shade is a state, so sleep and wake are retained and a client that
// restarts reads the shade it left; the focus and the present are moments,
// so they are not, and a restart replays no press. The caller holds ic.mu.
func (ic *idleCommander) publishScreen(message screenMessage) {
	// A word and an index always marshal, so the error is the
	// interface's and not a state this code reaches.
	payload, _ := json.Marshal(message)
	ic.publish(ic.screenTopic, payload, screenRetains(message.Event))
}

// The shade moments are the two the broker holds; MQTT keeps one
// retained message per topic, so the last shade stands and a moment
// published after it does not clear it.
func screenRetains(event string) bool {
	return event == screenSleepEvent || event == screenWakeEvent
}

// bindingForLocked reports what this press names on the remote that
// published it. The match runs the translator's own matchBinding
// against that remote's compiled table, so back and a volume step
// mean here exactly what they mean during a film. A remote with no
// keymap, or a press no binding matches, names nothing.
func (ic *idleCommander) bindingForLocked(topic string, event remoteEvent) (compiledBinding, bool) {
	keymap := ic.remotes[topic].keymap
	if keymap == "" {
		return compiledBinding{}, false
	}
	return matchBinding(ic.tables[keymap],
		inputEvent{Type: event.Type, Code: event.Code, Value: event.Value})
}

// holdVolume folds one message off the volume topic. The sidecar draws
// nothing, so it keeps the level for one reason: a volume press steps
// from the last level the topic delivered. The idle client subscribes to
// the same retained topic and draws the indicator from it.
func (ic *idleCommander) holdVolume(payload []byte) {
	state, ok := parseVolumeState(payload)
	if !ok {
		return
	}
	ic.volumeMu.Lock()
	ic.volume = state
	ic.haveVolume = true
	ic.volumeMu.Unlock()
}

// onBusConnect runs at the start of every bus session. A fresh session
// redelivers every retained mark, so each one is a catch-up again and
// pulses nothing. The mark itself stands across the reconnect, so the
// gate does not open or close on a broker restart alone.
//
// The shade is the sidecar's own retained state the way the
// desire is, so it goes out again on every session. A pod that rolled
// while the screen was dark starts awake and overwrites the sleep the
// process before it left, so the screen comes back lit and the quiet
// window runs down from here.
func (ic *idleCommander) onBusConnect(*Bus) {
	ic.focusMu.Lock()
	for events, mark := range ic.marks {
		mark.caughtUp = false
		ic.marks[events] = mark
	}
	ic.focusMu.Unlock()
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.publishScreen(screenMessage{Event: ic.shadeLocked()})
	// The panel desire is the sidecar's own retained state, so
	// it goes out again on every session. A pod that returns while the
	// panel is dark states the on desire here, and the operator lifts
	// the override.
	ic.publishDesireLocked()
}

// shadeLocked is the shade the sidecar holds now, as the moment
// that states it. The caller holds ic.mu.
func (ic *idleCommander) shadeLocked() string {
	if ic.asleep {
		return screenSleepEvent
	}
	return screenWakeEvent
}

// pressVolume publishes what a press means, retained. An empty action is
// the ordinary case of a press that named no level, and it publishes
// nothing. It computes from
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
	ic.publish(ic.volumeTopic, payload, true)
}

// startRepeat runs one held control's repeat on the shared clock the
// translator ticks on, so a hold feels the same on the idle screen as
// during a film. A press of the same code while a repeat runs replaces
// it, because a second press means the first release was missed or the
// direction changed, and one control drives one repeat.
func (ic *idleCommander) startRepeat(code uint16, press mediaCommand, delayMillis, intervalMillis int) {
	ctx, cancel := context.WithCancel(ic.runCtx)
	ic.repeatMu.Lock()
	if previous, ok := ic.repeats[code]; ok {
		previous()
	}
	ic.repeats[code] = cancel
	ic.repeatMu.Unlock()
	go runRepeat(ctx,
		time.Duration(delayMillis)*time.Millisecond,
		time.Duration(intervalMillis)*time.Millisecond,
		func() { ic.repeatVolume(press) })
}

// stopRepeat ends the repeat a release names. A release for a code with
// no repeat is the ordinary case of a control that does not repeat, and
// it does nothing.
func (ic *idleCommander) stopRepeat(code uint16) {
	ic.repeatMu.Lock()
	if cancel, ok := ic.repeats[code]; ok {
		cancel()
		delete(ic.repeats, code)
	}
	ic.repeatMu.Unlock()
}

// stopAllRepeats cancels every held control's repeat at once, so no
// repeat outlives the focus that started it. It is the same act the
// translator makes when a mark moves off its Player.
func (ic *idleCommander) stopAllRepeats() {
	ic.repeatMu.Lock()
	for code, cancel := range ic.repeats {
		cancel()
		delete(ic.repeats, code)
	}
	ic.repeatMu.Unlock()
}

// repeatVolume is one tick of a held volume control. It reads the two
// press gates again on every tick, because a Play can start or the
// screen can sleep mid-hold, and a tick in either state must publish
// nothing. A tick that acts restarts the quiet window the way the press
// did, so a long hold never fades the screen it is adjusting.
func (ic *idleCommander) repeatVolume(press mediaCommand) {
	ic.mu.Lock()
	act := ic.idle && !ic.asleep
	if act {
		ic.rearmLocked()
	}
	ic.mu.Unlock()
	if act {
		ic.pressVolume(press)
	}
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
	for _, remote := range ic.remotes {
		if remote.keymap == topic {
			return true
		}
	}
	return false
}

// setTable replaces one keymap's held table. A payload that does not
// decode leaves the last good table in place, the same way the
// playback translator holds its own.
//
// The repeat milliseconds are clamped here, because a table off
// the bus is not the operator's compile and a value that overflows a
// Duration would panic the ticker a held control starts.
func (ic *idleCommander) setTable(topic string, payload []byte) {
	var table []compiledBinding
	if err := json.Unmarshal(payload, &table); err != nil {
		return
	}
	ic.mu.Lock()
	ic.tables[topic] = clampRepeats(table)
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
	// The second window runs from the moment the shade came
	// down, so the two windows measure one quiet stretch. It arms only
	// for a Player that states a window, and only while the desire is
	// still on.
	if ic.asleep {
		if ic.offAfter <= 0 || ic.desire != panelDesireOn {
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
	ic.applyShadeLocked(screenSleepEvent)
	ic.mu.Unlock()
}

// darken is the off window running out. It states the off
// desire and writes no hardware. The operator reads the desire and
// overrides the screen's Display.
//
// The check and the publish run under one hold of ic.mu, so a
// press that wakes the screen between them cannot leave the panel dark
// behind a lit one.
func (ic *idleCommander) darken(generation uint64) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if generation != ic.generation || !ic.asleep {
		return
	}
	ic.offTimer = nil
	ic.setDesireLocked(panelDesireOff)
}

// setDesireLocked holds the new desire and publishes it retained, so
// the operator reads the current one the moment it subscribes. An
// unchanged desire publishes nothing.
//
// The caller holds ic.mu.
func (ic *idleCommander) setDesireLocked(desire string) {
	if ic.desire == desire {
		return
	}
	ic.desire = desire
	ic.publishDesireLocked()
}

// publishDesire states the desire the sidecar holds now. It
// runs on every bus session, because a sidecar that started while the
// panel was dark holds the on desire and the retained topic holds the
// off desire of the process before it.
func (ic *idleCommander) publishDesire() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.publishDesireLocked()
}

// PublishDesireLocked is the publish itself, for a caller that
// already holds ic.mu. A Player with no panel topic states no desire.
func (ic *idleCommander) publishDesireLocked() {
	if ic.panelTopic == "" {
		return
	}
	payload, err := json.Marshal(panelDesire{Desire: ic.desire})
	if err != nil {
		return
	}
	ic.publish(ic.panelTopic, payload, true)
}
