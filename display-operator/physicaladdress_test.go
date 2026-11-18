package main

// TestParseEDIDReadsRealMonitors in edid_test.go reads addresses from
// recorded EDIDs. The tests here build the vendor block by hand, so
// each one can choose the address bytes.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// ceaExtension builds a CTA-861 extension with a correct data block
// offset and checksum.
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

func ceaBlock(tag int, payload ...byte) []byte {
	return append([]byte{byte(tag<<5 | len(payload))}, payload...)
}

func hdmiVendorBlock(high, low byte) []byte {
	return ceaBlock(3, 0x03, 0x0c, 0x00, high, low)
}

// baseBlockOf returns the base block of a recorded EDID, so a test can
// append an extension of its own.
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
			name: "a vendor block after other data blocks",
			blocks: [][]byte{
				ceaBlock(2, 0x90, 0x04, 0x03),
				ceaBlock(1, 0x09, 0x07, 0x07),
				hdmiVendorBlock(0x11, 0x00),
			},
			want: "1.1.0.0",
		},
		{
			name:   "the root address",
			blocks: [][]byte{hdmiVendorBlock(0x00, 0x00)},
		},
		{
			name:   "f.f.f.f",
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
			// The header says the payload is five bytes, but the data
			// blocks end after three of them.
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
				t.Fatalf("parsing the EDID: %v", err)
			}
			if edid.PhysicalAddress != c.want {
				t.Errorf("PhysicalAddress = %q, want %q", edid.PhysicalAddress, c.want)
			}
		})
	}
}

// The base block's extension count says that an extension follows, so
// the parse must stop at the bytes it has.
func TestAnEDIDWithNoExtensionHasNoPhysicalAddress(t *testing.T) {
	edid, err := ParseEDID(baseBlockOf(t, "lg-hdr-wqhd"))
	if err != nil {
		t.Fatalf("parsing the EDID: %v", err)
	}
	if edid.PhysicalAddress != "" {
		t.Errorf("PhysicalAddress = %q, want empty", edid.PhysicalAddress)
	}
}

// edidStep is what the connector serves on one pass. An empty address
// is an EDID with no valid address, and disconnected is no EDID.
type edidStep struct {
	address      string
	disconnected bool
}

// The fixture's clock starts at the Unix epoch and each step runs one
// minute after the last, so the time in a message identifies the step
// where the connector stopped serving the address.
func TestTheDisplayReportsThePhysicalAddress(t *testing.T) {
	cases := []struct {
		name    string
		steps   []edidStep
		address string
		status  conditions.Status
		reason  string
		message string
	}{
		{
			name:    "an address the EDID serves",
			steps:   []edidStep{{address: "1.2.0.0"}},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "The EDID on HDMI-A-1 has the physical address 1.2.0.0",
		},
		{
			// A receiver in standby stops serving its EDID, but the
			// machine is still on the same input.
			name:    "a connector that stops serving the EDID",
			steps:   []edidStep{{address: "1.2.0.0"}, {disconnected: true}},
			address: "1.2.0.0",
			status:  conditionFalse,
			reason:  RetainedReason,
			message: "HDMI-A-1 no longer reports this monitor's EDID. status.physicalAddress keeps the address that the EDID had until 1970-01-01T00:01:00Z",
		},
		{
			name:    "an EDID that stops serving an address",
			steps:   []edidStep{{address: "1.2.0.0"}, {address: ""}},
			address: "1.2.0.0",
			status:  conditionFalse,
			reason:  RetainedReason,
			message: "The EDID on HDMI-A-1 has no valid physical address. status.physicalAddress keeps the address that the EDID had until 1970-01-01T00:01:00Z",
		},
		{
			name:    "a retained address across passes",
			steps:   []edidStep{{address: "1.2.0.0"}, {disconnected: true}, {disconnected: true}, {disconnected: true}},
			address: "1.2.0.0",
			status:  conditionFalse,
			reason:  RetainedReason,
			message: "HDMI-A-1 no longer reports this monitor's EDID. status.physicalAddress keeps the address that the EDID had until 1970-01-01T00:01:00Z",
		},
		{
			name:    "a connector that serves the address again",
			steps:   []edidStep{{address: "1.2.0.0"}, {disconnected: true}, {address: "1.2.0.0"}},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "The EDID on HDMI-A-1 has the physical address 1.2.0.0",
		},
		{
			// A cable moved to another input of the receiver.
			name:    "a connector that serves another address",
			steps:   []edidStep{{address: "1.2.0.0"}, {address: "1.3.0.0"}},
			address: "1.3.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "The EDID on HDMI-A-1 has the physical address 1.3.0.0",
		},
		{
			// A DisplayPort monitor serves no HDMI vendor block.
			name:  "a monitor that never served an address",
			steps: []edidStep{{address: ""}, {disconnected: true}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
			for _, step := range c.steps {
				f.serve(step)
				if err := f.pass(); err != nil {
					t.Fatalf("running a pass: %v", err)
				}
				f.advance(time.Minute)
			}

			display := f.display()
			if display.Status.PhysicalAddress != c.address {
				t.Errorf("physicalAddress = %q, want %q", display.Status.PhysicalAddress, c.address)
			}
			// condition returns the zero condition when there is none,
			// so a case with no status expects no condition.
			got := condition(display, PhysicalAddressCurrentCondition)
			if got.Status != c.status || got.Reason != c.reason || got.Message != c.message {
				t.Errorf("condition = %q %q %q, want %q %q %q",
					got.Status, got.Reason, got.Message, c.status, c.reason, c.message)
			}
		})
	}
}

