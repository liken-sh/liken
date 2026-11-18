package main

// The literal packets in this file are the worked examples of the
// DDC/CI 1.1 specification, checksums included. A change that makes
// the packet builders differ from the specification fails against
// these bytes without needing a monitor.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A busReply is the result of one read transaction: the bytes it
// returns, or the error that an address with no device produces.
type busReply struct {
	reply []byte
	err   error
}

// fakeBus records every message the client writes, and returns the
// queued replies in order, one for each read.
type fakeBus struct {
	writes   [][]byte
	writeErr error
	replies  []busReply
	reads    int
}

func (b *fakeBus) Write(request []byte) error {
	b.writes = append(b.writes, bytes.Clone(request))
	return b.writeErr
}

func (b *fakeBus) Read(reply []byte) error {
	if b.reads >= len(b.replies) {
		return errors.New("the test queued no reply for this read")
	}
	queued := b.replies[b.reads]
	b.reads++
	if queued.err != nil {
		return queued.err
	}
	if len(queued.reply) != len(reply) {
		return errors.New("the test queued a reply of the wrong length")
	}
	copy(reply, queued.reply)
	return nil
}

// ddcFixture replaces the protocol's delays with a recorder. Tests
// run without waiting and still check how long the client asked to
// wait before each read.
type ddcFixture struct {
	bus    *fakeBus
	client *DDC
	slept  []time.Duration
}

func newDDCFixture(t *testing.T, replies ...busReply) *ddcFixture {
	t.Helper()
	fixture := &ddcFixture{bus: &fakeBus{replies: replies}}
	fixture.client = newDDC(fixture.bus)
	fixture.client.sleep = func(waited time.Duration) {
		fixture.slept = append(fixture.slept, waited)
	}
	return fixture
}

// getReply builds a valid reply for a control that the display
// supports. Byte 5 is the control's type code, which this operator
// does not read, so it stays zero.
func getReply(code byte, current, max uint16) []byte {
	reply := []byte{
		ddcReplySource, ddcLengthFlag | getReplyDataLength, vcpGetReply, vcpResultOK,
		code, 0x00, byte(max >> 8), byte(max), byte(current >> 8), byte(current),
	}
	return append(reply, ddcChecksum(ddcVirtualHost, reply))
}

func TestGetVCPSendsAndParsesTheSpecificationExample(t *testing.T) {
	// Both packets are the specification's example bytes: a request
	// for brightness, and the reply of a monitor at 50 out of 100.
	fixture := newDDCFixture(t, busReply{
		reply: []byte{0x6e, 0x88, 0x02, 0x00, 0x10, 0x00, 0x00, 0x64, 0x00, 0x32, 0xf2},
	})

	current, max, err := fixture.client.GetVCP(0x10)
	if err != nil {
		t.Fatalf("reading VCP code 0x10: %v", err)
	}
	if current != 50 || max != 100 {
		t.Errorf("got %d of %d, want 50 of 100", current, max)
	}

	want := []byte{0x51, 0x82, 0x01, 0x10, 0xac}
	if len(fixture.bus.writes) != 1 || !bytes.Equal(fixture.bus.writes[0], want) {
		t.Errorf("wrote %#x, want one message of %#x", fixture.bus.writes, want)
	}
	// The specification's 40ms between the request and the read.
	if len(fixture.slept) != 1 || fixture.slept[0] != ddcReplyDelay {
		t.Errorf("waited %v, want one wait of %v", fixture.slept, ddcReplyDelay)
	}
}

func TestGetVCPReturnsCurrentAndMaximumForEachControl(t *testing.T) {
	cases := []struct {
		name             string
		code             byte
		current, max     uint16
		wantCur, wantMax uint16
	}{
		{name: "brightness", code: 0x10, current: 50, max: 100, wantCur: 50, wantMax: 100},
		{name: "power mode", code: 0xd6, current: 1, max: 5, wantCur: 1, wantMax: 5},
		// A value above 255 uses both bytes.
		{name: "a two-byte value", code: 0x12, current: 0x0140, max: 0x03e8, wantCur: 320, wantMax: 1000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDDCFixture(t, busReply{reply: getReply(c.code, c.current, c.max)})

			current, max, err := fixture.client.GetVCP(c.code)
			if err != nil {
				t.Fatalf("reading VCP code %#x: %v", c.code, err)
			}
			if current != c.wantCur || max != c.wantMax {
				t.Errorf("got %d of %d, want %d of %d", current, max, c.wantCur, c.wantMax)
			}
		})
	}
}

