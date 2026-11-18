package indi

import (
	"context"
	"fmt"
	"math"
)

// Sent identifies a change that the client sent, so that Settle,
// Answered, and Accepted can find the device's reply to it.
type Sent struct {
	Device   string
	Property string
	// connection, defined, and sequence are read from the store at the
	// send: the connection number, the sequence number at which the
	// property was defined, and the last sequence number applied. A
	// reply is an update after sequence, on the same connection, to the
	// same definition of the property.
	connection uint64
	defined    uint64
	sequence   uint64
	// sent is the property as the client wrote it, and changed lists
	// the members whose values the change sets.
	sent    Property
	changed []string
}

// matches reports whether p has the value of each member that the
// change set. Numbers compare with a relative tolerance of 1e-9.
func (s Sent) matches(p Property) bool {
	for _, name := range s.changed {
		want, _ := s.sent.Member(name)
		got, ok := p.Member(name)
		if !ok || got.Switch != want.Switch || got.Text != want.Text ||
			math.Abs(got.Number-want.Number) > 1e-9*math.Max(1, math.Abs(want.Number)) {
			return false
		}
	}
	return true
}

// SetNumbers sends a newNumberVector that sets the members in values.
// The vector has every member of the property, and a member that
// values does not name keeps its value from the store, because a
// driver such as the telescope simulator ignores a coordinate that
// arrives without its pair.
func (c *Client) SetNumbers(device, property string, values map[string]float64) (Sent, error) {
	return c.send(device, property, NumberType, func(p *Property) ([]string, error) {
		var changed []string
		for name, value := range values {
			i := memberIndex(*p, name)
			if i < 0 {
				return nil, fmt.Errorf("%w: %s.%s has no member %q", ErrInvalid, device, property, name)
			}
			p.Members[i].Number = value
			changed = append(changed, name)
		}
		return changed, nil
	})
}

// SetTexts sends a newTextVector that sets the named members. Like
// SetNumbers, it sends every member of the property.
func (c *Client) SetTexts(device, property string, values map[string]string) (Sent, error) {
	return c.send(device, property, TextType, func(p *Property) ([]string, error) {
		var changed []string
		for name, value := range values {
			i := memberIndex(*p, name)
			if i < 0 {
				return nil, fmt.Errorf("%w: %s.%s has no member %q", ErrInvalid, device, property, name)
			}
			p.Members[i].Text = value
			changed = append(changed, name)
		}
		return changed, nil
	})
}

// SetSwitches sends a newSwitchVector that turns the members in values
// On or Off, and applies the property's rule. Under OneOfMany and
// AtMostOne, turning a member On turns every other member Off, like a
// radio button. Under AnyOfMany, a member that values does not name
// keeps its state. SetSwitches returns ErrInvalid when the result has
// no member On or more than one under OneOfMany, or more than one
// under AtMostOne. The vector has every member of the property.
func (c *Client) SetSwitches(device, property string, values map[string]bool) (Sent, error) {
	return c.send(device, property, SwitchType, func(p *Property) ([]string, error) {
		for name, on := range values {
			if memberIndex(*p, name) < 0 {
				return nil, fmt.Errorf("%w: %s.%s has no member %q", ErrInvalid, device, property, name)
			}
			if on && p.Rule != AnyOfMany {
				for i := range p.Members {
					p.Members[i].Switch = false
				}
			}
		}
		for name, on := range values {
			p.Members[memberIndex(*p, name)].Switch = on
		}
		on := len(p.On())
		if (p.Rule == OneOfMany && on != 1) || (p.Rule == AtMostOne && on > 1) {
			return nil, fmt.Errorf("%w: %s.%s is %s, and the change leaves %d members On", ErrInvalid, device, property, p.Rule, on)
		}
		// Under OneOfMany and AtMostOne, a change to one member also
		// changes the others, so the device's reply must have the value
		// of every member.
		var changed []string
		for _, m := range p.Members {
			changed = append(changed, m.Name)
		}
		return changed, nil
	})
}

// send checks a change against the property's definition, applies it
// to a copy of the property, and writes the copy as a new*Vector. The
// store keeps the old values until the device sends an update.
func (c *Client) send(device, property string, t Type, change func(*Property) ([]string, error)) (Sent, error) {
	c.mutex.Lock()
	if !c.store.connected {
		c.mutex.Unlock()
		return Sent{}, ErrNotConnected
	}
	e := c.store.entry(device, property)
	if e == nil {
		c.mutex.Unlock()
		return Sent{}, fmt.Errorf("%w: %s.%s", ErrNotDefined, device, property)
	}
	p := e.property.clone()
	sent := Sent{
		Device: device, Property: property,
		connection: c.store.connection, defined: e.defined, sequence: c.store.sequence,
	}
	c.mutex.Unlock()

	if p.Type != t {
		return Sent{}, fmt.Errorf("%w: %s.%s is a %s property, not %s", ErrInvalid, device, property, p.Type, t)
	}
	if p.Perm == ReadOnly {
		return Sent{}, fmt.Errorf("%w: %s.%s is read-only", ErrInvalid, device, property)
	}
	changed, err := change(&p)
	if err != nil {
		return Sent{}, err
	}
	sent.sent, sent.changed = p, changed
	return sent, c.write(newVector(p))
}

// AlertError is the error for a change that the device replied to with
// the state Alert. Property is the property as the device sent it. A
// driver usually sends the reason in a message after the update.
type AlertError struct {
	Property Property
}

func (e *AlertError) Error() string {
	return fmt.Sprintf("indi: %s.%s is Alert", e.Property.Device, e.Property.Name)
}

