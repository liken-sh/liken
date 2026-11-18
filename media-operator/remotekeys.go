package main

// The remote pod translates each evdev event of its controller with the
// key table that the operator publishes on the Remote's keys topic, and
// publishes the key name to the Remote's events topic. It also generates
// repeats for controls that the kernel does not autorepeat, such as a
// hat axis. The translation runs in the remote pod because that pod
// reads the device, and every consumer of the Remote then receives the
// same key names.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
)

// maxRepeatWindow is the longest that one generated repeat runs. A
// controller that goes to sleep while a control is held sends no
// release, and without this limit the repeat would continue until the
// film ended.
var maxRepeatWindow = 30 * time.Second

// maxRepeatMillis is the largest repeat delay or interval that setTable
// accepts from the bus. A larger value can overflow when it is converted
// to a time.Duration and become negative, and time.NewTicker panics on
// a non-positive interval.
const maxRepeatMillis = 60000

// control identifies one button or axis on the controller by its evdev
// event type and code. The type is part of the identity because a hat
// axis and a key can have the same code.
type control struct {
	eventType uint16
	code      uint16
}

type keyState struct {
	mu    sync.Mutex
	table []compiledBinding
	// held is the key that each hat axis last pressed, by axis code.
	held    map[uint16]string
	repeats map[control]context.CancelFunc
}

// setTable replaces the key table with the one in payload. A payload
// that does not decode as JSON leaves the previous table in place, so a
// malformed publish does not stop a controller in the middle of a film.
// The repeat times are clamped because the payload comes from the bus
// and not from this process.
//
// A new table logs one line, because it means someone edited a Keymap
// or a Remote. The topic is retained, so a reconnect delivers the same
// table again, and an unchanged table logs nothing.
func (r *reader) setTable(payload []byte) {
	var table []compiledBinding
	if err := json.Unmarshal(payload, &table); err != nil {
		logLine(r.log, "remote: key table on %s ignored, the last good table stays: %v", r.keysTopic, err)
		return
	}
	for index := range table {
		table[index].RepeatDelay = clampMillis(table[index].RepeatDelay)
		table[index].RepeatInterval = clampMillis(table[index].RepeatInterval)
	}
	r.keys.mu.Lock()
	changed := !slices.Equal(r.keys.table, table)
	r.keys.table = table
	r.keys.mu.Unlock()
	if changed {
		logLine(r.log, "remote: key table on %s applied: %s", r.keysTopic, countOf(len(table), "row", "rows"))
	}
}

// clampMillis limits a repeat time to maxRepeatMillis. A value of zero
// or less is kept, because an interval of zero or less turns the repeat
// off, and a delay of zero or less starts the repeat at once.
func clampMillis(millis int) int {
	if millis > maxRepeatMillis {
		return maxRepeatMillis
	}
	return millis
}

// fold translates one evdev event and publishes it with the event's
// value. A control with a row in the table publishes the row's key, and
// a code with no row publishes the kernel's name for the code. Nothing
// is published when the row maps the control to none, or when the code
// has no row and no kernel name. A press starts the row's repeat, and
// the release stops it.
//
// fold returns a log line for a press, with the control and the key it
// published or the reason it published nothing. It returns "" for a
// release and a repeat, so one press logs one line.
func (r *reader) fold(event inputEvent) string {
	if event.Type == evAbs {
		return r.foldAxis(event)
	}
	row, mapped := lookupKey(r.table(), event)
	name := keyCodeNames[event.Code]
	pressed := codeField(name, event.Code) + " pressed"
	if mapped {
		name = row.Key
	}
	if name == "" || name == keyNone {
		if event.Value != 1 {
			return ""
		}
		if mapped {
			return pressed + ", published nothing, because the key table maps it to none"
		}
		return pressed + ", published nothing, because the kernel gives the code no name and the key table has no row for it"
	}
	switch {
	case event.Value == 0:
		r.stopRepeat(control{eventType: evKey, code: event.Code})
		r.publishKey(name, 0)
	case event.Value == 1:
		r.publishKey(name, 1)
		r.startRepeat(control{eventType: evKey, code: event.Code}, name, row)
		return pressed + ", published " + name + " to " + r.eventsTopic
	default:
		// The kernel's own autorepeat of a keyboard key passes through
		// unchanged, and the pod generates no repeat of its own for it.
		r.publishKey(name, event.Value)
	}
	return ""
}

