package main

// The press that wakes the room when a sleeping remote connects.
//
// A Bluetooth LE remote ends its link after an idle time, and a press
// wakes it. The remote sends no report for that press: the relay opens
// the remote's node before the link is encrypted, and the first report
// the remote sends is the one for the next press. So without this file,
// a person presses once to wake the remote and again to wake the room.
//
// When a remote connects after a sleep, the relay writes one press and
// one release of KEY_UNKNOWN into the remote's virtual device, before it
// starts to move the real node's events. The relay cannot tell which
// button was pressed, and KEY_UNKNOWN is the kernel's code for a key
// with no name. A consumer with no binding for it treats it as a press
// and nothing more: a sleeping media-screen wakes the screen and states
// the on desire, and media-operator asks the receiver for the unit's
// input. KEY_WAKEUP is not used, because media-operator and
// media-screen read it as the TV remote's own Power On, which sends no
// input ask.
//
// The pass arms a controller when the Peripheral's Connected condition
// holds Asleep, with the condition's lastTransitionTime, and disarms it
// for every other reason (peripheral.go). The relay decides at the open
// from the arming alone. It reads no Peripheral and makes no API call,
// so a pass that wrote LinkUp a moment before the open cannot stop the
// press, and the lock the relay holds never waits on the API server.

import (
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"time"
)

// keyUnknown is KEY_UNKNOWN, and synReport is SYN_REPORT, the event
// that ends each frame.
const (
	keyUnknown = 240
	synReport  = 0
)

// wakeAfter is how long a remote's link must have been down before its
// connect writes a press. A remote that lost its link while it was
// awake, to interference or to a stream of audio on the same radio,
// also reports a timeout, and it connects again on its own within
// seconds. A person rarely presses a button within 10 seconds of the
// moment the remote went to sleep, an idle hour after the last press.
const wakeAfter = 10 * time.Second

// follow arms or disarms one controller from the Connected condition a
// status write settled on. Asleep arms it with the time the link ended,
// and every other reason disarms it. A pass writes the same Asleep, with
// the same time, on each pass while the remote sleeps, so a time that
// already gave a press arms nothing: each sleep gives one press.
func (r *relays) follow(mac string, connection Condition) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if connection.Reason != reasonAsleep {
		if held := r.held[mac]; held != nil {
			held.armed = time.Time{}
		}
		return
	}
	held := r.controller(mac)
	if connection.LastTransitionTime.Equal(held.pressed) {
		return
	}
	held.armed = connection.LastTransitionTime
}

// pressToWake writes the press into one node that the relay has just
// opened, when the controller is armed and slept long enough, and then
// disarms it. A remote opens two nodes, and an HID device can be removed
// and added again on one link, so the disarm keeps each sleep to one
// press. The caller holds the lock.
//
// The node must hold a keyboard code, a key below BTN_MISC, so a
// gamepad's node gets nothing. It must also deliver EV_KEY to a claim.
// The press goes into the virtual device and does not pass through the
// mask on the real node, so the relay asks the same inputMasks that set
// that mask whether a key would reach the claim.
func (r *relays) pressToWake(mac string, held *controllerRelay, relay *nodeRelay, want inputClasses) {
	if held.armed.IsZero() || r.now().Sub(held.armed) < wakeAfter {
		return
	}
	if !holdsKeyboardCode(relay.caps) || !deliversKeys(relay.caps, want) {
		return
	}
	if err := relay.device.write(wakePress()); err != nil {
		fmt.Fprintf(os.Stderr, "relay: writing the wake press to %s: %v\n", relay.device.node(), err)
		return
	}
	held.pressed, held.armed = held.armed, time.Time{}
	r.metrics.countWakePress(deviceName(mac))
	fmt.Printf("relay: controller %s woke from sleep, so the relay wrote KEY_UNKNOWN to %s\n", publishedMAC(mac), relay.device.node())
}

// holdsKeyboardCode reports whether a node reports a key below BTN_MISC,
// the codes of a keyboard.
func holdsKeyboardCode(caps evdevCapabilities) bool {
	return anyInRange(caps.Codes["EV_KEY"], 0, btnMisc)
}

// deliversKeys reports whether a node passes EV_KEY to the claims that
// demand want. A node no claim demands delivers nothing, and a nil mask
// delivers every event type.
func deliversKeys(caps evdevCapabilities, want inputClasses) bool {
	if want == 0 {
		return false
	}
	masks := inputMasks(caps, want)
	return masks == nil || slices.Contains(masks[0].codes, evKey)
}

// declaredCodes is the codes a virtual device declares for one event
// type: the codes the real node reports, and KEY_UNKNOWN on a node with
// a keyboard code. The kernel drops a key that the device did not
// declare, with no error, and a uinput device cannot declare a code
// after it is created. The code is not added to the stored
// capabilities, so the snapshot in the bond's Secret, the input
// classes, and the ResourceSlice do not change.
func declaredCodes(caps evdevCapabilities, eventType string) []uint16 {
	codes := caps.Codes[eventType]
	if eventType != "EV_KEY" || !holdsKeyboardCode(caps) || slices.Contains(codes, keyUnknown) {
		return codes
	}
	return append(slices.Clone(codes), keyUnknown)
}

// wakePress is one press and one release of KEY_UNKNOWN, each in its
// own frame. The kernel stamps the time of an event written to uinput,
// so the time fields stay zero.
func wakePress() []byte {
	var events []byte
	for _, value := range []int32{1, 0} {
		events = append(events, inputRecord(evKey, keyUnknown, value)...)
		events = append(events, inputRecord(evSyn, synReport, 0)...)
	}
	return events
}

// inputRecord lays out one struct input_event: two time words, then the
// type, the code, and the value, in the machine's own byte order.
func inputRecord(eventType, code uint16, value int32) []byte {
	record := make([]byte, inputEventSize)
	binary.NativeEndian.PutUint16(record[16:], eventType)
	binary.NativeEndian.PutUint16(record[18:], code)
	binary.NativeEndian.PutUint32(record[20:], uint32(value))
	return record
}
