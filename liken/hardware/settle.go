package hardware

import (
	"context"
	"time"
)

// Settle drains further uevent signals until quiet lasts a full
// interval, so a burst of arrivals becomes one walk, but only up to
// a ceiling. Waiting for true silence does not work on a node that
// runs Kubernetes. Each pod that starts or stops adds or removes a
// veth pair, and the pair and each of its queues announce themselves,
// so the stream can stay busy for minutes. The lab observed this
// blocking a hot-plugged disk's report for minutes because of an
// unrelated crash-looping pod. Walks are cheap
// and idempotent, so when the stream will not go quiet, walking
// anyway is the correct move. Anything that changes during the walk
// sends another uevent signal. A closed channel means the listener
// stopped, so the wait ends at once, and the caller's next receive
// finds the close and opens the listener again.
func Settle(ctx context.Context, uevents <-chan struct{}, quiet, ceiling time.Duration) {
	deadline := time.NewTimer(ceiling)
	defer deadline.Stop()
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-uevents:
			if !ok {
				return
			}
			timer.Reset(quiet)
		case <-timer.C:
			return
		case <-deadline.C:
			return
		}
	}
}
