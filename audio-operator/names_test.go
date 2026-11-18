package main

import (
	"slices"
	"strings"
	"testing"
)

// The expected names come from the identities that two test machines
// report. If a name changed, every claim that holds that endpoint
// would name a device that no longer exists.
func TestEndpointName(t *testing.T) {
	cases := []struct {
		name    string
		machine string
		card    cardIdentity
		pcmID   string
		capture bool
		want    string
	}{
		{
			name:    "an onboard PCI card",
			machine: "liken-1",
			card:    cardIdentity{Bus: "pci", Location: "0000:00:1f.3"},
			pcmID:   "HDMI 0",
			want:    "liken-1-pci-0000-00-1f-3-hdmi-0",
		},
		{
			// The serial tells apart two dongles of one model on one
			// machine, and the machine name tells apart two machines
			// whose dongles report the same serial.
			name:    "a USB card with a serial",
			machine: "liken-1",
			card: cardIdentity{
				Bus: "usb", Location: "1-6",
				Vendor: "0573", Product: "1573", Serial: "A34004801402",
			},
			pcmID: "USB Audio",
			want:  "liken-1-usb-0573-1573-a34004801402-usb-audio",
		},
		{
			// Dongles of one model can report the same serial. Without
			// the machine name, every machine with such a dongle would
			// write the same cluster-scoped Sink.
			name:    "the same dongle model and serial on a second machine",
			machine: "stick-1",
			card: cardIdentity{
				Bus: "usb", Location: "1-2",
				Vendor: "0573", Product: "1573", Serial: "A34004801402",
			},
			pcmID: "USB Audio",
			want:  "stick-1-usb-0573-1573-a34004801402-usb-audio",
		},
		{
			// The card plays and records through one PCM device with
			// one id, so only the capture suffix makes the names
			// differ.
			name:    "the capture side of the same PCM device",
			machine: "liken-1",
			card: cardIdentity{
				Bus: "usb", Location: "1-6",
				Vendor: "0573", Product: "1573", Serial: "A34004801402",
			},
			pcmID:   "USB Audio",
			capture: true,
			want:    "liken-1-usb-0573-1573-a34004801402-usb-audio-capture",
		},
		{
			// Without a serial the identity is the port path, so a
			// dongle that moves to another port gets a new name.
			name:    "a USB card with no serial",
			machine: "liken-1",
			card:    cardIdentity{Bus: "usb", Location: "1-6", Vendor: "1b3f", Product: "2008"},
			pcmID:   "USB Audio",
			want:    "liken-1-usb-1-6-usb-audio",
		},
		{
			name:    "the second machine's second HDMI slot",
			machine: "stick-1",
			card:    cardIdentity{Bus: "pci", Location: "0000:00:0e.0"},
			pcmID:   "HDMI 1",
			want:    "stick-1-pci-0000-00-0e-0-hdmi-1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := endpointName(c.machine, c.card, c.pcmID, c.capture)
			if err != nil {
				t.Fatalf("endpointName returned the error %v, want %q", err, c.want)
			}
			if got != c.want {
				t.Errorf("endpointName = %q, want %q", got, c.want)
			}
		})
	}
}

// A USB name with the serial that is longer than 63 characters uses
// serialHash in place of the serial and keeps every other part. The
// 23-character serials are the length that a common webcam microphone
// reports.
func TestAUSBNameLongerThanADNSLabelUsesAHashOfTheSerial(t *testing.T) {
	webcam := func(serial string) cardIdentity {
		return cardIdentity{Bus: "usb", Location: "1-6", Vendor: "046d", Product: "0a44", Serial: serial}
	}
	cases := []struct {
		name    string
		machine string
		serial  string
		capture bool
		want    string
	}{
		{
			name:    "exactly 63 characters keeps the serial",
			machine: "node-100",
			serial:  "ABCDEF0123456789ABCDEF",
			capture: true,
			want:    "node-100-usb-046d-0a44-abcdef0123456789abcdef-usb-audio-capture",
		},
		{
			name:    "64 characters uses the hash",
			machine: "node-100",
			serial:  "ABCDEF0123456789ABCDEF0",
			capture: true,
			want:    "node-100-usb-046d-0a44-" + serialHash("ABCDEF0123456789ABCDEF0") + "-usb-audio-capture",
		},
		{
			// The capture name of this dongle is 64 characters with
			// the serial, and uses the hash in the row above.
			name:    "the playback side of the dongle whose capture name uses the hash",
			machine: "node-100",
			serial:  "ABCDEF0123456789ABCDEF0",
			want:    "node-100-usb-046d-0a44-abcdef0123456789abcdef0-usb-audio",
		},
		{
			name:    "a longer machine's playback side",
			machine: "studio-screen-12",
			serial:  "ABCDEF0123456789ABCDEF0",
			want:    "studio-screen-12-usb-046d-0a44-" + serialHash("ABCDEF0123456789ABCDEF0") + "-usb-audio",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := endpointName(c.machine, webcam(c.serial), "USB Audio", c.capture)
			if err != nil {
				t.Fatalf("endpointName returned the error %v, want %q", err, c.want)
			}
			if got != c.want {
				t.Errorf("endpointName = %q, want %q", got, c.want)
			}
			if len(got) > maxDeviceName {
				t.Errorf("name %q is %d characters, want at most %d", got, len(got), maxDeviceName)
			}
		})
	}
}

