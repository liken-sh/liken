package indi

import "fmt"

// A driver can snoop only the devices on its own server, through
// IDSnoopDevice. indiserver still delivers a snooped report from one
// other source: a set*Vector that a client sends goes to each driver
// that snoops its device and property, and to no client
// (ClInfo::onMessage in indiserver/ClInfo.cpp). indiserver opens that
// path for a server chained upstream, which connects as a client. Relay
// uses it to report a device that runs on another server.

// Relay sends a property as a set*Vector, with its state and every
// member, as if the device it names reported it. The server holds no
// such device, so it records nothing and echoes nothing to its other
// clients, and the client's store does not change. Only a driver that
// snoops the device and the property receives it. A Light or BLOB
// property cannot be relayed.
func (c *Client) Relay(p Property) error {
	if p.Type != SwitchType && p.Type != NumberType && p.Type != TextType {
		return fmt.Errorf("%w: %s.%s is a %s property, which a client cannot relay", ErrInvalid, p.Device, p.Name, p.Type)
	}
	return c.write(setVector(p))
}
