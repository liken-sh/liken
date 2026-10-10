package main

// Powering every adapter off before bluetoothd starts.
//
// When bluetoothd registers an adapter that is already powered, it
// reads the kernel's open connections and marks each of those devices
// Connected. It never attaches the input profile to such a link, so a
// remote whose link outlived the last bluetoothd is Connected with no
// HID device and sends no input. A bluetoothd that exits on TERM powers
// its adapters off first, and their links end with it. A bluetoothd
// that the kernel kills, or that crashes, leaves the adapters powered
// and their links up.
//
// So this program powers every adapter off at the kernel before it
// becomes bluetoothd. bluetoothd then registers each adapter unpowered,
// does not read the connections, and powers the adapter on itself,
// because main.conf sets AutoEnable. Each device that had a link
// connects again, the same as after any restart of the pod.
//
// The command goes over the Bluetooth management channel, the kernel
// interface that bluetoothd itself drives: an HCI socket bound to the
// control channel. bluetoothd manages every adapter that the channel
// lists, so this program powers off every one of them, not only the
// adapter that the pod claimed. The kernel accepts SET_POWERED only
// from a socket that a process with CAP_NET_ADMIN opened, and this
// container holds that capability for bluetoothd.
//
// A failure here does not stop the start. bluetoothd then starts on a
// powered adapter, which works for every device that has no link at
// that moment, and the log line names the kernel's answer.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// The packet format and the numbers of the Bluetooth Management API,
// from doc/mgmt-api.txt in BlueZ. Every field is little-endian. A
// command and an event both start with a header of three fields: the
// command or event code, the adapter index, and the length of the
// parameters that follow.
const (
	mgmtHeaderSize = 6

	// mgmtIndexNone addresses the channel itself, not one adapter. The
	// socket binds to it, and READ_INDEX_LIST is sent to it.
	mgmtIndexNone = 0xffff

	mgmtOpReadIndexList = 0x0003
	mgmtOpSetPowered    = 0x0005

	// The kernel answers each command with one of these two events. A
	// Command Complete carries the command's code, a status, and the
	// return parameters. A Command Status carries the code and a status
	// with no return parameters, and the kernel sends it for a refusal.
	mgmtEvCmdComplete = 0x0001
	mgmtEvCmdStatus   = 0x0002

	// mgmtTimeout bounds each read of the channel. A power-off ends
	// every link on the adapter before the kernel replies, so the bound
	// leaves room for an adapter with several links.
	mgmtTimeout = 10 * time.Second
)

// mgmtStatus is a status code that the kernel answered a command with.
type mgmtStatus uint8

// mgmtStatusNames are the names that doc/mgmt-api.txt gives the
// status codes.
var mgmtStatusNames = map[mgmtStatus]string{
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

func (s mgmtStatus) Error() string {
	if name, known := mgmtStatusNames[s]; known {
		return fmt.Sprintf("%s (0x%02x)", name, uint8(s))
	}
	return fmt.Sprintf("status 0x%02x", uint8(s))
}

// execFunc has the signature of syscall.Exec, so a test can see what
// would replace this process.
type execFunc func(path string, argv, env []string) error

// becomeBluetoothd powers every adapter off and then replaces this
// process with bluetoothd. open opens the management channel; the
// program passes openManagement, and a test passes a fake kernel.
//
// -n keeps bluetoothd in the foreground, because it daemonizes by
// default and a daemonized process would leave this container with
// nothing running in it.
func becomeBluetoothd(open func() (io.ReadWriteCloser, error), log io.Writer, exec execFunc) error {
	powerOffAdapters(open, log)
	return exec(bluetoothdPath, []string{bluetoothdPath, "-n"}, os.Environ())
}

// powerOffAdapters powers off each adapter that the management channel
// lists, and writes one log line for each result. It returns nothing,
// because no failure here stops the start.
func powerOffAdapters(open func() (io.ReadWriteCloser, error), log io.Writer) {
	channel, err := open()
	if err != nil {
		fmt.Fprintf(log, "start-bluetoothd: %v; bluetoothd starts each adapter as it is\n", err)
		return
	}
	defer channel.Close()
	indexes, err := readIndexList(channel)
	if err != nil {
		fmt.Fprintf(log, "start-bluetoothd: listing the adapters failed: %v; bluetoothd starts each adapter as it is\n", err)
		return
	}
	for _, index := range indexes {
		if _, err := mgmtCommand(channel, mgmtOpSetPowered, index, []byte{0}); err != nil {
			fmt.Fprintf(log, "start-bluetoothd: powering hci%d off failed: %v; bluetoothd starts it as it is\n", index, err)
			continue
		}
		fmt.Fprintf(log, "start-bluetoothd: powered hci%d off, so bluetoothd starts it with no connection\n", index)
	}
}

// readIndexList asks for the index of every adapter the kernel offers
// to the management channel. The return parameters are a count and
// then that many indexes.
func readIndexList(channel io.ReadWriter) ([]uint16, error) {
	reply, err := mgmtCommand(channel, mgmtOpReadIndexList, mgmtIndexNone, nil)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("the index list reply holds %d bytes", len(reply))
	}
	count := int(binary.LittleEndian.Uint16(reply))
	if len(reply) < 2+2*count {
		return nil, fmt.Errorf("the index list reply names %d adapters in %d bytes", count, len(reply))
	}
	indexes := make([]uint16, count)
	for i := range indexes {
		indexes[i] = binary.LittleEndian.Uint16(reply[2+2*i:])
	}
	return indexes, nil
}