// corrupt changes one byte of a valid reply and recomputes the
// checksum, so each case below fails on the field it changed and not
// on the checksum.
func corrupt(reply []byte, index int, value byte) []byte {
	broken := bytes.Clone(reply)
	broken[index] = value
	broken[getReplyLength-1] = ddcChecksum(ddcVirtualHost, broken[:getReplyLength-1])
	return broken
}

// withBadChecksum returns a reply as a noisy wire would deliver it.
// Every field is valid, but the checksum does not match the bytes.
func withBadChecksum(reply []byte) []byte {
	broken := bytes.Clone(reply)
	broken[getReplyLength-1] ^= 0x01
	return broken
}

func TestGetVCPRejectsAnInvalidReplyAfterAllAttempts(t *testing.T) {
	good := getReply(0x10, 50, 100)
	cases := []struct {
		name  string
		reply []byte
	}{
		{name: "a checksum that does not add up", reply: withBadChecksum(good)},
		{name: "a source address that is not the display", reply: corrupt(good, 0, 0x6f)},
		{name: "a length byte with no high bit", reply: corrupt(good, 1, 0x08)},
		{name: "a length that is not eight data bytes", reply: corrupt(good, 1, 0x86)},
		{name: "an opcode that is not a Get reply", reply: corrupt(good, 2, 0x03)},
		{name: "a result code that names no failure", reply: corrupt(good, 3, 0x7f)},
		{name: "the reply for another VCP code", reply: corrupt(good, 4, 0x12)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDDCFixture(t,
				busReply{reply: c.reply}, busReply{reply: c.reply}, busReply{reply: c.reply})

			_, _, err := fixture.client.GetVCP(0x10)
			if !errors.Is(err, ErrGarbledReply) {
				t.Fatalf("GetVCP error is %v, want ErrGarbledReply", err)
			}
			// Noise on the wire can garble one reply, so the client
			// retries before it reports the failure.
			if len(fixture.bus.writes) != ddcGetAttempts {
				t.Errorf("wrote %d messages, want %d", len(fixture.bus.writes), ddcGetAttempts)
			}
		})
	}
}

func TestGetVCPRetriesAfterAnInvalidReply(t *testing.T) {
	good := getReply(0x10, 50, 100)
	fixture := newDDCFixture(t,
		busReply{reply: withBadChecksum(good)},
		busReply{reply: good},
	)

	current, max, err := fixture.client.GetVCP(0x10)
	if err != nil {
		t.Fatalf("reading VCP code 0x10: %v", err)
	}
	if current != 50 || max != 100 {
		t.Errorf("got %d of %d, want 50 of 100", current, max)
	}
	if len(fixture.bus.writes) != 2 {
		t.Errorf("wrote %d messages, want 2", len(fixture.bus.writes))
	}
	// The waits, in order: the first attempt's reply delay, the
	// retry delay from the specification, and the second attempt's
	// reply delay, which is twice the first.
	want := []time.Duration{ddcReplyDelay, ddcRetryDelay, 2 * ddcReplyDelay}
	if !slices.Equal(fixture.slept, want) {
		t.Errorf("waited %v, want %v", fixture.slept, want)
	}
}

