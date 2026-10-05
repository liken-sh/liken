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
