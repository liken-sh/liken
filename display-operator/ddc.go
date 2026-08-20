package main

// The DDC/CI client: how the operator reaches a monitor's own
// controls.
//
// Two wires of a display cable are an I2C bus, and the monitor
// answers on that bus in DDC/CI, the VESA protocol that reads and
// sets the same controls as the buttons on the monitor's bezel:
// brightness, power, input source. The kernel exposes each
// connector's bus as a /dev/i2c-N node, so a process that holds the
// node can set those controls with a few bytes, without a compositor,
// a library, or a helper binary. This file is the protocol: it builds
// the request packets, reads the replies, and waits the delays the
// specification requires.
//
// The operator speaks the protocol itself rather than running
// ddcutil, because the operator's image holds one static binary and
// nothing else, and the whole exchange is five bytes out and eleven
// back. That is less code than running another program and parsing
// its output.
//
// The delays below come from the specification. A monitor's DDC
// receiver is a slow microcontroller, and the specification states
// how long the host must wait between messages. A host that reads
// early gets a garbled reply from a working panel.
//
// Section numbers in this file cite VESA DDC/CI 1.1.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// DDC/CI addressing, which the specification writes in 8-bit form and
// Linux writes in 7-bit form. The display answers at 7-bit 0x37. The
// wire carries that address shifted left with a direction bit behind
// it: 0x6e writes to the display and 0x6f reads from it. The host has
// a pair of its own, virtual because no hardware answers at it: 0x50
// and 0x51, and a request names 0x51 as its source. (Sections 2.2
// and 2.3.)
//
// 0x6e appears in two places. It is the byte the kernel drives on the
// wire to address the display, and it is the first byte of a reply,
// the source address the display writes.
const (
	ddcDisplayAddress = 0x37
	ddcWriteAddress   = ddcDisplayAddress << 1
	ddcHostAddress    = 0x51
	ddcVirtualHost    = 0x50
	ddcReplySource    = 0x6e
)

// The three opcodes this operator sends and reads. VCP, the Virtual
// Control Panel, is the register file behind DDC/CI: every control is
// a numbered code, and the MCCS specification names many more codes
// than the ones this operator touches.
const (
	vcpGetRequest = 0x01
	vcpGetReply   = 0x02
	vcpSetRequest = 0x03
)

// The length byte of every DDC/CI message: the high bit is always
// set, and the low seven bits count the data bytes that follow. The
// source address before the count and the checksum after it are not
// in the count.
const (
	ddcLengthFlag = 0x80
	ddcLengthMask = 0x7f
)

// The result code of a Get reply. Zero says the display answered the
// code. One says the display has no such control, which is a
// well-formed answer. The wire did not fail.
const (
	vcpResultOK          = 0x00
	vcpResultUnsupported = 0x01
)

// A Get VCP Feature reply is eight data bytes, eleven with the source
// address and the checksum around them. The specification draws the
// frame with a twelfth byte in front, the 0x6f that addresses the
// read, but the kernel consumes that byte and it never reaches this
// buffer.
const (
	getReplyDataLength = 8
	getReplyLength     = getReplyDataLength + 3
)

// These delays are the specification's numbers. A display needs 40ms
// to decode a request and prepare its reply before the host may read
// it (sections 4.3 and 6.6). After a Set, a display needs 50ms before
// it accepts the next message (section 4.4). Between a failed attempt
// and its retry, the host waits 40ms (section 5).
const (
	ddcReplyDelay = 40 * time.Millisecond
	ddcSetDelay   = 50 * time.Millisecond
	ddcRetryDelay = 40 * time.Millisecond
)

// One refusal says nothing about a monitor. A panel that is busy with
// its own menu, or that just powered on, drops one message and
// answers the next, so a Get runs up to three attempts. Section 5 asks the host
// for three retries, which is four attempts; this operator stops at
// three because the probe runs against every connector on the card,
// and each attempt against a dead address costs its full delay.
const ddcGetAttempts = 3

// I2C_SLAVE, from <linux/i2c-dev.h>. The ioctl binds a slave address
// to the open descriptor, and every later read and write on that
// descriptor talks to the bound address. That is why no packet in
// this file carries the display's address: the kernel adds it on the
// wire.
const i2cSlaveRequest = 0x0703

