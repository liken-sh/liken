package main

// The two lab panels serve the physical address of the port this
// machine's cable is in: the ultrawide serves 2.0.0.0 and the portable
// serves 1.0.0.0, and TestParseEDIDReadsRealMonitors reads both from
// their own bytes. The tests here build the vendor block by hand, so
// each one states the bytes a sink could serve and reads what the
// operator makes of them.

import (
	"strings"
	"testing"
	"time"
)

// An extension block built by hand, so a test can state its data
// blocks and read what the walk makes of them.
func ceaExtension(blocks ...[]byte) []byte {
	extension := []byte{0x02, 0x03, 0x00, 0x00}
	for _, block := range blocks {
		extension = append(extension, block...)
	}
	extension[2] = byte(len(extension))
	extension = append(extension, make([]byte, blockSize-len(extension))...)
	var sum byte
	for _, b := range extension[:blockSize-1] {
		sum += b
	}
	extension[blockSize-1] = -sum
	return extension
}

// One data block: the tag in the top three bits, the payload
// length in the low five.
func ceaBlock(tag int, payload ...byte) []byte {
	return append([]byte{byte(tag<<5 | len(payload))}, payload...)
}

// The HDMI vendor block with one address, as a sink serves it: the
// OUI least significant byte first, then the address's two bytes.
func hdmiVendorBlock(high, low byte) []byte {
	return ceaBlock(3, 0x03, 0x0c, 0x00, high, low)
}

// One fixture's first block, the part that carries the identity,
// with whatever extension a test wants behind it.
func baseBlockOf(t *testing.T, fixture string) []byte {
	t.Helper()
	return loadEDID(t, fixture)[:blockSize]
}

func TestParseEDIDReadsThePhysicalAddress(t *testing.T) {
	cases := []struct {
		name   string
		blocks [][]byte
		want   string
	}{
		{
			name:   "a port of the sink itself",
			blocks: [][]byte{hdmiVendorBlock(0x20, 0x00)},
			want:   "2.0.0.0",
		},
		{
			// The bytes the 2026-09-26 drill read on the connector that
			// carries the picture to a receiver.
			name:   "a machine behind a receiver",
			blocks: [][]byte{hdmiVendorBlock(0x12, 0x00)},
			want:   "1.2.0.0",
		},
		{
			name:   "three levels deep",
			blocks: [][]byte{hdmiVendorBlock(0x12, 0x30)},
			want:   "1.2.3.0",
		},
		{
			name:   "four levels deep",
			blocks: [][]byte{hdmiVendorBlock(0x12, 0x34)},
			want:   "1.2.3.4",
		},
		{
			name:   "the digits above nine",
			blocks: [][]byte{hdmiVendorBlock(0xab, 0xc0)},
			want:   "a.b.c.0",
		},
		{
			// A video block and an audio block before the vendor block,
			// so the walk has to step over blocks it does not read.
			name: "a vendor block after other data blocks",
			blocks: [][]byte{
				ceaBlock(2, 0x90, 0x04, 0x03),
				ceaBlock(1, 0x09, 0x07, 0x07),
				hdmiVendorBlock(0x11, 0x00),
			},
			want: "1.1.0.0",
		},
		{
			// The TV's own address, and the one a sink serves when it
			// states none.
			name:   "the root address",
			blocks: [][]byte{hdmiVendorBlock(0x00, 0x00)},
		},
		{
			name:   "the invalid address",
			blocks: [][]byte{hdmiVendorBlock(0xff, 0xff)},
		},
		{
			name:   "a digit after a zero in the second place",
			blocks: [][]byte{hdmiVendorBlock(0x01, 0x00)},
		},
		{
			name:   "a digit after a zero in the last place",
			blocks: [][]byte{hdmiVendorBlock(0x10, 0x02)},
		},
		{
			name:   "a vendor block cut short before the address",
			blocks: [][]byte{ceaBlock(3, 0x03, 0x0c, 0x00, 0x12)},
		},
		{
			// The header states five bytes of payload, and the data
			// block collection ends after three of them.
			name:   "a vendor block that runs past the data blocks",
			blocks: [][]byte{{3<<5 | 5, 0x03, 0x0c, 0x00}},
		},
		{
			name:   "another vendor's block",
			blocks: [][]byte{ceaBlock(3, 0xd8, 0x5d, 0xc4, 0x12, 0x00)},
		},
		{
			name:   "a block that is not a vendor block",
			blocks: [][]byte{ceaBlock(2, 0x03, 0x0c, 0x00, 0x12, 0x00)},
		},
		{
			name:   "an extension with no vendor block",
			blocks: [][]byte{ceaBlock(2, 0x90, 0x04, 0x03)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := append(baseBlockOf(t, "lg-hdr-wqhd"), ceaExtension(c.blocks...)...)

			edid, err := ParseEDID(raw)
			if err != nil {
				t.Fatal(err)
			}
			if edid.PhysicalAddress != c.want {
				t.Errorf("PhysicalAddress = %q, want %q", edid.PhysicalAddress, c.want)
			}
		})
	}
}

