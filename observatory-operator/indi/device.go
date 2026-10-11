package indi

import (
	"context"
	"fmt"
	"slices"
)

// connection is the standard property that connects a driver to its
// hardware. Every INDI driver defines it, with the members CONNECT and
// DISCONNECT under the rule OneOfMany.
const connection = "CONNECTION"

// ConnectDevice connects a device to its hardware through CONNECTION
// and waits until the driver reports the result. It first waits for the
// device to define CONNECTION, so a caller can call it as soon as the
// device's pod starts. When CONNECT is On and the state is Ok, it sends
// nothing. A driver that cannot reach its hardware answers with Alert,
// which returns an AlertError. ctx bounds the whole wait.
func (c *Client) ConnectDevice(ctx context.Context, device string) error {
	return c.setConnection(ctx, device, "CONNECT")
}

// DisconnectDevice disconnects a device from its hardware, the same
// way ConnectDevice connects it. The driver keeps running, and it
// deletes the properties that it defined on connect.
func (c *Client) DisconnectDevice(ctx context.Context, device string) error {
	return c.setConnection(ctx, device, "DISCONNECT")
}

func (c *Client) setConnection(ctx context.Context, device, member string) error {
	if err := c.WaitFor(ctx, func(s *Store) bool {
		_, ok := s.Property(device, connection)
		return ok
	}); err != nil {
		return fmt.Errorf("waiting for %s to define %s: %w", device, connection, err)
	}
	if p, _ := c.Property(device, connection); p.State == Ok && slices.Equal(p.On(), []string{member}) {
		return nil
	}
	sent, err := c.SetSwitches(device, connection, map[string]bool{member: true})
	if err != nil {
		return err
	}
	p, err := c.Settle(ctx, sent)
	if err != nil {
		return err
	}
	if on := p.On(); !slices.Equal(on, []string{member}) {
		return fmt.Errorf("indi: %s answered %s with %v On, not %s", device, connection, on, member)
	}
	return nil
}

// Sync waits until the client holds everything that a device's driver
// wrote before the call, such as the properties that a driver defines
// just after it reports CONNECTION, with no timer. It asks for the
// device's properties with getProperties. indiserver hands a
// getProperties that names a device to that device's driver
// (indiserver/ClInfo.cpp), and the driver answers it by defining its
// properties again, CONNECTION among them (DefaultDevice::ISGetProperties
// in libs/indibase/defaultdevice.cpp). A driver answers in the order it
// reads, so its answer comes after everything it wrote before, and the
// next report of CONNECTION shows that the client has applied all of
// it. It fails when the connection ends or the device deletes
// CONNECTION, and ctx bounds the wait.
func (c *Client) Sync(ctx context.Context, device string) error {
	c.mutex.Lock()
	opened, sequence := c.store.connection, c.store.sequence
	e := c.store.entry(device, connection)
	defined := uint64(0)
	if e != nil {
		defined = e.defined
	}
	c.mutex.Unlock()
	if e == nil {
		return fmt.Errorf("%w: %s.%s", ErrNotDefined, device, connection)
	}
	if err := c.write(getDeviceProperties(device)); err != nil {
		return err
	}
	return c.waitFor(ctx, func(s *Store) (bool, error) {
		if !s.connected || s.connection != opened {
			return false, fmt.Errorf("%w: waiting for %s", ErrDisconnected, device)
		}
		e := s.entry(device, connection)
		if e == nil || e.defined != defined {
			return false, fmt.Errorf("%w: waiting for %s.%s", ErrDeleted, device, connection)
		}
		return e.updated > sequence, nil
	})
}
