package main

// The read of one snapshot out of a managed-object tree, which is the
// one place the pairing controllers depend on BlueZ's own shape, and
// the fake radio every other test drives the controllers through.

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// windowState adds the properties an adapter has while a pairing
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
			"UUIDs":     []string{fullUUID("1124")},
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
	// The browsed UUIDs come along in the snapshot, because the pass
	// reads them to tell an audio sink the operator pages from an
	// input device it leaves alone.
	if isAudioSink(paired) || len(paired.UUIDs) != 1 {
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
// The level and the icon arrive in the same managed-objects read as
// everything else, because BlueZ publishes org.bluez.Battery1 beside
// org.bluez.Device1 on the device object.
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
		t.Fatal(err)
	}

	charged, _ := snapshot.device(testAddress(t, "A0:AB:51:33:B7:12"))
	if charged.Icon != "input-gaming" || charged.AddressType != "random" {
		t.Errorf("device = %+v", charged)
	}
	if charged.Battery == nil {
		t.Fatalf("the device reports a level and the snapshot holds none: %+v", charged)
	}
	if charged.Battery.Percentage != 62 || charged.Battery.Source != "HID" {
		t.Errorf("battery = %+v", charged.Battery)
	}
	// A device with no battery has no Battery1 interface at all, which is
	// the ordinary case.
	flat, _ := snapshot.device(testAddress(t, "CC:CC:CC:CC:CC:CC"))
	if flat.Battery != nil {
		t.Errorf("battery = %+v, want none", flat.Battery)
	}
}

func TestSnapshotReportsNoAdapter(t *testing.T) {
	if _, err := snapshotFrom(nil); err != ErrNoAdapter {
		t.Fatalf("an empty tree gave %v, want ErrNoAdapter", err)
	}
}

// fakeRadio returns a snapshot the test supplies, and records
// every call the controllers make into bluetoothd.
type fakeRadio struct {
	snapshot radioSnapshot
	err      error
	calls    []string

	// pairErr makes Pair fail, the way bluetoothd does for a controller
	// that stopped answering mid-pairing.
	pairErr error

	// connectErr makes Connect fail the way bluetoothd does for a
	// speaker that is switched off or out of range.
	connectErr error

	// connectHolds holds a call in flight until the test closes it. A
	// nil channel is a call that returns at once.
	connectHolds chan struct{}

	// mu covers the fields above, because Connect runs on its own
	// goroutine while the pass reads the snapshot on the loop's.
	mu sync.Mutex
}

func (r *fakeRadio) record(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
}

// called reports whether this call was recorded, matched by prefix,
// so a test names the call and not its arguments when the arguments
// do not matter.
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

// counted answers how many recorded calls start with a prefix, which
// is what a test asks when one call is right and two are wrong.
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

// Snapshot copies the device list, because a Connect in flight writes
// into it while the caller reads what the call returned.
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
	r.update(device, func(state *deviceState) { state.Paired = true })
	return nil
}

func (r *fakeRadio) Connect(device bonds.Address) error {
	r.record("Connect %s", device.Key())
	r.mu.Lock()
	holds, err := r.connectHolds, r.connectErr
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
	r.update(device, func(state *deviceState) { state.Connected = false })
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

// testRadio is one adapter with the devices a test names.
func testRadio(t *testing.T, devices ...deviceState) *fakeRadio {
	t.Helper()
	return &fakeRadio{snapshot: radioSnapshot{
		Adapter: adapterState{Address: testAdapterAddress(t), Powered: true, Connectable: true, Alias: "liken-1"},
		Devices: devices,
	}}
}

// pairedDevice is a controller with a bond, as bluetoothd reports it.
func pairedDevice(t *testing.T, address string) deviceState {
	t.Helper()
	return deviceState{
		Address:   testAddress(t, address),
		Name:      "DualSense Wireless Controller",
		Alias:     "DualSense Wireless Controller",
		Paired:    true,
		Connected: true,
		Trusted:   true,
	}
}

// seenDevice is a controller the radio has observed and holds no bond
// with, which is the kind of device a pairing window reports.
func seenDevice(t *testing.T, address, name string) deviceState {
	t.Helper()
	return deviceState{Address: testAddress(t, address), Name: name, Alias: name}
}
