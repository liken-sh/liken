package main

// The translator sidecar runs one per controller a Player names. It
// subscribes to the Remote's events topic and to the retained keymap
// topic, matches each raw evdev event against the held table, and
// publishes the named command to the Play's commands topic. It never
// touches mpv's IPC socket: the command sidecar owns that, and the
// commands topic is the seam between them.
//
// The split keeps each vocabulary on its own side. Only the translator
// reads a controller's evdev codes and a Keymap's table; only the
// command sidecar turns a named command into mpv's own words.
//
// One controller can drive several units, so it has a translator in each
// of their pods, all reading its one events topic. The translator
// subscribes to the controller's focus topic and gates on the retained
// mark: it acts on a press only when the mark names the Player its own
// Play runs on, and stays quiet otherwise. The mark names a Player and
// never a Play, because a claim is exclusive and one active Play holds one
// Player. A source press it holds asks the operator to move the mark to
// the next unit.

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// maxRepeatWindow caps one synthesized repeat. A controller that sleeps
// mid-hold publishes no release, so without this cap the repeat would
// fire until the film ended. A person does not hold a control this long,
// so the cap ends a repeat a lost release left running.
var maxRepeatWindow = 30 * time.Second

// translator holds the current compiled table and the repeat state. The
// keymap topic is retained, so the current table arrives the instant the
// client connects, and a later message on it replaces the held table.
// Before any keymap arrives, the table is empty and events match
// nothing. tableMu guards the table because the bus reader replaces it
// while an event read holds it.
type translator struct {
	commandsTopic string
	eventsTopic   string
	keymapTopicID string
	focusTopicID  string

	bus *Bus

	// playName is this translator's own Play. It names the commands topic
	// the translator publishes into and the client id it connects with.
	playName string
	// playerName is the Player this translator's Play runs on. The focus
	// mark must hold this value for a press to act, so it is the gate.
	playerName string
	// focusCycleTopic is where a cycle-focus press publishes its request for
	// the operator to arbitrate.
	focusCycleTopic string

	tableMu sync.Mutex
	table   []compiledBinding

	// focusOwner is the Player the retained mark names. The gate compares
	// it against playerName, and focusMu guards it because the bus reader
	// writes it while an event read holds it.
	focusMu    sync.Mutex
	focusOwner string

	// repeats holds one cancel per held control that repeats, keyed by
	// its evdev code, so the release stops the repeat the press started.
	// repeatCtx is the run's context, so every repeat ends when the
	// translator does.
	repeatCtx context.Context
	repeatMu  sync.Mutex
	repeats   map[uint16]context.CancelFunc
}

// runTranslator connects to the bus, applies the retained keymap to
// each controller event, and publishes named commands to the Play's
// commands topic. It returns on the kubelet's grace signal.
func runTranslator() {
	playNamespace := os.Getenv(playNamespaceVariable)
	playName := os.Getenv(playNameVariable)
	busAddress := os.Getenv(busAddressVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}
	playerName := os.Getenv(playerNameVariable)
	remoteName := os.Getenv(remoteNameVariable)
	eventsTopic := os.Getenv(remoteEventsVariable)
	keymapTopicID := os.Getenv(keymapTopicVariable)
	focusTopicID := os.Getenv(focusTopicVariable)

	// The translator is its container's PID 1, so the signal context ends
	// the run on the kubelet's SIGTERM. It is built before the bus, so a
	// repeat that a press starts the moment the bus connects has a context
	// to end on.
	runCtx, stopRun := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopRun()

	tr := &translator{
		commandsTopic:   playCommandsTopic(base, playNamespace, playName),
		eventsTopic:     eventsTopic,
		keymapTopicID:   keymapTopicID,
		focusTopicID:    focusTopicID,
		playName:        playName,
		playerName:      playerName,
		focusCycleTopic: remoteFocusCycleTopic(base, playNamespace, remoteName),
		repeatCtx:       runCtx,
		repeats:         map[uint16]context.CancelFunc{},
	}
	tr.bus = newBus(busAddress, "translate-"+playNamespace+"-"+playName+"-"+remoteName, nil, nil, tr.handle)
	// The three subscriptions are made once. The Bus remembers each
	// filter and re-sends it on every reconnect, so a broker restart
	// delivers the retained keymap again without the translator
	// subscribing a second time.
	tr.bus.Subscribe(tr.eventsTopic)
	tr.bus.Subscribe(tr.keymapTopicID)
	tr.bus.Subscribe(tr.focusTopicID)

	tr.bus.Run(runCtx)
}

// handle dispatches one inbound message by its topic: the keymap topic
// replaces the held table, the focus topic stores the mark, and the
// events topic turns a controller event into a command.
func (tr *translator) handle(topic string, payload []byte) {
	switch topic {
	case tr.keymapTopicID:
		tr.setTable(payload)
	case tr.focusTopicID:
		tr.setFocus(payload)
	case tr.eventsTopic:
		tr.event(payload)
	}
}

