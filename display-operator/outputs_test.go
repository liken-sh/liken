package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSysfs builds the part of /sys/class/drm that this operator
// reads: one directory for each connector, holding a status file and
// an edid file. The fixture returns the root to pass as sysRoot.
func fakeSysfs(t *testing.T, card string, connectors map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for connector, fixture := range connectors {
		dir := filepath.Join(root, "class", "drm", card+"-"+connector)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		status, edid := "disconnected\n", []byte{}
		if fixture != "" {
			text, err := os.ReadFile("testdata/" + fixture + ".edid.hex")
			if err != nil {
				t.Fatal(err)
			}
			edid, err = hex.DecodeString(strings.TrimSpace(string(text)))
			if err != nil {
				t.Fatal(err)
			}
			status = "connected\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "edid"), edid, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// labSysfs is the lab machine's own arrangement: an ultrawide, a
// portable monitor, and a DisplayPort connector with nothing on it.
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
		t.Fatalf("got %d outputs, want 3: %+v", len(outputs), outputs)
	}
	// Sorted, so the same hardware always produces the same list and
	// the slice comparison sees real changes only.
	if outputs[0].Connector != "DP-1" || outputs[1].Connector != "HDMI-A-1" || outputs[2].Connector != "HDMI-A-2" {
		t.Fatalf("connectors = %q, %q, %q", outputs[0].Connector, outputs[1].Connector, outputs[2].Connector)
	}
	// The connector with nothing on it is a device too. Membership
	// never depends on what is plugged in.
	if outputs[0].Connected || outputs[0].Monitor != (EDID{}) {
		t.Errorf("the empty connector reported a monitor: %+v", outputs[0])
	}
	if !outputs[1].Connected || outputs[1].Monitor.ModelName != "LG HDR WQHD" {
		t.Errorf("HDMI-A-1 = %+v", outputs[1])
	}
	if !outputs[2].Connected || outputs[2].Monitor.WidthPixels != 1920 {
		t.Errorf("HDMI-A-2 = %+v", outputs[2])
	}
}

func TestDiscoverOutputsReadsOneCard(t *testing.T) {
	// A machine can have a second card that another pod holds, and
	// this operator must publish only the connectors of the card its
	// own claim delivered.
	root := fakeSysfs(t, "card1", map[string]string{"HDMI-A-1": "lg-hdr-wqhd"})
	second := fakeSysfs(t, "card0", map[string]string{"DP-1": "portable-display"})
	if err := os.Rename(
		filepath.Join(second, "class", "drm", "card0-DP-1"),
		filepath.Join(root, "class", "drm", "card0-DP-1"),
	); err != nil {
		t.Fatal(err)
	}

	outputs := discoverOutputs(root, "card1")
	if len(outputs) != 1 || outputs[0].Connector != "HDMI-A-1" {
		t.Fatalf("outputs = %+v", outputs)
	}
}

func TestDiscoverOutputsOnAMachineWithNoDRM(t *testing.T) {
	if outputs := discoverOutputs(t.TempDir(), "card1"); outputs != nil {
		t.Fatalf("outputs = %+v", outputs)
	}
}

func TestConnectedKeepsWhatCanServeAClient(t *testing.T) {
	live := connected(discoverOutputs(labSysfs(t), "card1"))
	if len(live) != 2 {
		t.Fatalf("got %d live outputs, want 2: %+v", len(live), live)
	}
	if live[0].Connector != "HDMI-A-1" || live[1].Connector != "HDMI-A-2" {
		t.Fatalf("live = %+v", live)
	}
}

func TestCardNode(t *testing.T) {
	dri := t.TempDir()
	for _, node := range []string{"card1", "renderD128", "by-path"} {
		if err := os.WriteFile(filepath.Join(dri, node), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cards, err := cardNode(dri)
	if err != nil {
		t.Fatal(err)
	}
	// The render node comes with the second claim and is not a card.
	if len(cards) != 1 || cards[0] != "card1" {
		t.Fatalf("cards = %v", cards)
	}
}

func TestCardNodeWithoutADisplayClaim(t *testing.T) {
	cards, err := cardNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("cards = %v", cards)
	}
}
