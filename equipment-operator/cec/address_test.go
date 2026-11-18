package cec_test

import (
	"testing"

	"github.com/liken-sh/liken/equipment-operator/cec"
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
				t.Fatalf("parsing %q: %v", c.text, err)
			}
			if parsed != c.address {
				t.Errorf("ParsePhysicalAddress(%q) = %#x, want %#x", c.text, parsed, c.address)
			}
			if c.address.String() != c.text {
				t.Errorf("String() of %#x is %q, want %q", c.address, c.address.String(), c.text)
			}
		})
	}
}

func TestParsePhysicalAddressReturnsAnErrorForWhatNoDeviceAnnounces(t *testing.T) {
	cases := []string{"", "1.3.0", "1.3.0.0.0", "1.g.0.0", "1.10.0.0", "f.f.f.f"}
	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			if _, err := cec.ParsePhysicalAddress(text); err == nil {
				t.Errorf("ParsePhysicalAddress(%q) returned no error, want one", text)
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
				t.Errorf("logical address %d has type %s, want %s", c.address, got, c.kind)
			}
		})
	}
}

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
				t.Errorf("%s.Above(%s) is %v, want %v", c.above, c.below, got, c.want)
			}
		})
	}
}
