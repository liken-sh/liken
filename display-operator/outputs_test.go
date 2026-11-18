package main

import (
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeSysfs builds a /sys/class/drm with a directory for each
// connector and returns the root to pass as sysRoot. A connector with
// no fixture gets an empty edid and an empty modes file, as the kernel
// leaves them.
func fakeSysfs(t *testing.T, card string, connectors map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for connector, fixture := range connectors {
		writeConnector(t, root, card, connector, fixture)
	}
	return root
}

// writeConnector writes a connector's edid, modes, and status files.
// The kernel reports a hotplug by changing these files in a directory
// that already exists, so a test simulates one by calling
// writeConnector again. It writes status last, so a reader in another
// goroutine sees the connector as disconnected until the edid and
// modes files are complete.
func writeConnector(t *testing.T, root, card, connector, fixture string) {
	t.Helper()
	dir := filepath.Join(root, "class", "drm", card+"-"+connector)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the connector directory: %v", err)
	}
	status, edid, modes := "disconnected\n", []byte{}, []byte{}
	if fixture != "" {
		text, err := os.ReadFile("testdata/" + fixture + ".edid.hex")
		if err != nil {
			t.Fatalf("reading the EDID fixture: %v", err)
		}
		edid, err = hex.DecodeString(strings.TrimSpace(string(text)))
		if err != nil {
			t.Fatalf("decoding the EDID fixture: %v", err)
		}
		modes, err = os.ReadFile("testdata/" + fixture + ".modes")
		if err != nil {
			t.Fatalf("reading the modes fixture: %v", err)
		}
		status = "connected\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "edid"), edid, 0o644); err != nil {
		t.Fatalf("writing the edid file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modes"), modes, 0o644); err != nil {
		t.Fatalf("writing the modes file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatalf("writing the status file: %v", err)
	}
}

// labSysfs is the lab machine's layout: an ultrawide monitor, a
// portable monitor, and a DisplayPort connector with no monitor.
func labSysfs(t *testing.T) string {
	t.Helper()
	return fakeSysfs(t, "card1", map[string]string{
		"HDMI-A-1": "lg-hdr-wqhd",
		"HDMI-A-2": "portable-display",
		"DP-1":     "",
	})
}

func TestDiscoverOutputsPublishesEveryConnector(t *testing.T) {
	outputs := discoverOutputs(labSysfs(t), "card1")

	if len(outputs) != 3 {
		t.Fatalf("discoverOutputs returned %d outputs, want 3: %+v", len(outputs), outputs)
	}
	// The list is sorted by connector name.
	if outputs[0].Connector != "DP-1" || outputs[1].Connector != "HDMI-A-1" || outputs[2].Connector != "HDMI-A-2" {
		t.Fatalf("the connectors are %q, %q, %q, want DP-1, HDMI-A-1, HDMI-A-2", outputs[0].Connector, outputs[1].Connector, outputs[2].Connector)
	}
	// A connector with no monitor is listed too.
	if outputs[0].Connected || outputs[0].Monitor != (EDID{}) {
		t.Errorf("DP-1 is %+v, want disconnected with no monitor", outputs[0])
	}
	if !outputs[1].Connected || outputs[1].Monitor.ModelName != "LG HDR WQHD" {
		t.Errorf("HDMI-A-1 is %+v, want connected with the model name LG HDR WQHD", outputs[1])
	}
	if !outputs[2].Connected || outputs[2].Monitor.WidthPixels != 1920 {
		t.Errorf("HDMI-A-2 is %+v, want connected with a width of 1920 pixels", outputs[2])
	}
}

func TestDiscoverOutputsListsEachModeNameOnce(t *testing.T) {
	outputs := discoverOutputs(labSysfs(t), "card1")

	// The file has a line for each refresh variant of a mode, and the
	// names carry no refresh rate, so the variants become one name.
	ultrawide := outputs[1]
	if len(ultrawide.Modes) != 16 {
		t.Fatalf("HDMI-A-1 has %d mode names, want 16: %v", len(ultrawide.Modes), ultrawide.Modes)
	}
	// The kernel lists the preferred mode first, then descending, and
	// the read keeps that order, because the slice drops modes from the
	// end of the list.
	if ultrawide.Modes[0] != "3840x1600" || ultrawide.Modes[1] != "3840x2160" {
		t.Errorf("HDMI-A-1 modes start with %v, want 3840x1600, 3840x2160", ultrawide.Modes[:2])
	}
	if ultrawide.Modes[15] != "640x480" {
		t.Errorf("the last HDMI-A-1 mode is %q, want 640x480", ultrawide.Modes[15])
	}

	portable := outputs[2]
	if len(portable.Modes) != 8 || portable.Modes[0] != "1920x1080" {
		t.Errorf("HDMI-A-2 has %d modes starting with %q, want 8 starting with 1920x1080", len(portable.Modes), portable.Modes[0])
	}

	// An empty modes file gives no modes, not one blank name.
	if len(outputs[0].Modes) != 0 {
		t.Errorf("DP-1 has no monitor and lists modes %v, want none", outputs[0].Modes)
	}
}

func TestDiscoverOutputsWithoutAModesFile(t *testing.T) {
	// A connector can lose its files between the directory listing and
	// the read. A missing modes file removes the mode list and leaves
	// the rest of the output intact.
	root := fakeSysfs(t, "card1", map[string]string{"HDMI-A-1": "lg-hdr-wqhd"})
	if err := os.Remove(filepath.Join(root, "class", "drm", "card1-HDMI-A-1", "modes")); err != nil {
		t.Fatalf("removing the modes file: %v", err)
	}

	outputs := discoverOutputs(root, "card1")
	if len(outputs) != 1 {
		t.Fatalf("discoverOutputs returned %+v, want one output", outputs)
	}
	if len(outputs[0].Modes) != 0 {
		t.Errorf("the output lists modes %v, want none", outputs[0].Modes)
	}
	if !outputs[0].Connected || outputs[0].Monitor.ModelName != "LG HDR WQHD" {
		t.Errorf("the output is %+v, want connected with the model name LG HDR WQHD", outputs[0])
	}
}

func TestDiscoverOutputsReadsOneCard(t *testing.T) {
	// A machine can have a second card that another pod holds. The
	// operator must publish only the connectors of the card that its
	// own claim delivered.
	root := fakeSysfs(t, "card1", map[string]string{"HDMI-A-1": "lg-hdr-wqhd"})
	second := fakeSysfs(t, "card0", map[string]string{"DP-1": "portable-display"})
	if err := os.Rename(
		filepath.Join(second, "class", "drm", "card0-DP-1"),
		filepath.Join(root, "class", "drm", "card0-DP-1"),
	); err != nil {
		t.Fatalf("moving the second card into the tree: %v", err)
	}

	outputs := discoverOutputs(root, "card1")
	if len(outputs) != 1 || outputs[0].Connector != "HDMI-A-1" {
		t.Fatalf("discoverOutputs(card1) returned %+v, want only HDMI-A-1", outputs)
	}
}

func TestDiscoverOutputsOnAMachineWithNoDRM(t *testing.T) {
	if outputs := discoverOutputs(t.TempDir(), "card1"); outputs != nil {
		t.Fatalf("discoverOutputs on a tree with no DRM returned %+v, want nil", outputs)
	}
}

func TestConnectedKeepsOnlyConnectorsWithAMonitor(t *testing.T) {
	live := connected(discoverOutputs(labSysfs(t), "card1"))
	if len(live) != 2 {
		t.Fatalf("connected returned %d outputs, want 2: %+v", len(live), live)
	}
	if live[0].Connector != "HDMI-A-1" || live[1].Connector != "HDMI-A-2" {
		t.Fatalf("connected returned %+v, want HDMI-A-1 and HDMI-A-2", live)
	}
}

func TestCardNode(t *testing.T) {
	dri := t.TempDir()
	for _, node := range []string{"card1", "renderD128", "by-path"} {
		if err := os.WriteFile(filepath.Join(dri, node), nil, 0o644); err != nil {
			t.Fatalf("creating a node of the DRI directory: %v", err)
		}
	}

	cards, err := cardNode(dri)
	if err != nil {
		t.Fatalf("listing the card nodes: %v", err)
	}
	// The render node and by-path are not card nodes.
	if len(cards) != 1 || cards[0] != "card1" {
		t.Fatalf("cardNode returned %v, want only card1", cards)
	}
}

func TestCardNodeWithoutADisplayClaim(t *testing.T) {
	cards, err := cardNode(t.TempDir())
	if err != nil {
		t.Fatalf("listing the card nodes: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("cardNode returned %v, want none", cards)
	}
}

// The card reports one entry per timing, and two timings can have the
// same name and refresh. Each name and refresh is listed once.
func TestWithOfferedModesPublishesEachNameAndRefreshOnce(t *testing.T) {
	outputs := []Output{
		{Connector: "HDMI-A-1", Connected: true},
		{Connector: "DP-1"},
	}
	offered := map[string][]drmMode{
		"HDMI-A-1": {
			{Name: "3840x1600", Refresh: 60},
			{Name: "3840x1600", Refresh: 60},
			{Name: "3840x1600", Refresh: 24},
			{Name: "1920x1080", Refresh: 60},
		},
	}

	got := withOfferedModes(outputs, offered)

	want := []string{"3840x1600@60", "3840x1600@24", "1920x1080@60"}
	if !slices.Equal(got[0].OfferedModes, want) {
		t.Errorf("HDMI-A-1 offered modes are %q, want %q", got[0].OfferedModes, want)
	}
	// A connector that the card returned no modes for has a nil list.
	if got[1].OfferedModes != nil {
		t.Errorf("DP-1 offered modes are %q, want nil because the card returned none", got[1].OfferedModes)
	}
}

// Each connector's DRM object id is in its connector_id file, which is
// the id that a hotplug event uses. A connector with no such file has
// an empty ConnectorID.
func TestDiscoverOutputsReadsEachConnectorsID(t *testing.T) {
	root := labSysfs(t)
	if err := os.WriteFile(filepath.Join(root, "class", "drm", "card1-HDMI-A-1", "connector_id"), []byte("403\n"), 0o644); err != nil {
		t.Fatalf("writing the connector_id file: %v", err)
	}

	ids := map[string]string{}
	for _, output := range discoverOutputs(root, "card1") {
		ids[output.Connector] = output.ConnectorID
	}

	if want := map[string]string{"DP-1": "", "HDMI-A-1": "403", "HDMI-A-2": ""}; !maps.Equal(ids, want) {
		t.Errorf("the connector ids are %v, want %v", ids, want)
	}
}