// Settle waits for the device's reply to a change. It returns the
// property when the state is Ok or Idle, an AlertError when the state
// is Alert, ErrDeleted when the device deleted the property,
// ErrDisconnected when the connection ended, or the error of ctx. The
// caller sets the time limit with ctx, because the property's own
// timeout is only the driver's estimate.
//
// INDI messages have no request identifier, so Settle reads the reply
// from the updates that arrive after the send. Some devices set Busy
// while they work and then Ok, like a dome that turns. Others set Ok at
// once with the new values, like the focuser simulator. So an update
// that is not Busy ends the wait only when a Busy update came after the
// send, or when the update has the values sent. Without that rule, the
// position that a dome sends on each poll, before it reads the change,
// would end the wait early.
func (c *Client) Settle(ctx context.Context, sent Sent) (Property, error) {
	return c.settle(ctx, sent, true)
}

// Answered is Settle for a reply with any values. A switch that starts
// an action, such as CONFIG_PROCESS, turns its members Off again when
// the action ends, so its reply never has the values sent. Answered
// ends on the first update after the send that is not Busy, so use it
// only for a property that the driver does not also send on a poll.
func (c *Client) Answered(ctx context.Context, sent Sent) (Property, error) {
	return c.settle(ctx, sent, false)
}

// Accepted waits for an update that has the values sent, in any state
// but Alert, and returns the errors that Settle returns. Use it for a
// property that stays Busy while the change is in effect, where Settle
// would wait until ctx ends. INDI's telescope base replies to TRACK_ON
// with TELESCOPE_TRACK_STATE Busy, and keeps it Busy while the mount
// tracks.
func (c *Client) Accepted(ctx context.Context, sent Sent) (Property, error) {
	var accepted Property
	err := c.waitFor(ctx, func(s *Store) (bool, error) {
		e, err := s.reply(sent)
		if e == nil || err != nil || !sent.matches(e.property) {
			return false, err
		}
		accepted = e.property.clone()
		return true, nil
	})
	return accepted, err
}

func (c *Client) settle(ctx context.Context, sent Sent, matchValues bool) (Property, error) {
	var settled Property
	err := c.waitFor(ctx, func(s *Store) (bool, error) {
		e, err := s.reply(sent)
		if e == nil || err != nil || e.property.State == Busy {
			return false, err
		}
		if matchValues && e.busy <= sent.sequence && !sent.matches(e.property) {
			return false, nil
		}
		settled = e.property.clone()
		return true, nil
	})
	return settled, err
}

// reply returns the entry of the property that a change was sent to,
// once the device has updated it after the send. It returns nil while
// no update has arrived, and an error when the connection ended, the
// device deleted the property, or the update is Alert.
func (s *Store) reply(sent Sent) (*entry, error) {
	if !s.connected || s.connection != sent.connection {
		return nil, fmt.Errorf("%w: waiting for %s.%s", ErrDisconnected, sent.Device, sent.Property)
	}
	e := s.entry(sent.Device, sent.Property)
	if e == nil || e.defined != sent.defined {
		return nil, fmt.Errorf("%w: waiting for %s.%s", ErrDeleted, sent.Device, sent.Property)
	}
	if e.updated <= sent.sequence {
		return nil, nil
	}
	if e.property.State == Alert {
		return nil, &AlertError{Property: e.property.clone()}
	}
	return e, nil
}

// BLOBMode is the enableBLOB mode: which messages the server sends
// this client for a device or a property.
type BLOBMode string

const (
	// BLOBNever sends no BLOBs. It is indiserver's default for every
	// client.
	BLOBNever BLOBMode = "Never"
	// BLOBAlso sends BLOBs with every other message.
	BLOBAlso BLOBMode = "Also"
	// BLOBOnly sends BLOBs and nothing else.
	BLOBOnly BLOBMode = "Only"
)

type blobRequest struct {
	device, property string
	mode             BLOBMode
}

// EnableBLOB sets the BLOB mode for one device, or for one property
// when property is not empty. Until a caller enables BLOBs, the client
// receives no frame data. EnableBLOB sends the request at once when the
// client has a connection, and the client sends it again after
// getProperties on each new connection.
//
// indiserver applies an enableBLOB that names no property to the
// client as a whole, whatever device it names
// (ClInfo::crackBLOBHandling). To receive the BLOBs of one device and
// no other, name the device's BLOB property.
func (c *Client) EnableBLOB(device, property string, mode BLOBMode) error {
	if mode != BLOBNever && mode != BLOBAlso && mode != BLOBOnly {
		return fmt.Errorf("%w: BLOB mode %q", ErrInvalid, mode)
	}
	r := blobRequest{device: device, property: property, mode: mode}
	c.mutex.Lock()
	replaced := false
	for i, existing := range c.blobs {
		if existing.device == device && existing.property == property {
			c.blobs[i], replaced = r, true
		}
	}
	if !replaced {
		c.blobs = append(c.blobs, r)
	}
	connected := c.conn != nil
	c.mutex.Unlock()
	if !connected {
		return nil
	}
	return c.write(enableBLOB(r))
}

// wantsBLOBs reports whether a caller enabled the BLOBs of a property.
// The client discards the data of every other BLOB. The caller holds
// c.mutex.
func (c *Client) wantsBLOBs(device, property string) bool {
	wanted := false
	for _, r := range c.blobs {
		if r.device == device && (r.property == "" || r.property == property) {
			wanted = r.mode != BLOBNever
			if r.property == property {
				return wanted
			}
		}
	}
	return wanted
}
