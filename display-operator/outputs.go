package main

// Finding the card's outputs in sysfs.
//
// The kernel registers one directory for each connector a graphics
// card has, named <card>-<connector> under /sys/class/drm. Three files
// in it answer everything this operator publishes: `status` says
// connected, disconnected, or unknown, `edid` holds the monitor's
// EDID while a monitor is attached and nothing while none is, and
// `modes` lists the modes the connector can drive.
//
// The mode list is the kernel's, not ours, on purpose. An EDID
// states its modes in four encodings spread over the base block and
// the extensions, and DRM decodes all four. DRM also drops every
// mode the connector's hardware cannot carry, a judgment no EDID
// parse could make, because the EDID only says what the monitor
// accepts. The file holds the survivors, so reading it beats
// growing our parser by three encodings and still publishing modes
// the card cannot drive.
//
// The compositor is not the source. Weston reports the same facts
// only over its private IPC, and reading a file needs no protocol, no
// library, and no version agreement with the compositor running beside
// it. What the operator does need weston for is the delivery, and that
// is the socket.
//
// The walk is scoped to one card. The operator holds one exclusive
// display claim, and a machine can have a second graphics card that
// another pod holds, whose connectors this operator must not publish.
// The claim delivers the card node, so the node is what names the
// card: /dev/dri/card1 makes the walk read the card1-* directories.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Output is one of the card's connectors as sysfs shows it now.
type Output struct {
	// Connector is the kernel's own name: HDMI-A-1.
	Connector string
	// Connected reports a monitor on the wire. A connector whose
	// status is unknown counts as disconnected, because a monitor
	// that cannot be detected also answers no EDID.
	Connected bool
	// Monitor holds what the EDID says. It is the zero value while
	// nothing is attached, and while an attached monitor answers an
	// EDID this operator cannot read.
	//
	// Read this only when Connected is true. Some drivers keep the
	// last EDID they read in the file after the monitor leaves. So a
	// dark connector can answer a whole, valid block that describes a
	// monitor that is no longer on the wire. sliceDevices gates every
	// attribute on Connected for that reason, and every other reader
	// must do the same.
	Monitor EDID
	// Modes holds the mode names the connector can drive, in the
	// kernel's order: the preferred mode first, then descending. A
	// name is a resolution with no refresh rate, so a resolution the
	// monitor accepts at several rates appears once.
	Modes []string
	// Controls is what the panel itself answered when the operator
	// probed its DDC/CI wire. Nothing in sysfs holds it, and it is the
	// zero value until withControls has run. The probe behind it is
	// cached against the monitor, so carrying this costs a walk
	// nothing.
	Controls supportedControls
	// OfferedModes is every mode the card reports for this
	// connector, with the refresh, in the form a claim and spec.mode
	// state: 1920x1080@60. It comes from the same ioctl the prepare
	// path validates a mode against, where Modes above is the sysfs
	// list of bare names. It is empty until withOfferedModes has run.
	OfferedModes []string
	// CurrentMode is the mode this output runs right now, with the
	// refresh the card reports: 3840x1600@24, the vocabulary a claim
	// states, while Modes stays name-only. It comes from the card
	// node, not from sysfs: sysfs publishes what a connector accepts
	// and never what it drives. It is empty when the output drives
	// nothing and when the card could not answer.
	CurrentMode string
}

// WithCurrentModes puts the card's readback beside what sysfs
// said about each connector.
//
// The two reads are separate because they answer different
// questions and can fail apart. A connector the readback skipped keeps
// an empty CurrentMode, and the slice publishes no attribute for it.
func withCurrentModes(outputs []Output, modes map[string]string) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].CurrentMode = modes[output.Connector]
	}
	return out
}

