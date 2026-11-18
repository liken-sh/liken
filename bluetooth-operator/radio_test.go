package main

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// window sets the adapter properties that a pairing window changes.
// The adapter helper in bluez_test.go sets the rest.
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
			"Bonded":    true,
			"Connected": true,
			"Trusted":   true,
			"UUIDs":     []string{fullUUID("1124")},
		}).
		device("/org/bluez/hci0/dev_CC_CC_CC_CC_CC_CC", map[string]any{
			"Address": "CC:CC:CC:CC:CC:CC",
			"Name":    "Somebody's Phone",
		})

	snapshot, err := snapshotFrom(objects)
	if err != nil {
		t.Fatalf("reading the snapshot from the object tree: %v", err)
	}

	if snapshot.Adapter.Address.Directory() != "00:1A:7D:DA:71:13" || !snapshot.Adapter.Powered {
		t.Errorf("the adapter is %+v, want address 00:1A:7D:DA:71:13 and powered", snapshot.Adapter)
	}
	// These flags tell an idle adapter from one with an open window.
	if !snapshot.Adapter.Discoverable || !snapshot.Adapter.Pairable || !snapshot.Adapter.Discovering {
		t.Errorf("the adapter is %+v, want discoverable, pairable, and discovering", snapshot.Adapter)
	}
	if len(snapshot.Devices) != 2 {
		t.Fatalf("the snapshot has devices %+v, want 2", snapshot.Devices)
	}

	// The snapshot has both the bonded device, which the inventory
	// reads, and the detected one, which a pairing window reports.
	paired, found := snapshot.device(testAddress(t, "A0:AB:51:33:B7:12"))
	if !found {
		t.Fatalf("the paired device is not in the snapshot: %+v", snapshot.Devices)
	}
	if !paired.Paired || !paired.Bonded || !paired.Connected || !paired.Trusted {
		t.Errorf("the paired device is %+v, want paired, bonded, connected, and trusted", paired)
	}
	if paired.Name != "DualSense Wireless Controller" || paired.Alias != "player-one-pad" {
		t.Errorf("the paired device is %+v, want name DualSense Wireless Controller and alias player-one-pad", paired)
	}
	// The pass reads the UUIDs to tell an audio sink, which the
	// operator connects to, from an input device.
	if isAudioSink(paired) || len(paired.UUIDs) != 1 {
		t.Errorf("the paired device is %+v, want no audio sink and 1 UUID", paired)
	}
	seen, found := snapshot.device(testAddress(t, "CC:CC:CC:CC:CC:CC"))
	if !found || seen.Paired {
		t.Errorf("the device with no bond is %+v, want found and not paired", seen)
	}
}

// BlueZ publishes org.bluez.Battery1 beside org.bluez.Device1 on the
// device object, so one read of the tree gives the battery level too.
func TestSnapshotReadsTheBatteryTheIconAndTheAddressType(t *testing.T) {
	objects := managedObjects{}.
		adapter("/org/bluez/hci0").
		device("/org/bluez/hci0/dev_A0_AB_51_33_B7_12", map[string]any{
			"Address":     "A0:AB:51:33:B7:12",
			"Icon":        "input-gaming",
			"AddressType": "random",
			"Paired":      true,
		}).
		battery("/org/bluez/hci0/dev_A0_AB_51_33_B7_12", 62, "HID").
		device("/org/bluez/hci0/dev_CC_CC_CC_CC_CC_CC", map[string]any{
			"Address": "CC:CC:CC:CC:CC:CC",
			"Paired":  true,
		})

	snapshot, err := snapshotFrom(objects)
	if err != nil {
		t.Fatalf("reading the snapshot from the object tree: %v", err)
	}

	charged, _ := snapshot.device(testAddress(t, "A0:AB:51:33:B7:12"))
	if charged.Icon != "input-gaming" || charged.AddressType != "random" {
		t.Errorf("the device is %+v, want icon input-gaming and address type random", charged)
	}
	if charged.Battery == nil {
		t.Fatalf("the device is %+v, want a battery because BlueZ publishes org.bluez.Battery1 for it", charged)
	}
	if charged.Battery.Percentage != 62 || charged.Battery.Source != "HID" {
		t.Errorf("the battery is %+v, want 62 percent from HID", charged.Battery)
	}
	flat, _ := snapshot.device(testAddress(t, "CC:CC:CC:CC:CC:CC"))
	if flat.Battery != nil {
		t.Errorf("the battery is %+v, want none", flat.Battery)
	}
}

// bluetoothd returns a tree with no adapter before it publishes its
// objects and after the adapter is unplugged. snapshotFrom returns
// ErrNoAdapter, not an empty device list, so that a caller does not
// read the tree as a radio with no bonds.
func TestSnapshotReportsNoAdapter(t *testing.T) {
	if _, err := snapshotFrom(nil); err != ErrNoAdapter {
		t.Fatalf("snapshotFrom(nil) returned %v, want ErrNoAdapter", err)
	}
}

// fakeRadio returns the snapshot that the test sets, records every call,
// and changes the snapshot as bluetoothd would.
type fakeRadio struct {
	snapshot radioSnapshot
	err      error
	calls    []string

	// pairErr makes Pair fail, as it does when a controller stops
	// responding during a pairing.
	pairErr error

	// windowErr makes OpenWindow fail, as it does when the adapter is
	// powered off.
	windowErr error

	// connectErr makes Connect fail, as it does when the speaker is
	// switched off or out of range.
	connectErr error

	// connectBlocks blocks Connect until the test closes the channel. A
	// nil channel makes Connect return at once.
	connectBlocks chan struct{}

	// disconnectErr makes Disconnect fail and leave the device
	// connected, as it does when bluetoothd does not reply in time.
	disconnectErr error

	// powerOffErr and powerOnErr make SetAdapterPowered fail, with the
	// adapter unchanged, when it powers the adapter off or on.
	powerOffErr error
	powerOnErr  error

	// mu guards the fields above, because Connect runs on its own
	// goroutine while the pass reads the snapshot.
	mu sync.Mutex
}

