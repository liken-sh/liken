package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/godbus/dbus/v5"
)

// managedObjects builds a GetManagedObjects reply in the shape BlueZ
// returns: each object path maps to its interfaces, and each interface
// maps to its properties.
type managedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant

func (m managedObjects) adapter(path string) managedObjects {
	m[dbus.ObjectPath(path)] = map[string]map[string]dbus.Variant{
		adapterInterface: {
			"Address": dbus.MakeVariant("00:1A:7D:DA:71:13"),
			"Powered": dbus.MakeVariant(true),
		},
	}
	return m
}

func (m managedObjects) device(path string, properties map[string]any) managedObjects {
	variants := map[string]dbus.Variant{}
	for key, value := range properties {
		variants[key] = dbus.MakeVariant(value)
	}
	m[dbus.ObjectPath(path)] = map[string]map[string]dbus.Variant{deviceInterface: variants}
	return m
}

// battery adds the Battery1 interface to a device already in the tree.
func (m managedObjects) battery(path string, percentage byte, source string) managedObjects {
	m[dbus.ObjectPath(path)][batteryInterface] = map[string]dbus.Variant{
		"Percentage": dbus.MakeVariant(percentage),
		"Source":     dbus.MakeVariant(source),
	}
	return m
}

func TestControllersFromKeepsThePairedSet(t *testing.T) {
	objects := managedObjects{}.
		adapter("/org/bluez/hci0").
		device("/org/bluez/hci0/dev_A0_AB_51_33_B7_12", map[string]any{
			"Address":   "A0:AB:51:33:B7:12",
			"Alias":     "DualSense Wireless Controller",
			"Paired":    true,
			"Connected": true,
		}).
		device("/org/bluez/hci0/dev_B4_8C_9D_11_22_33", map[string]any{
			"Address":   "B4:8C:9D:11:22:33",
			"Alias":     "Player Two",
			"Paired":    true,
			"Connected": false,
		}).
		// Found by discovery and never paired, so it has no link key.
		device("/org/bluez/hci0/dev_CC_CC_CC_CC_CC_CC", map[string]any{
			"Address": "CC:CC:CC:CC:CC:CC",
			"Paired":  false,
		}).
		// A claim cannot refer to a device whose address is not a MAC.
		device("/org/bluez/hci0/dev_broken", map[string]any{
			"Address": "not-an-address",
			"Paired":  true,
		})

	controllers, err := controllersFrom(objects)
	if err != nil {
		t.Fatalf("reading the controllers: %v", err)
	}
	if len(controllers) != 2 {
		t.Fatalf("the set holds %d controllers, want 2: %+v", len(controllers), controllers)
	}
	one := controllers["a0:ab:51:33:b7:12"]
	if one.Name != "DualSense Wireless Controller" || !one.Connected {
		t.Errorf("the controller a0:ab:51:33:b7:12 is %+v, want the name DualSense Wireless Controller and Connected", one)
	}
	// A paired controller that is disconnected stays in the set.
	two, ok := controllers["b4:8c:9d:11:22:33"]
	if !ok || two.Connected {
		t.Errorf("the controller b4:8c:9d:11:22:33 is %+v (present %v), want present and not Connected", two, ok)
	}
}

