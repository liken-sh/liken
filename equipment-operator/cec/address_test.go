package cec_test

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
)

func TestAPhysicalAddressReadsAndWritesTheDottedForm(t *testing.T) {
	cases := []struct {
		text    string
		address cec.PhysicalAddress
	}{
		{"0.0.0.0", 0x0000},
		{"1.0.0.0", 0x1000},
		{"1.3.0.0", 0x1300},
		{"a.b.c.d", 0xabcd},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			parsed, err := cec.ParsePhysicalAddress(c.text)
			if err != nil {
				t.Fatal(err)
			}
			if parsed != c.address {
				t.Errorf("parsed %#x, want %#x", parsed, c.address)
			}
			if c.address.String() != c.text {
				t.Errorf("wrote %q, want %q", c.address.String(), c.text)
			}
		})
	}
}

func TestParsePhysicalAddressRefusesWhatNoDeviceAnnounces(t *testing.T) {
	cases := []string{"", "1.3.0", "1.3.0.0.0", "1.g.0.0", "1.10.0.0", "f.f.f.f"}
	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			if _, err := cec.ParsePhysicalAddress(text); err == nil {
				t.Errorf("%q parsed with no error", text)
			}
		})
	}
}

func TestALogicalAddressImpliesItsDeviceType(t *testing.T) {
	cases := []struct {
		address cec.LogicalAddress
		kind    cec.DeviceType
	}{
		{0, cec.TypeTV},
		{1, cec.TypeRecording},
		{3, cec.TypeTuner},
		{4, cec.TypePlayback},
		{5, cec.TypeAudioSystem},
		{8, cec.TypePlayback},
		{11, cec.TypePlayback},
		{12, cec.TypeBackup},
		{14, cec.TypeSpecific},
		{15, cec.TypeUnregistered},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			if got := c.address.Type(); got != c.kind {
				t.Errorf("address %d is %s, want %s", c.address, got, c.kind)
			}
		})
	}
}

// One address is above another when the other is in the subtree below
// it: a receiver at 1.0.0.0 is above a machine on its input 3 at
// 1.3.0.0, and a machine straight on the TV's input 2 at 2.0.0.0 is
// below the TV and not below that receiver.
func TestAnAddressIsAboveTheAddressesBelowIt(t *testing.T) {
	cases := []struct {
		above, below cec.PhysicalAddress
		want         bool
	}{
		{0x0000, 0x1300, true},
		{0x1000, 0x1300, true},
		{0x1000, 0x1320, true},
		{0x1300, 0x1320, true},
		{0x1000, 0x2000, false},
		{0x1000, 0x1000, false},
		{0x1300, 0x1000, false},
		{0x1300, 0x1400, false},
		{0x0000, 0x0000, false},
	}
	for _, c := range cases {
		t.Run(c.above.String()+" over "+c.below.String(), func(t *testing.T) {
			if got := c.above.Above(c.below); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
