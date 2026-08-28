package main

import (
	"encoding/binary"
	"os"
	"slices"
	"testing"
	"unsafe"
)

// The ioctl number carries the argument size, and the value
// union's offset is where every reader of an element's information
// starts.
func TestMixerStructureLayout(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{name: "snd_ctl_elem_info.value", got: unsafe.Offsetof(ctlElemInfo{}.Value), want: 80},
		{name: "snd_ctl_elem_value.value", got: unsafe.Offsetof(ctlElemValue{}.Value), want: 72},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// The request number that writes an element's value.
func TestMixerIoctlNumber(t *testing.T) {
	if got, want := ctlIoctlElemWrite, uintptr(0xc4c85513); got != want {
		t.Errorf("SNDRV_CTL_IOCTL_ELEM_WRITE = %#x, want %#x", got, want)
	}
}

// integerInfo builds the element information the kernel returns for an
// integer control, which holds its range in the value union.
func integerInfo(count uint32, min, max, step int64) ctlElemInfo {
	info := ctlElemInfo{Type: ctlElemTypeInteger, Count: count}
	binary.NativeEndian.PutUint64(info.Value[0:8], uint64(min))
	binary.NativeEndian.PutUint64(info.Value[8:16], uint64(max))
	binary.NativeEndian.PutUint64(info.Value[16:24], uint64(step))
	return info
}

// enumeratedInfo builds the element information for an enumerated
// control, which holds its item count in the same union.
func enumeratedInfo(count, items uint32) ctlElemInfo {
	info := ctlElemInfo{Type: ctlElemTypeEnumerated, Count: count}
	binary.NativeEndian.PutUint32(info.Value[0:4], items)
	return info
}

// What each element type becomes in status.capabilities, and
// which types have no spelling and are left out.
func TestCapabilityFromElementInfo(t *testing.T) {
	cases := []struct {
		name    string
		info    ctlElemInfo
		items   []string
		levels  *decibelRange
		skipped bool
		want    controlCapability
	}{
		{
			name: "a volume with a step and a dB range",
			info: integerInfo(2, 0, 87, 1),
			levels: &decibelRange{
				Low:  -6525,
				High: 0,
			},
			want: controlCapability{
				Type: capabilityInteger, Min: pointerTo(int64(0)), Max: pointerTo(int64(87)),
				Step: pointerTo(int64(1)), MinDecibels: "-65.25", MaxDecibels: "0", Channels: 2,
			},
		},
		{
			name: "a step of zero means every number in the range",
			info: integerInfo(1, 0, 3, 0),
			want: controlCapability{
				Type: capabilityInteger, Min: pointerTo(int64(0)), Max: pointerTo(int64(3)), Channels: 1,
			},
		},
		{
			name:   "a mute level is no level",
			info:   integerInfo(1, 0, 31, 1),
			levels: &decibelRange{Low: gainMute, High: 3000},
			want: controlCapability{
				Type: capabilityInteger, Min: pointerTo(int64(0)), Max: pointerTo(int64(31)),
				Step: pointerTo(int64(1)), MaxDecibels: "30", Channels: 1,
			},
		},
		{
			name: "a switch",
			info: ctlElemInfo{Type: ctlElemTypeBoolean, Count: 2},
			want: controlCapability{Type: capabilityBoolean, Channels: 2},
		},
		{
			name:  "an enumerated control carries its values",
			info:  enumeratedInfo(1, 3),
			items: []string{"Disabled", "Enabled", "Line Out+Speaker"},
			want: controlCapability{
				Type:     capabilityEnumerated,
				Values:   []string{"Disabled", "Enabled", "Line Out+Speaker"},
				Channels: 1,
			},
		},
		{name: "a block of bytes has no spelling", info: ctlElemInfo{Type: ctlElemTypeBytes, Count: 128}, skipped: true},
		{name: "IEC958 channel status has no spelling", info: ctlElemInfo{Type: 5, Count: 4}, skipped: true},
		{name: "a 64-bit integer has no spelling", info: ctlElemInfo{Type: 6, Count: 1}, skipped: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := capabilityFrom(c.info, c.items, c.levels)
			if ok == c.skipped {
				t.Fatalf("the element was taken = %v, want %v", ok, !c.skipped)
			}
			if c.skipped {
				return
			}
			if !sameCapability(got, c.want) {
				t.Errorf("capability = %+v, want %+v", got, c.want)
			}
		})
	}
}

// pointerTo takes the address of a value, which is how the capability
// tells a min of zero from no min at all.
func pointerTo[T any](value T) *T { return &value }

// sameCapability compares two capabilities through the pointers.
func sameCapability(a, b controlCapability) bool {
	same := func(x, y *int64) bool {
		return (x == nil) == (y == nil) && (x == nil || *x == *y)
	}
	return a.Type == b.Type && same(a.Min, b.Min) && same(a.Max, b.Max) && same(a.Step, b.Step) &&
		a.MinDecibels == b.MinDecibels && a.MaxDecibels == b.MaxDecibels &&
		slices.Equal(a.Values, b.Values) && a.Channels == b.Channels
}

// elementValue builds the union an element's value carries, with one
// number in every channel.
func elementValue(width, channels int, number int64) []byte {
	raw := make([]byte, 1024)
	for channel := range channels {
		switch width {
		case 8:
			binary.NativeEndian.PutUint64(raw[channel*8:], uint64(number))
		case 4:
			binary.NativeEndian.PutUint32(raw[channel*4:], uint32(number))
		}
	}
	return raw
}