// mgmtCommand sends one command and returns the return parameters of
// its reply.
//
// The kernel sends every management socket the events of every
// adapter, such as New Settings when a power-off lands, and those can
// arrive before the reply. So the read skips each packet that is not
// the reply to this command for this index.
func mgmtCommand(channel io.ReadWriter, opcode, index uint16, params []byte) ([]byte, error) {
	packet := binary.LittleEndian.AppendUint16(nil, opcode)
	packet = binary.LittleEndian.AppendUint16(packet, index)
	packet = binary.LittleEndian.AppendUint16(packet, uint16(len(params)))
	if _, err := channel.Write(append(packet, params...)); err != nil {
		return nil, err
	}
	// The largest reply here is the index list, at most a few dozen
	// bytes. A raw socket drops the part of a packet that does not fit,
	// so the buffer holds the largest packet the header can describe.
	buffer := make([]byte, mgmtHeaderSize+0xffff)
	for {
		n, err := channel.Read(buffer)
		if err != nil {
			return nil, err
		}
		event, from, reply, ok := parseEvent(buffer[:n])
		if !ok || from != index || len(reply) < 3 || binary.LittleEndian.Uint16(reply) != opcode {
			continue
		}
		switch status := mgmtStatus(reply[2]); {
		case event == mgmtEvCmdComplete && status == 0:
			return reply[3:], nil
		case event == mgmtEvCmdComplete || (event == mgmtEvCmdStatus && status != 0):
			return nil, fmt.Errorf("the kernel answered %w", status)
		}
	}
}

// parseEvent reads the header of one packet from the channel. ok is
// false for a packet shorter than its header states.
func parseEvent(packet []byte) (event, index uint16, params []byte, ok bool) {
	if len(packet) < mgmtHeaderSize {
		return 0, 0, nil, false
	}
	length := int(binary.LittleEndian.Uint16(packet[4:]))
	if len(packet) < mgmtHeaderSize+length {
		return 0, 0, nil, false
	}
	event = binary.LittleEndian.Uint16(packet)
	index = binary.LittleEndian.Uint16(packet[2:])
	return event, index, packet[mgmtHeaderSize : mgmtHeaderSize+length], true
}

// managementSocket is the kernel's management channel. Each read
// returns one whole packet.
type managementSocket struct {
	fd int
}

// openManagement opens an HCI socket and binds it to the control
// channel. The kernel's SO_RCVTIMEO ends a read that waits longer than
// mgmtTimeout, so a kernel that does not answer delays the start by
// that long and no more.
func openManagement() (io.ReadWriteCloser, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return nil, fmt.Errorf("opening an HCI socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrHCI{Dev: mgmtIndexNone, Channel: unix.HCI_CHANNEL_CONTROL}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("binding the HCI socket to the management channel: %w", err)
	}
	timeout := unix.NsecToTimeval(mgmtTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("setting a read timeout on the management channel: %w", err)
	}
	return &managementSocket{fd: fd}, nil
}

func (s *managementSocket) Read(buffer []byte) (int, error) {
	n, err := unix.Read(s.fd, buffer)
	if errors.Is(err, unix.EAGAIN) {
		return 0, fmt.Errorf("the kernel sent no reply within %s", mgmtTimeout)
	}
	if err != nil {
		return 0, fmt.Errorf("reading the management channel: %w", err)
	}
	return n, nil
}

func (s *managementSocket) Write(packet []byte) (int, error) {
	n, err := unix.Write(s.fd, packet)
	if err != nil {
		return 0, fmt.Errorf("writing to the management channel: %w", err)
	}
	return n, nil
}

func (s *managementSocket) Close() error {
	return unix.Close(s.fd)
}