// foldAxis translates an ABS event. A hat axis reports its direction in
// the event value, and its return to center has value 0 and no
// direction. So foldAxis records the key that each press published, and
// publishes the release of that key.
func (r *reader) foldAxis(event inputEvent) string {
	if event.Value == 0 {
		name := r.releaseAxis(event.Code)
		if name == "" {
			return ""
		}
		r.stopRepeat(control{eventType: evAbs, code: event.Code})
		r.publishKey(name, 0)
		return ""
	}
	pressed := fmt.Sprintf("%s %d pressed", codeField(axisCodeNames[event.Code], event.Code), event.Value)
	row, mapped := lookupKey(r.table(), event)
	if !mapped {
		return pressed + ", published nothing, because the key table has no row for it"
	}
	if row.Key == keyNone {
		return pressed + ", published nothing, because the key table maps it to none"
	}
	r.holdAxis(event.Code, row.Key)
	r.publishKey(row.Key, 1)
	r.startRepeat(control{eventType: evAbs, code: event.Code}, row.Key, row)
	return pressed + ", published " + row.Key + " to " + r.eventsTopic
}

func (r *reader) table() []compiledBinding {
	r.keys.mu.Lock()
	defer r.keys.mu.Unlock()
	return r.keys.table
}

// holdAxis records the key that the axis last pressed. A second press
// with no release between replaces the first, because both directions
// of an axis are one control.
func (r *reader) holdAxis(code uint16, key string) {
	r.keys.mu.Lock()
	defer r.keys.mu.Unlock()
	if r.keys.held == nil {
		r.keys.held = map[uint16]string{}
	}
	r.keys.held[code] = key
}

// releaseAxis returns and deletes the key that the axis last pressed.
// It returns "" when no press was recorded, as for an axis that has no
// row or is mapped to none.
func (r *reader) releaseAxis(code uint16) string {
	r.keys.mu.Lock()
	defer r.keys.mu.Unlock()
	key := r.keys.held[code]
	delete(r.keys.held, code)
	return key
}

// publishKey publishes a key event to the Remote's events topic. The
// message is not retained, because a subscriber that connects later must
// not receive an old press.
func (r *reader) publishKey(key string, value int32) {
	// Marshaling a string and an integer cannot fail.
	payload, _ := json.Marshal(keyEvent{Key: key, Value: value})
	r.bus.Publish(r.eventsTopic, payload, false)
}

// startRepeat starts the generated repeat for a pressed control. A
// second press of the same control replaces the running repeat, because
// the release was missed or the hat changed direction.
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

func (r *reader) stopRepeat(held control) {
	r.keys.mu.Lock()
	if cancel, running := r.keys.repeats[held]; running {
		cancel()
		delete(r.keys.repeats, held)
	}
	r.keys.mu.Unlock()
}

// stopAllRepeats stops every running repeat and deletes the recorded
// axis keys. readAndPublish calls it when it stops reading the device
// nodes, because a node that closes during a press sends no release.
func (r *reader) stopAllRepeats() {
	r.keys.mu.Lock()
	for held, cancel := range r.keys.repeats {
		cancel()
		delete(r.keys.repeats, held)
	}
	r.keys.held = nil
	r.keys.mu.Unlock()
}

// runRepeat calls fire every interval after delay, so a short tap is
// released before the first repeat. It returns when ctx is canceled, or
// maxRepeatWindow after the delay ends.
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
