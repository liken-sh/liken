package main

// The literal packets in this file are the bytes of the DDC/CI 1.1
// specification's own worked examples, checksums included, written
// out by hand. A change that bends the packet builders away from the
// specification fails against these bytes, rather than against a
// monitor.

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

// A busAnswer is one read transaction: either the bytes it brings
// back, or the error an unanswered address produces.
type busAnswer struct {
	reply []byte
	err   error
}

// The fake bus keeps every message the client wrote, so a test
// checks the packets that would reach the wire instead of the code
// that built them.
type fakeBus struct {
	writes   [][]byte
	writeErr error
	answers  []busAnswer
	reads    int
}

func (b *fakeBus) Write(request []byte) error {
	b.writes = append(b.writes, bytes.Clone(request))
	return b.writeErr
}

func (b *fakeBus) Read(reply []byte) error {
	if b.reads >= len(b.answers) {
		return errors.New("the test queued no answer for this read")
	}
	answer := b.answers[b.reads]
	b.reads++
	if answer.err != nil {
		return answer.err
	}
	if len(answer.reply) != len(reply) {
		return errors.New("the test queued an answer of the wrong length")
	}
	copy(reply, answer.reply)
	return nil
}

// The fixture replaces the protocol's delays with a recorder. The
// tests run with no waiting, and they still prove the client asked to
// wait the specification's time before each read.
type ddcFixture struct {
	bus    *fakeBus
	client *DDC
	slept  []time.Duration
}

func newDDCFixture(t *testing.T, answers ...busAnswer) *ddcFixture {
	t.Helper()
	fixture := &ddcFixture{bus: &fakeBus{answers: answers}}
	fixture.client = newDDC(fixture.bus)
	fixture.client.sleep = func(waited time.Duration) {
		fixture.slept = append(fixture.slept, waited)
	}
	return fixture
}

// getReply builds a well-formed reply for a control that answered.
// Byte 5 is the control's type code, which this operator reads
// nothing from, so it stays zero.
func getReply(code byte, current, max uint16) []byte {
	reply := []byte{
		ddcReplySource, ddcLengthFlag | getReplyDataLength, vcpGetReply, vcpResultOK,
		code, 0x00, byte(max >> 8), byte(max), byte(current >> 8), byte(current),
	}
	return append(reply, ddcChecksum(ddcVirtualHost, reply))
}

func TestGetVCPSpeaksTheSpecPacket(t *testing.T) {
	// Both packets are the specification's example bytes, checksums
	// included: a request for brightness, and the reply of a monitor
	// at 50 out of 100.
	fixture := newDDCFixture(t, busAnswer{
		reply: []byte{0x6e, 0x88, 0x02, 0x00, 0x10, 0x00, 0x00, 0x64, 0x00, 0x32, 0xf2},
	})

	current, max, err := fixture.client.GetVCP(0x10)
	if err != nil {
		t.Fatal(err)
	}
	if current != 50 || max != 100 {
		t.Errorf("got %d of %d, want 50 of 100", current, max)
	}

	want := []byte{0x51, 0x82, 0x01, 0x10, 0xac}
	if len(fixture.bus.writes) != 1 || !bytes.Equal(fixture.bus.writes[0], want) {
		t.Errorf("wrote %#x, want one message of %#x", fixture.bus.writes, want)
	}
	// The wait between the request and the read is the
	// specification's 40ms. A host that reads earlier gets a stale
	// or garbled reply.
	if len(fixture.slept) != 1 || fixture.slept[0] != ddcReplyDelay {
		t.Errorf("waited %v, want one wait of %v", fixture.slept, ddcReplyDelay)
	}
}

func TestGetVCPReadsTheControlsThisOperatorProbes(t *testing.T) {
	cases := []struct {
		name             string
		code             byte
		current, max     uint16
		wantCur, wantMax uint16
	}{
		{name: "brightness", code: 0x10, current: 50, max: 100, wantCur: 50, wantMax: 100},
		{name: "power mode", code: 0xd6, current: 1, max: 5, wantCur: 1, wantMax: 5},
		// A control's value is 16-bit, and a monitor with a wide
		// range uses both bytes.
		{name: "a two-byte value", code: 0x12, current: 0x0140, max: 0x03e8, wantCur: 320, wantMax: 1000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDDCFixture(t, busAnswer{reply: getReply(c.code, c.current, c.max)})

			current, max, err := fixture.client.GetVCP(c.code)
			if err != nil {
				t.Fatal(err)
			}
			if current != c.wantCur || max != c.wantMax {
				t.Errorf("got %d of %d, want %d of %d", current, max, c.wantCur, c.wantMax)
			}
		})
	}
}

