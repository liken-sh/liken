package main

// This file reads the paired controllers from bluetoothd over D-Bus and
// subscribes to bluetoothd's signals. Only bluetoothd records which
// controllers are paired and which are connected. Neither fact is in
// sysfs. The D-Bus system bus is private to the pod, and only
// bluetoothd and the operator connect to it.
//
// A signal only wakes the loop. The pass that follows reads the whole
// object tree again, because state built from signal payloads can drift
// from bluetoothd's and a full read cannot. Disconnected is the
// exception: the tree does not record why a link ended, so the signal
// reader stores the reason in linkEnds until a pass writes it
// (linkend.go).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

const (
	bluezService     = "org.bluez"
	deviceInterface  = "org.bluez.Device1"
	adapterInterface = "org.bluez.Adapter1"
)

// ErrNoAdapter means the tree has no adapter, so it cannot tell which
// controllers are paired. A tree with no devices is also what
// bluetoothd returns just after it claims its bus name and after the
// adapter goes away, because it removes every device object with the
// adapter. If the operator read that tree as "nothing is paired", it
// would remove every controller from the ResourceSlice while claims
// still held them.
var ErrNoAdapter = errors.New("bluetoothd published no adapter")

// controller holds the Device1 properties of one paired controller. A
// field is its zero value when bluetoothd reports no such property, and
// devices.go publishes no attribute for a zero value.
type controller struct {
	Name        string
	Connected   bool
	Class       uint32
	Appearance  uint16
	Icon        string
	Modalias    string
	AddressType string
	UUIDs       []string
}

// pairedControllers reads every paired device from bluetoothd, keyed
// by normalized MAC address.
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

// controllersFrom is pairedControllers without the bus, so a test can
// give it a tree.
//
// A device with Paired false was found by discovery and has no link
// key, so it is skipped. A paired controller that is disconnected stays
// in the map, because devices.go publishes it with a taint, and a claim
// can name a controller that is switched off.
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
		class, _ := properties["Class"].Value().(uint32)
		appearance, _ := properties["Appearance"].Value().(uint16)
		icon, _ := properties["Icon"].Value().(string)
		modalias, _ := properties["Modalias"].Value().(string)
		addressType, _ := properties["AddressType"].Value().(string)
		uuids, _ := properties["UUIDs"].Value().([]string)
		controllers[mac] = controller{
			Name:        name,
			Connected:   connected,
			Class:       class,
			Appearance:  appearance,
			Icon:        icon,
			Modalias:    modalias,
			AddressType: addressType,
			UUIDs:       uuids,
		}
	}
	if adapters == 0 {
		return nil, ErrNoAdapter
	}
	return controllers, nil
}

// adapterAddress reads the address of bluetoothd's adapter. The bond
// Secrets are labeled with it, so the operator needs it before it can
// write a bond. Reading it from Adapter1 gives the adapter whose bond
// directory bluetoothd writes. bondfetch runs before bluetoothd starts,
// so it reads the address from the kernel with the HCIGETDEVINFO ioctl.
func adapterAddress(conn *dbus.Conn) (bonds.Address, error) {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := conn.Object(bluezService, "/").
		Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).
		Store(&objects)
	if err != nil {
		return bonds.Address{}, fmt.Errorf("reading BlueZ's managed objects: %w", err)
	}
	return adapterAddressFrom(objects)
}

// adapterAddressFrom is adapterAddress without the bus, so a test can
// give it a tree.
//
// A pod claims one adapter. If the tree has more than one, this picks
// the lowest object path, so every read of the same tree returns the
// same address. Go's map order would pick a different one each time.
func adapterAddressFrom(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) (bonds.Address, error) {
	chosen, address := "", ""
	for path, interfaces := range objects {
		properties, ok := interfaces[adapterInterface]
		if !ok {
			continue
		}
		if chosen != "" && string(path) >= chosen {
			continue
		}
		chosen = string(path)
		address, _ = properties["Address"].Value().(string)
	}
	if chosen == "" {
		return bonds.Address{}, ErrNoAdapter
	}
	parsed, err := bonds.ParseAddress(address)
	if err != nil {
		return bonds.Address{}, fmt.Errorf("the adapter at %s reports no usable address: %w", chosen, err)
	}
	return parsed, nil
}

