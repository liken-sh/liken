package main

import (
	"context"
	"fmt"
	"time"
)

// wakes merges the kernel's uevents, bluetoothd's signals, the
// PairingRequests that need a pass, the edits to Adapters and
// Peripherals, the loop's own retries, and a timer at backstopInterval
// into one channel. A wake carries no data and the pass reads the whole
// state, so merging loses nothing.
//
// wakes also sends each uevent to the second channel, which the loop
// reads without waiting for settle, so the relay opens a new evdev node
// at once. It sends every uevent there, not only an input add, because
// the uevent channel drops events when it is full, and the dropped event
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

func kernelEventLine(event kernelEvent) string {
	if event.Lost {
		return "kernel: uevents were lost, so the operator reads the whole state"
	}
	if event.MAC == "" {
		return fmt.Sprintf("kernel: %s %s", event.Subsystem, event.Action)
	}
	return fmt.Sprintf("controller %s: %s %s", publishedMAC(event.MAC), event.Subsystem, event.Action)
}

// settle collapses a burst of wakes into one, so a controller that
// connects with many uevents and D-Bus signals causes one pass. It sends
// a wake when the input has been quiet for window, or when limit has
// passed since the first wake of the burst, whichever comes first.
//
// Without limit, a controller that reconnects more often than window
// would restart the wait each time, and the loop would never run a pass.
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