// corrupt changes one byte of a good reply and recomputes the
// checksum to match, so each case below fails on the field it names
// rather than on the checksum.
func corrupt(reply []byte, index int, value byte) []byte {
	broken := bytes.Clone(reply)
	broken[index] = value
	broken[getReplyLength-1] = ddcChecksum(ddcVirtualHost, broken[:getReplyLength-1])
	return broken
}

// withBadChecksum is the reply a noisy wire delivers: every field
// reads well, and the bytes do not add up to the sum the display
// sent.
func withBadChecksum(reply []byte) []byte {
	broken := bytes.Clone(reply)
	broken[getReplyLength-1] ^= 0x01
	return broken
}

func TestGetVCPRefusesAReplyItCannotTrust(t *testing.T) {
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
		{name: "the answer to another VCP code", reply: corrupt(good, 4, 0x12)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDDCFixture(t,
				busAnswer{reply: c.reply}, busAnswer{reply: c.reply}, busAnswer{reply: c.reply})

			_, _, err := fixture.client.GetVCP(0x10)
			if !errors.Is(err, ErrGarbledReply) {
				t.Fatalf("err = %v, want a garbled reply", err)
			}
			// The client asked three times before it reported the
			// failure. The retries are what separate a noisy wire,
			// which answers well eventually, from a monitor that
			// speaks no DDC/CI at all.
			if len(fixture.bus.writes) != ddcGetAttempts {
				t.Errorf("wrote %d messages, want %d", len(fixture.bus.writes), ddcGetAttempts)
			}
		})
	}
}

func TestGetVCPRetriesUntilTheDisplayAnswersWell(t *testing.T) {
	good := getReply(0x10, 50, 100)
	fixture := newDDCFixture(t,
		busAnswer{reply: withBadChecksum(good)},
		busAnswer{reply: good},
	)

	current, max, err := fixture.client.GetVCP(0x10)
	if err != nil {
		t.Fatal(err)
	}
	if current != 50 || max != 100 {
		t.Errorf("got %d of %d, want 50 of 100", current, max)
	}
	if len(fixture.bus.writes) != 2 {
		t.Errorf("wrote %d messages, want 2", len(fixture.bus.writes))
	}
	// The waits, in order: the reply delay of the first attempt, the
	// delay the specification puts before a retry, and the second
	// attempt's reply delay, which is twice the first.
	want := []time.Duration{ddcReplyDelay, ddcRetryDelay, 2 * ddcReplyDelay}
	if !slices.Equal(fixture.slept, want) {
		t.Errorf("waited %v, want %v", fixture.slept, want)
	}
}

