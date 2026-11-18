// After a setter, the client reads back the settings that the setter
// changed. A poll reads the settings only when readsEveryFamily returns
// true, and without the read back the controller would compare the
// declared settings with the amp's value from before the change.

package wiim

import "context"

// A family is the group of settings fields that one setter changes and
// one read returns. The values are bit flags, so one family value can
// hold a set of families.
type family uint8

const (
	familyBalance family = 1 << iota
	familyName
	familyControls
	familySleep
)

// families lists every family in the order they are read.
var families = []family{familyBalance, familyName, familyControls, familySleep}

func (c *Client) readFamily(ctx context.Context, f family, scratch *Status) {
	switch f {
	case familyBalance:
		c.readBalance(ctx, scratch)
	case familyName:
		c.readName(ctx, scratch)
	case familyControls:
		c.readControls(ctx, scratch)
	case familySleep:
		c.readSleep(ctx, scratch)
	}
}

// copyInto copies only the fields of family f from scratch into state,
// so a volume change that a setter made during the read is kept.
func (f family) copyInto(state *Status, scratch Status) {
	switch f {
	case familyBalance:
		state.Audio.Balance = scratch.Audio.Balance
	case familyName:
		state.Device.Name = scratch.Device.Name
	case familyControls:
		state.Controls = scratch.Controls
	case familySleep:
		state.Timers.SleepSeconds = scratch.Timers.SleepSeconds
	}
}

// readBack reads the families in changed. The controller's pass calls
// the setter, and a poll can hold c.reads for many seconds. A wait for
// c.reads would stall the pass for that time. So when c.reads is held,
// readBack adds the families to c.pendingReads and returns, and the
// poll reads them before it publishes.
func (c *Client) readBack(ctx context.Context, changed family) {
	if changed == 0 {
		return
	}
	if !c.reads.TryLock() {
		c.mutex.Lock()
		c.pendingReads |= changed
		c.mutex.Unlock()
		return
	}
	defer c.reads.Unlock()
	c.mutex.Lock()
	scratch := c.state
	c.mutex.Unlock()
	for _, f := range families {
		if changed&f != 0 {
			c.readFamily(ctx, f, &scratch)
		}
	}
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	for _, f := range families {
		if changed&f != 0 {
			f.copyInto(&c.state, scratch)
		}
	}
	next := c.state
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

// readPending reads the families in c.pendingReads into next and clears
// c.pendingReads.
// The caller holds c.reads.
func (c *Client) readPending(ctx context.Context, next *Status) {
	c.mutex.Lock()
	pending := c.pendingReads
	c.pendingReads = 0
	c.mutex.Unlock()
	for _, f := range families {
		if pending&f != 0 {
			c.readFamily(ctx, f, next)
		}
	}
}