func TestControllersFromReadsTheDeviceProperties(t *testing.T) {
	objects := managedObjects{}.
		adapter("/org/bluez/hci0").
		device("/org/bluez/hci0/dev_E3_28_E9_23_21_6F", map[string]any{
			"Address":     "E3:28:E9:23:21:6F",
			"AddressType": "public",
			"Alias":       "studio-pa",
			"Paired":      true,
			"Connected":   false,
			"Class":       uint32(0x2c0418),
			"Appearance":  uint16(0x0941),
			"Icon":        "audio-headphones",
			"Modalias":    "bluetooth:v000ApFFFFdFFFF",
			"UUIDs": []string{
				"0000110b-0000-1000-8000-00805f9b34fb",
				"0000110a-0000-1000-8000-00805f9b34fb",
			},
		})

	controllers, err := controllersFrom(objects)
	if err != nil {
		t.Fatalf("reading the controllers: %v", err)
	}
	got := controllers["e3:28:e9:23:21:6f"]
	want := controller{
		Name:        "studio-pa",
		Connected:   false,
		Class:       0x2c0418,
		Appearance:  0x0941,
		Icon:        "audio-headphones",
		Modalias:    "bluetooth:v000ApFFFFdFFFF",
		AddressType: "public",
		UUIDs: []string{
			"0000110b-0000-1000-8000-00805f9b34fb",
			"0000110a-0000-1000-8000-00805f9b34fb",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the controller is %+v, want %+v", got, want)
	}
}

func TestControllersFromLeavesAnAbsentPropertyZero(t *testing.T) {
	objects := managedObjects{}.
		adapter("/org/bluez/hci0").
		device("/org/bluez/hci0/dev_A0_AB_51_33_B7_12", map[string]any{
			"Address": "A0:AB:51:33:B7:12",
			"Paired":  true,
		})

	controllers, err := controllersFrom(objects)
	if err != nil {
		t.Fatalf("reading the controllers: %v", err)
	}
	if got := controllers["a0:ab:51:33:b7:12"]; !reflect.DeepEqual(got, controller{}) {
		t.Fatalf("the controller is %+v, want the zero value", got)
	}
}

func TestControllersFromWithAnAdapterAndNoPeripherals(t *testing.T) {
	// An adapter with no paired devices is an empty paired set and not
	// an error, because unpairing the last controller must remove it.
	controllers, err := controllersFrom(managedObjects{}.adapter("/org/bluez/hci0"))
	if err != nil {
		t.Fatalf("reading the controllers: %v", err)
	}
	if len(controllers) != 0 {
		t.Fatalf("the set is %+v, want empty", controllers)
	}
}

func TestControllersFromWithoutAnAdapter(t *testing.T) {
	cases := map[string]managedObjects{
		"an empty tree, which is bluetoothd still starting": {},
		"the bus root alone": managedObjects{
			"/org/bluez": {"org.freedesktop.DBus.ObjectManager": {}},
		},
	}
	for name, objects := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := controllersFrom(objects); err != ErrNoAdapter {
				t.Fatalf("the error is %v, want ErrNoAdapter", err)
			}
		})
	}
}

func TestAdapterAddressFromReadsTheAdapter(t *testing.T) {
	objects := managedObjects{}.
		adapter("/org/bluez/hci0").
		device("/org/bluez/hci0/dev_A0_AB_51_33_B7_12", map[string]any{
			"Address": "A0:AB:51:33:B7:12",
			"Paired":  true,
		})

	address, err := adapterAddressFrom(objects)
	if err != nil {
		t.Fatalf("reading the adapter address: %v", err)
	}
	// The bond Secrets are labeled with the adapter's address, not a
	// device's.
	if address.Directory() != "00:1A:7D:DA:71:13" {
		t.Fatalf("the adapter address is %s, want the address of the adapter", address)
	}
}

func TestAdapterAddressFromWithoutAnAdapter(t *testing.T) {
	if _, err := adapterAddressFrom(managedObjects{}); err != ErrNoAdapter {
		t.Fatalf("the error is %v, want ErrNoAdapter", err)
	}
}

func TestAdapterAddressFromWithTwoAdapters(t *testing.T) {
	// Every read picks the lowest object path, so every bond gets the
	// same label.
	objects := managedObjects{}.adapter("/org/bluez/hci1")
	objects[dbus.ObjectPath("/org/bluez/hci0")] = map[string]map[string]dbus.Variant{
		adapterInterface: {"Address": dbus.MakeVariant("14:B4:57:91:2F:C8")},
	}

	for range 4 {
		address, err := adapterAddressFrom(objects)
		if err != nil {
			t.Fatalf("reading the adapter address: %v", err)
		}
		if address.Directory() != "14:B4:57:91:2F:C8" {
			t.Fatalf("the adapter address is %s, want the address of the adapter", address)
		}
	}
}

func TestAdapterAddressFromWithAnUnusableAddress(t *testing.T) {
	objects := managedObjects{}
	objects[dbus.ObjectPath("/org/bluez/hci0")] = map[string]map[string]dbus.Variant{
		adapterInterface: {"Address": dbus.MakeVariant("not-an-address")},
	}

	if _, err := adapterAddressFrom(objects); err == nil {
		t.Fatal("an adapter with the address not-an-address returned no error, want one")
	}
}

// nameOwnerChanged builds a NameOwnerChanged signal. An empty owner
// means the name has no owner after the change.
func nameOwnerChanged(name, owner string) *dbus.Signal {
	return &dbus.Signal{
		Name: "org.freedesktop.DBus.NameOwnerChanged",
		Body: []any{name, ":1.7", owner},
	}
}

// ready does a non-blocking receive. got is true when the receive
// completed, either with a value or because the channel is closed, and
// open is false only for a closed channel. A test calls it after
// synctest.Wait, when the goroutine under test is blocked.
func ready[T any](from <-chan T) (open, got bool) {
	select {
	case _, open := <-from:
		return open, true
	default:
		return false, false
	}
}

func TestRelayBlueZSignalsWakesOnASignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		signals := make(chan *dbus.Signal, 4)
		released := make(chan struct{})
		changed := relayBlueZSignals(t.Context(), signals, newLinkEnds(), func() { close(released) })

		signals <- &dbus.Signal{Name: "org.freedesktop.DBus.Properties.PropertiesChanged"}
		synctest.Wait()
		if open, got := ready(changed); !open || !got {
			t.Fatal("a signal from bluetoothd sent no wake")
		}

		// godbus closes the signal channel when the connection to the
		// bus is lost, and the relay must then close its own channel.
		close(signals)
		synctest.Wait()
		if open, got := ready(changed); open || !got {
			t.Fatal("the relay left its channel open after its source closed, want it closed")
		}
		if _, got := ready(released); !got {
			t.Fatal("the relay did not remove its signal channel, want it removed")
		}
	})
}

func TestRelayBlueZSignalsRecordsWhyALinkEnded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		signals := make(chan *dbus.Signal, 4)
		ends := newLinkEnds()
		changed := relayBlueZSignals(t.Context(), signals, ends, func() {})

		signals <- disconnected("/org/bluez/hci0/dev_C2_11_22_33_44_55", blueZReasonTimeout)
		synctest.Wait()

		if _, got := ready(changed); !got {
			t.Error("the signal sent no wake")
		}
		if reason := ends.reason(testAddress(t, sampleRemote)); reason != blueZReasonTimeout {
			t.Errorf("the record holds %q, want %q", reason, blueZReasonTimeout)
		}
	})
}

func TestRelayBlueZSignalsStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		signals := make(chan *dbus.Signal, 1)
		released := make(chan struct{})
		changed := relayBlueZSignals(ctx, signals, newLinkEnds(), func() { close(released) })

		cancel()
		synctest.Wait()
		if open, got := ready(changed); open || !got {
			t.Fatal("the relay left its channel open after its context ended, want it closed")
		}
		if _, got := ready(released); !got {
			t.Fatal("the relay did not remove its signal channel, want it removed")
		}
	})
}

func TestWatchNameLossReportsADeadConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// godbus closes every registered signal channel when the
		// connection to the bus is lost, and that counts as bluetoothd
		// leaving.
		names := make(chan *dbus.Signal, 1)
		released := make(chan struct{})
		gone := watchNameLoss(t.Context(), names, func() { close(released) })

		close(names)
		synctest.Wait()
		if _, got := ready(gone); !got {
			t.Fatal("a closed signal channel did not report bluetoothd gone, want it reported")
		}
		if _, got := ready(released); !got {
			t.Fatal("the watcher did not remove its signal channel, want it removed")
		}
	})
}

func TestWatchNameLossReportsTheNameLosingItsOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		names := make(chan *dbus.Signal, 4)
		gone := watchNameLoss(t.Context(), names, func() {})

		// None of these means bluetoothd left the bus.
		names <- nameOwnerChanged("org.freedesktop.systemd1", "")
		names <- nameOwnerChanged(bluezService, ":1.9")
		names <- &dbus.Signal{Name: "org.bluez.Adapter1.PropertiesChanged"}
		synctest.Wait()
		if _, got := ready(gone); got {
			t.Fatal("an unrelated signal reported bluetoothd gone, want no report")
		}

		names <- nameOwnerChanged(bluezService, "")
		synctest.Wait()
		if _, got := ready(gone); !got {
			t.Fatal("bluetoothd losing its bus name did not report it gone, want it reported")
		}
	})
}

func TestWatchNameLossStaysQuietOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		names := make(chan *dbus.Signal, 1)
		released := make(chan struct{})
		gone := watchNameLoss(ctx, names, func() { close(released) })

		cancel()
		synctest.Wait()
		if _, got := ready(released); !got {
			t.Fatal("the watcher did not remove its signal channel, want it removed")
		}
		if _, got := ready(gone); got {
			t.Fatal("the shutdown reported bluetoothd gone, want no report")
		}
	})
}

// absentBusAddress points D-Bus at a socket that does not exist, which
// is what the operator finds when it starts before the bluetoothd
// container has bound the socket.
func absentBusAddress(t *testing.T) {
	t.Helper()
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "system_bus_socket"))
}

func TestWaitForBusReportsABusThatNeverArrives(t *testing.T) {
	absentBusAddress(t)

	if _, err := waitForBus(context.Background(), 0); err == nil {
		t.Fatal("waitForBus returned a connection, want an error because the bus is not there")
	}
}

func TestWaitForBusStopsOnShutdown(t *testing.T) {
	absentBusAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForBus(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForBus err = %v, want %v", err, context.Canceled)
	}
}