// withOfferedModes puts the card's own mode list beside what
// sysfs said, the way withCurrentModes puts the readback there. The
// list is deduplicated on the name and refresh together, because the
// card reports one entry per timing and two timings can share both.
func withOfferedModes(outputs []Output, offered map[string][]drmMode) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].OfferedModes = modeStrings(offered[output.Connector])
	}
	return out
}

// One mode as a person states it, name and refresh joined by
// the @ the claim parameter uses.
func modeStrings(offered []drmMode) []string {
	var modes []string
	for _, mode := range offered {
		name := fmt.Sprintf("%s@%d", mode.Name, mode.Refresh)
		if !slices.Contains(modes, name) {
			modes = append(modes, name)
		}
	}
	return modes
}

// discoverOutputs lists every connector on one card, sorted by
// connector name so that the same hardware always produces the same
// list and the slice comparison reports real changes only.
//
// Every connector publishes, including one that has never had a
// monitor on it. Membership must not depend on what is plugged in.
// Deleting a device that a claim holds strands the next consumer:
// the allocation still names the device, and the kubelet's
// prepare call retries against a device that is in no slice, with no
// bound on the retry. The connector list changes only when the card
// itself leaves.
func discoverOutputs(sysRoot, card string) []Output {
	entries, err := os.ReadDir(filepath.Join(sysRoot, "class", "drm"))
	if err != nil {
		return nil
	}
	var outputs []Output
	for _, entry := range entries {
		connector, found := strings.CutPrefix(entry.Name(), card+"-")
		if !found {
			continue
		}
		dir := filepath.Join(sysRoot, "class", "drm", entry.Name())
		output := Output{
			Connector: connector,
			Connected: strings.TrimSpace(readFile(filepath.Join(dir, "status"))) == "connected",
			Modes:     readModes(filepath.Join(dir, "modes")),
		}
		if edid, err := ParseEDID([]byte(readFile(filepath.Join(dir, "edid")))); err == nil {
			output.Monitor = edid
		}
		outputs = append(outputs, output)
	}
	slices.SortFunc(outputs, func(a, b Output) int {
		return strings.Compare(a.Connector, b.Connector)
	})
	return outputs
}

// connected keeps the outputs that can serve a client right now, and
// every other connector publishes with the taint that says it delivers
// nothing.
func connected(outputs []Output) []Output {
	var live []Output
	for _, output := range outputs {
		if output.Connected {
			live = append(live, output)
		}
	}
	return live
}

// cardNode names the card the claim delivered, as sysfs spells it:
// card1. The kernel numbers cards in the order it probes them and the
// numbering moves across a reboot, so nothing may name one. The
// operator takes whichever node its own claim put in /dev/dri.
//
// More than one card node means the pod holds more than one display
// claim, which this operator does not support: it runs one compositor,
// and one compositor drives one card. The caller reports that as a
// failure rather than choosing a card.
func cardNode(driRoot string) ([]string, error) {
	entries, err := os.ReadDir(driRoot)
	if err != nil {
		return nil, err
	}
	var cards []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "card") {
			cards = append(cards, name)
		}
	}
	slices.Sort(cards)
	return cards, nil
}

// readModes reads the connector's mode list and returns each name
// once, in the order the kernel listed it.
//
// The file holds one name per line and one line per timing, so a
// resolution the monitor accepts at several refresh rates repeats
// once per rate. The name states no rate, which makes the repeats
// one fact, and the preferred mode's rate already publishes as the
// refreshMillihertz attribute.
//
// A connector with nothing on it has an empty file, and a connector
// that is going away has no file at all. Both answer no modes, which
// the slice publishes as an absent attribute.
func readModes(path string) []string {
	var modes []string
	for _, line := range strings.Split(readFile(path), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || slices.Contains(modes, name) {
			continue
		}
		modes = append(modes, name)
	}
	return modes
}

// readFile reads a small sysfs file and returns an empty string when
// it is not there. A connector can disappear between the directory
// listing and the read, and a card that is being removed answers an
// error on files that existed a moment ago.
func readFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
