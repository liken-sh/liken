package indi

import (
	"context"
	"fmt"
	"math"
)

// Sent identifies one change that the client sent, for Settle.
type Sent struct {
	Device   string
	Property string
	// connection, defined, and sequence record the store as it was at
	// the send: the connection, the definition of the property, and
	// the last change applied.
	connection uint64
	defined    uint64
	sequence   uint64
	// sent is the property as the client sent it, and changed names
	// the members whose values the change sets.
	sent    Property
	changed []string
}

// carries reports whether a property holds the values that the change
// set.
func (s Sent) carries(p Property) bool {
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

// SetNumbers sends a newNumberVector that gives the named members new
// values. It sends every member of the property, and the members it
// does not name keep the values in the store, because a driver such as
// the telescope simulator ignores a coordinate that arrives without
// its pair.
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

// SetTexts sends a newTextVector that gives the named members new
// values. It sends every member, as SetNumbers does.
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

// SetSwitches sends a newSwitchVector that turns the named members On
// or Off, and follows the property's rule. Under OneOfMany and
// AtMostOne, a member turned On turns every other member Off, as a
// radio button does. The result must leave exactly one member On under
// OneOfMany, and at most one under AtMostOne, or SetSwitches refuses
// it. Under AnyOfMany the members it does not name keep their state.
// It sends every member.
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
		// Under OneOfMany and AtMostOne, a change to one member sets the
		// others, so the device's answer must carry every member.
		var changed []string
		for _, m := range p.Members {
			changed = append(changed, m.Name)
		}
		return changed, nil
	})
}

// send checks a change against the property's definition, applies it
// to a copy of the property, and writes the copy as a new*Vector. The
// store keeps its values until the device reports the change.
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

// AlertError is the error of a change that the device answered with
// the state Alert. Property is the property as the device reported it.
// A driver usually sends its reason as a Message event, after the
// update.
type AlertError struct {
	Property Property
}

func (e *AlertError) Error() string {
	return fmt.Sprintf("indi: %s.%s is Alert", e.Property.Device, e.Property.Name)
}

// Settle waits until the device answers a change that the client
// sent. It returns the property when the state is Ok or Idle, an
// AlertError when the state is Alert, ErrDeleted when the device
// deleted the property, ErrDisconnected when the connection ended, or
// the error of ctx. The property's own timeout is the driver's
// estimate, so the caller sets the limit with ctx.
//
// INDI carries no identifier for a request, so Settle reads the
// device's answer from the updates after the send. A device answers in
// one of two ways: it sets Busy while it works and then Ok, as a dome
// that turns does, or it sets Ok at once with the new values, as the
// focuser simulator does. So an update counts as the answer when it is
// not Busy, and either a Busy update came after the send or the update
// carries the values sent. Without that rule, an update that the
// device sent before it read the change, such as the dome's position
// on its poll, would end the wait early. Alert ends the wait at once.
func (c *Client) Settle(ctx context.Context, sent Sent) (Property, error) {
	var settled Property
	err := c.waitFor(ctx, func(s *Store) (bool, error) {
		if !s.connected || s.connection != sent.connection {
			return false, fmt.Errorf("%w: waiting for %s.%s", ErrDisconnected, sent.Device, sent.Property)
		}
		e := s.entry(sent.Device, sent.Property)
		if e == nil || e.defined != sent.defined {
			return false, fmt.Errorf("%w: waiting for %s.%s", ErrDeleted, sent.Device, sent.Property)
		}
		if e.updated <= sent.sequence || e.property.State == Busy {
			return false, nil
		}
		if e.property.State == Alert {
			return false, &AlertError{Property: e.property.clone()}
		}
		if e.busy <= sent.sequence && !sent.carries(e.property) {
			return false, nil
		}
		settled = e.property.clone()
		return true, nil
	})
	return settled, err
}

// BLOBMode says which messages a server sends this client for a
// device or a property.
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

// EnableBLOB asks the server for BLOBs from one device, or from one
// property when property is not empty. The client sends no enableBLOB
// unless a caller asks, so by default it receives no frame data. The
// client sends the request now when it has a connection, and again
// after getProperties on each new connection.
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

// wantsBLOBs reports whether a caller asked for the BLOBs of a
// property. The client keeps the data of no other BLOB. The caller
// holds c.mutex.
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