// setTable replaces the held compiled table with the one the retained
// keymap topic carries. A payload that does not decode leaves the
// last-good table in place, so a cleared or malformed keymap does not
// empty a running translation.
func (tr *translator) setTable(payload []byte) {
	var table []compiledBinding
	if err := json.Unmarshal(payload, &table); err != nil {
		return
	}
	tr.tableMu.Lock()
	tr.table = table
	tr.tableMu.Unlock()
}

// setFocus records the new mark. When focus leaves this Player, it stops
// every repeat, so a control held as focus moves away does not keep firing
// on a film this translator no longer drives.
func (tr *translator) setFocus(payload []byte) {
	owner := string(payload)
	tr.focusMu.Lock()
	tr.focusOwner = owner
	tr.focusMu.Unlock()
	if owner != tr.playerName {
		tr.stopAllRepeats()
	}
}

// event turns one controller event into a command. A release stops any
// repeat and returns, focused or not, so a held control always cleans up.
// A press acts only when the mark names the Player this Play runs on. A
// cycle-focus binding asks the operator to move the mark and never reaches
// mpv; any other binding publishes its named command.
func (tr *translator) event(payload []byte) {
	var event remoteEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	if event.Value == 0 {
		tr.stopRepeat(event.Code)
		return
	}
	tr.focusMu.Lock()
	owner := tr.focusOwner
	tr.focusMu.Unlock()
	if owner != tr.playerName {
		return
	}
	tr.tableMu.Lock()
	table := tr.table
	tr.tableMu.Unlock()
	binding, ok := matchBinding(table, inputEvent{Type: event.Type, Code: event.Code, Value: event.Value})
	if !ok {
		return
	}
	if binding.Action == actionCycleFocus {
		tr.publishCycle()
		return
	}
	command := mediaCommand{Action: binding.Action, Amount: binding.Amount}
	tr.publish(command)
	// A binding that repeats publishes the same command again while the
	// control is held. The press published once above, so the repeat
	// re-publishes until the release stops it.
	if binding.RepeatInterval > 0 {
		tr.startRepeat(event.Code, command, binding.RepeatDelay, binding.RepeatInterval)
	}
}

// publishCycle sends the cycle request the operator arbitrates. It is not
// retained, because a cycle is an event and not a state.
func (tr *translator) publishCycle() {
	tr.bus.Publish(tr.focusCycleTopic, nil, false)
}

// stopAllRepeats cancels every held-control repeat at once, so no repeat
// outlives the focus that started it when the mark moves to another Play.
func (tr *translator) stopAllRepeats() {
	tr.repeatMu.Lock()
	for code, cancel := range tr.repeats {
		cancel()
		delete(tr.repeats, code)
	}
	tr.repeatMu.Unlock()
}

// publish encodes one named command and sends it to the commands topic,
// not retained, because a command is an event and not a state.
func (tr *translator) publish(command mediaCommand) {
	payload, err := json.Marshal(command)
	if err != nil {
		return
	}
	tr.bus.Publish(tr.commandsTopic, payload, false)
}

// startRepeat runs one held control's repeat. A press of the same code
// while a repeat runs replaces it, because a second press means the first
// release was missed or the direction changed, and one control drives one
// repeat.
func (tr *translator) startRepeat(code uint16, command mediaCommand, delayMillis, intervalMillis int) {
	ctx, cancel := context.WithCancel(tr.repeatCtx)
	tr.repeatMu.Lock()
	if previous, ok := tr.repeats[code]; ok {
		previous()
	}
	tr.repeats[code] = cancel
	tr.repeatMu.Unlock()
	go tr.repeatLoop(ctx, command,
		time.Duration(delayMillis)*time.Millisecond,
		time.Duration(intervalMillis)*time.Millisecond)
}

// stopRepeat ends the repeat a release names. A release for a code with
// no repeat is the ordinary case of a control that does not repeat, and
// it does nothing.
func (tr *translator) stopRepeat(code uint16) {
	tr.repeatMu.Lock()
	if cancel, ok := tr.repeats[code]; ok {
		cancel()
		delete(tr.repeats, code)
	}
	tr.repeatMu.Unlock()
}

// repeatLoop re-publishes one held control's command. The press already
// published once, so the loop waits the delay, which is what separates a
// tap from a hold, then publishes every interval. It ends on the
// release, on the translator shutting down, or on the safety window.
func (tr *translator) repeatLoop(ctx context.Context, command mediaCommand, delay, interval time.Duration) {
	runRepeat(ctx, delay, interval, func() {
		tr.publish(command)
	})
}

// runRepeat is the one clock a held control ticks on, for the translator
// during a film and for the idle sidecar between films, so a hold feels
// the same on both screens. It waits the delay, which is what separates
// a tap from a hold, then fires every interval. It ends on the context,
// which the release cancels, or on the safety window.
func runRepeat(ctx context.Context, delay, interval time.Duration, fire func()) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	window := time.NewTimer(maxRepeatWindow)
	defer window.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-window.C:
			return
		case <-ticker.C:
			fire()
		}
	}
}
