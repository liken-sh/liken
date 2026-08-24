package main

// The DDC/CI client, the panel's brightness and power controls
// reached over the display cable's own i2c wire. It is modeled on the
// display-operator's ddc.go and kept to the two VCP codes the idle
// sidecar writes: 0x10, the backlight, and 0xD6, the power mode.

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// The DDC/CI addresses. The display answers at 7-bit 0x37, which the
// kernel drives as 0x6e on a write. The host's pair is virtual, and no
// hardware answers it: a request names 0x51 as its source, and a
// reply's checksum seeds with 0x50.
const (
	ddcDisplayAddress = 0x37
	ddcWriteAddress   = ddcDisplayAddress << 1
	ddcHostAddress    = 0x51
	ddcVirtualHost    = 0x50
	ddcReplySource    = 0x6e
)

// The two VCP opcodes this file sends, and the reply opcode it reads.
const (
	ddcOpGetRequest = 0x01
	ddcOpGetReply   = 0x02
	ddcOpSetRequest = 0x03
)

// The length byte carries a flag bit over the count that follows it.
const (
	ddcLengthFlag = 0x80
	ddcLengthMask = 0x7f
)

// A Get reply's result byte: the code answered, or the display
// carries no such code.
const (
	ddcResultOK          = 0x00
	ddcResultUnsupported = 0x01
)

// A Get reply's frame size, the source and checksum bytes included.
const (
	ddcGetReplyDataLength = 8
	ddcGetReplyLength     = ddcGetReplyDataLength + 3
)

// The standard's timing: the wait before a reply read, the settle
// after a set, and the gap between retries.
const (
	ddcReplyDelay = 40 * time.Millisecond
	ddcSetDelay   = 50 * time.Millisecond
	ddcRetryDelay = 40 * time.Millisecond
)

// A Get runs at most this many exchanges before it gives up.
const ddcGetAttempts = 3

// I2C_SLAVE from <linux/i2c-dev.h>, which binds an address to a
// descriptor.
const i2cSlaveRequest = 0x0703

// The two VCP codes this sidecar speaks: the backlight, and the
// panel's power mode.
const (
	vcpBrightness byte = 0x10
	vcpPowerMode  byte = 0xD6
)

// The two values the sidecar writes to the power mode: on, and DPM
// off.
const (
	powerModeOn  uint16 = 0x01
	powerModeOff uint16 = 0x04
)

// The three failures a caller must tell apart: nothing answered,
// something answered bytes that are not a reply, or the display named
// the code unsupported. Only the first two are worth a retry.
var (
	ErrDDCNoAnswer       = errors.New("no display answered on the DDC/CI address")
	ErrDDCGarbledReply   = errors.New("the display answered bytes that are not a DDC/CI reply")
	ErrDDCUnsupportedVCP = errors.New("the display carries no such VCP code")
)

// panelWire is the narrow seam the sidecar logic tests against, so a
// scripted wire stands in for the panel.
type panelWire interface {
	SetVCP(code byte, value uint16) error
	GetVCP(code byte) (uint16, error)
}

// ddcBus is the byte-level seam under the wire, so a test drives the
// framing without an i2c-dev node.
type ddcBus interface {
	Write(request []byte) error
	Read(reply []byte) error
}

// ddcWire is a DDC/CI client bound to one display's address. fd is
// the open i2c-dev node, kept for Close. bus is what SetVCP and
// GetVCP write and read, and a test replaces it with a script.
type ddcWire struct {
	fd    int
	bus   ddcBus
	sleep func(time.Duration)
}

// openPanelWire opens the i2c-dev node and binds the display's
// DDC/CI address to it.
func openPanelWire(path string) (*ddcWire, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if err := bindDDCAddress(fd); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	return &ddcWire{
		fd:    fd,
		bus:   &ddcDescriptor{fd: fd},
		sleep: time.Sleep,
	}, nil
}

// bindDDCAddress binds the display's 7-bit address to the descriptor
// with I2C_SLAVE.
func bindDDCAddress(fd int) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), uintptr(i2cSlaveRequest), uintptr(ddcDisplayAddress))
	if errno != 0 {
		return fmt.Errorf("addressing %#04x: %w", ddcDisplayAddress, errno)
	}
	return nil
}

func (w *ddcWire) Close() error {
	return syscall.Close(w.fd)
}

// GetVCP asks the display for one control's current value. It
// retries up to ddcGetAttempts times, except after an
// unsupported-code answer, which a retry would only repeat.
func (w *ddcWire) GetVCP(code byte) (uint16, error) {
	var value uint16
	var err error
	for attempt := 0; attempt < ddcGetAttempts; attempt++ {
		if attempt > 0 {
			w.sleep(ddcRetryDelay)
		}
		value, err = w.getOnce(code)
		if err == nil {
			return value, nil
		}
		if errors.Is(err, ErrDDCUnsupportedVCP) {
			break
		}
	}
	return 0, fmt.Errorf("reading VCP code %#04x: %w", code, err)
}

