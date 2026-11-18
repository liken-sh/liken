package main

// A DDC/CI client for monitor controls such as brightness, power, and
// input source.
//
// DDC/CI is the VESA protocol that a monitor speaks over two wires of
// its display cable, which form an I2C bus. The kernel exposes each
// connector's bus as a /dev/i2c-N node, so a process that opens the
// node can read and set a control with a few bytes. No compositor,
// library, or helper binary is needed.
//
// The operator implements the protocol itself instead of running
// ddcutil, because its image holds only its own static binary, and a
// Get is five bytes out and eleven bytes back.
//
// A monitor's DDC receiver is a slow microcontroller, and the
// specification sets how long the host must wait between messages. A
// host that reads too early gets a garbled reply from a working
// monitor.
//
// Section numbers in this file refer to VESA DDC/CI 1.1.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The specification writes DDC/CI addresses in 8-bit form, and Linux
// uses the 7-bit form. The display is at 7-bit address 0x37. On the
// wire the address is shifted left one bit and followed by a direction
// bit, so 0x6e is a write to the display and 0x6f is a read from it.
// The host has its own virtual pair, 0x50 and 0x51, where no hardware
// responds, and a request uses 0x51 as its source address (sections
// 2.2 and 2.3). A reply starts with 0x6e, the display's write address,
// as its source address.
const (
	ddcDisplayAddress = 0x37
	ddcWriteAddress   = ddcDisplayAddress << 1
	ddcHostAddress    = 0x51
	ddcVirtualHost    = 0x50
	ddcReplySource    = 0x6e
)

// Opcodes of the VCP (Virtual Control Panel) messages. VCP is the set
// of numbered controls, such as 0x10 for brightness, that a host reads
// and writes over DDC/CI.
const (
	vcpGetRequest = 0x01
	vcpGetReply   = 0x02
	vcpSetRequest = 0x03
)

// The length byte of a DDC/CI message always has its high bit set,
// and its low seven bits count the data bytes that follow. The count
// excludes the source address before the length byte and the checksum
// after the data.
const (
	ddcLengthFlag = 0x80
	ddcLengthMask = 0x7f
)

// The result code in a Get reply. Zero means the display returned the
// control's value. One means the display has no such control. That is
// a valid reply, not a bus failure.
const (
	vcpResultOK          = 0x00
	vcpResultUnsupported = 0x01
)

// A Get VCP Feature reply has eight data bytes, and eleven with the
// source address and the checksum. The specification's frame has a
// twelfth byte at the front, the 0x6f read address, but the kernel
// consumes it, so it is not in the buffer that Read fills.
const (
	getReplyDataLength = 8
	getReplyLength     = getReplyDataLength + 3
)

// The delays come from the specification. A display needs 40ms to
// decode a request and prepare its reply before the host reads it
// (sections 4.3 and 6.6). After a Set, it needs 50ms before it accepts
// the next message (section 4.4). The host waits 40ms between a failed
// attempt and its retry (section 5).
//
// ddcReplyDelay is the wait before the first read, and each retry
// doubles it: 40ms, 80ms, then 160ms. Some monitors reply later than
// 40ms, and with a fixed 40ms wait the probe reports such a monitor as
// one with no DDC/CI. A monitor that replies on time still gets its
// reply read after 40ms.
const (
	ddcReplyDelay = 40 * time.Millisecond
	ddcSetDelay   = 50 * time.Millisecond
	ddcRetryDelay = 40 * time.Millisecond
)

// ddcBusGap is how long a caller waits after another caller's
// exchange on the same bus ends. It is the 50ms that section 4.4
// requires after a Set.
const ddcBusGap = 50 * time.Millisecond

// A monitor that is busy with its own menu or has just powered on
// drops one message and replies to the next, so one failed read does not
// show that the monitor lacks DDC/CI. Section 5 asks the host for
// three retries, which is four attempts. The operator stops at three,
// because the probe reads every connector on the card, and on a
// connector with no monitor each attempt fails and adds its delay.
const ddcGetAttempts = 3

// I2C_SLAVE, from <linux/i2c-dev.h>. The ioctl binds a slave address
// to an open descriptor, and the kernel sends that address before
// every later read and write on it. So no packet in this file contains
// the display's address.
const i2cSlaveRequest = 0x0703

