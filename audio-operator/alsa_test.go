package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"
)

// The ioctl number encodes the size of its argument, so a Go
// structure that disagrees with the kernel's builds a request the
// kernel does not answer. These are the sizes from
// include/uapi/sound/asound.h on a 64-bit kernel.
func TestControlStructureSizes(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{name: "snd_ctl_elem_id", got: unsafe.Sizeof(ctlElemID{}), want: ctlElemIDSize},
		{name: "snd_ctl_elem_list", got: unsafe.Sizeof(ctlElemList{}), want: ctlElemListSize},
		{name: "snd_ctl_elem_info", got: unsafe.Sizeof(ctlElemInfo{}), want: ctlElemInfoSize},
		{name: "snd_ctl_elem_value", got: unsafe.Sizeof(ctlElemValue{}), want: ctlElemValueSize},
		{name: "snd_ctl_card_info", got: unsafe.Sizeof(ctlCardInfo{}), want: ctlCardInfoSize},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("sizeof(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// The request numbers the ALSA control interface publishes. A driver
// that writes them by hand and a driver that computes them from the
// structure sizes must agree.
func TestControlIoctlNumbers(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{name: "SNDRV_CTL_IOCTL_ELEM_LIST", got: ctlIoctlElemList, want: 0xc0505510},
		{name: "SNDRV_CTL_IOCTL_ELEM_INFO", got: ctlIoctlElemInfo, want: 0xc1105511},
		{name: "SNDRV_CTL_IOCTL_ELEM_READ", got: ctlIoctlElemRead, want: 0xc4c85512},
		// The card information is the one request that only reads, so
		// its number carries the read bit alone.
		{name: "SNDRV_CTL_IOCTL_CARD_INFO", got: ctlIoctlCardInfo, want: 0x81785501},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

func TestElementName(t *testing.T) {
	var id ctlElemID
	copy(id.Name[:], "ELD")
	if got := id.name(); got != "ELD" {
		t.Errorf("name = %q", got)
	}

	// A name that fills the field has no terminator.
	for i := range id.Name {
		id.Name[i] = 'x'
	}
	if got := len(id.name()); got != len(id.Name) {
		t.Errorf("length = %d, want %d", got, len(id.Name))
	}
}

// readEndpoints enumerates the PCM devices the claim delivered in both
// directions. The fixture's control node is an ordinary file, so no
// PCM device has an ELD element, which is what an analog jack looks
// like.
func TestReadOutputsFindsEveryPCMDeviceInBothDirections(t *testing.T) {
	machineFixture(t, "lab")

	outputs, err := readEndpoints()
	if err != nil {
		t.Fatal(err)
	}
	addresses := []string{}
	for _, output := range outputs {
		address := output.Address()
		if output.Capture {
			address += captureSuffix
		}
		addresses = append(addresses, address)
		if output.HDMI || output.Monitor {
			t.Errorf("%s reports a monitor with no ELD element", address)
		}
		if got := output.connectionType(); got != "analog" {
			t.Errorf("%s connection type = %q", address, got)
		}
	}
	slices.Sort(addresses)
	want := []string{"card0-pcm0", "card0-pcm0-capture", "card0-pcm3"}
	if !slices.Equal(addresses, want) {
		t.Fatalf("endpoints = %v, want %v", addresses, want)
	}
}

// The whole enumeration on each lab machine, named. These are the
// names a claim holds and a person types, so a change to any part of
// the derivation fails here.
func TestReadOutputsNamesEveryEndpointOfAMachine(t *testing.T) {
	cases := []struct {
		machine string
		want    []string
	}{
		{
			machine: "liken-1",
			want: []string{
				"liken-1-pci-0000-00-1f-3-hdmi-0",
				"liken-1-pci-0000-00-1f-3-hdmi-1",
				"liken-1-pci-0000-00-1f-3-hdmi-2",
				"liken-1-pci-0000-00-1f-3-hdmi-3",
				"usb-0573-1573-a34004801402-usb-audio",
				"usb-0573-1573-a34004801402-usb-audio-capture",
			},
		},
		{
			machine: "stick-1",
			want: []string{
				"stick-1-pci-0000-00-0e-0-hdmi-0",
				"stick-1-pci-0000-00-0e-0-hdmi-1",
				"stick-1-pci-0000-00-0e-0-hdmi-2",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.machine, func(t *testing.T) {
			machineFixture(t, c.machine)
			outputs, err := readEndpoints()
			if err != nil {
				t.Fatal(err)
			}

			named, refused := nameEndpoints(c.machine, outputs)
			if len(refused) != 0 {
				t.Fatalf("the operator refused to name %v", refused)
			}
			names := []string{}
			for _, endpoint := range named {
				names = append(names, endpoint.Name())
			}
			slices.Sort(names)
			if !slices.Equal(names, c.want) {
				t.Fatalf("names = %v, want %v", names, c.want)
			}
		})
	}
}

// The USB card's endpoints publish the usb connection type in both
// directions, and the onboard card's analog jack keeps analog.
func TestConnectionTypeNamesTheBus(t *testing.T) {
	machineFixture(t, "liken-1")
	outputs, err := readEndpoints()
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range outputs {
		want := "usb"
		if output.Card == 0 {
			// The fixture's control node is an ordinary file, so no HDMI
			// slot reports an ELD element here.
			want = "analog"
		}
		if got := output.connectionType(); got != want {
			t.Errorf("%s connection type = %q, want %q", output.Address(), got, want)
		}
	}
}

// A node with no sound card has no /dev/snd. The operator publishes no
// output and keeps serving, so a missing directory is a clean idle and
// not an error.
func TestReadOutputsIdlesWithNoDirectory(t *testing.T) {
	sndDir = filepath.Join(t.TempDir(), "absent")
	outputs, err := readEndpoints()
	if err != nil {
		t.Fatalf("a missing directory reported an error: %v", err)
	}
	if len(outputs) != 0 {
		t.Fatalf("a missing directory produced %d outputs, want zero", len(outputs))
	}
}

// deliveredNodes builds a directory that stands in for the /dev/snd
// the claim delivers, and points the identity trees at the lab
// fixture, whose card holds the same PCM devices these callers name.
// A caller that varies the node list still gets an endpoint the
// operator can name, because a name it cannot build is a device it
// does not publish.
func deliveredNodes(t *testing.T, names ...string) string {
	t.Helper()
	machineFixture(t, "lab")
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sndDir = dir
	return dir
}
