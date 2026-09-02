package main

// The standing pod's normalising half. Normalisation runs here and
// nowhere else, because this is the one process beside the device, it
// is where hwdb runs on any Linux machine, and one pod serves every
// consumer at once. The pod holds the table the operator publishes on
// this Remote's keys topic, folds each raw evdev event through it,
// publishes the kernel's name for the control, and synthesises the
// repeat a device that never autorepeats cannot report.

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// maxRepeatWindow caps one synthesised repeat. A controller that
// sleeps mid-hold publishes no release, and without the cap the repeat
// would run until the film ended. No person holds a control this
// long.
var maxRepeatWindow = 30 * time.Second

// The ceiling on the milliseconds a table off the bus can name. It is
// past any rate a person holds a control at and past the window above,
// and it keeps a value that overflows a Duration from panicking the
// ticker.
const maxRepeatMillis = 60000

// A control is one physical thing on the controller: the event type
// with the code. The type belongs in the key because a hat axis and a
// key share the number space, so the code alone would collide.
type control struct {
	eventType uint16
	code      uint16
}

// keyState is what the fold holds: the table the keys topic
// delivered, the key each hat axis is holding down, and one cancel
// per control that repeats. The axis needs holding because the
// release of a hat carries value 0 and no direction, so the name of
// the key to release comes from the press.
type keyState struct {
	mu      sync.Mutex
	table   []compiledBinding
	held    map[uint16]string
	repeats map[control]context.CancelFunc
}

// setTable replaces the held table. A payload that does not decode
// leaves the last good table in place, so a cleared or malformed table
// does not stop a controller mid-film. The repeat milliseconds are
// clamped, because a table off the bus is not necessarily the
// operator's own compile.
func (r *reader) setTable(payload []byte) {
	var table []compiledBinding
	if err := json.Unmarshal(payload, &table); err != nil {
		return
	}
	for index := range table {
		table[index].RepeatDelay = clampMillis(table[index].RepeatDelay)
		table[index].RepeatInterval = clampMillis(table[index].RepeatInterval)
	}
	r.keys.mu.Lock()
	r.keys.table = table
	r.keys.mu.Unlock()
}

// clampMillis holds one value under the ceiling. A value at or below
// zero passes through: an interval below zero is a row that does not
// repeat, and a delay that low starts the repeat at once.
func clampMillis(millis int) int {
	if millis > maxRepeatMillis {
		return maxRepeatMillis
	}
	return millis
}

// fold is the whole of what the pod does with one event, and it has
// three answers. A control the table names publishes that name. A
// KEY_* code with no row publishes itself. A control the table maps to
// none, or the kernel does not name, publishes nothing. The value is
// the kernel's, and a row with a repeat block makes the press start
// the synthesised stream the release ends.
func (r *reader) fold(event inputEvent) {
	if event.Type == evAbs {
		r.foldAxis(event)
		return
	}
	row, mapped := lookupKey(r.table(), event)
	name := keyCodeNames[event.Code]
	if mapped {
		name = row.Key
	}
	if name == "" || name == keyNone {
		return
	}
	switch {
	case event.Value == 0:
		r.stopRepeat(control{eventType: evKey, code: event.Code})
		r.publishKey(name, 0)
	case event.Value == 1:
		r.publishKey(name, 1)
		r.startRepeat(control{eventType: evKey, code: event.Code}, name, row)
	default:
		// The kernel's own autorepeat on a keyboard key. It passes as it
		// arrived, and the pod synthesises nothing beside it.
		r.publishKey(name, event.Value)
	}
}

// foldAxis is the hat's half. It is separate because a hat reports its
// direction in the value: the press names the key and the release
// carries no direction at all, so the pod holds the key the press
// published to name the release.
func (r *reader) foldAxis(event inputEvent) {
	if event.Value == 0 {
		name := r.releaseAxis(event.Code)
		if name == "" {
			return
		}
		r.stopRepeat(control{eventType: evAbs, code: event.Code})
		r.publishKey(name, 0)
		return
	}
	row, mapped := lookupKey(r.table(), event)
	if !mapped || row.Key == keyNone {
		return
	}
	r.holdAxis(event.Code, row.Key)
	r.publishKey(row.Key, 1)
	r.startRepeat(control{eventType: evAbs, code: event.Code}, row.Key, row)
}

func (r *reader) table() []compiledBinding {
	r.keys.mu.Lock()
	defer r.keys.mu.Unlock()
	return r.keys.table
}

// holdAxis records the key one hat direction is holding down. A
// second press with no release between them replaces the first,
// because the two directions of one axis are one control.
func (r *reader) holdAxis(code uint16, key string) {
	r.keys.mu.Lock()
	defer r.keys.mu.Unlock()
	if r.keys.held == nil {
		r.keys.held = map[uint16]string{}
	}
	r.keys.held[code] = key
}

// releaseAxis names the key a hat's return to center releases, and
// forgets it. A center with no press before it names nothing, which
// is the ordinary case of an axis the table drops.
func (r *reader) releaseAxis(code uint16) string {
	r.keys.mu.Lock()
	defer r.keys.mu.Unlock()
	key := r.keys.held[code]
	delete(r.keys.held, code)
	return key
}

// publishKey writes one key event to the Remote's events topic, not
// retained, because a press is an event and not a state.
func (r *reader) publishKey(key string, value int32) {
	// A string and an integer marshal unconditionally, so the error is
	// dropped.
	payload, _ := json.Marshal(keyEvent{Key: key, Value: value})
	r.bus.Publish(r.eventsTopic, payload, false)
}

// startRepeat runs one held control's synthesised repeat. A row with
// no interval starts nothing. A second press of the same control
// replaces a running repeat, because a second press means the release
// was missed or the hat changed direction.
func (r *reader) startRepeat(held control, key string, row compiledBinding) {
	if row.RepeatInterval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(r.repeatCtx)
	r.keys.mu.Lock()
	if previous, running := r.keys.repeats[held]; running {
		previous()
	}
	if r.keys.repeats == nil {
		r.keys.repeats = map[control]context.CancelFunc{}
	}
	r.keys.repeats[held] = cancel
	r.keys.mu.Unlock()
	go runRepeat(ctx,
		time.Duration(row.RepeatDelay)*time.Millisecond,
		time.Duration(row.RepeatInterval)*time.Millisecond,
		func() { r.publishKey(key, 2) })
}

// stopRepeat ends the repeat a release names. A release for a control
// with no repeat is the ordinary case and does nothing.
func (r *reader) stopRepeat(held control) {
	r.keys.mu.Lock()
	if cancel, running := r.keys.repeats[held]; running {
		cancel()
		delete(r.keys.repeats, held)
	}
	r.keys.mu.Unlock()
}

// stopAllRepeats ends every synthesised repeat at once, for the moment
// the controller's nodes vanish. A controller that slept mid-hold
// sends no release, and nothing may keep publishing for a device that
// is gone.
func (r *reader) stopAllRepeats() {
	r.keys.mu.Lock()
	for held, cancel := range r.keys.repeats {
		cancel()
		delete(r.keys.repeats, held)
	}
	r.keys.held = nil
	r.keys.mu.Unlock()
}

// runRepeat is the clock a held control ticks on. It waits the delay,
// which is what separates a tap from a hold, then fires every
// interval. It ends on the context, which the release cancels, or on
// the safety window.
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