// watchBlueZ returns a channel that receives a value whenever the
// paired set, a connection state, or the bonds on disk may have
// changed. It subscribes to these signals:
//
//   - InterfacesAdded and InterfacesRemoved. bluetoothd creates a
//     device object at discovery, so an added object is not always a
//     new pairing.
//   - PropertiesChanged of any Device1 property, for a connect, a
//     disconnect, a pairing, or an unpairing.
//   - Disconnected, whose reason the relay stores in ends before it
//     wakes the loop. BlueZ emits it before Connected goes false.
//
// The bond store copies a bond as soon as BlueZ writes it, which is
// when Bonded goes true. Paired goes true later, after service
// discovery finishes. When bluetoothd creates a device object and
// bonds it in the same main-loop iteration, GDBus sends no
// PropertiesChanged for the new object, and only InterfacesAdded
// reports Bonded=true.
//
// The channel has a buffer of one, and the relay drops a signal that
// finds it full. The pass reads the whole tree, so one pending wake
// covers a burst of signals.
func watchBlueZ(ctx context.Context, conn *dbus.Conn, ends *linkEnds) (<-chan struct{}, error) {
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
		{
			dbus.WithMatchSender(bluezService),
			dbus.WithMatchInterface(deviceInterface),
			dbus.WithMatchMember("Disconnected"),
		},
	}
	for _, match := range matches {
		if err := conn.AddMatchSignal(match...); err != nil {
			return nil, fmt.Errorf("subscribing to BlueZ's signals: %w", err)
		}
	}

	signals := make(chan *dbus.Signal, 64)
	conn.Signal(signals)
	return relayBlueZSignals(ctx, signals, ends, func() { conn.RemoveSignal(signals) }), nil
}

// relayBlueZSignals is the goroutine of watchBlueZ, separate so a test
// can send it signals without a bus. It closes its channel when signals
// closes, which godbus does when the connection to the bus is lost.
//
// release unregisters signals from the connection when the goroutine
// exits. Without it, godbus starts a goroutine for each signal that the
// full channel cannot take, and each goroutine blocks until something
// reads the channel, which never happens.
func relayBlueZSignals(ctx context.Context, signals <-chan *dbus.Signal, ends *linkEnds, release func()) <-chan struct{} {
	changed := make(chan struct{}, 1)
	go func() {
		defer close(changed)
		defer release()
		for {
			select {
			case <-ctx.Done():
				return
			case signal, ok := <-signals:
				if !ok {
					return
				}
				if device, reason, read := disconnectedFrom(signal); read {
					ends.record(device, reason)
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

// waitForBus connects to the pod's D-Bus system bus, and retries until
// it connects or the timeout passes. dbus-daemon runs in the bluetoothd
// container, and the containers share the socket through an emptyDir,
// so the operator can start before the socket exists. Retrying here is
// faster than exiting, because the kubelet's restart backoff grows to
// five minutes.
func waitForBus(ctx context.Context, timeout time.Duration) (*dbus.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		// godbus caches only a successful connection, so each call after
		// a failure dials again.
		conn, err := dbus.SystemBus()
		if err == nil {
			return conn, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("the system bus is not reachable after %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(busRetryDelay):
		}
	}
}

// waitForBlueZ blocks until bluetoothd owns its bus name or the timeout
// passes, because the bus can be up before bluetoothd has claimed the
// name. When bluetoothd never claims it, the operator exits, so the
// failure shows in the pod's restarts and not only in a log.
func waitForBlueZ(ctx context.Context, conn *dbus.Conn, timeout time.Duration) error {
	// Subscribe before checking, so a name claimed between the check
	// and the wait still sends a signal.
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
			return fmt.Errorf("bluetoothd has not claimed the bus name %s after %s", bluezService, timeout)
		case <-names:
		}
	}
}

// watchBlueZExit returns a channel that receives one value when
// bluetoothd leaves the bus, or when the connection to the bus is lost,
// because then the operator cannot read the paired set either way. The
// loop exits on it (main.go).
//
// The channel is never closed, not even when ctx ends. A closed channel
// is always ready, so on a normal shutdown it could win the loop's
// select against ctx.Done and report an exit of bluetoothd that did not
// happen.
func watchBlueZExit(ctx context.Context, conn *dbus.Conn) (<-chan struct{}, error) {
	if err := matchBlueZName(conn); err != nil {
		return nil, err
	}
	names := make(chan *dbus.Signal, 8)
	conn.Signal(names)
	return watchNameLoss(ctx, names, func() { conn.RemoveSignal(names) }), nil
}

// watchNameLoss is the goroutine of watchBlueZExit, separate so a test
// can send it signals without a bus. godbus closes names when the
// connection to the bus is lost. release works as in relayBlueZSignals.
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

// matchBlueZName subscribes to NameOwnerChanged for org.bluez. Each
// caller adds its own match rule, so neither depends on a rule that
// another caller added.
func matchBlueZName(conn *dbus.Conn) error {
	err := conn.AddMatchSignal(
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, bluezService),
	)
	if err != nil {
		return fmt.Errorf("subscribing to bus name owner changes: %w", err)
	}
	return nil
}

func blueZOwned(conn *dbus.Conn) (bool, error) {
	var owned bool
	err := conn.BusObject().
		Call("org.freedesktop.DBus.NameHasOwner", 0, bluezService).
		Store(&owned)
	if err != nil {
		return false, fmt.Errorf("checking the owner of the bus name %s: %w", bluezService, err)
	}
	return owned, nil
}
