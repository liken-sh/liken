package main

// A Bluetooth LE remote drops its link when it has been idle, and the
// next button press reconnects it. What happens to that press depends
// on how the remote dropped the link:
//
//   - A T6 remote stops responding, so the link times out and the
//     Peripheral's Connected condition gets the reason Asleep. The
//     relay opens the remote's input node before the link is
//     encrypted, and the report of the waking press is lost. The first
//     report the relay receives is for the next press.
//   - An X6 air-mouse remote closes the link itself, so the reason is
//     ClosedByDevice. The remote sends the waking press after the link
//     is encrypted, and it reaches the claim like any other press.
//
// For the Asleep case, the relay writes one press and one release of
// KEY_UNKNOWN into the remote's virtual device when it reconnects,
// before it forwards any event from the real node. The relay cannot
// tell which button was pressed, and KEY_UNKNOWN is the kernel's code
// for a key with no name. A consumer with no binding for it treats it
// as a plain press, so media-screen turns a sleeping screen on and
// media-operator selects the Player's input on the receiver. The press
// is not KEY_WAKEUP, because media-operator reads KEY_WAKEUP as the
// Power On key of a TV remote (media-operator/powerkeys.go).
//
// Each status write passes the Connected condition to follow, which
// records the time of an Asleep reason in asleepSince and clears it for
// any other reason. When the relay opens a node, it reads only
// asleepSince, so it makes no API call while it holds its lock. A pass
// that writes LinkUp before the relay opens the remote's node clears
// asleepSince, and no press is written.

import (
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"time"
)

// keyUnknown is the evdev code KEY_UNKNOWN. synReport is SYN_REPORT, the
// event that ends each frame of events.
const (
	keyUnknown = 240
	synReport  = 0
)

// wakeAfter is how long a remote's link must have been down before its
// reconnect gets a press. A remote that loses its link while awake, from
// interference or from audio traffic on the same radio, also reports a
// timeout, and it reconnects by itself within seconds, with no button
// pressed. A remote sleeps only after it has been idle, so a person is
// unlikely to press a button within 10 seconds of the sleep.
const wakeAfter = 10 * time.Second

// follow sets or clears asleepSince of one controller from its
// Connected condition. Asleep sets it to the condition's
// lastTransitionTime, and any other reason clears it. Every pass while
// the remote sleeps reports the same Asleep with the same time, so a
// time that already produced a press does not set asleepSince again.
func (r *relays) follow(mac string, connection Condition) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if connection.Reason != reasonAsleep {
		if controller := r.controllers[mac]; controller != nil {
			controller.asleepSince = time.Time{}
		}
		return
	}
	controller := r.controller(mac)
	if connection.LastTransitionTime.Equal(controller.wakePressedFor) {
		return
	}
	controller.asleepSince = connection.LastTransitionTime
}

// pressToWake writes the wake press into a node that the relay has just
// opened, when asleepSince is set and the link was down for at least
// wakeAfter, and then clears asleepSince. A remote can have several
// input nodes, and the kernel can remove and add its HID device again
// on one link, so clearing it limits each sleep to one press. The
// caller holds the lock.
//
// Only a node with a keyboard code gets the press, so a gamepad's node
// gets none. The press goes straight into the virtual device and skips
// the event mask on the real node, so deliversKeys checks the mask that
// inputMasks would set, to find whether a claim receives EV_KEY.
func (r *relays) pressToWake(mac string, controller *controllerRelay, relay *nodeRelay, want inputClasses) {
	if controller.asleepSince.IsZero() || r.now().Sub(controller.asleepSince) < wakeAfter {
		return
	}
	if !hasKeyboardCode(relay.caps) || !deliversKeys(relay.caps, want) {
		return
	}
	if err := relay.device.write(wakePress()); err != nil {
		fmt.Fprintf(os.Stderr, "relay: writing the wake press to %s: %v\n", relay.device.node(), err)
		return
	}
	controller.wakePressedFor, controller.asleepSince = controller.asleepSince, time.Time{}
	r.metrics.countWakePress(deviceName(mac))
	fmt.Printf("relay: controller %s reconnected after sleeping, so the relay wrote KEY_UNKNOWN to %s\n", publishedMAC(mac), relay.device.node())
}

// hasKeyboardCode reports whether the node reports an EV_KEY code below
// BTN_MISC, the range of the keyboard keys.
func hasKeyboardCode(caps evdevCapabilities) bool {
	return anyInRange(caps.Codes["EV_KEY"], 0, btnMisc)
}

// deliversKeys reports whether the node passes EV_KEY events to the
// claims that request the input classes in want. A nil mask from
// inputMasks means that every event type passes.
func deliversKeys(caps evdevCapabilities, want inputClasses) bool {
	if want == 0 {
		return false
	}
	masks := inputMasks(caps, want)
	return masks == nil || slices.Contains(masks[0].codes, evKey)
}

// declaredCodes returns the codes that the virtual device declares for
// one event type: the codes the real node reports, plus KEY_UNKNOWN for
// a node that has a keyboard code. The kernel drops an event whose code
// the device did not declare, and a uinput device cannot declare more
// codes after it is created. KEY_UNKNOWN is not added to the stored
// capabilities, so the snapshot in the bond's Secret, the input
// classes, and the ResourceSlice do not change.
func declaredCodes(caps evdevCapabilities, eventType string) []uint16 {
	codes := caps.Codes[eventType]
	if eventType != "EV_KEY" || !hasKeyboardCode(caps) || slices.Contains(codes, keyUnknown) {
		return codes
	}
	return append(slices.Clone(codes), keyUnknown)
}

// wakePress returns the events for one press and one release of
// KEY_UNKNOWN, each followed by SYN_REPORT. The kernel sets the time of
// an event written to uinput, so the time fields stay zero.
func wakePress() []byte {
	var events []byte
	for _, value := range []int32{1, 0} {
		events = append(events, inputRecord(evKey, keyUnknown, value)...)
		events = append(events, inputRecord(evSyn, synReport, 0)...)
	}
	return events
}

// inputRecord encodes one struct input_event of a 64-bit kernel in the
// machine's byte order: two 8-byte time fields, then the type, the
// code, and the value.
func inputRecord(eventType, code uint16, value int32) []byte {
	record := make([]byte, inputEventSize)
	binary.NativeEndian.PutUint16(record[16:], eventType)
	binary.NativeEndian.PutUint16(record[18:], code)
	binary.NativeEndian.PutUint32(record[20:], uint32(value))
	return record
}
