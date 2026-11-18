package indi

import "fmt"

// Relay sends p as a set*Vector with its state and every member, as if
// the device it names had reported it. A driver can snoop, through
// IDSnoopDevice, only the devices on its own server, so Relay is how
// the operator reports a device that runs on another server. When a
// client sends a set*Vector for a device the server does not run,
// indiserver forwards it only to the drivers that snoop that device and
// property (ClInfo::onMessage in indiserver/ClInfo.cpp). indiserver
// handles it this way for a server chained upstream, which connects as
// a client. The server records nothing, its other clients receive
// nothing, and the client's store does not change.
//
// Relay returns ErrInvalid for a Light or BLOB property, which a client
// cannot send.
func (c *Client) Relay(p Property) error {
	if p.Type != SwitchType && p.Type != NumberType && p.Type != TextType {
		return fmt.Errorf("%w: %s.%s is a %s property, which a client cannot relay", ErrInvalid, p.Device, p.Name, p.Type)
	}
	return c.write(setVector(p))
}
