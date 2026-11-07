// The read back after a setter. The poll reads the settings families
// only on a trigger, so a setter reads its own family back at once, and
// the controller's next compare reads the amp's answer to the send and
// not the value from before it.

package wiim

import "context"

// family is one group of fields that a setter changes and one read
// answers.
type family uint8

const (
	familyBalance family = 1 << iota
	familyName
	familyControls
	familySleep
)

// families is every family, in the order a read back reads them.
var families = []family{familyBalance, familyName, familyControls, familySleep}

// readFamily reads one family into a scratch copy of the state.
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

// take copies the fields of one family from a scratch copy into the
// state, and nothing else, so a volume a setter took while the family
// was read stays.
func (f family) take(state *Status, scratch Status) {
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

// readBack reads the families a setter changed. The controller's pass
// sends the setter, and a poll can hold the reads for many seconds when
// it reads every family, so a read back never waits for a poll: while
// one runs, the families are owed, and the poll reads them before it
// publishes.
func (c *Client) readBack(ctx context.Context, owed family) {
	if owed == 0 {
		return
	}
	if !c.reads.TryLock() {
		c.mutex.Lock()
		c.owed |= owed
		c.mutex.Unlock()
		return
	}
	defer c.reads.Unlock()
	c.mutex.Lock()
	scratch := c.state
	c.mutex.Unlock()
	for _, f := range families {
		if owed&f != 0 {
			c.readFamily(ctx, f, &scratch)
		}
	}
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	for _, f := range families {
		if owed&f != 0 {
			f.take(&c.state, scratch)
		}
	}
	next := c.state
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

// readOwed reads the families that a read back left to the poll. The
// caller holds the reads.
func (c *Client) readOwed(ctx context.Context, next *Status) {
	c.mutex.Lock()
	owed := c.owed
	c.owed = 0
	c.mutex.Unlock()
	for _, f := range families {
		if owed&f != 0 {
			c.readFamily(ctx, f, next)
		}
	}
}
