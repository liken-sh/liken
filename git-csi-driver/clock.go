package main

// clock.go holds the time source of the watch on a work tree. The
// watch measures the quiesce and the sweep against it. The driver
// runs on the wall clock. A test that runs the kernel's inotify watch
// drives the quiesce on a clock of its own, so a class's quiesce of
// 5s, the floor that `shortestQuiesce` sets, runs out when the test
// says and not 5 real seconds later. `synctest` cannot give that watch
// a fake clock, because a goroutine that reads the inotify file is not
// durably blocked.

import "time"

// clock is the time the watch reads and the timers it waits on.
type clock interface {
	Now() time.Time
	NewTimer(wait time.Duration) timer
}

// timer is the part of a `time.Timer` the watch uses.
type timer interface {
	Chan() <-chan time.Time
	Reset(wait time.Duration)
	Stop()
}

// wallClock is the clock the driver runs on.
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) NewTimer(wait time.Duration) timer {
	return wallTimer{time.NewTimer(wait)}
}

// wallTimer is a `time.Timer`. Since Go 1.23, Reset and Stop discard a
// value the timer sent and nobody received, so the watch needs no drain.
type wallTimer struct {
	*time.Timer
}

func (t wallTimer) Chan() <-chan time.Time { return t.C }

func (t wallTimer) Reset(wait time.Duration) { t.Timer.Reset(wait) }

func (t wallTimer) Stop() { t.Timer.Stop() }