// Callers handle each of these failures differently:
//
//   - ErrNoAnswer: nothing responded at the address, so the connector
//     has no monitor or the monitor has no DDC/CI.
//   - ErrGarbledReply: something responded with bytes that are not a
//     valid reply. A retry may get a clean one.
//   - ErrUnsupportedVCP: the display replied that it has no such
//     control. Asking again gives the same reply.
var (
	ErrNoAnswer       = errors.New("no device replied on the DDC/CI address")
	ErrGarbledReply   = errors.New("the bytes that the display returned are not a valid DDC/CI reply")
	ErrUnsupportedVCP = errors.New("the display does not support the VCP code")
)

// i2cBus is an interface so tests can check the packets and the
// timing without a monitor. Write is one I2C write transaction and
// Read is one I2C read transaction, both to the address that was bound
// when the node was opened.
type i2cBus interface {
	Write(request []byte) error
	Read(reply []byte) error
}

// A DDC runs the protocol over one bus. sleep is a field so tests can
// record the delays without waiting.
type DDC struct {
	bus   i2cBus
	sleep func(time.Duration)
}

// newDDC wraps a bus that is already bound to the display's address.
func newDDC(bus i2cBus) *DDC {
	return &DDC{bus: bus, sleep: time.Sleep}
}

// GetVCP reads one control and returns its current value and its
// maximum value, both 16-bit. A caller finds out whether a control
// exists by reading it and checking for ErrUnsupportedVCP.
func (d *DDC) GetVCP(code byte) (uint16, uint16, error) {
	var err error
	wait := ddcReplyDelay
	for attempt := 0; attempt < ddcGetAttempts; attempt++ {
		if attempt > 0 {
			d.sleep(ddcRetryDelay)
			wait *= 2
		}
		var current, max uint16
		current, max, err = d.getOnce(code, wait)
		if err == nil {
			return current, max, nil
		}
		if errors.Is(err, ErrUnsupportedVCP) {
			// The display gives the same result on every attempt.
			break
		}
	}
	return 0, 0, fmt.Errorf("reading VCP code %#04x: %w", code, err)
}

// getOnce writes the request, waits, and reads a fixed-length reply.
// A fixed length is safe because section 6.5 has the display pad a
// shorter reply instead of holding the bus.
func (d *DDC) getOnce(code byte, wait time.Duration) (uint16, uint16, error) {
	if err := d.bus.Write(getRequest(code)); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrNoAnswer, err)
	}
	d.sleep(wait)
	reply := make([]byte, getReplyLength)
	if err := d.bus.Read(reply); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrNoAnswer, err)
	}
	return parseGetReply(reply, code)
}

// SetVCP writes one control. The display sends no reply to a Set, so
// a caller that must confirm the change reads it back with GetVCP, as
// writeControl does. SetVCP waits ddcSetDelay before it returns,
// because a Get sent sooner reads the value that the Set replaced.
func (d *DDC) SetVCP(code byte, value uint16) error {
	if err := d.bus.Write(setRequest(code, value)); err != nil {
		return fmt.Errorf("setting VCP code %#04x: %w: %w", code, ErrNoAnswer, err)
	}
	d.sleep(ddcSetDelay)
	return nil
}

// getRequest builds a Get VCP Feature request: source, length,
// opcode, code, checksum (section 4.3).
func getRequest(code byte) []byte {
	return withChecksum([]byte{ddcHostAddress, ddcLengthFlag | 2, vcpGetRequest, code})
}

// setRequest builds a Set VCP Feature request. It has four data bytes,
// and the value is big-endian (section 4.4).
func setRequest(code byte, value uint16) []byte {
	return withChecksum([]byte{
		ddcHostAddress, ddcLengthFlag | 4, vcpSetRequest, code,
		byte(value >> 8), byte(value),
	})
}

// withChecksum appends the checksum of a request. The checksum covers
// the write address that the kernel sends before the buffer, so the
// sum starts from that address.
func withChecksum(packet []byte) []byte {
	return append(packet, ddcChecksum(ddcWriteAddress, packet))
}

// ddcChecksum XORs seed and every byte of packet. seed is the address
// byte that comes before the packet on the wire. A request uses 0x6e.
// For a reply, section 6.3 has the display send 0x6f on the wire but
// compute its checksum from 0x50, the host's virtual write address, so
// a reply checked against 0x6f fails on every monitor.
func ddcChecksum(seed byte, packet []byte) byte {
	sum := seed
	for _, b := range packet {
		sum ^= b
	}
	return sum
}