// getOnce is one exchange: write the request, wait out the reply
// delay, read the reply.
func (w *ddcWire) getOnce(code byte) (uint16, error) {
	if err := w.bus.Write(ddcGetRequest(code)); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrDDCNoAnswer, err)
	}
	w.sleep(ddcReplyDelay)
	reply := make([]byte, ddcGetReplyLength)
	if err := w.bus.Read(reply); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrDDCNoAnswer, err)
	}
	return parseDDCGetReply(reply, code)
}

// SetVCP writes one control and waits out the display's settle time.
// It reads nothing back, because a DDC read is itself a wake stimulus
// on some panels, and a verify after the sleep write would light the
// panel it just put down.
func (w *ddcWire) SetVCP(code byte, value uint16) error {
	if err := w.bus.Write(ddcSetRequest(code, value)); err != nil {
		return fmt.Errorf("setting VCP code %#04x: %w: %w", code, ErrDDCNoAnswer, err)
	}
	w.sleep(ddcSetDelay)
	return nil
}

// ddcGetRequest builds a Get VCP Feature request.
func ddcGetRequest(code byte) []byte {
	return withDDCChecksum([]byte{ddcHostAddress, ddcLengthFlag | 2, ddcOpGetRequest, code})
}

// ddcSetRequest builds a Set VCP Feature request, the value
// big-endian.
func ddcSetRequest(code byte, value uint16) []byte {
	return withDDCChecksum([]byte{
		ddcHostAddress, ddcLengthFlag | 4, ddcOpSetRequest, code,
		byte(value >> 8), byte(value),
	})
}

// withDDCChecksum ends a request with the sum of everything before
// it, seeded with the write address the kernel drives on the wire.
func withDDCChecksum(packet []byte) []byte {
	return append(packet, ddcChecksum(ddcWriteAddress, packet))
}

// ddcChecksum is an exclusive-or over the seed and the packet. The
// seed is the leading address byte the frame is summed under: 0x6e
// for a request the kernel addresses, 0x50 for a reply the host
// verifies.
func ddcChecksum(seed byte, packet []byte) byte {
	sum := seed
	for _, b := range packet {
		sum ^= b
	}
	return sum
}

// parseDDCGetReply checks that the bytes are a DDC/CI reply at all
// before it reads any field: source, length, and opcode first, the
// checksum next, and the fields only after that.
func parseDDCGetReply(reply []byte, code byte) (uint16, error) {
	if ddcSilent(reply) {
		return 0, ErrDDCNoAnswer
	}
	if len(reply) != ddcGetReplyLength {
		return 0, fmt.Errorf("%w: it carries %d bytes, not %d",
			ErrDDCGarbledReply, len(reply), ddcGetReplyLength)
	}
	if reply[0] != ddcReplySource {
		return 0, fmt.Errorf("%w: it starts with %#04x, not %#04x",
			ErrDDCGarbledReply, reply[0], ddcReplySource)
	}
	if reply[1] != ddcLengthFlag|ddcGetReplyDataLength {
		return 0, fmt.Errorf("%w: its length byte is %#04x", ErrDDCGarbledReply, reply[1])
	}
	if reply[2] != ddcOpGetReply {
		return 0, fmt.Errorf("%w: its opcode is %#04x, not %#04x",
			ErrDDCGarbledReply, reply[2], ddcOpGetReply)
	}
	body := reply[:ddcGetReplyLength-1]
	if sum := ddcChecksum(ddcVirtualHost, body); sum != reply[ddcGetReplyLength-1] {
		return 0, fmt.Errorf("%w: its checksum is %#04x, and the bytes add up to %#04x",
			ErrDDCGarbledReply, reply[ddcGetReplyLength-1], sum)
	}
	if reply[3] == ddcResultUnsupported {
		return 0, fmt.Errorf("%w: %#04x", ErrDDCUnsupportedVCP, code)
	}
	if reply[3] != ddcResultOK {
		return 0, fmt.Errorf("%w: its result code is %#04x", ErrDDCGarbledReply, reply[3])
	}
	// A slow panel can answer this request with an earlier exchange's
	// reply, so the echoed code is checked too.
	if reply[4] != code {
		return 0, fmt.Errorf("%w: it answers VCP code %#04x, and the request asked for %#04x",
			ErrDDCGarbledReply, reply[4], code)
	}
	current := uint16(reply[8])<<8 | uint16(reply[9])
	return current, nil
}

// ddcSilent reports a bus with nothing driving it. An undriven line
// reads as all ones and a held line as all zeros. Both patterns are
// the absence of a reply, not a wrong one.
func ddcSilent(reply []byte) bool {
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

// ddcDescriptor is one open i2c-dev node already bound to the
// display's address.
type ddcDescriptor struct {
	fd int
}

// A short write fails the whole message rather than sending a
// truncated frame.
func (d *ddcDescriptor) Write(request []byte) error {
	written, err := syscall.Write(d.fd, request)
	if err != nil {
		return err
	}
	if written != len(request) {
		return fmt.Errorf("wrote %d bytes of a %d-byte message", written, len(request))
	}
	return nil
}

// A short read fails the same way, so the parser never reads a
// partial frame as a reply.
func (d *ddcDescriptor) Read(reply []byte) error {
	read, err := syscall.Read(d.fd, reply)
	if err != nil {
		return err
	}
	if read != len(reply) {
		return fmt.Errorf("read %d bytes of a %d-byte reply", read, len(reply))
	}
	return nil
}
