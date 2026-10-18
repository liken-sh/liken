package mgmt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

// channel is a management socket with scripted replies. Each Read
// returns one whole packet, as a read of the real socket does, and each
// Write is kept so a test reads back what the kernel received.
type channel struct {
	replies [][]byte
	written [][]byte
}

func (c *channel) Write(packet []byte) (int, error) {
	c.written = append(c.written, bytes.Clone(packet))
	return len(packet), nil
}

func (c *channel) Read(buffer []byte) (int, error) {
	if len(c.replies) == 0 {
		return 0, errors.New("the kernel sent no reply within 10s")
	}
	packet := c.replies[0]
	c.replies = c.replies[1:]
	return copy(buffer, packet), nil
}

// event builds one packet the kernel sends: the header, then params.
func event(code, index uint16, params ...byte) []byte {
	packet := binary.LittleEndian.AppendUint16(nil, code)
	packet = binary.LittleEndian.AppendUint16(packet, index)
	packet = binary.LittleEndian.AppendUint16(packet, uint16(len(params)))
	return append(packet, params...)
}

// reply builds the kernel's answer to opcode on index.
func reply(code, opcode, index uint16, status byte, params ...byte) []byte {
	body := binary.LittleEndian.AppendUint16(nil, opcode)
	return event(code, index, append(append(body, status), params...)...)
}

// A command returns its own reply's parameters. The kernel sends other
// sockets' events and other adapters' replies on the same channel, and
// they come first.
func TestACommandReadsPastOtherPackets(t *testing.T) {
	c := &channel{replies: [][]byte{
		event(0x0006, 0, 0, 0, 0, 0),
		reply(EvCmdComplete, OpAddDevice, 1, 0),
		{0x01, 0x00},
		reply(EvCmdComplete, OpReadIndexList, IndexNone, 0, 1, 0, 0, 0),
	}}
	got, err := Command(c, OpReadIndexList, IndexNone, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 0, 0, 0}; !bytes.Equal(got, want) {
		t.Fatalf("the reply holds % x, want % x", got, want)
	}
}

// A refusal names the kernel's status, so a log line says why.
func TestACommandReturnsTheKernelsRefusal(t *testing.T) {
	cases := []struct {
		name  string
		reply []byte
		want  string
	}{
		{"a command status", reply(EvCmdStatus, OpSetPowered, 0, 0x14), "the kernel answered Permission Denied (0x14)"},
		{"a command complete", reply(EvCmdComplete, OpSetPowered, 0, 0x0d), "the kernel answered Invalid Parameters (0x0d)"},
		{"no reply", nil, "the kernel sent no reply within 10s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := &channel{}
			if c.reply != nil {
				ch.replies = [][]byte{c.reply}
			}
			_, err := Command(ch, OpSetPowered, 0, []byte{0})
			if err == nil || err.Error() != c.want {
				t.Fatalf("the command failed with %v, want %q", err, c.want)
			}
		})
	}
}

// Add Device carries the address with its least significant byte
// first, the order of the kernel's bdaddr_t, then the type and the
// action.
func TestAddDeviceSendsTheAddressTheWayTheKernelReadsIt(t *testing.T) {
	c := &channel{replies: [][]byte{reply(EvCmdComplete, OpAddDevice, 0, 0, 0x47, 0x91, 0x24, 0x4b, 0xab, 0x58, AddressLEPublic)}}
	address := [6]byte{0x58, 0xab, 0x4b, 0x24, 0x91, 0x47}
	if err := AddDevice(c, 0, address, AddressLEPublic, ActionAutoConnect); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{event(OpAddDevice, 0, 0x47, 0x91, 0x24, 0x4b, 0xab, 0x58, AddressLEPublic, ActionAutoConnect)}
	if len(c.written) != 1 || !bytes.Equal(c.written[0], want[0]) {
		t.Fatalf("the kernel received % x, want % x", c.written, want)
	}
}

// A packet shorter than its header states is not an event.
func TestAShortPacketIsNoEvent(t *testing.T) {
	cases := [][]byte{
		{0x01, 0x00, 0x00},
		event(EvCmdComplete, 0, 1, 2, 3)[:8],
	}
	for i, packet := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, _, _, ok := ParseEvent(packet); ok {
				t.Fatalf("% x parsed as an event", packet)
			}
		})
	}
}

// The status names come from the Management API's list, and a status
// outside it still prints its number.
func TestStatusNames(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{0x0a, "Busy (0x0a)"},
		{0x12, "RFKilled (0x12)"},
		{0x7f, "status 0x7f"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			if got := c.status.Error(); got != c.want {
				t.Fatalf("%d reads %q, want %q", c.status, got, c.want)
			}
		})
	}
}
