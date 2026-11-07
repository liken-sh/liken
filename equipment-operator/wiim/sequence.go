// The order of the writes to the fields that both a poll and an event
// or a setter write: the volume, the mute, and the play state. A poll
// reads the player and publishes seconds later, and an event or a
// setter can write one of these fields in between. So each such write
// takes a number from one counter. Right before the player read, the
// poll takes the counter and copies the three fields, and at publish
// it keeps the held value of a field only when a write after that
// mark moved it.
// Every other field takes the poll's read, which is the amp's newest
// answer, whether or not a subscription is live. An amp sends no event
// again that the client missed, so a held value with no later write is
// never trusted over the poll.

package wiim

import "github.com/liken-sh/equipment-operator/equipment"

// fieldWrites holds the number of the last write to each field.
type fieldWrites struct {
	volume, mute, status uint64
}

// eventMark answers the number of the last write so far. A poll takes
// it right before it reads the player.
func (c *Client) eventMark() uint64 {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.writes
}

// wrote numbers one write to a field. The caller holds the mutex.
func (c *Client) wrote(field *uint64) {
	c.writes++
	*field = c.writes
}

// publishSince publishes a poll's state, keeping the held value of
// each field that a write numbered after mark moved.
func (c *Client) publishSince(next Status, mark uint64) {
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	if c.written.volume > mark {
		next.Playback.Volume = previous.Playback.Volume
	}
	if c.written.mute > mark {
		next.Playback.Mute = previous.Playback.Mute
	}
	if c.written.status > mark {
		next.Playback.Status = previous.Playback.Status
	}
	c.state = next
	c.reachable = equipment.ConditionTrue
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}