// The three failures a caller must tell apart. ErrNoAnswer: nothing
// answered the address, so this connector has no monitor or no
// DDC/CI. ErrGarbledReply: something answered outside the protocol,
// and a retry may get a clean reply. ErrUnsupportedVCP: the display
// answered plainly that it has no such control, so the capability is
// absent and asking again changes nothing.
var (
	ErrNoAnswer       = errors.New("no display answered on the DDC/CI address")
	ErrGarbledReply   = errors.New("the display answered bytes that are not a DDC/CI reply")
	ErrUnsupportedVCP = errors.New("the display carries no such VCP code")
)

// The bus is an interface because the packet format and the timing
// are the parts worth testing, and neither needs a monitor. A Write
// is one I2C write transaction and a Read is one I2C read
// transaction, both to the address the node was bound to at open.
type i2cBus interface {
	Write(request []byte) error
	Read(reply []byte) error
}

// A DDC speaks the protocol over one bus. sleep is a field so a test
// can run the exchanges without waiting out the specification's
// delays.
type DDC struct {
	bus   i2cBus
	sleep func(time.Duration)
}

// newDDC wraps a bus that is already bound to the display's address.
func newDDC(bus i2cBus) *DDC {
	return &DDC{bus: bus, sleep: time.Sleep}
}

// GetVCP asks the display for one control and returns the value the
// control holds now and the largest value it accepts, both 16-bit.
// The maximum is what turns a raw value into a percentage. A caller
// probes for a control by asking for it and checking for
// ErrUnsupportedVCP, the display's answer that the control is absent.
//
// The retry is here rather than in the caller, because one refusal
// says nothing about the monitor and every caller would need the same
// loop.
func (d *DDC) GetVCP(code byte) (uint16, uint16, error) {
	var err error
	for attempt := 0; attempt < ddcGetAttempts; attempt++ {
		if attempt > 0 {
			d.sleep(ddcRetryDelay)
		}
		var current, max uint16
		current, max, err = d.getOnce(code)
		if err == nil {
			return current, max, nil
		}
		if errors.Is(err, ErrUnsupportedVCP) {
			// A display that named the code unsupported will name it
			// unsupported again. Two more attempts would wait 80ms
			// for the same answer.
			break
		}
	}
	return 0, 0, fmt.Errorf("reading VCP code %#04x: %w", code, err)
}

// getOnce is the one exchange: write the request, wait, read the
// fixed-length reply. Reading a fixed length is safe even when the
// display's answer is shorter, because section 6.5 makes a display
// pad a short answer rather than hold the bus.
func (d *DDC) getOnce(code byte) (uint16, uint16, error) {
	if err := d.bus.Write(getRequest(code)); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrNoAnswer, err)
	}
	d.sleep(ddcReplyDelay)
	reply := make([]byte, getReplyLength)
	if err := d.bus.Read(reply); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrNoAnswer, err)
	}
	return parseGetReply(reply, code)
}

// SetVCP writes one control. The display acknowledges the bytes on
// the wire and sends nothing back, so the only proof the control
// moved is a Get afterwards. The caller owns that readback. The delay
// here is what makes the readback meaningful: a Get inside the
// display's settle window reads the value the Set replaced.
func (d *DDC) SetVCP(code byte, value uint16) error {
	if err := d.bus.Write(setRequest(code, value)); err != nil {
		return fmt.Errorf("setting VCP code %#04x: %w: %w", code, ErrNoAnswer, err)
	}
	d.sleep(ddcSetDelay)
	return nil
}

// getRequest builds a Get VCP Feature request: source, length,
// opcode, code, checksum (section 4.3). The destination address is
// not in the buffer, because the kernel drives it from the bound
// address. The checksum still covers it, which is why the sum is
// seeded with a constant rather than a byte copied from the buffer.
func getRequest(code byte) []byte {
	return withChecksum([]byte{ddcHostAddress, ddcLengthFlag | 2, vcpGetRequest, code})
}

// setRequest builds a Set VCP Feature request: four data bytes, with
// the value big-endian (section 4.4).
func setRequest(code byte, value uint16) []byte {
	return withChecksum([]byte{
		ddcHostAddress, ddcLengthFlag | 4, vcpSetRequest, code,
		byte(value >> 8), byte(value),
	})
}

