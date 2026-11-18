package main

// The operator reads a card's connectors from /sys/class/drm, where
// the kernel creates a directory named <card>-<connector> for each
// connector. In it, `status` is connected, disconnected, or unknown,
// `edid` holds the monitor's EDID, `modes` lists the mode names the
// connector can drive, and `connector_id` holds the connector's DRM
// object id. Reading these files needs no connection to the
// compositor.
//
// The mode names come from the kernel and not from this operator's
// EDID parser, because DRM decodes every part of the EDID that lists
// modes and also removes the modes that the connector cannot drive.
// The EDID lists only what the monitor accepts.
//
// The walk reads only the card that the operator's display claim
// delivered. A machine can have a second card that another pod holds,
// and this operator must not publish its connectors. So a claim that
// delivers /dev/dri/card1 makes the walk read the card1-* directories.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Output is one of the card's connectors as sysfs shows it now.
type Output struct {
	// Connector is the kernel's name for the connector, such as
	// HDMI-A-1.
	Connector string
	// ConnectorID is the DRM object id that a hotplug event uses to
	// name the connector. It is empty on a kernel that has no
	// connector_id file.
	ConnectorID string
	// Connected is true when the status file says connected. A status
	// of unknown counts as disconnected, because a monitor that cannot
	// be detected also returns no EDID.
	Connected bool
	// Monitor is the EDID parsed from the edid file, or the zero value
	// when the file is empty or does not parse. Some drivers leave the
	// last EDID in the file after the monitor is unplugged, so read
	// Monitor only when Connected is true, or call monitor().
	Monitor EDID
	// Modes lists the names in the modes file in the kernel's order,
	// the preferred mode first and then descending. A name has no
	// refresh rate, so each resolution appears once.
	Modes []string
	// Controls lists the DDC/CI controls that the panel supports. It is
	// the zero value until withControls has run.
	Controls supportedControls
	// OfferedModes lists every mode the card offers on this connector,
	// with its refresh rate, as 1920x1080@60, which is the form that a
	// claim and spec.mode use. prepare checks a requested mode against
	// the same ioctl. It is empty until withOfferedModes has run.
	OfferedModes []string
	// ModesRead is true when the card's mode lists were read in this
	// pass. The card is not read while the operator has no connection
	// to a compositor (cardgate.go), and an empty OfferedModes then
	// says nothing about what the card offers.
	ModesRead bool
	// CurrentMode is the mode that the card runs on this output, with
	// its refresh rate, such as 3840x1600@24. sysfs does not show it.
	// It is empty when the output runs no mode and when the card was
	// not read.
	CurrentMode string
	// Remembered is the last monitor seen on a disconnected connector,
	// set by withLinks. A monitor that switches to another input drops
	// hot plug detect, and the kernel reports that the same way as an
	// unplugged cable. The monitor is usually still there, so the
	// operator keeps publishing its identity and a claim on it can
	// still allocate.
	Remembered EDID
	// Replaced is true, for disconnectGrace, after the connector
	// reconnects with a different monitor, and the slice then taints
	// the device. withLinks sets it.
	Replaced bool
	// Held is true when sysfs reports the connector as disconnected but
	// the outage is shorter than hotplugSettle. The output is then its
	// last connected reading, with Connected true (hotplughold.go).
	Held bool
}

// monitor returns the connected monitor, or the remembered one while
// the connector is disconnected.
func (o Output) monitor() EDID {
	if o.Connected {
		return o.Monitor
	}
	return o.Remembered
}

// withCurrentModes sets CurrentMode from a map keyed by connector
// name. It is a separate step from the sysfs walk because reading the
// card can fail while sysfs reads.
func withCurrentModes(outputs []Output, modes map[string]string) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].CurrentMode = modes[output.Connector]
	}
	return out
}

// withOfferedModes sets OfferedModes and ModesRead. A nil map means
// the card was not read.
func withOfferedModes(outputs []Output, offered map[string][]drmMode) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].OfferedModes = modeStrings(offered[output.Connector])
		out[i].ModesRead = offered != nil
	}
	return out
}

// modeStrings formats each mode as name@refresh and drops repeats. The
// card reports one entry per timing, and two timings can have the same
// name and refresh.
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

// discoverOutputs returns every connector of the card, sorted by name
// so that a comparison of two reads finds only real changes.
//
// It includes connectors with no monitor, so the list changes only
// when the card goes away. If a device that a claim holds dropped out
// of the slice, the claim's allocation would still name it, and the
// kubelet would retry the prepare call against a device that no slice
// lists, with no limit on the retries.
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
			Connector:   connector,
			ConnectorID: strings.TrimSpace(readFile(filepath.Join(dir, "connector_id"))),
			Connected:   strings.TrimSpace(readFile(filepath.Join(dir, "status"))) == "connected",
			Modes:       readModes(filepath.Join(dir, "modes")),
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

func connected(outputs []Output) []Output {
	var live []Output
	for _, output := range outputs {
		if output.Connected {
			live = append(live, output)
		}
	}
	return live
}

// cardNode returns the sorted names of the card nodes in driRoot, such
// as card1. claimedCard requires exactly one.
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

// readModes returns the names in a modes file, each once, in the
// kernel's order. The file has one line per timing, so a resolution
// that the monitor accepts at several refresh rates repeats, and the
// names have no rate to tell the repeats apart. An empty or missing
// file returns no modes.
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

// readFile returns a sysfs file's contents, or an empty string when it
// cannot be read. A connector can disappear between the directory
// listing and the read, and a card that is being removed returns
// errors for files that existed a moment ago.
func readFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