// A monitor that serves one block and no extension states no address.
// The base block's own extension count says one follows, so the parse
// must stop at the bytes it holds.
func TestAnEDIDWithNoExtensionStatesNoPhysicalAddress(t *testing.T) {
	edid, err := ParseEDID(baseBlockOf(t, "lg-hdr-wqhd"))
	if err != nil {
		t.Fatal(err)
	}
	if edid.PhysicalAddress != "" {
		t.Errorf("PhysicalAddress = %q, want none", edid.PhysicalAddress)
	}
}

// What one connector serves on one pass of the bench: an address, no
// valid address, or no EDID at all.
type edidStep struct {
	address      string
	disconnected bool
}

// The clock of the bench starts at the epoch, and each step of these
// tests runs one minute after the last, so the message's time is the
// step where the connector stopped serving the address.
func TestTheDisplayReportsThePhysicalAddress(t *testing.T) {
	cases := []struct {
		name    string
		steps   []edidStep
		address string
		status  string
		reason  string
		message string
	}{
		{
			name:    "an address the EDID serves",
			steps:   []edidStep{{address: "1.2.0.0"}},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "HDMI-A-1 serves 1.2.0.0 in its EDID",
		},
		{
			// A receiver in standby stops serving its EDID, and the
			// machine's port has not moved.
			name:    "a connector that stops serving the EDID",
			steps:   []edidStep{{address: "1.2.0.0"}, {disconnected: true}},
			address: "1.2.0.0",
			status:  conditionFalse,
			reason:  RetainedReason,
			message: "HDMI-A-1 no longer serves this monitor's EDID; this is the address it served until 1970-01-01T00:01:00Z",
		},
		{
			name:    "an EDID that stops stating an address",
			steps:   []edidStep{{address: "1.2.0.0"}, {address: ""}},
			address: "1.2.0.0",
			status:  conditionFalse,
			reason:  RetainedReason,
			message: "HDMI-A-1 serves no valid physical address in its EDID; this is the address it served until 1970-01-01T00:01:00Z",
		},
		{
			// The time stays the step where the address stopped, however
			// long the receiver sleeps.
			name:    "a retained address across passes",
			steps:   []edidStep{{address: "1.2.0.0"}, {disconnected: true}, {disconnected: true}, {disconnected: true}},
			address: "1.2.0.0",
			status:  conditionFalse,
			reason:  RetainedReason,
			message: "HDMI-A-1 no longer serves this monitor's EDID; this is the address it served until 1970-01-01T00:01:00Z",
		},
		{
			name:    "a connector that serves the address again",
			steps:   []edidStep{{address: "1.2.0.0"}, {disconnected: true}, {address: "1.2.0.0"}},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "HDMI-A-1 serves 1.2.0.0 in its EDID",
		},
		{
			// A cable moved to another input of the receiver.
			name:    "a connector that serves another address",
			steps:   []edidStep{{address: "1.2.0.0"}, {address: "1.3.0.0"}},
			address: "1.3.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "HDMI-A-1 serves 1.3.0.0 in its EDID",
		},
		{
			// A DisplayPort monitor serves no HDMI vendor block.
			name:  "a monitor that never served an address",
			steps: []edidStep{{address: ""}, {disconnected: true}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
			for _, step := range c.steps {
				f.serve(step)
				if err := f.pass(); err != nil {
					t.Fatal(err)
				}
				f.advance(time.Minute)
			}

			display := f.display()
			if display.Status.PhysicalAddress != c.address {
				t.Errorf("physicalAddress = %q, want %q", display.Status.PhysicalAddress, c.address)
			}
			// A case that states no condition wants none at all, and the
			// lookup answers the zero condition for one that is absent.
			got := condition(display, PhysicalAddressCurrentCondition)
			if got.Status != c.status || got.Reason != c.reason || got.Message != c.message {
				t.Errorf("condition = %q %q %q, want %q %q %q",
					got.Status, got.Reason, got.Message, c.status, c.reason, c.message)
			}
		})
	}
}

// A pass over a retained address writes nothing. The message states a
// time, and a time read from the clock on every pass would make every
// pass a status write.
func TestARetainedAddressWritesNothingOnTheNextPass(t *testing.T) {
	f := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	for _, step := range []edidStep{{address: "1.2.0.0"}, {address: ""}} {
		f.serve(step)
		if err := f.pass(); err != nil {
			t.Fatal(err)
		}
		f.advance(time.Minute)
	}
	before := len(f.lines())

	if err := f.pass(); err != nil {
		t.Fatal(err)
	}

	for _, line := range f.lines()[before:] {
		if strings.HasPrefix(line, "status ") {
			t.Errorf("a steady retained address wrote %q", line)
		}
	}
}

