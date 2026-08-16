package main

// Reading the paired set from bluetoothd.
//
// bluetoothd holds the fact that no other layer can read: which
// controllers are paired, and which of them are connected right now.
// It is not in sysfs and it is not on the Machine, and that is the
// whole reason this operator exists. The API is BlueZ's D-Bus
// interface, on a bus that runs inside this pod for these two
// processes alone.
//
// Membership in the ResourceSlice is the paired set, not the
// connected set. A paired controller that is switched off still
// publishes, so a person can create a pod for it and the pod starts
// when somebody turns the controller on. Connection state publishes
// as an attribute and drives the taint (slices.go).
//
// The signals here say only that something changed. Every consumer
// re-reads the whole managed-object tree, the same way liken's
// hardware watcher re-walks sysfs after a uevent. A mirror built from
// signal payloads can drift out of step with the daemon, and a
// re-read cannot.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezService     = "org.bluez"
	deviceInterface  = "org.bluez.Device1"
	adapterInterface = "org.bluez.Adapter1"
)

// ErrNoAdapter reports that bluetoothd published no adapter, so its
// answer says nothing about which controllers are paired.
//
// This is the difference between "no controller is paired" and "there
// is nothing to ask". bluetoothd publishes its object tree a moment
// after it claims its bus name, and it removes every device object
// when the adapter itself goes away, so an empty answer arrives in
// both of those cases as well. Treating one as the other would retract
// every published controller while a claim still held one, which is
// the deletion that strands a consumer.
var ErrNoAdapter = errors.New("bluetoothd published no adapter")

// controller is one paired controller, as bluetoothd reports it. The
// address is the map key that pairedControllers returns it under, in
// the one normalized form this program keys on.
type controller struct {
	Name      string
	Connected bool
}

// pairedControllers reads every paired device from bluetoothd, keyed
// by the normalized MAC address.
//
// The call is GetManagedObjects on BlueZ's root object, which returns
// the adapters, the devices below them, and every interface each one
// carries, in one round trip.
func pairedControllers(conn *dbus.Conn) (map[string]controller, error) {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := conn.Object(bluezService, "/").
		Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).
		Store(&objects)
	if err != nil {
		return nil, fmt.Errorf("reading BlueZ's managed objects: %w", err)
	}
	return controllersFrom(objects)
}

// controllersFrom reads the paired set out of one managed-object
// tree. It is separate from the call so that the rules below are
// testable without a bus.
//
// A device object with Paired false is a controller that BlueZ has
// seen on the air and holds no link key for, so it is not this
// machine's to offer. An answer with no adapter in it is ErrNoAdapter,
// never an empty paired set.
func controllersFrom(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) (map[string]controller, error) {
	adapters := 0
	controllers := map[string]controller{}
	for _, interfaces := range objects {
		if _, ok := interfaces[adapterInterface]; ok {
			adapters++
		}
		properties, ok := interfaces[deviceInterface]
		if !ok {
			continue
		}
		address, _ := properties["Address"].Value().(string)
		mac := normalizeMAC(address)
		if !validMAC(mac) {
			continue
		}
		if paired, _ := properties["Paired"].Value().(bool); !paired {
			continue
		}
		connected, _ := properties["Connected"].Value().(bool)
		name, _ := properties["Alias"].Value().(string)
		controllers[mac] = controller{Name: name, Connected: connected}
	}
	if adapters == 0 {
		return nil, ErrNoAdapter
	}
	return controllers, nil
}

// watchBlueZ returns a channel that reports whenever the paired set
// or a connection state may have changed. Three signals cover it:
// InterfacesAdded for a new pairing, InterfacesRemoved for an
// unpairing, and PropertiesChanged on a device object for a connect
// or a disconnect.
//
// The channel is buffered and a full channel drops the signal, for
// the same reason the uevent channel does: the reader re-reads the
// whole tree, so one wake answers a burst.
func watchBlueZ(ctx context.Context, conn *dbus.Conn) (<-chan struct{}, error) {
	matches := [][]dbus.MatchOption{
		{
			dbus.WithMatchSender(bluezService),
			dbus.WithMatchInterface("org.freedesktop.DBus.ObjectManager"),
		},
		{
			dbus.WithMatchSender(bluezService),
			dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
			dbus.WithMatchMember("PropertiesChanged"),
			dbus.WithMatchArg(0, deviceInterface),
		},
	}
	for _, match := range matches {
		if err := conn.AddMatchSignal(match...); err != nil {
			return nil, fmt.Errorf("subscribing to BlueZ's signals: %w", err)
		}
	}

	signals := make(chan *dbus.Signal, 64)
	conn.Signal(signals)
	return relayBlueZSignals(ctx, signals, func() { conn.RemoveSignal(signals) }), nil
}

