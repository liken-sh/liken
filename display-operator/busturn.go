package main

// One caller at a time on a panel's DDC/CI bus.
//
// A panel holds one reply at a time. An exchange writes a request,
// waits, and reads the reply, so two exchanges that overlap on one bus
// read each other's replies: the drill on stick-1 saw a brightness read
// get the contrast reply while the Display pass read the contrast. The
// claim's prepare, the Display pass, the probe, and a restore each
// reach the panel from goroutines of their own, so every one of them
// opens the bus through busFor, and busFor hands each bus to one caller
// at a time. The next caller also waits out ddcBusGap after the last
// one closed the bus, because the panel needs that gap before the next
// message whoever sends it.
//
// The turn is a channel and not a mutex, because a caller that waits
// for its turn is blocked the way a caller that waits on the wire is,
// and the tests run both on a fake clock that moves only while every
// goroutine waits on a channel or a timer.

import (
	"sync"
	"time"
)

// busTurn is the turn on one bus: a token that one caller holds, and
// when the last holder gave it back.
type busTurn struct {
	token    chan struct{}
	returned time.Time
}

// turn answers the turn on one bus, made on first use.
func (c *panelControls) turn(path string) *busTurn {
	c.turnsMu.Lock()
	defer c.turnsMu.Unlock()
	if c.turns == nil {
		c.turns = map[string]*busTurn{}
	}
	turn, known := c.turns[path]
	if !known {
		turn = &busTurn{token: make(chan struct{}, 1)}
		c.turns[path] = turn
	}
	return turn
}

// take waits for the bus, then for the gap since the last holder gave
// it back.
func (t *busTurn) take(now func() time.Time, pause func(time.Duration)) {
	t.token <- struct{}{}
	if wait := t.returned.Add(ddcBusGap).Sub(now()); wait > 0 {
		pause(wait)
	}
}

// give hands the bus to the next caller.
func (t *busTurn) give(at time.Time) {
	t.returned = at
	<-t.token
}

// pause waits on the protocol's clock, which a test replaces.
func (c *panelControls) pause(wait time.Duration) {
	if c.sleep == nil {
		time.Sleep(wait)
		return
	}
	c.sleep(wait)
}

// turnBus is an open bus that gives its turn back when it closes. A
// caller that closes twice gives the turn back once.
type turnBus struct {
	controlBus
	turn  *busTurn
	clock func() time.Time
	once  sync.Once
}

func (b *turnBus) Close() error {
	err := b.controlBus.Close()
	b.once.Do(func() { b.turn.give(b.clock()) })
	return err
}
