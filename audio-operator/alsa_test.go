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

// readOutputs enumerates the playback PCM devices the claim
// delivered. The fixture holds no real card, so every output it finds
// is one with no ELD element, which is what an analog jack looks
// like.
func TestReadOutputsFindsPlaybackDevices(t *testing.T) {
	sndDir = deliveredNodes(t, "controlC0", "pcmC0D0p", "pcmC0D0c", "pcmC0D3p", "timer", "seq")

	outputs, err := readOutputs()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, output := range outputs {
		names = append(names, output.Name())
		if output.HDMI || output.Monitor {
			t.Errorf("%s reports a monitor with no ELD element", output.Name())
		}
		if got := output.connectionType(); got != "analog" {
			t.Errorf("%s connection type = %q", output.Name(), got)
		}
	}
	slices.Sort(names)
	if want := []string{"card0-pcm0", "card0-pcm3"}; !slices.Equal(names, want) {
		t.Fatalf("outputs = %v, want %v", names, want)
	}
}

// A node with no sound card has no /dev/snd. The operator publishes no
// output and keeps serving, so a missing directory is a clean idle and
// not an error.
func TestReadOutputsIdlesWithNoDirectory(t *testing.T) {
	sndDir = filepath.Join(t.TempDir(), "absent")
	outputs, err := readOutputs()
	if err != nil {
		t.Fatalf("a missing directory reported an error: %v", err)
	}
	if len(outputs) != 0 {
		t.Fatalf("a missing directory produced %d outputs, want zero", len(outputs))
	}
}

// deliveredNodes builds a directory that stands in for the /dev/snd
// the claim delivers.
func deliveredNodes(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { sndDir = "/dev/snd" })
	return dir
}
