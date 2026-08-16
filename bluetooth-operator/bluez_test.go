package main

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// managedObjects builds a GetManagedObjects answer out of whole
// objects, in the shape BlueZ returns: a path, the interfaces it
// carries, and each interface's properties.
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
		// Seen on the air with no link key, so it is not this
		// machine's to offer.
		device("/org/bluez/hci0/dev_CC_CC_CC_CC_CC_CC", map[string]any{
			"Address": "CC:CC:CC:CC:CC:CC",
			"Paired":  false,
		}).
		// A device object with no usable address has no identity a
		// claim could name.
		device("/org/bluez/hci0/dev_broken", map[string]any{
			"Address": "not-an-address",
			"Paired":  true,
		})

	controllers, err := controllersFrom(objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(controllers) != 2 {
		t.Fatalf("got %d controllers, want 2: %+v", len(controllers), controllers)
	}
	one := controllers["a0:ab:51:33:b7:12"]
	if one.Name != "DualSense Wireless Controller" || !one.Connected {
		t.Errorf("controller = %+v", one)
	}
	// Paired and disconnected is still a member. A person can claim it
	// and the pod starts when somebody turns the controller on.
	two, ok := controllers["b4:8c:9d:11:22:33"]
	if !ok || two.Connected {
		t.Errorf("controller = %+v, present = %v", two, ok)
	}
}

func TestControllersFromWithAnAdapterAndNoPairings(t *testing.T) {
	// An adapter with nothing paired is an authoritative empty set,
	// and unpairing is the one sanctioned removal.
	controllers, err := controllersFrom(managedObjects{}.adapter("/org/bluez/hci0"))
	if err != nil {
		t.Fatal(err)
	}
	if len(controllers) != 0 {
		t.Fatalf("got %+v, want an empty set", controllers)
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
				t.Fatalf("err = %v, want ErrNoAdapter", err)
			}
		})
	}
}

// nameOwnerChanged builds the bus daemon's report that a name changed
// hands. An empty owner means the name has no owner now.
func nameOwnerChanged(name, owner string) *dbus.Signal {
	return &dbus.Signal{
		Name: "org.freedesktop.DBus.NameOwnerChanged",
		Body: []any{name, ":1.7", owner},
	}
}

func TestRelayBlueZSignalsWakesOnASignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signals := make(chan *dbus.Signal, 4)
	released := make(chan struct{})
	changed := relayBlueZSignals(ctx, signals, func() { close(released) })

	signals <- &dbus.Signal{Name: "org.freedesktop.DBus.Properties.PropertiesChanged"}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("a signal produced no wake")
	}

	// A closed signal channel is godbus reporting that the connection
	// to the bus is gone. The relay closes its own channel, which is
	// what the main loop reads as the lost bus.
	close(signals)
	select {
	case _, ok := <-changed:
		if ok {
			t.Fatal("the relay emitted after its source closed")
		}
	case <-time.After(time.Second):
		t.Fatal("the relay did not close its channel")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("the relay never released its signal channel")
	}
}

func TestRelayBlueZSignalsStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan *dbus.Signal, 1)
	released := make(chan struct{})
	changed := relayBlueZSignals(ctx, signals, func() { close(released) })

	cancel()
	select {
	case _, ok := <-changed:
		if ok {
			t.Fatal("the relay emitted after its context ended")
		}
	case <-time.After(time.Second):
		t.Fatal("the relay did not close its channel")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("the relay never released its signal channel")
	}
}

func TestWatchNameLossReportsADeadConnection(t *testing.T) {
	// godbus closes every registered signal channel when the
	// connection to the bus is lost. bluetoothd is then unreachable
	// whether or not it is still running, so this counts as the daemon
	// leaving. Without it the operator would exit zero and look like a
	// clean shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	names := make(chan *dbus.Signal, 1)
	released := make(chan struct{})
	gone := watchNameLoss(ctx, names, func() { close(released) })

	close(names)
	select {
	case <-gone:
	case <-time.After(time.Second):
		t.Fatal("a closed signal channel did not report the daemon gone")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("the watcher never released its signal channel")
	}
}

func TestWatchNameLossReportsTheNameLosingItsOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	names := make(chan *dbus.Signal, 4)
	gone := watchNameLoss(ctx, names, func() {})

	// Neither of these is bluetoothd going away.
	names <- nameOwnerChanged("org.freedesktop.systemd1", "")
	names <- nameOwnerChanged(bluezService, ":1.9")
	names <- &dbus.Signal{Name: "org.bluez.Adapter1.PropertiesChanged"}
	select {
	case <-gone:
		t.Fatal("an unrelated signal reported the daemon gone")
	case <-time.After(50 * time.Millisecond):
	}

	names <- nameOwnerChanged(bluezService, "")
	select {
	case <-gone:
	case <-time.After(time.Second):
		t.Fatal("bluetoothd losing its name did not report it gone")
	}
}

func TestWatchNameLossStaysQuietOnShutdown(t *testing.T) {
	// A shutdown must not read as the daemon dying. The channel stays
	// open and empty, so the loop's own ctx.Done branch wins.
	ctx, cancel := context.WithCancel(context.Background())
	names := make(chan *dbus.Signal, 1)
	released := make(chan struct{})
	gone := watchNameLoss(ctx, names, func() { close(released) })

	cancel()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("the watcher never released its signal channel")
	}
	select {
	case <-gone:
		t.Fatal("the shutdown reported the daemon gone")
	case <-time.After(50 * time.Millisecond):
	}
}