// relayBlueZSignals is watchBlueZ's loop, over a signal channel that
// is already subscribed. It is separate from the subscription so that
// a test can drive it without a bus.
//
// release unregisters the channel from the connection. The loop calls
// it on the way out, whichever way it leaves, because a connection
// that keeps delivering to a channel nobody reads holds the channel
// and the goroutine godbus spawns for it forever.
func relayBlueZSignals(ctx context.Context, signals <-chan *dbus.Signal, release func()) <-chan struct{} {
	changed := make(chan struct{}, 1)
	go func() {
		defer close(changed)
		defer release()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-signals:
				if !ok {
					return
				}
				select {
				case changed <- struct{}{}:
				default:
				}
			}
		}
	}()
	return changed
}

// waitForBlueZ blocks until bluetoothd owns its bus name, or until
// the timeout passes. The operator and bluetoothd start in the same
// container, so the operator can reach the bus before the daemon has
// claimed its name.
//
// The wait is bounded on purpose. A bluetoothd that never claims the
// name is a failure to report, not a state to sit in: the pod's
// restart is the retry, and the failure is visible in kubectl instead
// of hidden in a log.
func waitForBlueZ(ctx context.Context, conn *dbus.Conn, timeout time.Duration) error {
	// The match goes on before the check, so a daemon that claims the
	// name between the two still wakes this wait.
	if err := matchBlueZName(conn); err != nil {
		return err
	}
	names := make(chan *dbus.Signal, 8)
	conn.Signal(names)
	defer conn.RemoveSignal(names)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		owned, err := blueZOwned(conn)
		if err != nil {
			return err
		}
		if owned {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("bluetoothd did not claim %s within %s", bluezService, timeout)
		case <-names:
		}
	}
}

// watchBlueZExit returns a channel that carries one value when
// bluetoothd leaves the bus.
//
// bluetoothd owns the HID sessions, and killing it disconnects every
// controller at once, so an operator that kept publishing after the
// daemon died would offer devices that no pod can use. The operator
// ends instead, the container ends with it, and the kubelet restarts
// the pair.
//
// The channel carries a value rather than closing, and the watcher
// leaves it open when its context ends. A closed channel is always
// ready to receive, so closing it on an ordinary shutdown would race
// the shutdown's own branch and report the daemon's death when
// nothing died.
//
// A closed signal channel counts as the daemon leaving. godbus closes
// every registered signal channel when the connection to the bus is
// lost, and a lost bus is a bus daemon or a bluetoothd that is no
// longer there. Either way this operator can no longer read the
// paired set.
func watchBlueZExit(ctx context.Context, conn *dbus.Conn) (<-chan struct{}, error) {
	if err := matchBlueZName(conn); err != nil {
		return nil, err
	}
	names := make(chan *dbus.Signal, 8)
	conn.Signal(names)
	return watchNameLoss(ctx, names, func() { conn.RemoveSignal(names) }), nil
}

// watchNameLoss is watchBlueZExit's loop, over a signal channel that
// is already subscribed. It is separate from the subscription so that
// a test can drive it without a bus, and release does the same job it
// does in relayBlueZSignals.
func watchNameLoss(ctx context.Context, names <-chan *dbus.Signal, release func()) <-chan struct{} {
	gone := make(chan struct{}, 1)
	go func() {
		defer release()
		for {
			select {
			case <-ctx.Done():
				return
			case signal, ok := <-names:
				if !ok {
					gone <- struct{}{}
					return
				}
				if signal.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(signal.Body) != 3 {
					continue
				}
				name, _ := signal.Body[0].(string)
				owner, _ := signal.Body[2].(string)
				if name == bluezService && owner == "" {
					gone <- struct{}{}
					return
				}
			}
		}
	}()
	return gone
}

// matchBlueZName subscribes to the bus daemon's report that BlueZ's
// name changed hands. Both the startup wait and the exit watch need
// it, and each one asks for it, because a match rule that one
// function adds as a side effect for another is a dependency that
// nothing states.
func matchBlueZName(conn *dbus.Conn) error {
	err := conn.AddMatchSignal(
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, bluezService),
	)
	if err != nil {
		return fmt.Errorf("subscribing to bus name changes: %w", err)
	}
	return nil
}

// blueZOwned asks the bus daemon whether anything owns BlueZ's name.
func blueZOwned(conn *dbus.Conn) (bool, error) {
	var owned bool
	err := conn.BusObject().
		Call("org.freedesktop.DBus.NameHasOwner", 0, bluezService).
		Store(&owned)
	if err != nil {
		return false, fmt.Errorf("asking the bus who owns %s: %w", bluezService, err)
	}
	return owned, nil
}
