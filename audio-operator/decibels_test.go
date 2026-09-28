package main

import (
	"encoding/binary"
	"os"
	"testing"
	"unsafe"
)

// The request number that reads a control's TLV payload, and
// the size of the header it carries.
func TestTLVIoctlNumber(t *testing.T) {
	if got, want := ctlIoctlTLVRead, uintptr(0xc008551a); got != want {
		t.Errorf("SNDRV_CTL_IOCTL_TLV_READ = %#x, want %#x", got, want)
	}
	if got, want := uintptr(ctlTLVHeaderSize), unsafe.Sizeof(uint32(0))*2; got != want {
		t.Errorf("sizeof(struct snd_ctl_tlv) = %d, want %d", got, want)
	}
}

// tlvFixture reads a TLV payload the card would answer TLV_READ with.
// The files hold 32-bit words in the machine's own order.
func tlvFixture(t *testing.T, name string) []uint32 {
	t.Helper()
	raw, err := os.ReadFile("testdata/mixer/" + name)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]uint32, len(raw)/4)
	for i := range payload {
		payload[i] = binary.NativeEndian.Uint32(raw[i*4 : i*4+4])
	}
	return payload
}

// tlvWord writes one signed level the way a TLV payload carries it,
// as the bits of a 32-bit word.
func tlvWord(value int32) uint32 { return uint32(value) }

// The four dB TLV shapes a card writes, and the outer bounds
// rule for a range of ranges.
func TestDecibelRangeFromTLV(t *testing.T) {
	cases := []struct {
		name     string
		payload  []uint32
		min, max int64
		absent   bool
		want     decibelRange
	}{
		{
			name:    "a scale runs from its minimum by its step",
			payload: tlvFixture(t, "tlv-db-scale-master.bin"),
			min:     0, max: 87,
			want: decibelRange{Low: -6525, High: 0},
		},
		{
			name:    "a minimum and maximum are stated outright",
			payload: tlvFixture(t, "tlv-db-minmax-pcm.bin"),
			min:     0, max: 127,
			want: decibelRange{Low: -6000, High: 0},
		},
		{
			name:    "a range of ranges reports the outer bounds",
			payload: tlvFixture(t, "tlv-db-range-capture.bin"),
			min:     0, max: 63,
			want: decibelRange{Low: -1725, High: 5200},
		},
		{
			name:    "a linear scale states both ends",
			payload: []uint32{tlvTypeDBLinear, 8, tlvWord(-9000), 0},
			min:     0, max: 65535,
			want: decibelRange{Low: -9000, High: 0},
		},
		{
			name:    "a scale whose minimum mutes reports no minimum",
			payload: []uint32{tlvTypeDBScale, 8, tlvWord(gainMute), 100},
			min:     0, max: 10,
			want: decibelRange{Low: gainMute, High: gainMute + 1000},
		},
		{
			name:    "a container carries the scale inside it",
			payload: []uint32{tlvTypeContainer, 16, tlvTypeDBMinMax, 8, tlvWord(-3000), tlvWord(600)},
			min:     0, max: 100,
			want: decibelRange{Low: -3000, High: 600},
		},
		{name: "a payload too short to hold a header declares nothing", payload: []uint32{1}, absent: true},
		{name: "a type this operator does not read declares nothing", payload: []uint32{0x1000, 4, 0}, absent: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := decibelRangeFrom(c.payload, c.min, c.max)
			if ok == c.absent {
				t.Fatalf("a range was read = %v, want %v", ok, !c.absent)
			}
			if !c.absent && got != c.want {
				t.Errorf("range = %+v, want %+v", got, c.want)
			}
		})
	}
}

// The decimal string form, which keeps the hundredths the TLV
// states and drops the zeros it does not.
func TestFormatDecibels(t *testing.T) {
	cases := []struct {
		hundredths int32
		want       string
	}{
		{hundredths: 0, want: "0"},
		{hundredths: -6525, want: "-65.25"},
		{hundredths: -1750, want: "-17.5"},
		{hundredths: 5200, want: "52"},
		{hundredths: 75, want: "0.75"},
		{hundredths: -5, want: "-0.05"},
	}
	for _, c := range cases {
		if got := formatDecibels(c.hundredths); got != c.want {
			t.Errorf("formatDecibels(%d) = %q, want %q", c.hundredths, got, c.want)
		}
	}
}
