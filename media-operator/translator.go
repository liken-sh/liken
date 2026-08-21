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
// command sidecar turns a named command into mpv's own words. It
// subscribes to the focus topic too and acts on every press for now,
// because a unit names one controller and there is no focus to honor.
// Plan 06 makes the focus gate live.

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

	tableMu sync.Mutex
	table   []compiledBinding

	// focus is the latest payload from the focus topic. Nothing reads it
	// yet: plan 06 makes the focus gate live, and for now the translator
	// acts on every press. The subscription is made from the start so the
	// mark can arrive in a later plan without a pod restart.
	focusMu sync.Mutex
	focus   []byte

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
		commandsTopic: playCommandsTopic(base, playNamespace, playName),
		eventsTopic:   eventsTopic,
		keymapTopicID: keymapTopicID,
		focusTopicID:  focusTopicID,
		repeatCtx:     runCtx,
		repeats:       map[uint16]context.CancelFunc{},
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

// setFocus stores the latest focus payload. Nothing reads it yet; plan
// 06 makes the focus gate live.
func (tr *translator) setFocus(payload []byte) {
	tr.focusMu.Lock()
	tr.focus = append(tr.focus[:0], payload...)
	tr.focusMu.Unlock()
}

// event matches one controller event against the held table and
// publishes the named command. A release, value 0, stops any repeat the
// press started and matches no binding. A press that no binding names,
// or a press that arrives before any keymap has been delivered,
// publishes nothing.
func (tr *translator) event(payload []byte) {
	var event remoteEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	if event.Value == 0 {
		tr.stopRepeat(event.Code)
		return
	}
	tr.tableMu.Lock()
	table := tr.table
	tr.tableMu.Unlock()
	binding, ok := matchBinding(table, inputEvent{Type: event.Type, Code: event.Code, Value: event.Value})
	if !ok {
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
			tr.publish(command)
		}
	}
}
