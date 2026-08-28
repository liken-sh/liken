package main

import (
	"path/filepath"
	"testing"
	"unsafe"
)

// machineFixture points the three trees this operator reads at a
// captured copy of one machine's own. The trees under testdata mirror
// the real ones, symbolic links included, because the card's place is
// a chain of links and a fixture that flattened it would test
// nothing.
func machineFixture(t *testing.T, machine string) {
	t.Helper()
	sndDir = filepath.Join("testdata", machine, "snd")
	sysDir = filepath.Join("testdata", machine, "sys")
	procDir = filepath.Join("testdata", machine, "proc")
	t.Cleanup(func() {
		sndDir, sysDir, procDir = "/dev/snd", "/sys", "/proc"
	})
}

// The identity of each card on the two lab machines, read the way
// udev's 60-persistent-alsa.rules reads it and with no udev running.
func TestReadCardIdentityReadsWhereTheCardIs(t *testing.T) {
	cases := []struct {
		name    string
		machine string
		card    int
		want    cardIdentity
	}{
		{
			name:    "the onboard controller on liken-1",
			machine: "liken-1",
			card:    0,
			want:    cardIdentity{Bus: "pci", Location: "0000:00:1f.3"},
		},
		{
			// The USB device above the interface carries the identity,
			// and the serial is what makes the dongle's name follow it
			// from machine to machine.
			name:    "the USB card on liken-1",
			machine: "liken-1",
			card:    1,
			want: cardIdentity{
				Bus:      "usb",
				Location: "1-6",
				Vendor:   "0573",
				Product:  "1573",
				Serial:   "A34004801402",
			},
		},
		{
			name:    "the onboard controller on stick-1",
			machine: "stick-1",
			card:    0,
			want:    cardIdentity{Bus: "pci", Location: "0000:00:0e.0"},
		},
		{
			// A dongle that states no serial. The port path is the whole
			// of what identifies it.
			name:    "a USB card with no serial",
			machine: "no-serial",
			card:    0,
			want: cardIdentity{
				Bus:      "usb",
				Location: "1-6",
				Vendor:   "1b3f",
				Product:  "2008",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machineFixture(t, c.machine)
			if got := readCardIdentity(c.card); got != c.want {
				t.Errorf("identity = %+v, want %+v", got, c.want)
			}
		})
	}
}

// A card that sysfs says nothing about reports no bus, and the name
// builder is what refuses it.
func TestReadCardIdentityReportsNothingForACardSysfsDoesNotHold(t *testing.T) {
	machineFixture(t, "liken-1")
	if got := readCardIdentity(7); got != (cardIdentity{}) {
		t.Errorf("identity = %+v, want an empty one", got)
	}
}

func TestReadPCMID(t *testing.T) {
	machineFixture(t, "liken-1")
	cases := []struct {
		name    string
		card    int
		pcm     int
		capture bool
		want    string
	}{
		{name: "the first HDMI slot", card: 0, pcm: 3, want: "HDMI 0"},
		{name: "the last HDMI slot", card: 0, pcm: 9, want: "HDMI 3"},
		{name: "the DAC's playback side", card: 1, pcm: 0, want: "USB Audio"},
		// One PCM device of a USB card runs in both directions, and the
		// driver gives the two the same id. That is why a capture
		// endpoint's device name carries a word the playback one does
		// not.
		{name: "the DAC's capture side", card: 1, pcm: 0, capture: true, want: "USB Audio"},
		{name: "a PCM device the card does not have", card: 0, pcm: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := readPCMID(c.card, c.pcm, c.capture); got != c.want {
				t.Errorf("readPCMID(%d, %d, %v) = %q, want %q", c.card, c.pcm, c.capture, got, c.want)
			}
		})
	}
}

// The control device fills the three fixed-length fields, and a value
// that fills one has no terminator.
func TestCardInfoReadsTheKernelsFixedFields(t *testing.T) {
	var info ctlCardInfo
	copy(info.ID[:], "PCH")
	copy(info.Driver[:], "HDA-Intel")
	copy(info.Name[:], "HDA Intel PCH")
	if got := cText(info.ID[:]); got != "PCH" {
		t.Errorf("id = %q", got)
	}
	if got := cText(info.Driver[:]); got != "HDA-Intel" {
		t.Errorf("driver = %q", got)
	}
	if got := cText(info.Name[:]); got != "HDA Intel PCH" {
		t.Errorf("name = %q", got)
	}

	filled := [16]byte{}
	for i := range filled {
		filled[i] = 'x'
	}
	if got := len(cText(filled[:])); got != len(filled) {
		t.Errorf("length = %d, want %d", got, len(filled))
	}
}

// The PCM info request carries its argument size, so a structure
// that disagrees with the kernel's builds a number the kernel does
// not answer.
func TestPCMInfoLayout(t *testing.T) {
	if got := unsafe.Sizeof(sndPCMInfo{}); got != pcmInfoSize {
		t.Errorf("sizeof(snd_pcm_info) = %d, want %d", got, pcmInfoSize)
	}
	if got, want := ctlIoctlPCMInfo, uintptr(0xc1205531); got != want {
		t.Errorf("SNDRV_CTL_IOCTL_PCM_INFO = %#x, want %#x", got, want)
	}
}