func (r *fakeRadio) record(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
}

// called reports whether a recorded call starts with prefix, so that a
// test can match a call without its arguments.
func (r *fakeRadio) called(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

// counted returns how many recorded calls start with prefix.
func (r *fakeRadio) counted(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

// Snapshot copies the device list, because a Connect that is still
// running can change the list while the caller reads it.
func (r *fakeRadio) Snapshot() (radioSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return radioSnapshot{}, r.err
	}
	snapshot := r.snapshot
	snapshot.Devices = slices.Clone(r.snapshot.Devices)
	return snapshot, nil
}

func (r *fakeRadio) SetAdapterAlias(alias string) error {
	r.record("SetAdapterAlias %s", alias)
	r.snapshot.Adapter.Alias = alias
	return nil
}

func (r *fakeRadio) SetAdapterConnectable(connectable bool) error {
	r.record("SetAdapterConnectable %t", connectable)
	r.snapshot.Adapter.Connectable = connectable
	return nil
}

func (r *fakeRadio) SetDeviceAlias(device bonds.Address, alias string) error {
	r.record("SetDeviceAlias %s %s", device.Key(), alias)
	r.update(device, func(state *deviceState) { state.Alias = alias })
	return nil
}

func (r *fakeRadio) SetDeviceTrusted(device bonds.Address, trusted bool) error {
	r.record("SetDeviceTrusted %s %t", device.Key(), trusted)
	r.update(device, func(state *deviceState) { state.Trusted = trusted })
	return nil
}

func (r *fakeRadio) OpenWindow(window time.Duration) error {
	r.record("OpenWindow %s", window)
	if r.windowErr != nil {
		return r.windowErr
	}
	r.snapshot.Adapter.Discoverable = true
	r.snapshot.Adapter.Pairable = true
	r.snapshot.Adapter.Discovering = true
	return nil
}

func (r *fakeRadio) CloseWindow() error {
	r.record("CloseWindow")
	r.snapshot.Adapter.Discoverable = false
	r.snapshot.Adapter.Pairable = false
	r.snapshot.Adapter.Discovering = false
	return nil
}

func (r *fakeRadio) Pair(device bonds.Address) error {
	r.record("Pair %s", device.Key())
	if r.pairErr != nil {
		return r.pairErr
	}
	r.update(device, func(state *deviceState) { state.Paired, state.Bonded = true, true })
	return nil
}

func (r *fakeRadio) Connect(device bonds.Address) error {
	r.record("Connect %s", device.Key())
	r.mu.Lock()
	holds, err := r.connectBlocks, r.connectErr
	r.mu.Unlock()
	if holds != nil {
		<-holds
	}
	if err != nil {
		return err
	}
	r.update(device, func(state *deviceState) { state.Connected = true })
	return nil
}

func (r *fakeRadio) Disconnect(device bonds.Address) error {
	r.record("Disconnect %s", device.Key())
	r.mu.Lock()
	err := r.disconnectErr
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.update(device, func(state *deviceState) { state.Connected = false })
	return nil
}

// SetAdapterPowered disconnects every device on a power-off, as
// bluetoothd does.
func (r *fakeRadio) SetAdapterPowered(powered bool) error {
	r.record("SetAdapterPowered %t", powered)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := map[bool]error{false: r.powerOffErr, true: r.powerOnErr}[powered]; err != nil {
		return err
	}
	r.snapshot.Adapter.Powered = powered
	if !powered {
		for index := range r.snapshot.Devices {
			r.snapshot.Devices[index].Connected = false
		}
	}
	return nil
}

func (r *fakeRadio) Remove(device bonds.Address) error {
	r.record("Remove %s", device.Key())
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]deviceState, 0, len(r.snapshot.Devices))
	for _, state := range r.snapshot.Devices {
		if state.Address != device {
			kept = append(kept, state)
		}
	}
	r.snapshot.Devices = kept
	return nil
}

func (r *fakeRadio) update(device bonds.Address, change func(*deviceState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.snapshot.Devices {
		if r.snapshot.Devices[index].Address == device {
			change(&r.snapshot.Devices[index])
		}
	}
}

// testRadio returns a fake with a powered adapter and the given devices.
func testRadio(t *testing.T, devices ...deviceState) *fakeRadio {
	t.Helper()
	return &fakeRadio{snapshot: radioSnapshot{
		Adapter: adapterState{Address: testAdapterAddress(t), Powered: true, Connectable: true, Alias: "liken-1"},
		Devices: devices,
	}}
}

// pairedDevice returns a connected controller that has a bond.
func pairedDevice(t *testing.T, address string) deviceState {
	t.Helper()
	return deviceState{
		Address:   testAddress(t, address),
		Name:      "DualSense Wireless Controller",
		Alias:     "DualSense Wireless Controller",
		Paired:    true,
		Bonded:    true,
		Connected: true,
		Trusted:   true,
	}
}

// seenDevice returns a controller that the adapter detected and has no
// bond with, which is the kind of device a pairing window reports.
func seenDevice(t *testing.T, address, name string) deviceState {
	t.Helper()
	return deviceState{Address: testAddress(t, address), Name: name, Alias: name}
}