// withChecksum ends a request with the sum of everything before it,
// seeded with the address byte the kernel will drive.
func withChecksum(packet []byte) []byte {
	return append(packet, ddcChecksum(ddcWriteAddress, packet))
}

// ddcChecksum is an exclusive-or over the whole frame, the leading
// address byte included. The seed is that address byte: 0x6e for a
// request the host sends. A reply is the exception that section 6.3
// calls out: the display drives 0x6f on the wire but seeds its
// checksum with 0x50, the host's virtual write address. A reader that
// folds in the byte it saw computes the wrong sum on every monitor.
func ddcChecksum(seed byte, packet []byte) byte {
	sum := seed
	for _, b := range packet {
		sum ^= b
	}
	return sum
}

// parseGetReply checks that the bytes are a DDC/CI reply at all
// before it reads any field: source, length, and opcode first, the
// checksum next, and the fields only after that. A wrong source or
// length means the bytes are not a reply, and an error that named a
// field inside them would name the wrong fault.
//
// The length check also rejects the null message of section 6.4, the
// three bytes a display sends when it is not ready to answer yet. The
// retry in GetVCP covers it.
func parseGetReply(reply []byte, code byte) (uint16, uint16, error) {
	if silent(reply) {
		return 0, 0, ErrNoAnswer
	}
	if reply[0] != ddcReplySource {
		return 0, 0, fmt.Errorf("%w: it starts with %#04x, not %#04x", ErrGarbledReply, reply[0], ddcReplySource)
	}
	if int(reply[1]&ddcLengthMask) != getReplyDataLength || reply[1]&ddcLengthFlag == 0 {
		return 0, 0, fmt.Errorf("%w: its length byte is %#04x", ErrGarbledReply, reply[1])
	}
	if reply[2] != vcpGetReply {
		return 0, 0, fmt.Errorf("%w: its opcode is %#04x, not %#04x", ErrGarbledReply, reply[2], vcpGetReply)
	}
	body := reply[:getReplyLength-1]
	if sum := ddcChecksum(ddcVirtualHost, body); sum != reply[getReplyLength-1] {
		return 0, 0, fmt.Errorf("%w: its checksum is %#04x, and the bytes add up to %#04x",
			ErrGarbledReply, reply[getReplyLength-1], sum)
	}
	if reply[3] == vcpResultUnsupported {
		return 0, 0, fmt.Errorf("%w: %#04x", ErrUnsupportedVCP, code)
	}
	if reply[3] != vcpResultOK {
		return 0, 0, fmt.Errorf("%w: its result code is %#04x", ErrGarbledReply, reply[3])
	}
	if reply[4] != code {
		return 0, 0, fmt.Errorf("%w: it answers VCP code %#04x, and the request asked for %#04x",
			ErrGarbledReply, reply[4], code)
	}
	max := uint16(reply[6])<<8 | uint16(reply[7])
	current := uint16(reply[8])<<8 | uint16(reply[9])
	return current, max, nil
}

// silent reports a bus with nothing driving it. An undriven I2C data
// line floats high and reads as all ones, and a line held low reads
// as all zeros. Neither pattern is a reply that went wrong; both are
// the absence of a reply.
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

// connectorBus maps a connector to the /dev node of its DDC channel.
// The kernel publishes the channel as a ddc symlink beside the
// connector's edid and status files. The link names the I2C adapter,
// and the adapter's number is the /dev node's number.
//
// An absent link returns an empty path rather than an error. A
// connector with no DDC wire has no channel, and a DisplayPort
// connector behind an MST hub carries its DDC inside the AUX stream,
// where no i2c-dev node reaches it.
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

// An i2cDevice is one open i2c-dev node whose slave address is
// already bound, so every read and write on it is a DDC/CI message
// and nothing else.
type i2cDevice struct {
	fd int
}

// openI2C opens the node and binds the display's address once, at
// open, rather than before every message.
//
// The kernel refuses the bind with EBUSY when a driver is already
// bound to the address. A graphics card's DDC channel has no such
// driver, so a refusal reports a real conflict. This operator reports
// it rather than overriding it with I2C_SLAVE_FORCE, because forcing
// an address another driver holds writes on that driver's wire.
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

// A short write fails the whole message. A truncated packet has a
// length byte and a checksum that no longer match its bytes, so the
// display discards it.
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

// A short read fails the same way. The display's reply arrives in one
// transaction of the length the caller asked for.
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
