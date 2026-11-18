// A poll reads the player over several seconds, and an event or a
// setter can change the volume, the mute, or the play state while the
// poll reads. So that the poll does not overwrite the newer value with
// an older one, each event and setter write takes the next number of
// c.writes, and the poll records that number before it reads. When the
// poll publishes, it keeps the client's value of a field only when a
// write numbered after the poll's mark changed it. Every other field
// takes the poll's value, because an amp does not resend an event that
// the client missed.

package wiim

import "github.com/liken-sh/liken/equipment-operator/equipment"

// fieldWrites holds the number of the last write to each field.
type fieldWrites struct {
	volume, mute, status uint64
}

// eventMark returns the number of the most recent write.
func (c *Client) eventMark() uint64 {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.writes
}

// wrote gives a write to field the next number. The caller holds
// c.mutex.
func (c *Client) wrote(field *uint64) {
	c.writes++
	*field = c.writes
}

// publishSince publishes a poll's state, keeping the client's current
// value of each field that a write numbered after mark changed.
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