func TestARetainedAddressWritesNothingOnTheNextPass(t *testing.T) {
	f := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	for _, step := range []edidStep{{address: "1.2.0.0"}, {address: ""}} {
		f.serve(step)
		if err := f.pass(); err != nil {
			t.Fatalf("running a pass: %v", err)
		}
		f.advance(time.Minute)
	}
	before := len(f.lines())

	if err := f.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	for _, line := range f.lines()[before:] {
		if strings.HasPrefix(line, "status ") {
			t.Errorf("a pass with an unchanged retained address wrote %q, want no status write", line)
		}
	}
}

func (f *displayFixture) serve(step edidStep) {
	f.present[f.wired[0].Connector] = !step.disconnected
	f.wired[0].Monitor.PhysicalAddress = step.address
}

// The cases follow Pulse-Eight's two-cable setup, which
// ambiguousAddresses in physicaladdress.go describes.
func TestTheDisplayDoesNotPublishAnAmbiguousAddress(t *testing.T) {
	cases := []struct {
		name    string
		wired   []wiredPanel
		address string
		status  conditions.Status
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
			message: "HDMI-A-1 reports 1.2.0.0 and HDMI-A-2 reports 1.1.0.0 for this monitor. " +
				"status.physicalAddress is not updated while the connectors report different addresses",
		},
		{
			name: "the same two connectors, found in the other order",
			wired: []wiredPanel{
				address(t, "HDMI-A-2", "1.1.0.0"),
				address(t, "HDMI-A-1", "1.2.0.0"),
			},
			status: conditionFalse,
			reason: AmbiguousReason,
			message: "HDMI-A-1 reports 1.2.0.0 and HDMI-A-2 reports 1.1.0.0 for this monitor. " +
				"status.physicalAddress is not updated while the connectors report different addresses",
		},
		{
			// The Display reads the connector that is last in the
			// card's list.
			name: "two connectors serving the same address",
			wired: []wiredPanel{
				address(t, "HDMI-A-1", "1.2.0.0"),
				address(t, "HDMI-A-2", "1.2.0.0"),
			},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "The EDID on HDMI-A-2 has the physical address 1.2.0.0",
		},
		{
			name: "one connector with no valid address",
			wired: []wiredPanel{
				address(t, "HDMI-A-1", ""),
				address(t, "HDMI-A-2", "1.2.0.0"),
			},
			address: "1.2.0.0",
			status:  conditionTrue,
			reason:  ReadFromEDIDReason,
			message: "The EDID on HDMI-A-2 has the physical address 1.2.0.0",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayBench(t, c.wired...)

			if err := fixture.pass(); err != nil {
				t.Fatalf("running a pass: %v", err)
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

// address returns a connector that serves labMonitor's EDID with the
// given physical address.
func address(t *testing.T, connector, physicalAddress string) wiredPanel {
	t.Helper()
	monitor := labMonitor()
	monitor.PhysicalAddress = physicalAddress
	return wiredPanel{Connector: connector, Monitor: monitor, Panel: recordedPanel(t, "lg-hdr-wqhd")}
}

func TestAnAmbiguousAddressKeepsTheLastKnownGoodAddress(t *testing.T) {
	fixture := newDisplayBench(t,
		address(t, "HDMI-A-1", "1.2.0.0"),
		address(t, "HDMI-A-2", "1.1.0.0"),
	)
	fixture.present["HDMI-A-2"] = false

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if got := fixture.display().Status.PhysicalAddress; got != "1.2.0.0" {
		t.Fatalf("physicalAddress = %q, want 1.2.0.0 before the second cable arrives", got)
	}

	fixture.present["HDMI-A-2"] = true
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
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
