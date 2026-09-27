package main

import (
	"slices"
	"strings"
	"testing"
)

// The three name forms, against the identities the two lab machines
// really report. A name that changed here would strand every claim
// that holds the endpoint.
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
			// The serial tells two identical dongles on one machine
			// apart, and the machine name tells apart two machines
			// whose dongles of one model report the same serial.
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
			// A USB serial is not unique across machines. Dongles of
			// one model can all report one serial, and without the
			// machine in the name every machine that holds one would
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
			// One PCM device of that card records as well as plays, and
			// both directions report the same id, so the capture
			// endpoint carries a word that keeps the two names apart.
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
			// With no serial the port path is the identity, so moving
			// the dongle to another port makes a new object.
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
				t.Fatalf("endpointName reported %v", err)
			}
			if got != c.want {
				t.Errorf("name = %q, want %q", got, c.want)
			}
		})
	}
}

// A name that would pass 63 characters keeps the machine, the vendor,
// the product, and the PCM id, and holds the first eight hex digits of
// the serial's SHA-256 in place of the serial. A name that fits keeps
// the serial itself. The two 23-character serials are the length a
// common webcam microphone reports.
func TestAUSBNamePastALabelHoldsAHashOfTheSerial(t *testing.T) {
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
			name:    "64 characters holds the hash",
			machine: "node-100",
			serial:  "ABCDEF0123456789ABCDEF0",
			capture: true,
			want:    "node-100-usb-046d-0a44-" + serialHash("ABCDEF0123456789ABCDEF0") + "-usb-audio-capture",
		},
		{
			// The capture suffix adds eight characters, so a playback
			// name of 56 to 63 characters keeps the serial while the
			// capture name of the same dongle holds the hash.
			name:    "the playback side of the dongle whose capture name holds the hash",
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
				t.Fatalf("endpointName reported %v", err)
			}
			if got != c.want {
				t.Errorf("name = %q, want %q", got, c.want)
			}
			if len(got) > maxDeviceName {
				t.Errorf("name %q is %d characters", got, len(got))
			}
		})
	}
}

// The hash is the contract a person can compute by hand from the
// serial the card reports, so the digits are pinned here.
func TestSerialHash(t *testing.T) {
	// printf %s ABCDEF0123456789ABCDEF0 | sha256sum | cut -c1-8
	if got := serialHash("ABCDEF0123456789ABCDEF0"); got != "5fe3de05" {
		t.Errorf("serialHash = %q", got)
	}
}

// Two dongles of one model on one machine keep two names when both
// names hold a hash, because their serials differ.
func TestTwoHashedSerialsGiveTwoNames(t *testing.T) {
	first, err := endpointName("studio-screen-12",
		cardIdentity{Bus: "usb", Vendor: "046d", Product: "0a44", Serial: "ABCDEF0123456789ABCDEF0"}, "USB Audio", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := endpointName("studio-screen-12",
		cardIdentity{Bus: "usb", Vendor: "046d", Product: "0a44", Serial: "ABCDEF0123456789ABCDEF1"}, "USB Audio", false)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Errorf("two serials both named %q", first)
	}
}

// A name this operator cannot build is a device it does not publish,
// and the reason reaches the log rather than a shortened name that
// nobody can predict.
func TestEndpointNameRefusesWhatItCannotBuild(t *testing.T) {
	pci := cardIdentity{Bus: "pci", Location: "0000:00:1f.3"}
	cases := []struct {
		name    string
		machine string
		card    cardIdentity
		pcmID   string
		says    string
	}{
		{
			name:    "the driver states no PCM id",
			machine: "liken-1",
			card:    pci,
			says:    "no id for the PCM device",
		},
		{
			name: "the card is on no bus this operator names",
			// A virtual card, such as the loopback, has no device link
			// in sysfs at all.
			machine: "liken-1",
			card:    cardIdentity{},
			pcmID:   "Loopback PCM",
			says:    "no bus this operator can name it by",
		},
		{
			name:    "sysfs states no address",
			machine: "liken-1",
			card:    cardIdentity{Bus: "pci"},
			pcmID:   "HDMI 0",
			says:    "no pci address",
		},
		{
			name:  "the machine has no name",
			card:  pci,
			pcmID: "HDMI 0",
			says:  "this machine has no name",
		},
		{
			name:    "the name is longer than a DNS label",
			machine: strings.Repeat("long-machine-name", 4),
			card:    pci,
			pcmID:   "HDMI 0",
			says:    "and a device name holds 63",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, err := endpointName(c.machine, c.card, c.pcmID, false)
			if err == nil {
				t.Fatalf("endpointName built %q", name)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("error = %v, want it to say %q", err, c.says)
			}
		})
	}
}

