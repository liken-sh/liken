package main

import (
	"reflect"
	"testing"
)

func TestMajorClassNames(t *testing.T) {
	cases := []struct {
		class uint32
		want  string
	}{
		{0x000000, "miscellaneous"},
		{0x000100, "computer"},
		{0x000200, "phone"},
		{0x000300, "lan-access-point"},
		{0x000400, "audio-video"},
		{0x000500, "peripheral"},
		{0x000600, "imaging"},
		{0x000700, "wearable"},
		{0x000800, "toy"},
		{0x000900, "health"},
		{0x001F00, "uncategorized"},
		{0x000A00, ""},
		{0x001E00, ""},
	}
	for _, c := range cases {
		if got := majorClass(c.class); got != c.want {
			t.Errorf("majorClass(%#06x) = %q, want %q", c.class, got, c.want)
		}
	}
}

func TestMinorClassNames(t *testing.T) {
	cases := []struct {
		name  string
		class uint32
		want  string
	}{
		{"computer laptop", 0x000100 | 0x03<<2, "laptop"},
		{"computer tablet", 0x000100 | 0x07<<2, "tablet"},
		{"phone smartphone", 0x000200 | 0x03<<2, "smartphone"},
		{"phone isdn", 0x000200 | 0x05<<2, "isdn"},
		{"audio-video headphones", 0x000400 | 0x06<<2, "headphones"},
		{"audio-video hands-free", 0x000400 | 0x02<<2, "hands-free"},
		{"audio-video gaming", 0x000400 | 0x12<<2, "gaming"},
		{"peripheral gamepad", 0x000500 | 0x02<<2, "gamepad"},
		{"peripheral digital-pen", 0x000500 | 0x07<<2, "digital-pen"},
		{"peripheral keyboard", 0x000500 | 0x10<<2, "keyboard"},
		{"peripheral pointing-device", 0x000500 | 0x20<<2, "pointing-device"},
		{"peripheral keyboard-pointing", 0x000500 | 0x30<<2, "keyboard-pointing"},
		{"peripheral keyboard that is also a joystick", 0x000500 | 0x11<<2, "joystick"},
		{"wearable wristwatch", 0x000700 | 0x01<<2, "wristwatch"},
		{"wearable glasses", 0x000700 | 0x05<<2, "glasses"},
		{"toy robot", 0x000800 | 0x01<<2, "robot"},
		{"toy game", 0x000800 | 0x05<<2, "game"},
		{"health heart-rate", 0x000900 | 0x06<<2, "heart-rate"},
		{"health blood-pressure", 0x000900 | 0x01<<2, "blood-pressure"},
		// Imaging spreads its minor across independent flag bits, so
		// no single name applies and the decode stays silent.
		{"imaging", 0x000600 | 0x10<<2, ""},
		{"an unknown minor under a known major", 0x000400 | 0x3F<<2, ""},
		{"a peripheral with neither half set", 0x000500, ""},
		{"an unknown major", 0x000A00 | 0x01<<2, ""},
		{"uncategorized", 0x001F00 | 0x01<<2, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := minorClass(c.class); got != c.want {
				t.Fatalf("minorClass(%#06x) = %q, want %q", c.class, got, c.want)
			}
		})
	}
}

func TestServiceClassFlags(t *testing.T) {
	cases := []struct {
		name  string
		class uint32
		want  []string
	}{
		{"no service bits", 0x000508, nil},
		{"positioning", 1 << 16, []string{"servicePositioning"}},
		{"networking", 1 << 17, []string{"serviceNetworking"}},
		{"rendering", 1 << 18, []string{"serviceRendering"}},
		{"capturing", 1 << 19, []string{"serviceCapturing"}},
		{"object transfer", 1 << 20, []string{"serviceObjectTransfer"}},
		{"audio", 1 << 21, []string{"serviceAudio"}},
		{"telephony", 1 << 22, []string{"serviceTelephony"}},
		{"information", 1 << 23, []string{"serviceInformation"}},
		// Bit 13 is set only during a pairing window and bit 14 has
		// no named use yet, so neither publishes a flag.
		{"limited discoverable and LE audio", 1<<13 | 1<<14, nil},
		{
			name:  "the headphone word",
			class: 0x2c0418,
			want:  []string{"serviceRendering", "serviceCapturing", "serviceAudio"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serviceClassFlags(c.class); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("serviceClassFlags(%#06x) = %v, want %v", c.class, got, c.want)
			}
		})
	}
}

// fullUUID builds the 128-bit form BlueZ reports for a 16-bit
// assigned number.
func fullUUID(short string) string {
	return "0000" + short + "-0000-1000-8000-00805f9b34fb"
}

func TestProfileFlags(t *testing.T) {
	cases := []struct {
		name  string
		uuids []string
		want  []string
	}{
		{"nothing browsed yet", nil, nil},
		{"audio sink", []string{fullUUID("110b")}, []string{"audioSink"}},
		{"audio source", []string{fullUUID("110a")}, []string{"audioSource"}},
		{"avrcp target", []string{fullUUID("110c")}, []string{"avrcpTarget"}},
		{"avrcp controller", []string{fullUUID("110f")}, []string{"avrcpController"}},
		{"handsfree", []string{fullUUID("111e")}, []string{"handsfree"}},
		{"headset", []string{fullUUID("1108")}, []string{"headset"}},
		{"battery", []string{fullUUID("180f")}, []string{"battery"}},
		{"serial port", []string{fullUUID("1101")}, []string{"serialPort"}},
		{"classic HID", []string{fullUUID("1124")}, []string{"input"}},
		{"HID over GATT", []string{fullUUID("1812")}, []string{"input"}},
		{
			name:  "both HID transports collapse into one flag",
			uuids: []string{fullUUID("1124"), fullUUID("1812")},
			want:  []string{"input"},
		},
		{
			name:  "a UUID the vocabulary does not name",
			uuids: []string{fullUUID("1200"), fullUUID("1801")},
			want:  nil,
		},
		{
			name:  "a vendor UUID outside the base range",
			uuids: []string{"931c7e8a-540f-4686-b798-e8df0a2ad9f7"},
			want:  nil,
		},
		{
			name:  "a malformed UUID",
			uuids: []string{"", "0000110b", "0000zzzz-0000-1000-8000-00805f9b34fb"},
			want:  nil,
		},
		{
			name:  "uppercase, as some sources print it",
			uuids: []string{"0000110B-0000-1000-8000-00805F9B34FB"},
			want:  []string{"audioSink"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := profileFlags(c.uuids); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("profileFlags(%v) = %v, want %v", c.uuids, got, c.want)
			}
		})
	}
}

// The published flags must not depend on the order the browse
// returned the UUIDs, or a reordered answer would rewrite the slice
// with no real change.
func TestProfileFlagsAreOrdered(t *testing.T) {
	forward := profileFlags([]string{
		fullUUID("110b"), fullUUID("110a"), fullUUID("110c"), fullUUID("110f"), fullUUID("1101"),
	})
	backward := profileFlags([]string{
		fullUUID("1101"), fullUUID("110f"), fullUUID("110c"), fullUUID("110a"), fullUUID("110b"),
	})
	if !reflect.DeepEqual(forward, backward) {
		t.Fatalf("forward = %v, backward = %v", forward, backward)
	}
}