func TestGetVCPWaitsLongerOnEachAttempt(t *testing.T) {
	cases := []struct {
		name       string
		replyDelay time.Duration
		attempts   int
	}{
		{name: "a panel that replies on time", replyDelay: ddcReplyDelay, attempts: 1},
		{name: "a panel that replies at twice the delay", replyDelay: 2 * ddcReplyDelay, attempts: 2},
		{name: "a panel that replies at four times the delay", replyDelay: 4 * ddcReplyDelay, attempts: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := &slowPanel{replyDelay: c.replyDelay, reply: getReply(0x10, 50, 100)}
			client := newDDC(panel)
			client.sleep = func(waited time.Duration) { panel.waited = waited }

			current, max, err := client.GetVCP(0x10)
			if err != nil {
				t.Fatalf("reading VCP code 0x10: %v", err)
			}
			if current != 50 || max != 100 {
				t.Errorf("got %d of %d, want 50 of 100", current, max)
			}
			if panel.reads != c.attempts {
				t.Errorf("the panel was read %d times, want %d", panel.reads, c.attempts)
			}
		})
	}
}

// slowPanel returns its reply only when the last wait before the read
// was at least replyDelay. Otherwise the read returns all ones, as from a
// connector with no monitor.
type slowPanel struct {
	replyDelay time.Duration
	waited     time.Duration
	reply      []byte
	reads      int
}

func (p *slowPanel) Write([]byte) error { return nil }

func (p *slowPanel) Read(reply []byte) error {
	p.reads++
	if p.waited < p.replyDelay {
		copy(reply, bytes.Repeat([]byte{0xff}, len(reply)))
		return nil
	}
	copy(reply, p.reply)
	return nil
}

func TestGetVCPReportsASilentBusAsNoAnswer(t *testing.T) {
	// Callers take ErrNoAnswer to mean that the monitor has no DDC/CI,
	// so a silent bus must not be reported as a garbled reply.
	cases := []struct {
		name     string
		writeErr error
		reply    busReply
	}{
		{name: "the address acknowledges no write", writeErr: unix.ENXIO},
		{name: "the read fails on the wire", reply: busReply{err: unix.EREMOTEIO}},
		{name: "an undriven wire reads as all ones", reply: busReply{reply: bytes.Repeat([]byte{0xff}, getReplyLength)}},
		{name: "a wire held low reads as all zeros", reply: busReply{reply: make([]byte, getReplyLength)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDDCFixture(t, c.reply, c.reply, c.reply)
			fixture.bus.writeErr = c.writeErr

			_, _, err := fixture.client.GetVCP(0x10)
			if !errors.Is(err, ErrNoAnswer) {
				t.Fatalf("GetVCP error is %v, want ErrNoAnswer", err)
			}
			if errors.Is(err, ErrGarbledReply) {
				t.Errorf("GetVCP error is %v, want ErrNoAnswer and not ErrGarbledReply", err)
			}
		})
	}
}

func TestGetVCPReportsAnUnsupportedControlWithoutRetrying(t *testing.T) {
	unsupported := corrupt(getReply(0x10, 0, 0), 3, vcpResultUnsupported)
	fixture := newDDCFixture(t, busReply{reply: unsupported})

	_, _, err := fixture.client.GetVCP(0x10)
	if !errors.Is(err, ErrUnsupportedVCP) {
		t.Fatalf("GetVCP error is %v, want ErrUnsupportedVCP", err)
	}
	// A retry would get the same result, so the client sends one
	// request.
	if len(fixture.bus.writes) != 1 {
		t.Errorf("wrote %d messages, want 1", len(fixture.bus.writes))
	}
}

func TestSetVCPSendsTheSpecificationExample(t *testing.T) {
	fixture := newDDCFixture(t)

	if err := fixture.client.SetVCP(0x10, 50); err != nil {
		t.Fatalf("setting VCP code 0x10: %v", err)
	}
	want := []byte{0x51, 0x84, 0x03, 0x10, 0x00, 0x32, 0x9a}
	if len(fixture.bus.writes) != 1 || !bytes.Equal(fixture.bus.writes[0], want) {
		t.Errorf("wrote %#x, want one message of %#x", fixture.bus.writes, want)
	}
	// A display sends no reply to a Set, and needs 50ms before it
	// accepts the next message.
	if len(fixture.slept) != 1 || fixture.slept[0] != ddcSetDelay {
		t.Errorf("waited %v, want one wait of %v", fixture.slept, ddcSetDelay)
	}
	if fixture.bus.reads != 0 {
		t.Errorf("read %d replies, want none", fixture.bus.reads)
	}
}

