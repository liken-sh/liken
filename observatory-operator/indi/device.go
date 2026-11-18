package indi

import (
	"context"
	"fmt"
	"slices"
)

// connection is the standard INDI property that connects a driver to
// its hardware. Every INDI driver defines it as a switch vector with
// the members CONNECT and DISCONNECT and the rule OneOfMany.
const connection = "CONNECTION"

// ConnectDevice connects a device to its hardware through CONNECTION
// and waits for the driver's result. It first waits for the device to
// define CONNECTION, so a caller can call it as soon as the device's
// pod starts. When CONNECT is already On and the state is Ok, it sends
// nothing. When the driver cannot reach its hardware and reports
// Alert, it returns an AlertError. ctx bounds the whole wait.
func (c *Client) ConnectDevice(ctx context.Context, device string) error {
	return c.setConnection(ctx, device, "CONNECT")
}

// DisconnectDevice disconnects a device from its hardware in the same
// way. The driver keeps running and deletes the properties that it
// defined when it connected.
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
		return fmt.Errorf("%s reports %v On in %s, want only %s", device, on, connection, member)
	}
	return nil
}

// Sync waits until the client has received everything that a device's
// driver wrote before the call, such as the properties that a driver
// defines just after it reports CONNECTION. It waits on the driver's
// reply, not on a timer.
//
// Sync sends getProperties for the device. indiserver passes a
// getProperties that names a device to that device's driver
// (indiserver/ClInfo.cpp), and the driver answers by defining its
// properties again, CONNECTION among them
// (DefaultDevice::ISGetProperties in libs/indibase/defaultdevice.cpp).
// The driver handles requests in the order it reads them, so its reply
// comes after everything it wrote earlier, and the next definition of
// CONNECTION that the client receives marks the end of it.
//
// An update of CONNECTION does not count as the reply. A client such as
// KStars can connect the device during the wait, and the driver sends
// the Busy and Ok updates before the properties that the connection
// defines.
//
// Each Sync makes the driver define every property again, and
// indiserver sends that burst to every client, so call Sync only when
// you need it. Sync fails when the connection ends or the device
// deletes CONNECTION. ctx bounds the wait.
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
		return e.redefined > sequence, nil
	})
}
