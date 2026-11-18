package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"
)

// A Go structure whose size differs from the kernel's produces an ioctl
// request number that the kernel rejects. The expected sizes are those
// of include/uapi/sound/asound.h on a 64-bit kernel.
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

// The expected values are what the SNDRV_CTL_IOCTL_* macros of
// include/uapi/sound/asound.h expand to on a 64-bit kernel.
func TestControlIoctlNumbers(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{name: "SNDRV_CTL_IOCTL_ELEM_LIST", got: ctlIoctlElemList, want: 0xc0505510},
		{name: "SNDRV_CTL_IOCTL_ELEM_INFO", got: ctlIoctlElemInfo, want: 0xc1105511},
		{name: "SNDRV_CTL_IOCTL_ELEM_READ", got: ctlIoctlElemRead, want: 0xc4c85512},
		// Card info only reads its argument, so its number has the
		// read direction bit and not the write bit.
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
		t.Errorf("name() = %q, want \"ELD\"", got)
	}

	// A name that fills the field has no NUL terminator.
	for i := range id.Name {
		id.Name[i] = 'x'
	}
	if got := len(id.name()); got != len(id.Name) {
		t.Errorf("length = %d, want %d", got, len(id.Name))
	}
}

// The fixture's control node is a regular file, so no PCM device has an
// ELD element and every endpoint looks like an analog jack.
func TestReadEndpointsFindsEveryPCMDeviceInBothDirections(t *testing.T) {
	machineFixture(t, "lab")

	outputs, err := readEndpoints()
	if err != nil {
		t.Fatalf("reading the endpoints of the lab fixture: %v", err)
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
			t.Errorf("%s connection type = %q, want \"analog\"", address, got)
		}
	}
	slices.Sort(addresses)
	want := []string{"card0-pcm0", "card0-pcm0-capture", "card0-pcm3"}
	if !slices.Equal(addresses, want) {
		t.Fatalf("endpoints = %v, want %v", addresses, want)
	}
}

// A claim and a person refer to an endpoint by its name, so a change to
// how names are built must fail this test.
func TestReadEndpointsNamesEveryEndpointOfAMachine(t *testing.T) {
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
				"liken-1-usb-0573-1573-a34004801402-usb-audio",
				"liken-1-usb-0573-1573-a34004801402-usb-audio-capture",
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
				t.Fatalf("reading the endpoints of %s: %v", c.machine, err)
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

// The endpoints of the USB card publish usb in both directions, and the
// analog jack of the onboard card publishes analog.
func TestConnectionTypeNamesTheBus(t *testing.T) {
	machineFixture(t, "liken-1")
	outputs, err := readEndpoints()
	if err != nil {
		t.Fatalf("reading the endpoints of liken-1: %v", err)
	}
	for _, output := range outputs {
		want := "usb"
		if output.Card == 0 {
			// The fixture's control node is a regular file, so the HDMI
			// slots of this card have no ELD element.
			want = "analog"
		}
		if got := output.connectionType(); got != want {
			t.Errorf("%s connection type = %q, want %q", output.Address(), got, want)
		}
	}
}

// A Kubernetes node with no sound card has no /dev/snd, and
// readEndpoints returns no endpoints and no error.
func TestReadEndpointsIdlesWithNoDirectory(t *testing.T) {
	sndDir = filepath.Join(t.TempDir(), "absent")
	outputs, err := readEndpoints()
	if err != nil {
		t.Fatalf("a missing directory reported an error: %v", err)
	}
	if len(outputs) != 0 {
		t.Fatalf("a missing directory produced %d outputs, want zero", len(outputs))
	}
}

// sndDirWith creates a directory with an empty file for each name,
// sets sndDir to it, and returns it. It loads the lab fixture first,
// because endpointName needs a card identity for each PCM node, and the
// operator does not publish an endpoint that it cannot name.
func sndDirWith(t *testing.T, names ...string) string {
	t.Helper()
	machineFixture(t, "lab")
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("writing a fixture device node: %v", err)
		}
	}
	sndDir = dir
	return dir
}