// Every part of a name is a piece of a DNS label: lowercase letters,
// digits, and one dash for each run of anything else.
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

// The plugin resolves a prepare call's device name against what the
// last reconcile pass read, because the name holds no card and no PCM
// number to parse.
func TestEndpointInventoryResolvesAName(t *testing.T) {
	inventory := &endpointInventory{}
	inventory.publish([]alsaEndpoint{
		{Card: 1, PCM: 0, DeviceName: "liken-1-usb-0573-1573-a34004801402-usb-audio"},
		{Card: 1, PCM: 0, Capture: true, DeviceName: "liken-1-usb-0573-1573-a34004801402-usb-audio-capture"},
	})

	sink, published := inventory.lookup("liken-1-usb-0573-1573-a34004801402-usb-audio")
	if !published || sink.Capture || sink.Card != 1 || sink.PCM != 0 {
		t.Errorf("the sink resolved to %+v, %v", sink, published)
	}
	source, published := inventory.lookup("liken-1-usb-0573-1573-a34004801402-usb-audio-capture")
	if !published || !source.Capture {
		t.Errorf("the source resolved to %+v, %v", source, published)
	}
	// A name that this operator never published resolves to nothing,
	// and the prepare call fails with that name in its message.
	if _, published := inventory.lookup("card1-pcm0"); published {
		t.Error("a name this operator never published resolved to an endpoint")
	}
}

// A pass replaces the whole inventory, so a card that left the
// machine leaves no name behind for a prepare call to resolve.
func TestEndpointInventoryHoldsOnlyTheLastPass(t *testing.T) {
	inventory := &endpointInventory{}
	inventory.publish([]alsaEndpoint{{Card: 1, DeviceName: "liken-1-usb-0573-1573-a34004801402-usb-audio"}})
	inventory.publish([]alsaEndpoint{{Card: 0, DeviceName: "liken-1-pci-0000-00-1f-3-hdmi-0"}})

	if _, published := inventory.lookup("liken-1-usb-0573-1573-a34004801402-usb-audio"); published {
		t.Error("a card that left the machine still resolves")
	}
	if _, published := inventory.lookup("liken-1-pci-0000-00-1f-3-hdmi-0"); !published {
		t.Error("the card that is there does not resolve")
	}
}

// An operator that holds no inventory yet resolves nothing, rather
// than failing the prepare call in a way a reader cannot place.
func TestNilInventoryResolvesNothing(t *testing.T) {
	var inventory *endpointInventory
	inventory.publish([]alsaEndpoint{{DeviceName: "liken-1-pci-0000-00-1f-3-hdmi-0"}})
	if _, published := inventory.lookup("liken-1-pci-0000-00-1f-3-hdmi-0"); published {
		t.Error("an inventory that does not exist resolved a name")
	}
}

// nameEndpoints holds back what it cannot name and says why, so a
// person reading the log can tell a missing endpoint from a missing
// card.
func TestNameEndpointsHoldsBackWhatItCannotName(t *testing.T) {
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
		t.Fatalf("refused = %v", refused)
	}
}

func TestPNPID(t *testing.T) {
	cases := []struct {
		id   uint16
		want string
	}{
		// The two bytes in EDID order. The kernel prints the ELD's own
		// manufacture_id in the other byte order, 0x6d1e for this one.
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

// The pairing vectors. The display operator's suite holds the same
// table, because the two operators must publish the same value for
// the same monitor. A change to either derivation that broke the
// parity fails a test in both repositories.
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
			// A panel with no name descriptor. The value is the
			// manufacturer and the product alone, and not nothing,
			// because the display operator reads the same monitor and
			// must build the same value.
			name:         "no monitor name",
			manufacturer: "BOE", product: 0x095f, monitor: "",
			want: "boe-095f",
		},
		{
			// EDID pads a descriptor with spaces, and the padding must
			// not reach the value.
			name:         "padding does not reach the value",
			manufacturer: "DEL", product: 0xa0c5, monitor: "  DELL U2415  ",
			want: "del-a0c5-dell-u2415",
		},
		{
			// Two bytes that decode to something other than three
			// letters do not identify a monitor, so the caller publishes
			// no pairing attribute, and a constraint on that attribute
			// never allocates a device.
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
