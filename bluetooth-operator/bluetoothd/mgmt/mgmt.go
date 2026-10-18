// Package mgmt speaks the Bluetooth Management API, the kernel
// interface that bluetoothd itself drives: an HCI socket bound to the
// control channel. Two programs in the bluetoothd image send commands
// on it besides bluetoothd. start-bluetoothd powers every adapter off
// before bluetoothd starts, and start-rescan adds a device again when
// the kernel leaves its passive scan off.
//
// The kernel accepts a command that changes state only from a socket
// that a process with CAP_NET_ADMIN opened, so each container that
// runs one of these programs holds that capability.
package mgmt

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// The packet format and the numbers of the Bluetooth Management API,
// from doc/mgmt-api.txt in BlueZ. Every field is little-endian. A
// command and an event both start with a header of three fields: the
// command or event code, the adapter index, and the length of the
// parameters that follow.
const (
	HeaderSize = 6

	// IndexNone addresses the channel itself, not one adapter. The
	// socket binds to it, and READ_INDEX_LIST is sent to it.
	IndexNone = 0xffff

	OpReadIndexList = 0x0003
	OpSetPowered    = 0x0005
	OpAddDevice     = 0x0033
	OpRemoveDevice  = 0x0034

	// The kernel answers each command with one of these two events. A
	// Command Complete carries the command's code, a status, and the
	// return parameters. A Command Status carries the code and a status
	// with no return parameters, and the kernel sends it for a refusal.
	EvCmdComplete = 0x0001
	EvCmdStatus   = 0x0002

	// Timeout bounds each read of the channel. A power-off ends every
	// link on the adapter before the kernel replies, so the bound
	// leaves room for an adapter with several links.
	Timeout = 10 * time.Second
)

// The address types of the Management API. A device's address and its
// type together name it, because a Low Energy device's random address
// can repeat a public one.
const (
	AddressBREDR    = 0x00
	AddressLEPublic = 0x01
	AddressLERandom = 0x02
)

// ActionAutoConnect is the Add Device action that makes the kernel
// connect to a Low Energy device each time it advertises. bluetoothd
// adds every bonded device whose profile reconnects on its own, such
// as a HID remote, with this action (adapter_auto_connect_add in
// src/adapter.c).
const ActionAutoConnect = 0x02

// Status is a status code that the kernel answered a command with.
type Status uint8

// statusNames are the names that doc/mgmt-api.txt gives the status
// codes.
var statusNames = map[Status]string{
	0x01: "Unknown Command",
	0x02: "Not Connected",
	0x03: "Failed",
	0x04: "Connect Failed",
	0x05: "Authentication Failed",
	0x06: "Not Paired",
	0x07: "No Resources",
	0x08: "Timeout",
	0x09: "Already Connected",
	0x0a: "Busy",
	0x0b: "Rejected",
	0x0c: "Not Supported",
	0x0d: "Invalid Parameters",
	0x0e: "Disconnected",
	0x0f: "Not Powered",
	0x10: "Cancelled",
	0x11: "Invalid Index",
	0x12: "RFKilled",
	0x13: "Already Paired",
	0x14: "Permission Denied",
}

func (s Status) Error() string {
	if name, known := statusNames[s]; known {
		return fmt.Sprintf("%s (0x%02x)", name, uint8(s))
	}
	return fmt.Sprintf("status 0x%02x", uint8(s))
}

// Command sends one command and returns the return parameters of its
// reply.
//
// The kernel sends every management socket the events of every
// adapter, such as New Settings when a power-off lands, and those can
// arrive before the reply. So the read skips each packet that is not
// the reply to this command for this index.
func Command(channel io.ReadWriter, opcode, index uint16, params []byte) ([]byte, error) {
	packet := binary.LittleEndian.AppendUint16(nil, opcode)
	packet = binary.LittleEndian.AppendUint16(packet, index)
	packet = binary.LittleEndian.AppendUint16(packet, uint16(len(params)))
	if _, err := channel.Write(append(packet, params...)); err != nil {
		return nil, err
	}
	// The largest reply here is the index list, at most a few dozen
	// bytes. A raw socket drops the part of a packet that does not fit,
	// so the buffer holds the largest packet the header can describe.
	buffer := make([]byte, HeaderSize+0xffff)
	for {
		n, err := channel.Read(buffer)
		if err != nil {
			return nil, err
		}
		event, from, reply, ok := ParseEvent(buffer[:n])
		if !ok || from != index || len(reply) < 3 || binary.LittleEndian.Uint16(reply) != opcode {
			continue
		}
		switch status := Status(reply[2]); {
		case event == EvCmdComplete && status == 0:
			return reply[3:], nil
		case event == EvCmdComplete || (event == EvCmdStatus && status != 0):
			return nil, fmt.Errorf("the kernel answered %w", status)
		}
	}
}

// AddDevice asks the kernel to treat one device the way action says,
// with the same command bluetoothd sends. The address is in the order
// that a person reads it, most significant byte first.
func AddDevice(channel io.ReadWriter, index uint16, address [6]byte, addressType, action uint8) error {
	params := make([]byte, 0, 8)
	for i := len(address) - 1; i >= 0; i-- {
		params = append(params, address[i])
	}
	params = append(params, addressType, action)
	_, err := Command(channel, OpAddDevice, index, params)
	return err
}

// ParseEvent reads the header of one packet from the channel. ok is
// false for a packet shorter than its header states.
func ParseEvent(packet []byte) (event, index uint16, params []byte, ok bool) {
	if len(packet) < HeaderSize {
		return 0, 0, nil, false
	}
	length := int(binary.LittleEndian.Uint16(packet[4:]))
	if len(packet) < HeaderSize+length {
		return 0, 0, nil, false
	}
	event = binary.LittleEndian.Uint16(packet)
	index = binary.LittleEndian.Uint16(packet[2:])
	return event, index, packet[HeaderSize : HeaderSize+length], true
}