func TestSetVCPFollowedByGetVCPConfirmsTheValue(t *testing.T) {
	// A display that clamps or ignores a value still acknowledges the
	// write on the bus, so only the read confirms the value.
	fixture := newDDCFixture(t, busReply{reply: getReply(0x10, 50, 100)})

	if err := fixture.client.SetVCP(0x10, 50); err != nil {
		t.Fatalf("setting VCP code 0x10: %v", err)
	}
	current, _, err := fixture.client.GetVCP(0x10)
	if err != nil {
		t.Fatalf("reading VCP code 0x10: %v", err)
	}
	if current != 50 {
		t.Errorf("read back %d, want 50", current)
	}

	wantSet := []byte{0x51, 0x84, 0x03, 0x10, 0x00, 0x32, 0x9a}
	wantGet := []byte{0x51, 0x82, 0x01, 0x10, 0xac}
	if len(fixture.bus.writes) != 2 {
		t.Fatalf("wrote %#x, want a Set and a Get", fixture.bus.writes)
	}
	if !bytes.Equal(fixture.bus.writes[0], wantSet) || !bytes.Equal(fixture.bus.writes[1], wantGet) {
		t.Errorf("wrote %#x, want %#x then %#x", fixture.bus.writes, wantSet, wantGet)
	}
}

func TestDDCChecksumUsesADifferentSeedForRequestsAndReplies(t *testing.T) {
	cases := []struct {
		name   string
		seed   byte
		packet []byte
		want   byte
	}{
		{
			name:   "a request, seeded with the address on the wire",
			seed:   ddcWriteAddress,
			packet: []byte{0x51, 0x82, 0x01, 0x10},
			want:   0xac,
		},
		{
			name:   "a reply, seeded with the virtual host address",
			seed:   ddcVirtualHost,
			packet: []byte{0x6e, 0x88, 0x02, 0x00, 0x10, 0x00, 0x00, 0x64, 0x00, 0x32},
			want:   0xf2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ddcChecksum(c.seed, c.packet); got != c.want {
				t.Errorf("checksum = %#02x, want %#02x", got, c.want)
			}
		})
	}
}

// fakeDDCSysfs builds the part of sysfs that connectorBus reads: one
// directory per connector, with a ddc symlink to the map's value, or
// no symlink when the value is empty.
func fakeDDCSysfs(t *testing.T, card string, links map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for connector, target := range links {
		dir := filepath.Join(root, "class", "drm", card+"-"+connector)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the connector directory: %v", err)
		}
		if target == "" {
			continue
		}
		if err := os.Symlink(target, filepath.Join(dir, "ddc")); err != nil {
			t.Fatalf("linking the DDC bus: %v", err)
		}
	}
	return root
}

func TestConnectorBusFollowsTheDDCLink(t *testing.T) {
	// The link takes two forms on real machines. i915 writes a bare
	// adapter name, and other drivers write a relative path that ends
	// in the adapter's name.
	root := fakeDDCSysfs(t, "card1", map[string]string{
		"eDP-1":    "i2c-14",
		"HDMI-A-1": "../../../i2c-0",
		"DP-1":     "",
		"DP-2":     "../../../device",
	})
	cases := []struct {
		connector string
		want      string
	}{
		{connector: "eDP-1", want: "/dev/i2c-14"},
		{connector: "HDMI-A-1", want: "/dev/i2c-0"},
		{connector: "DP-1", want: ""},
		{connector: "DP-2", want: ""},
		{connector: "HDMI-A-9", want: ""},
	}
	for _, c := range cases {
		t.Run(c.connector, func(t *testing.T) {
			if got := connectorBus(root, "card1", c.connector); got != c.want {
				t.Errorf("connectorBus = %q, want %q", got, c.want)
			}
		})
	}
}
