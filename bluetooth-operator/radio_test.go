package main

// These tests read one snapshot out of a managed-object tree, which is
// the one place the pairing controllers depend on BlueZ's own shape.

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// windowState adds the properties an adapter carries while a pairing
// window is open. The tree helper in bluez_test.go writes the two
// every reader needs, and these are the rest.
func (m managedObjects) window(path string, discoverable, pairable, discovering bool) managedObjects {
	properties := m[dbus.ObjectPath(path)][adapterInterface]
	properties["Alias"] = dbus.MakeVariant("liken-1")
	properties["Discoverable"] = dbus.MakeVariant(discoverable)
	properties["Pairable"] = dbus.MakeVariant(pairable)
	properties["Discovering"] = dbus.MakeVariant(discovering)
	return m
}

func TestSnapshotReadsTheAdapterAndItsDevices(t *testing.T) {
	objects := managedObjects{}.
		adapter("/org/bluez/hci0").
		window("/org/bluez/hci0", true, true, true).
		device("/org/bluez/hci0/dev_A0_AB_51_33_B7_12", map[string]any{
			"Address":   "A0:AB:51:33:B7:12",
			"Name":      "DualSense Wireless Controller",
			"Alias":     "player-one-pad",
			"Paired":    true,
			"Connected": true,
			"Trusted":   true,
		}).
		device("/org/bluez/hci0/dev_CC_CC_CC_CC_CC_CC", map[string]any{
			"Address": "CC:CC:CC:CC:CC:CC",
			"Name":    "Somebody's Phone",
		})

	snapshot, err := snapshotFrom(objects)
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.Adapter.Address.Directory() != "00:1A:7D:DA:71:13" || !snapshot.Adapter.Powered {
		t.Errorf("adapter = %+v", snapshot.Adapter)
	}
	// The window's three flags separate an idle radio from one that is
	// still advertising itself.
	if !snapshot.Adapter.Discoverable || !snapshot.Adapter.Pairable || !snapshot.Adapter.Discovering {
		t.Errorf("adapter = %+v", snapshot.Adapter)
	}
	if len(snapshot.Devices) != 2 {
		t.Fatalf("devices = %+v", snapshot.Devices)
	}

	// A device the radio has only observed and one it holds a bond with
	// both appear, because a pairing window reports the first kind and
	// the inventory is built from the second.
	paired, found := snapshot.device(testAddress(t, "A0:AB:51:33:B7:12"))
	if !found {
		t.Fatalf("the paired device is not in the snapshot: %+v", snapshot.Devices)
	}
	if !paired.Paired || !paired.Connected || !paired.Trusted {
		t.Errorf("device = %+v", paired)
	}
	if paired.Name != "DualSense Wireless Controller" || paired.Alias != "player-one-pad" {
		t.Errorf("device = %+v", paired)
	}
	seen, found := snapshot.device(testAddress(t, "CC:CC:CC:CC:CC:CC"))
	if !found || seen.Paired {
		t.Errorf("the device with no bond was read as %+v", seen)
	}
}

// A tree with no adapter in it occurs in two cases: bluetoothd has
// not published its object tree yet, and the radio has gone away.
// Neither case reports anything about which devices are paired.
func TestSnapshotReportsNoAdapter(t *testing.T) {
	if _, err := snapshotFrom(nil); err != ErrNoAdapter {
		t.Fatalf("an empty tree gave %v, want ErrNoAdapter", err)
	}
}
