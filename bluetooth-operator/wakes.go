package main

// The wakes that start a pass. Six sources say that something changed.
// wakes merges them into one channel, and settle collapses each burst
// into one pass, so a controller that connects with a burst of uevents
// and D-Bus signals costs one write.

import (
	"context"
	"fmt"
	"time"
)

// wakes merges the kernel's uevents, bluetoothd's signals, the
// PairingRequests that need a pass, the edits to Adapters and
// Peripherals, and the loop's own retries into one channel. None of
// them holds state that the loop uses, so the merge loses nothing:
// each wake means look again.
//
// Each uevent also goes to the second channel, which the loop reads
// without the settle window, so the relay opens a new evdev node at
// once. Every uevent goes there, not only an input add, because the
// uevent channel drops an event when it is full, and the dropped event
// can be the input add.
func wakes(ctx context.Context, uevents <-chan kernelEvent, blueZChanges, retries, requests, edits <-chan struct{}) (<-chan struct{}, <-chan struct{}) {
	out := make(chan struct{}, 1)
	attaches := make(chan struct{}, 1)
	send := func(to chan<- struct{}) {
		select {
		case to <- struct{}{}:
		default:
		}
	}
	wake := func() { send(out) }
	go func() {
		defer close(out)
		tick := time.NewTicker(backstopInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-uevents:
				if !ok {
					return
				}
				fmt.Println(kernelEventLine(event))
				send(attaches)
				wake()
			case _, ok := <-blueZChanges:
				if !ok {
					return
				}
				wake()
			case _, ok := <-retries:
				if !ok {
					return
				}
				wake()
			case _, ok := <-requests:
				if !ok {
					return
				}
				wake()
			case _, ok := <-edits:
				if !ok {
					return
				}
				wake()
			case <-tick.C:
				wake()
			}
		}
	}()
	return out, attaches
}

// kernelEventLine says what one uevent reported. A HID event names its
// controller, and a power supply change names none, because the
// datagram carries no address. A lost datagram says so.
func kernelEventLine(event kernelEvent) string {
	if event.Lost {
		return "kernel: uevents were lost; reading the whole state"
	}
	if event.MAC == "" {
		return fmt.Sprintf("kernel: %s %s", event.Subsystem, event.Action)
	}
	return fmt.Sprintf("controller %s: %s %s", publishedMAC(event.MAC), event.Subsystem, event.Action)
}

// settle collapses a burst of events into one wake. It emits after
// the input has been quiet for window, or after limit has passed
// since the first event of the burst, whichever comes first.
//
// The limit keeps the loop publishing under a flapping controller.
// Without it, hardware that reconnects faster than the quiet window
// would restart the wait on every event and the loop would never
// write.
func settle(ctx context.Context, in <-chan struct{}, window, limit time.Duration) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		defer close(out)

		var quiet, deadline *time.Timer
		var quietC, deadlineC <-chan time.Time
		emit := func() {
			quiet.Stop()
			deadline.Stop()
			quiet, deadline = nil, nil
			quietC, deadlineC = nil, nil
			select {
			case out <- struct{}{}:
			default:
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-in:
				if !ok {
					return
				}
				if quiet == nil {
					quiet = time.NewTimer(window)
					deadline = time.NewTimer(limit)
					quietC, deadlineC = quiet.C, deadline.C
					continue
				}
				quiet.Stop()
				quiet.Reset(window)
			case <-quietC:
				emit()
			case <-deadlineC:
				emit()
			}
		}
	}()
	return out
}