// A person can compute the hash by hand from the serial that the card
// reports, so the test pins the digits.
func TestSerialHash(t *testing.T) {
	// printf %s ABCDEF0123456789ABCDEF0 | sha256sum | cut -c1-8
	if got := serialHash("ABCDEF0123456789ABCDEF0"); got != "5fe3de05" {
		t.Errorf("serialHash = %q, want 5fe3de05", got)
	}
}

// Two dongles of one model on one machine get two names when both names
// use a hash, because their serials differ.
func TestTwoHashedSerialsGiveTwoNames(t *testing.T) {
	first, err := endpointName("studio-screen-12",
		cardIdentity{Bus: "usb", Vendor: "046d", Product: "0a44", Serial: "ABCDEF0123456789ABCDEF0"}, "USB Audio", false)
	if err != nil {
		t.Fatalf("naming the first dongle: %v", err)
	}
	second, err := endpointName("studio-screen-12",
		cardIdentity{Bus: "usb", Vendor: "046d", Product: "0a44", Serial: "ABCDEF0123456789ABCDEF1"}, "USB Audio", false)
	if err != nil {
		t.Fatalf("naming the second dongle: %v", err)
	}
	if first == second {
		t.Errorf("two serials both named %q, want two names", first)
	}
}

// endpointName returns an error instead of a shortened or partial
// name, and the caller does not publish the endpoint.
func TestEndpointNameRefusesWhatItCannotBuild(t *testing.T) {
	pci := cardIdentity{Bus: "pci", Location: "0000:00:1f.3"}
	cases := []struct {
		name    string
		machine string
		card    cardIdentity
		pcmID   string
		wantErr string
	}{
		{
			name:    "the driver reports no PCM id",
			machine: "liken-1",
			card:    pci,
			wantErr: "no id for the PCM device",
		},
		{
			name: "the card is on no bus this operator names",
			// A virtual card, such as the loopback card, has no device
			// link in sysfs.
			machine: "liken-1",
			card:    cardIdentity{},
			pcmID:   "Loopback PCM",
			wantErr: "no bus this operator can name it by",
		},
		{
			name:    "sysfs reports no address",
			machine: "liken-1",
			card:    cardIdentity{Bus: "pci"},
			pcmID:   "HDMI 0",
			wantErr: "no pci address",
		},
		{
			name:    "the machine has no name",
			card:    pci,
			pcmID:   "HDMI 0",
			wantErr: "this machine has no name",
		},
		{
			name:    "the name is longer than a DNS label",
			machine: strings.Repeat("long-machine-name", 4),
			card:    pci,
			pcmID:   "HDMI 0",
			wantErr: "and a device name has at most 63",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, err := endpointName(c.machine, c.card, c.pcmID, false)
			if err == nil {
				t.Fatalf("endpointName built %q, want an error", name)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{value: "0000:00:1f.3", want: "0000-00-1f-3"},
		{value: "HDMI 0", want: "hdmi-0"},
		{value: "USB Audio", want: "usb-audio"},
		{value: "A34004801402", want: "a34004801402"},
		{value: "  padded  ", want: "padded"},
		{value: "ALC236 Analog", want: "alc236-analog"},
		{value: "___", want: ""},
		{value: "", want: ""},
	}
	for _, c := range cases {
		if got := slug(c.value); got != c.want {
			t.Errorf("slug(%q) = %q, want %q", c.value, got, c.want)
		}
	}
}

// A device name contains no card or PCM number, so the plugin looks the
// name up in the endpoints that the last reconcile pass published.
func TestEndpointInventoryResolvesAName(t *testing.T) {
	inventory := &endpointInventory{}
	inventory.publish([]alsaEndpoint{
		{Card: 1, PCM: 0, DeviceName: "liken-1-usb-0573-1573-a34004801402-usb-audio"},
		{Card: 1, PCM: 0, Capture: true, DeviceName: "liken-1-usb-0573-1573-a34004801402-usb-audio-capture"},
	})

	sink, published := inventory.lookup("liken-1-usb-0573-1573-a34004801402-usb-audio")
	if !published || sink.Capture || sink.Card != 1 || sink.PCM != 0 {
		t.Errorf("the sink resolved to %+v (published %v), want the playback endpoint of card 1, PCM 0", sink, published)
	}
	source, published := inventory.lookup("liken-1-usb-0573-1573-a34004801402-usb-audio-capture")
	if !published || !source.Capture {
		t.Errorf("the source resolved to %+v (published %v), want the capture endpoint", source, published)
	}
	// An ALSA address is not a device name.
	if _, published := inventory.lookup("card1-pcm0"); published {
		t.Error("a name this operator never published resolved to an endpoint, want nothing")
	}
}

// Each pass replaces the whole inventory, so the name of a card that
// left the machine no longer resolves in a prepare call.
func TestEndpointInventoryContainsOnlyTheLastPass(t *testing.T) {
	inventory := &endpointInventory{}
	inventory.publish([]alsaEndpoint{{Card: 1, DeviceName: "liken-1-usb-0573-1573-a34004801402-usb-audio"}})
	inventory.publish([]alsaEndpoint{{Card: 0, DeviceName: "liken-1-pci-0000-00-1f-3-hdmi-0"}})

	if _, published := inventory.lookup("liken-1-usb-0573-1573-a34004801402-usb-audio"); published {
		t.Error("a card that left the machine still resolves, want nothing")
	}
	if _, published := inventory.lookup("liken-1-pci-0000-00-1f-3-hdmi-0"); !published {
		t.Error("the card that is there does not resolve, want its endpoint")
	}
}

// An operator with no inventory yet resolves no name, and publish does
// not panic on a nil inventory.
func TestNilInventoryResolvesNothing(t *testing.T) {
	var inventory *endpointInventory
	inventory.publish([]alsaEndpoint{{DeviceName: "liken-1-pci-0000-00-1f-3-hdmi-0"}})
	if _, published := inventory.lookup("liken-1-pci-0000-00-1f-3-hdmi-0"); published {
		t.Error("an inventory that does not exist resolved a name, want nothing")
	}
}

// nameEndpoints returns a reason for each endpoint it cannot name,
// under the endpoint's ALSA address, because that endpoint has no
// device name.
func TestNameEndpointsLeavesOutWhatItCannotName(t *testing.T) {
	named, refused := nameEndpoints("liken-1", []alsaEndpoint{
		{
			Card: 0, PCM: 3,
			Identity: cardIdentity{Bus: "pci", Location: "0000:00:1f.3"},
			PCMID:    "HDMI 0",
		},
		{Card: 1, PCM: 0, PCMID: "Loopback PCM"},
	})

	names := []string{}
	for _, endpoint := range named {
		names = append(names, endpoint.Name())
	}
	if want := []string{"liken-1-pci-0000-00-1f-3-hdmi-0"}; !slices.Equal(names, want) {
		t.Fatalf("named = %v, want %v", names, want)
	}
	if len(refused) != 1 || !strings.HasPrefix(refused[0], "card1-pcm0: ") {
		t.Fatalf("refused = %v, want one reason for card1-pcm0", refused)
	}
}

func TestPNPID(t *testing.T) {
	cases := []struct {
		id   uint16
		want string
	}{
		// These values are in EDID byte order. The kernel prints the
		// ELD's manufacture_id with the bytes swapped, 0x6d1e for GSM.
		{id: 0x1e6d, want: "GSM"},
		{id: 0x10ac, want: "DEL"},
		{id: 0x0000, want: ""},
		{id: 0xffff, want: ""},
	}
	for _, c := range cases {
		if got := pnpID(c.id); got != c.want {
			t.Errorf("pnpID(%#04x) = %q, want %q", c.id, got, c.want)
		}
	}
}

// display-operator's tests contain the same table, because both
// operators must publish the same value for one monitor. A change to
// either derivation that breaks the match fails a test in both.
func TestMonitorID(t *testing.T) {
	cases := []struct {
		name         string
		manufacturer string
		product      uint16
		monitor      string
		want         string
	}{
		{
			name:         "the plain form",
			manufacturer: "GSM", product: 0x5b09, monitor: "LG ULTRAWIDE",
			want: "gsm-5b09-lg-ultrawide",
		},
		{
			// A panel with no name descriptor still gets a value, so
			// that both operators publish the attribute.
			name:         "no monitor name",
			manufacturer: "BOE", product: 0x095f, monitor: "",
			want: "boe-095f",
		},
		{
			// EDID pads a name descriptor with spaces.
			name:         "a name padded with spaces",
			manufacturer: "DEL", product: 0xa0c5, monitor: "  DELL U2415  ",
			want: "del-a0c5-dell-u2415",
		},
		{
			// The caller publishes no pairing attribute for an empty
			// value, so a claim that matches on it allocates nothing.
			name:         "the manufacturer did not decode",
			manufacturer: pnpID(0x0000), product: 0x5b09, monitor: "LG ULTRAWIDE",
			want: "",
		},
		{
			name:         "a product code with leading zeros",
			manufacturer: "GSM", product: 0x0001, monitor: "LG HDR WQHD",
			want: "gsm-0001-lg-hdr-wqhd",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := monitorID(c.manufacturer, c.product, c.monitor); got != c.want {
				t.Errorf("monitorID = %q, want %q", got, c.want)
			}
		})
	}
}