// The lab's LG answers DDC/CI and answers it late. It passes
// ddcutil, which waits generously, and it read as a panel with no
// DDC/CI here until the reply delay grew across the attempts. The
// first attempt keeps the specification's 40ms, so a panel that
// answers on time costs nothing.
func TestGetVCPWaitsLongerOnEachAttempt(t *testing.T) {
	cases := []struct {
		name     string
		answers  time.Duration
		attempts int
	}{
		{name: "a panel that answers on time", answers: ddcReplyDelay, attempts: 1},
		{name: "a panel that answers at twice the delay", answers: 2 * ddcReplyDelay, attempts: 2},
		{name: "a panel that answers at four times the delay", answers: 4 * ddcReplyDelay, attempts: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := &slowPanel{answers: c.answers, reply: getReply(0x10, 50, 100)}
			client := newDDC(panel)
			client.sleep = func(waited time.Duration) { panel.waited = waited }

			current, max, err := client.GetVCP(0x10)
			if err != nil {
				t.Fatal(err)
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

// A panel that answers only once the host has waited long
// enough. The wire is silent before that, which is the same thing an
// absent panel looks like, and the whole point of waiting longer.
type slowPanel struct {
	answers time.Duration
	waited  time.Duration
	reply   []byte
	reads   int
}

func (p *slowPanel) Write([]byte) error { return nil }

func (p *slowPanel) Read(reply []byte) error {
	p.reads++
	if p.waited < p.answers {
		copy(reply, bytes.Repeat([]byte{0xff}, len(reply)))
		return nil
	}
	copy(reply, p.reply)
	return nil
}

func TestGetVCPTellsSilenceFromGarbage(t *testing.T) {
	// A caller treats the two errors differently. Silence on the
	// address means this monitor carries no DDC/CI. That is a fact
	// the probe publishes, and a retry cannot change it.
	cases := []struct {
		name     string
		writeErr error
		answer   busAnswer
	}{
		{name: "the address answers no write", writeErr: unix.ENXIO},
		{name: "the read fails on the wire", answer: busAnswer{err: unix.EREMOTEIO}},
		{name: "an undriven wire reads as all ones", answer: busAnswer{reply: bytes.Repeat([]byte{0xff}, getReplyLength)}},
		{name: "a wire held low reads as all zeros", answer: busAnswer{reply: make([]byte, getReplyLength)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDDCFixture(t, c.answer, c.answer, c.answer)
			fixture.bus.writeErr = c.writeErr

			_, _, err := fixture.client.GetVCP(0x10)
			if !errors.Is(err, ErrNoAnswer) {
				t.Fatalf("err = %v, want no answer", err)
			}
			if errors.Is(err, ErrGarbledReply) {
				t.Errorf("err = %v, and silence is not garbage", err)
			}
		})
	}
}

func TestGetVCPReportsAControlTheDisplayDoesNotCarry(t *testing.T) {
	unsupported := corrupt(getReply(0x10, 0, 0), 3, vcpResultUnsupported)
	fixture := newDDCFixture(t, busAnswer{reply: unsupported})

	_, _, err := fixture.client.GetVCP(0x10)
	if !errors.Is(err, ErrUnsupportedVCP) {
		t.Fatalf("err = %v, want an unsupported code", err)
	}
	// "Unsupported" is the display's own answer, so asking again gets
	// the same answer, and the client asks once.
	if len(fixture.bus.writes) != 1 {
		t.Errorf("wrote %d messages, want 1", len(fixture.bus.writes))
	}
}

func TestSetVCPSpeaksTheSpecPacket(t *testing.T) {
	fixture := newDDCFixture(t)

	if err := fixture.client.SetVCP(0x10, 50); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x51, 0x84, 0x03, 0x10, 0x00, 0x32, 0x9a}
	if len(fixture.bus.writes) != 1 || !bytes.Equal(fixture.bus.writes[0], want) {
		t.Errorf("wrote %#x, want one message of %#x", fixture.bus.writes, want)
	}
	// A display sends nothing back after a Set. The wait is the 50ms
	// the display takes to act on the write before it accepts the
	// next message.
	if len(fixture.slept) != 1 || fixture.slept[0] != ddcSetDelay {
		t.Errorf("waited %v, want one wait of %v", fixture.slept, ddcSetDelay)
	}
	if fixture.bus.reads != 0 {
		t.Errorf("read %d replies, want none", fixture.bus.reads)
	}
}

func TestSetVCPThenReadsTheValueBack(t *testing.T) {
	// This is the pattern a claim runs: write the value, then ask the
	// display what it holds now. A display that clamps or drops a
	// value acknowledges the write all the same, so the readback is
	// the only proof.
	fixture := newDDCFixture(t, busAnswer{reply: getReply(0x10, 50, 100)})

	if err := fixture.client.SetVCP(0x10, 50); err != nil {
		t.Fatal(err)
	}
	current, _, err := fixture.client.GetVCP(0x10)
	if err != nil {
		t.Fatal(err)
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

func TestDDCChecksumSeeds(t *testing.T) {
	// The seed is the only part of the checksum that differs between
	// the directions. A request seeds with 0x6e, the address byte the
	// kernel drives. A reply seeds with 0x50, the virtual host
	// address, even though the wire carried 0x6f.
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

// fakeDDCSysfs builds the part of sysfs the mapping reads: one
// directory per connector, holding a ddc symlink when the map's value
// names a target and nothing when the value is empty.
func fakeDDCSysfs(t *testing.T, card string, links map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for connector, target := range links {
		dir := filepath.Join(root, "class", "drm", card+"-"+connector)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if target == "" {
			continue
		}
		if err := os.Symlink(target, filepath.Join(dir, "ddc")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestConnectorBusFollowsTheDDCLink(t *testing.T) {
	// The link takes two forms on real machines: i915 writes a bare
	// adapter name, and other drivers write a relative path up to the
	// adapter's directory. Both name the same adapter.
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
		// A connector with no link is one the driver gives no I2C
		// channel, which is every DisplayPort connector behind an
		// MST hub.
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
