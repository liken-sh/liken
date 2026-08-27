package hardware

import "testing"

// This table locks the whole class vocabulary. Machine manifests and
// operator habits build on these words, so a change to any existing
// word is a behavior change on every machine, and it must fail here
// before it can land quietly inside another change. Adding a row is
// the ordinary way in; moving a word takes a decision, not a diff.
func TestPCIClassWord(t *testing.T) {
	cases := []struct {
		name  string
		class string
		want  string
	}{
		{"mass storage", "0x010601", "storage"},
		{"network", "0x020000", "network"},
		{"display", "0x030000", "display"},
		{"multimedia", "0x040300", "multimedia"},
		{"memory", "0x050000", "memory"},
		{"bridge", "0x060400", "bridge"},
		{"communication", "0x070000", "communication"},
		{"system peripheral", "0x080000", "system"},
		{"sd host controller", "0x080500", "storage"},
		{"sd host controller with adma interface", "0x080501", "storage"},
		{"other system peripheral", "0x088000", "system"},
		{"input", "0x090000", "input"},
		{"serial bus", "0x0c0330", "serial-bus"},
		{"wireless", "0x0d1100", "wireless"},
		{"encryption", "0x108000", "encryption"},
		{"accelerator", "0x120000", "accelerator"},
		{"unclassified", "0x000000", ""},
		{"processor", "0x0b0000", ""},
		{"signal processing", "0x110000", ""},
		{"bare base class", "01", "storage"},
		{"bare subclass", "0805", "storage"},
		{"too short", "0", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pciClassWord(tc.class); got != tc.want {
				t.Errorf("pciClassWord(%q) = %q, want %q", tc.class, got, tc.want)
			}
		})
	}
}

// The USB class table gets the same lock, for the same reason.
func TestUSBClassWord(t *testing.T) {
	cases := []struct {
		class string
		want  string
	}{
		{"01", "audio"},
		{"02", "communications"},
		{"03", "hid"},
		{"06", "imaging"},
		{"07", "printer"},
		{"08", "mass-storage"},
		{"0a", "cdc-data"},
		{"0b", "smart-card"},
		{"0e", "video"},
		{"10", "audio-video"},
		{"e0", "wireless"},
		{"E0", "wireless"},
		{"ff", "vendor-specific"},
		{"09", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.class, func(t *testing.T) {
			if got := usbClassWord(tc.class); got != tc.want {
				t.Errorf("usbClassWord(%q) = %q, want %q", tc.class, got, tc.want)
			}
		})
	}
}
