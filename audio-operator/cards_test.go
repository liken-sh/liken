package main

import (
	"path/filepath"
	"testing"
	"unsafe"
)

// machineFixture points sndDir, sysDir, and procDir at the trees
// captured from one machine under testdata. The captures keep the
// symbolic links, because a card's location is at the end of a chain
// of links, and a flattened tree would not test the code that follows
// them.
func machineFixture(t *testing.T, machine string) {
	t.Helper()
	sndDir = filepath.Join("testdata", machine, "snd")
	sysDir = filepath.Join("testdata", machine, "sys")
	procDir = filepath.Join("testdata", machine, "proc")
	t.Cleanup(func() {
		sndDir, sysDir, procDir = "/dev/snd", "/sys", "/proc"
	})
}

// Each tree was captured with no udev running, from a real machine or,
// for the no-serial case, from a USB dongle.
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
			// Vendor, product, and serial are attributes of the USB
			// device above the interface. The serial tells two identical
			// dongles on one machine apart.
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
			// A dongle with no serial attribute is identified by its port
			// path alone.
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
				t.Errorf("readCardIdentity(%d) = %+v, want %+v", c.card, got, c.want)
			}
		})
	}
}

// names.go refuses the empty identity.
func TestReadCardIdentityIsEmptyForACardMissingFromSysfs(t *testing.T) {
	machineFixture(t, "liken-1")
	if got := readCardIdentity(7); got != (cardIdentity{}) {
		t.Errorf("readCardIdentity(7) = %+v, want an empty identity", got)
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
		// A USB card's PCM device runs in both directions and the
		// driver gives both streams the same id, so the device name of a
		// capture endpoint needs an extra word to differ from the
		// playback one.
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

func TestCardInfoReadsTheKernelsFixedFields(t *testing.T) {
	var info ctlCardInfo
	copy(info.ID[:], "PCH")
	copy(info.Driver[:], "HDA-Intel")
	copy(info.Name[:], "HDA Intel PCH")
	if got := cText(info.ID[:]); got != "PCH" {
		t.Errorf("the id is %q, want PCH", got)
	}
	if got := cText(info.Driver[:]); got != "HDA-Intel" {
		t.Errorf("the driver is %q, want HDA-Intel", got)
	}
	if got := cText(info.Name[:]); got != "HDA Intel PCH" {
		t.Errorf("the name is %q, want HDA Intel PCH", got)
	}

	filled := [16]byte{}
	for i := range filled {
		filled[i] = 'x'
	}
	if got := len(cText(filled[:])); got != len(filled) {
		t.Errorf("the text of a full field is %d bytes, want %d", got, len(filled))
	}
}

// The ioctl request number encodes sizeof(struct snd_pcm_info), so a Go
// struct of a different size produces a number that the kernel rejects.
func TestPCMInfoLayout(t *testing.T) {
	if got := unsafe.Sizeof(sndPCMInfo{}); got != pcmInfoSize {
		t.Errorf("sizeof(snd_pcm_info) = %d, want %d", got, pcmInfoSize)
	}
	if got, want := ctlIoctlPCMInfo, uintptr(0xc1205531); got != want {
		t.Errorf("SNDRV_CTL_IOCTL_PCM_INFO = %#x, want %#x", got, want)
	}
}
