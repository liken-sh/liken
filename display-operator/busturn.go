package main

// A panel handles one DDC/CI exchange at a time. An exchange writes a
// request, waits, and reads the reply, so two exchanges that overlap on
// one bus read each other's replies. A claim's prepare, the Display
// pass's poll, the probe, and the standby after a release each open the
// panel's bus from their own goroutine. So each opens it through
// busFor, which waits until no other caller has the bus open, and then
// waits until ddcBusGap has passed since the last caller closed it.
//
// The lock is a buffered channel and not a sync.Mutex, because
// testing/synctest treats a goroutine that waits on a channel as
// durably blocked, and one that waits on a mutex as not. The fake
// clock advances only while every goroutine of the bubble is durably
// blocked, so callers that waited on a mutex would stop it.

import (
	"sync"
	"time"
)

// busTurn is the lock on one bus. token holds a value while a caller
// has the bus open, and returned is when the last caller closed it.
type busTurn struct {
	token    chan struct{}
	returned time.Time
}

// turn returns the busTurn for the bus at path, and creates it on
// first use.
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

// take waits until the bus is free, then until ddcBusGap has passed
// since the last caller released it.
func (t *busTurn) take(now func() time.Time, pause func(time.Duration)) {
	t.token <- struct{}{}
	if wait := t.returned.Add(ddcBusGap).Sub(now()); wait > 0 {
		pause(wait)
	}
}

// give releases the bus. The next take waits ddcBusGap from at.
func (t *busTurn) give(at time.Time) {
	t.returned = at
	<-t.token
}

// pause sleeps for wait, through c.sleep when a test sets it.
func (c *panelControls) pause(wait time.Duration) {
	if c.sleep == nil {
		time.Sleep(wait)
		return
	}
	c.sleep(wait)
}

// turnBus is an open bus that releases its busTurn on the first
// Close.
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