// The bench's one connector serves what a step states.
func (f *displayFixture) serve(step edidStep) {
	f.present[f.wired[0].Connector] = !step.disconnected
	f.wired[0].Monitor.PhysicalAddress = step.address
}

// Pulse-Eight's two-cable setup: one cable carries the picture into a
// receiver input, and a second, through a CEC adapter, into another
// input of the same receiver. Both connectors answer to the
// receiver's own EDID for the panel, so they share one monitor
// identity and each serves the address of the input it is plugged
// into.
func TestTheDisplayRefusesAnAmbiguousAddress(t *testing.T) {
	cases := []struct {
		name    string
		wired   []wiredPanel
		address string
		status  string
		reason  string
		message string
	}{
		{
			name: "two connectors serving different addresses",
			wired: []wiredPanel{
				address(t, "HDMI-A-1", "1.2.0.0"),
				address(t, "HDMI-A-2", "1.1.0.0"),
			},
			status: conditionFalse,
			reason: AmbiguousReason,
			message: "HDMI-A-1 serves 1.2.0.0 and HDMI-A-2 serves 1.1.0.0 for this monitor; " +
				"the address is not published while more than one connector serves it",
		},
		{
			// The message names the connectors in the same order
			// whichever one this card happened to enumerate first.
			name: "the same two connectors, found in the other order",
			wired: []wiredPanel{
				address(t, "HDMI-A-2", "1.1.0.0"),
				address(t, "HDMI-A-1", "1.2.0.0"),
			},
			status: conditionFalse,
			reason: AmbiguousReason,
			message: "HDMI-A-1 serves 1.2.0.0 and HDMI-A-2 serves 1.1.0.0 for this monitor; " +
				"the address is not published while more than one connector serves it",
		},
		{
			// The same address on both connectors is not a
			// disagreement, so the merge's own choice of connector
			// reports it as it would for one connector alone.
			name: "two connectors serving the same address",
			wired: []wiredPanel{
				address(t, "HDMI-A-1", "1.2.0.0"),
				address(t, "HDMI-A-2", "1.2.0.0"),
			},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "HDMI-A-2 serves 1.2.0.0 in its EDID",
		},
		{
			// A connector with no valid address takes no side, so a
			// monitor lit on one connector and dark on the other is
			// not ambiguous either.
			name: "one connector dark",
			wired: []wiredPanel{
				address(t, "HDMI-A-1", ""),
				address(t, "HDMI-A-2", "1.2.0.0"),
			},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "HDMI-A-2 serves 1.2.0.0 in its EDID",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayBench(t, c.wired...)

			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			display := fixture.display()
			if display.Status.PhysicalAddress != c.address {
				t.Errorf("physicalAddress = %q, want %q", display.Status.PhysicalAddress, c.address)
			}
			got := condition(display, PhysicalAddressCurrentCondition)
			if got.Status != c.status || got.Reason != c.reason || got.Message != c.message {
				t.Errorf("condition = %q %q %q, want %q %q %q",
					got.Status, got.Reason, got.Message, c.status, c.reason, c.message)
			}
		})
	}
}

// One connector of the bench, serving the lab monitor's identity
// at the given physical address.
func address(t *testing.T, connector, physicalAddress string) wiredPanel {
	t.Helper()
	monitor := labMonitor()
	monitor.PhysicalAddress = physicalAddress
	return wiredPanel{Connector: connector, Monitor: monitor, Panel: drillPanel(t, "lg-hdr-wqhd")}
}

// An ambiguous pass never overwrites a physical address a CEC
// consumer may already be relying on: the field keeps the last one
// this Display served while its connectors disagree, the same way it
// keeps the last one while a connector goes dark.
func TestAnAmbiguousAddressKeepsTheLastKnownGoodAddress(t *testing.T) {
	fixture := newDisplayBench(t,
		address(t, "HDMI-A-1", "1.2.0.0"),
		address(t, "HDMI-A-2", "1.1.0.0"),
	)
	// The second connector's picture is not there yet on the first
	// pass, so the first cable is the only one the monitor answers.
	fixture.present["HDMI-A-2"] = false

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if got := fixture.display().Status.PhysicalAddress; got != "1.2.0.0" {
		t.Fatalf("physicalAddress = %q, want 1.2.0.0 before the second cable arrives", got)
	}

	// The second cable arrives serving a different input, so the
	// two connectors now disagree.
	fixture.present["HDMI-A-2"] = true
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	display := fixture.display()
	if got := display.Status.PhysicalAddress; got != "1.2.0.0" {
		t.Errorf("physicalAddress = %q, want the retained 1.2.0.0, not the ambiguous read", got)
	}
	got := condition(display, PhysicalAddressCurrentCondition)
	if got.Status != conditionFalse || got.Reason != AmbiguousReason {
		t.Errorf("condition = %q %q, want %q %q", got.Status, got.Reason, conditionFalse, AmbiguousReason)
	}
}