// The spelling status.observed.controls reports, one string per
// control, and the first channel for a control that carries several.
func TestControlValueSpelling(t *testing.T) {
	volume := controlCapability{Type: capabilityInteger, Min: pointerTo(int64(0)), Max: pointerTo(int64(87)), Channels: 2}
	switching := controlCapability{Type: capabilityBoolean, Channels: 2}
	source := controlCapability{Type: capabilityEnumerated, Values: []string{"Mic", "Line", "Internal Mic"}, Channels: 1}

	cases := []struct {
		name       string
		capability controlCapability
		raw        []byte
		want       string
		fails      bool
	}{
		{name: "a level is its number", capability: volume, raw: elementValue(8, 2, 64), want: "64"},
		{name: "a switch on", capability: switching, raw: elementValue(8, 2, 1), want: "on"},
		{name: "a switch off", capability: switching, raw: elementValue(8, 2, 0), want: "off"},
		{name: "an enumerated control is its item name", capability: source, raw: elementValue(4, 1, 2), want: "Internal Mic"},
		{name: "an item the control does not declare", capability: source, raw: elementValue(4, 1, 9), fails: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := controlValueFrom(c.capability, c.raw)
			if c.fails {
				if err == nil {
					t.Fatalf("value = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("value = %q, want %q", got, c.want)
			}
		})
	}
}

// A write parses the same spelling back, refuses what the
// control does not accept, and sets every channel alike.
func TestEncodeControlValue(t *testing.T) {
	volume := controlCapability{Type: capabilityInteger, Min: pointerTo(int64(0)), Max: pointerTo(int64(87)), Channels: 2}
	switching := controlCapability{Type: capabilityBoolean, Channels: 2}
	source := controlCapability{Type: capabilityEnumerated, Values: []string{"Mic", "Line"}, Channels: 1}

	cases := []struct {
		name       string
		capability controlCapability
		value      string
		want       []byte
		fails      bool
	}{
		{name: "a level reaches both channels", capability: volume, value: "64", want: elementValue(8, 2, 64)},
		{name: "a level above the range", capability: volume, value: "88", fails: true},
		{name: "a level below the range", capability: volume, value: "-1", fails: true},
		{name: "a level that is not a number", capability: volume, value: "loud", fails: true},
		{name: "a switch on", capability: switching, value: "on", want: elementValue(8, 2, 1)},
		{name: "a switch off", capability: switching, value: "off", want: elementValue(8, 2, 0)},
		{name: "a switch takes on or off alone", capability: switching, value: "true", fails: true},
		{name: "an item by name", capability: source, value: "Line", want: elementValue(4, 1, 1)},
		{name: "an item the control does not declare", capability: source, value: "Headset", fails: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := make([]byte, 1024)
			err := encodeControlValue(c.capability, c.value, raw)
			if c.fails {
				if err == nil {
					t.Fatal("the write was accepted, and the control does not accept it")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(raw, c.want) {
				t.Errorf("the written union does not match the expected one")
			}
		})
	}
}

// A jack is read-only and feeds the Connected condition, so it
// is told from a capability by its interface and its name.
func TestJackControls(t *testing.T) {
	cases := []struct {
		name  string
		iface int32
		jack  bool
	}{
		{name: "Headphone Jack", iface: ctlElemIfaceCard, jack: true},
		{name: "HDMI/DP,pcm=3 Jack", iface: ctlElemIfaceCard, jack: true},
		{name: "Mic Jack", iface: ctlElemIfaceCard, jack: true},
		{name: "Master Playback Volume", iface: ctlElemIfaceMixer, jack: false},
		{name: "Jack", iface: ctlElemIfaceCard, jack: false},
		{name: "Headphone Jack", iface: ctlElemIfaceMixer, jack: false},
	}
	for _, c := range cases {
		if got := isJackControl(c.iface, c.name); got != c.jack {
			t.Errorf("isJackControl(%d, %q) = %v, want %v", c.iface, c.name, got, c.jack)
		}
	}
}

// A name the card does not declare, and a value the control
// does not accept, are both refused before anything reaches the card.
func TestTheCardRefusesWhatItDoesNotDeclare(t *testing.T) {
	card := &mixer{card: 0, byName: map[string]control{}}

	if value, err := card.readControl("No Such Control"); err == nil {
		t.Errorf("an undeclared control read as %q", value)
	}
	if err := card.writeControl("No Such Control", controlOn); err == nil {
		t.Error("an undeclared control accepted a write")
	}
	volume := control{Name: "Master Playback Volume",
		Capability: controlCapability{Type: capabilityInteger, Min: pointerTo(int64(0)), Max: pointerTo(int64(87)), Channels: 1}}
	if err := card.writeElement(volume, "loud"); err == nil {
		t.Error("a control that takes a number accepted a word")
	}
}

// The live check. It runs only where a card answers, and it
// reads every control the card declares without writing one.
func TestMixerReadsTheLocalCard(t *testing.T) {
	sndDir = "/dev/snd"
	t.Cleanup(func() { sndDir = "/dev/snd" })
	if _, err := os.Stat(sndDir + "/controlC0"); err != nil {
		t.Skip("this machine has no card 0 to read")
	}

	card, err := openMixer(0)
	if err != nil {
		t.Skipf("card 0 does not open for this user: %v", err)
	}
	defer card.Close()

	if len(card.controls) == 0 {
		t.Fatal("card 0 declares no writable control, and every card declares some")
	}
	for _, c := range card.controls {
		value, err := card.readControl(c.Name)
		if err != nil {
			t.Errorf("reading %q: %v", c.Name, err)
			continue
		}
		if value == "" {
			t.Errorf("%q reads as an empty string", c.Name)
		}
	}
	if _, err := card.jackStates(); err != nil {
		t.Errorf("reading the jacks: %v", err)
	}
	if _, err := card.readControl("No Such Control"); err == nil {
		t.Error("a control the card does not declare read without an error")
	}
}