// parseGetReply returns the current and maximum values from a reply.
// It checks the source address, length, opcode, and checksum before it
// reads any field, because when the frame is wrong an error about a
// field inside it would report the wrong fault.
//
// The length check also rejects the null message of section 6.4, which
// a display sends when it is not ready to reply. GetVCP retries it.
func parseGetReply(reply []byte, code byte) (uint16, uint16, error) {
	if silent(reply) {
		return 0, 0, ErrNoAnswer
	}
	if reply[0] != ddcReplySource {
		return 0, 0, fmt.Errorf("%w: the first byte is %#04x, want %#04x", ErrGarbledReply, reply[0], ddcReplySource)
	}
	if int(reply[1]&ddcLengthMask) != getReplyDataLength || reply[1]&ddcLengthFlag == 0 {
		return 0, 0, fmt.Errorf("%w: the length byte is %#04x", ErrGarbledReply, reply[1])
	}
	if reply[2] != vcpGetReply {
		return 0, 0, fmt.Errorf("%w: the opcode is %#04x, want %#04x", ErrGarbledReply, reply[2], vcpGetReply)
	}
	body := reply[:getReplyLength-1]
	if sum := ddcChecksum(ddcVirtualHost, body); sum != reply[getReplyLength-1] {
		return 0, 0, fmt.Errorf("%w: the checksum byte is %#04x, and the bytes sum to %#04x",
			ErrGarbledReply, reply[getReplyLength-1], sum)
	}
	if reply[3] == vcpResultUnsupported {
		return 0, 0, fmt.Errorf("%w: %#04x", ErrUnsupportedVCP, code)
	}
	if reply[3] != vcpResultOK {
		return 0, 0, fmt.Errorf("%w: the result code is %#04x", ErrGarbledReply, reply[3])
	}
	if reply[4] != code {
		return 0, 0, fmt.Errorf("%w: the reply is for VCP code %#04x, and the request was for %#04x",
			ErrGarbledReply, reply[4], code)
	}
	max := uint16(reply[6])<<8 | uint16(reply[7])
	current := uint16(reply[8])<<8 | uint16(reply[9])
	return current, max, nil
}

// silent reports whether nothing drove the bus. An undriven I2C data
// line floats high and reads as all ones, and a line held low reads as
// all zeros. Both mean no reply, not a corrupted one.
func silent(reply []byte) bool {
	ones, zeros := true, true
	for _, b := range reply {
		if b != 0xff {
			ones = false
		}
		if b != 0x00 {
			zeros = false
		}
	}
	return ones || zeros
}

// connectorBus returns the /dev/i2c-N node for a connector's DDC
// channel. The kernel publishes the channel as a ddc symlink next to
// the connector's edid and status files, and the link target ends in
// the I2C adapter's name, i2c-N.
//
// It returns an empty path, not an error, when the link is missing or
// does not end in i2c-N. A connector with no DDC channel has no link,
// and a DisplayPort connector behind an MST hub carries DDC inside the
// AUX channel, which no i2c-dev node exposes.
func connectorBus(sysRoot, card, connector string) string {
	link := filepath.Join(sysRoot, "class", "drm", card+"-"+connector, "ddc")
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	adapter := filepath.Base(target)
	if !strings.HasPrefix(adapter, "i2c-") || adapter == "i2c-" {
		return ""
	}
	return filepath.Join("/dev", adapter)
}

// An i2cDevice is an open i2c-dev node with the display's address
// already bound, so every read and write on it is a DDC/CI message.
type i2cDevice struct {
	fd int
}

// openI2C opens the node and binds the display's address. The kernel
// fails the bind with EBUSY when a driver already owns the address. A
// graphics card's DDC channel has no such driver, so a failure is a
// real conflict. openI2C returns it and does not retry with
// I2C_SLAVE_FORCE, because forcing an address that another driver owns
// would write to that driver's device.
func openI2C(path string) (*i2cDevice, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if err := unix.IoctlSetInt(fd, i2cSlaveRequest, ddcDisplayAddress); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("addressing %#04x on %s: %w", ddcDisplayAddress, path, err)
	}
	return &i2cDevice{fd: fd}, nil
}

// Write fails on a short write, because a truncated packet no longer
// matches its length byte and checksum, so the display discards it.
func (d *i2cDevice) Write(request []byte) error {
	written, err := unix.Write(d.fd, request)
	if err != nil {
		return err
	}
	if written != len(request) {
		return fmt.Errorf("wrote %d bytes of a %d-byte message", written, len(request))
	}
	return nil
}

// Read fails on a short read, because a DDC/CI reply arrives in one
// read transaction of the requested length.
func (d *i2cDevice) Read(reply []byte) error {
	read, err := unix.Read(d.fd, reply)
	if err != nil {
		return err
	}
	if read != len(reply) {
		return fmt.Errorf("read %d bytes of an %d-byte reply", read, len(reply))
	}
	return nil
}

func (d *i2cDevice) Close() error {
	return unix.Close(d.fd)
}
