// Package mgmt sends commands to the kernel over the Bluetooth
// Management API, which is an HCI socket bound to the control channel.
// bluetoothd uses the same interface. In the bluetoothd image,
// start-bluetoothd uses it to power off every adapter before bluetoothd
// starts, and start-rescan uses it to add a device again when the
// kernel has left its passive scan off.
//
// The kernel accepts a command that changes state only from a socket
// that a process with CAP_NET_ADMIN opened, so each container that
// runs one of these programs needs that capability.
package mgmt

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// The packet format and the numbers of the Bluetooth Management API,
// from doc/mgmt-api.txt in BlueZ. Every field is little-endian. A
// command and an event both start with a 6-byte header: the command or
// event code, the adapter index, and the length of the parameters that
// follow.
const (
	HeaderSize = 6

	// IndexNone is the index for commands that apply to no adapter.
	// The socket binds to it, and READ_INDEX_LIST is sent to it.
	IndexNone = 0xffff

	OpReadIndexList = 0x0003
	OpSetPowered    = 0x0005
	OpAddDevice     = 0x0033
	OpRemoveDevice  = 0x0034

	// The kernel replies to each command with one of these two events.
	// Command Complete has the command's code, a status, and the return
	// parameters. Command Status has the code and a status but no
	// return parameters, and the kernel sends it when it refuses a
	// command.
	EvCmdComplete = 0x0001
	EvCmdStatus   = 0x0002

	// Timeout is how long a read of the channel may wait. The kernel
	// ends every link on an adapter before it replies to a power-off,
	// so the timeout allows for an adapter with several links.
	Timeout = 10 * time.Second
)

// The address types of the Management API. A device is identified by
// its address and its type together, because a Low Energy device's
// random address can equal another device's public address.
const (
	AddressBREDR    = 0x00
	AddressLEPublic = 0x01
	AddressLERandom = 0x02
)

// ActionAutoConnect is the Add Device action that makes the kernel
// connect to a Low Energy device each time it advertises. bluetoothd
// uses it for every bonded device whose profile reconnects on its own,
// such as a HID remote (adapter_auto_connect_add in src/adapter.c).
const ActionAutoConnect = 0x02

// ActionAllowIncoming is the Add Device action that makes the kernel
// connect to a Low Energy device only when the device sends a directed
// advertisement to this adapter.
const ActionAllowIncoming = 0x01

// Status is a status code in the kernel's reply to a command.
type Status uint8

// statusNames are the names of the status codes in doc/mgmt-api.txt.
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
// The kernel sends the events of every adapter to every management
// socket, such as New Settings when a power-off takes effect, and they
// can arrive before the reply. So Command skips each packet that is not
// the reply to this opcode on this index.
func Command(channel io.ReadWriter, opcode, index uint16, params []byte) ([]byte, error) {
	packet := binary.LittleEndian.AppendUint16(nil, opcode)
	packet = binary.LittleEndian.AppendUint16(packet, index)
	packet = binary.LittleEndian.AppendUint16(packet, uint16(len(params)))
	if _, err := channel.Write(append(packet, params...)); err != nil {
		return nil, err
	}
	// A read from a raw socket drops the part of a packet that does not
	// fit in the buffer, so the buffer is the size of the largest packet
	// the header's 16-bit length can describe.
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
			return nil, fmt.Errorf("the kernel replied with status %w", status)
		}
	}
}

// AddDevice sends Add Device, which sets the action that the kernel
// takes for one device, as bluetoothd does. address is most significant
// byte first, as people write it, and AddDevice reverses it into the
// kernel's order.
func AddDevice(channel io.ReadWriter, index uint16, address [6]byte, addressType, action uint8) error {
	params := make([]byte, 0, 8)
	for i := len(address) - 1; i >= 0; i-- {
		params = append(params, address[i])
	}
	params = append(params, addressType, action)
	_, err := Command(channel, OpAddDevice, index, params)
	return err
}

// ParseEvent splits one packet from the channel into its event code,
// adapter index, and parameters. ok is false when the packet is shorter
// than its header says.
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
