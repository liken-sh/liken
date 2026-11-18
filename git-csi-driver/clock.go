package main

// clock.go is the clock that the work-tree watch uses for its quiesce
// and sweep timers. A test of the kernel's inotify watch supplies its
// own clock and ends a quiesce when it chooses. The test cannot use `synctest`, because a goroutine that reads the inotify
// file is not durably blocked, so the bubble's clock never advances.

import "time"

type clock interface {
	Now() time.Time
	NewTimer(wait time.Duration) timer
}

// timer is the part of `time.Timer` that the watch uses.
type timer interface {
	Chan() <-chan time.Time
	Reset(wait time.Duration)
	Stop()
}

// wallClock is the real time, which the driver uses.
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) NewTimer(wait time.Duration) timer {
	return wallTimer{time.NewTimer(wait)}
}

// wallTimer wraps a `time.Timer`. From Go 1.23, Reset and Stop discard
// a value that the timer sent and nobody received, so the watch does
// not drain the channel.
type wallTimer struct {
	*time.Timer
}

func (t wallTimer) Chan() <-chan time.Time { return t.C }

func (t wallTimer) Reset(wait time.Duration) { t.Timer.Reset(wait) }

func (t wallTimer) Stop() { t.Timer.Stop() }
