package main

// Writing to the radio. The rest of this operator only reads it.
//
// Publishing a slice needs one question answered: which controllers
// are paired, and which of them are connected. The pairing API needs
// more. It opens a discovery window, it pairs a device a person
// approved, it trusts that device so it reconnects on its own, it
// renames a device and the radio itself, and it removes a bond. Every
// one of those is a D-Bus call into bluetoothd, and one interface
// collects them so that a test can drive the controllers above
// without a bus.
//
// The window is short on purpose. An adapter that stays pairable and
// discoverable is the exposure a window exists to bound, so the
// operator sets BlueZ's own PairableTimeout and DiscoverableTimeout
// to the window's length as well. If the operator dies mid-window,
// bluetoothd closes the window on its own at that deadline.
//
// The agent is registered for the window and unregistered when the
// window closes, for the same reason. It reports the NoInputNoOutput
// capability, which pairs a game controller, and it accepts a service
// authorization, so it must not stay registered while no window is
// open.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	agentManagerInterface = "org.bluez.AgentManager1"
	agentInterface        = "org.bluez.Agent1"
	propertiesInterface   = "org.freedesktop.DBus.Properties"

	// agentPath is the bus path of this operator's agent object. The
	// path is the operator's own, not BlueZ's, because the agent is
	// an object this program exports and bluetoothd calls back into.
	agentPath = dbus.ObjectPath("/sh/liken/bluetooth/agent")

	// agentCapability states what the operator can do during a pairing.
	// It has no display and no keypad, and that is the combination a
	// game controller pairs with: BlueZ runs Just Works and asks the
	// agent for nothing but an authorization. A device that needs a
	// passkey shown and typed is out of scope for this API version.
	agentCapability = "NoInputNoOutput"
)

const (
	// pairTimeout bounds a Device1.Pair call. The call does not return
	// until the bond completes or bluetoothd's own timeout ends the
	// attempt, and the reconcile loop is one goroutine, so an unbounded
	// call would stall every other thing the loop does. bluetoothd's
	// own bonding timeout is shorter than this on every path that
	// reaches the radio link.
	pairTimeout = 30 * time.Second

	// connectTimeout bounds Device1.Connect, which returns only when
	// the link is up or the page failed. A page at a speaker that is
	// switched off takes several seconds. The call runs off the
	// reconcile loop, so the bound is on the attempt and not on the
	// pass.
	connectTimeout = 30 * time.Second

	// callTimeout bounds every other call. Each one is a local method
	// call to a daemon in the same pod, so a call that takes seconds
	// means the daemon has already failed.
	callTimeout = 10 * time.Second
)

// ErrNoDevice reports that bluetoothd holds no device object for an
// address. It is the ordinary result for a controller that is out of
// range: BlueZ keeps a device object for a paired device, and drops
// every object for a device it only detected when the scan's results
// age out.
var ErrNoDevice = errors.New("bluetoothd holds no object for that device")

// adapterState is the radio itself, as bluetoothd reports it.
//
// Connectable is whether the radio answers a page: the inbound
// connection a bonded controller's reconnect button makes. The
// operator pages a bonded speaker itself (connect.go), so the setting
// covers the inbound direction only. It is separate from
// Discoverable, which only controls the inquiry scan a pairing needs.
type adapterState struct {
	Address      bonds.Address
	Alias        string
	Powered      bool
	Connectable  bool
	Discovering  bool
	Discoverable bool
	Pairable     bool
}

// deviceState is one device object, as bluetoothd reports it. A device
// object exists for every device the radio detected recently as well as
// for every device it holds a bond with, and Paired is what separates
// the two.
//
// UUIDs are the profile UUIDs from the SDP browse. classify.go decodes
// them, so the pass tells an audio sink from an input device out of
// the same snapshot it reads everything else from.
type deviceState struct {
	Address   bonds.Address
	Name      string
	Alias     string
	Paired    bool
	Connected bool
	Trusted   bool
	UUIDs     []string
}

// radioSnapshot is one read of bluetoothd's whole object tree. Every
// pass takes one and derives everything from it, the same way the
// publisher re-reads the tree instead of keeping a cache built from
// signal payloads.
type radioSnapshot struct {
	Adapter adapterState
	Devices []deviceState
}

// device returns one device out of the snapshot.
func (s radioSnapshot) device(address bonds.Address) (deviceState, bool) {
	for _, device := range s.Devices {
		if device.Address == address {
			return device, true
		}
	}
	return deviceState{}, false
}

// radio is the interface the pairing controllers act on bluetoothd
// through. The operator holds an implementation over D-Bus, and the
// tests hold one that records the calls and replies from a fixture.
type radio interface {
	// Snapshot reads the adapter and every device object in one call.
	// It returns ErrNoAdapter when bluetoothd published no adapter,
	// which happens during startup and after the dongle departs.
	Snapshot() (radioSnapshot, error)

	SetAdapterAlias(alias string) error

	// SetAdapterConnectable turns the page scan on or off. A fresh
	// bluetoothd starts the adapter with it off, and a radio that is
	// not connectable answers no bonded device, so the reconcile pass
	// asserts it on.
	SetAdapterConnectable(connectable bool) error

	SetDeviceAlias(device bonds.Address, alias string) error
	SetDeviceTrusted(device bonds.Address, trusted bool) error

	// OpenWindow makes the radio pairable, discoverable, and scanning
	// for the given length of time, with the operator's agent
	// registered. CloseWindow undoes all of that.
	OpenWindow(window time.Duration) error
	CloseWindow() error

	Pair(device bonds.Address) error

	// Connect pages a bonded device and returns when the link is up
	// or when the page failed, so the caller runs it off the reconcile
	// loop.
	Connect(device bonds.Address) error

	Disconnect(device bonds.Address) error
	Remove(device bonds.Address) error
}

// blueZRadio is the radio, over the bus in this pod.
type blueZRadio struct {
	conn *dbus.Conn

	// agentExported records that the agent object is on the bus. The
	// object is exported once and registered with BlueZ at each window,
	// because an export is this process's own state and a registration
	// is bluetoothd's.
	agentExported bool
}

func newBlueZRadio(conn *dbus.Conn) *blueZRadio { return &blueZRadio{conn: conn} }

// readObjectTree reads BlueZ's whole object tree in one round trip.
func readObjectTree(conn *dbus.Conn) (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	err := conn.Object(bluezService, "/").
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).
		Store(&objects)
	if err != nil {
		return nil, fmt.Errorf("reading BlueZ's managed objects: %w", err)
	}
	return objects, nil
}

func (r *blueZRadio) Snapshot() (radioSnapshot, error) {
	objects, err := readObjectTree(r.conn)
	if err != nil {
		return radioSnapshot{}, err
	}
	return snapshotFrom(objects)
}

// snapshotFrom reads the adapter and the devices out of one tree. It is
// separate from the call so that the rules below are testable without a
// bus.
//
// It selects the adapter at the lowest object path, so that two reads
// of the same tree select the same adapter. A map's iteration order
// alone would not give that. A pod claims one adapter, so a second
// one is not expected.
func snapshotFrom(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) (radioSnapshot, error) {
	snapshot := radioSnapshot{}
	chosen := ""
	for path, interfaces := range objects {
		properties, ok := interfaces[adapterInterface]
		if !ok {
			continue
		}
		if chosen != "" && string(path) >= chosen {
			continue
		}
		chosen = string(path)
		address, _ := properties["Address"].Value().(string)
		parsed, err := bonds.ParseAddress(address)
		if err != nil {
			return radioSnapshot{}, fmt.Errorf("the adapter at %s reports no usable address: %w", path, err)
		}
		alias, _ := properties["Alias"].Value().(string)
		powered, _ := properties["Powered"].Value().(bool)
		connectable, _ := properties["Connectable"].Value().(bool)
		discovering, _ := properties["Discovering"].Value().(bool)
		discoverable, _ := properties["Discoverable"].Value().(bool)
		pairable, _ := properties["Pairable"].Value().(bool)
		snapshot.Adapter = adapterState{
			Address:      parsed,
			Alias:        alias,
			Powered:      powered,
			Connectable:  connectable,
			Discovering:  discovering,
			Discoverable: discoverable,
			Pairable:     pairable,
		}
	}
	if chosen == "" {
		return radioSnapshot{}, ErrNoAdapter
	}

	for _, interfaces := range objects {
		properties, ok := interfaces[deviceInterface]
		if !ok {
			continue
		}
		address, _ := properties["Address"].Value().(string)
		parsed, err := bonds.ParseAddress(address)
		if err != nil {
			continue
		}
		name, _ := properties["Name"].Value().(string)
		alias, _ := properties["Alias"].Value().(string)
		paired, _ := properties["Paired"].Value().(bool)
		connected, _ := properties["Connected"].Value().(bool)
		trusted, _ := properties["Trusted"].Value().(bool)
		uuids, _ := properties["UUIDs"].Value().([]string)
		snapshot.Devices = append(snapshot.Devices, deviceState{
			Address:   parsed,
			Name:      name,
			Alias:     alias,
			Paired:    paired,
			Connected: connected,
			Trusted:   trusted,
			UUIDs:     uuids,
		})
	}
	return snapshot, nil
}

// adapterPath and devicePath find the object paths this operator calls
// methods on. Every
// actuation re-reads the tree rather than building a path from an
// address, because a fresh read is also the check that the object is
// still there, and a call against a path that went away is an error the
// caller would have to tell apart from a real failure.
func (r *blueZRadio) adapterPath() (dbus.ObjectPath, error) {
	objects, err := readObjectTree(r.conn)
	if err != nil {
		return "", err
	}
	chosen := dbus.ObjectPath("")
	for path, interfaces := range objects {
		if _, ok := interfaces[adapterInterface]; !ok {
			continue
		}
		if chosen == "" || path < chosen {
			chosen = path
		}
	}
	if chosen == "" {
		return "", ErrNoAdapter
	}
	return chosen, nil
}

func (r *blueZRadio) devicePath(device bonds.Address) (dbus.ObjectPath, error) {
	objects, err := readObjectTree(r.conn)
	if err != nil {
		return "", err
	}
	for path, interfaces := range objects {
		properties, ok := interfaces[deviceInterface]
		if !ok {
			continue
		}
		address, _ := properties["Address"].Value().(string)
		parsed, err := bonds.ParseAddress(address)
		if err == nil && parsed == device {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: %w", device, ErrNoDevice)
}

// call runs one method with a bound on how long it may take. None of
// the methods here returns a value, so the call's own error is the
// whole result.
func (r *blueZRadio) call(path dbus.ObjectPath, method string, timeout time.Duration, args ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return r.conn.Object(bluezService, path).CallWithContext(ctx, method, 0, args...).Err
}

// setProperty writes one property through the standard properties
// interface. BlueZ exposes Pairable, Discoverable, the two timeouts,
// Alias, and Trusted as writable properties rather than as methods.
func (r *blueZRadio) setProperty(path dbus.ObjectPath, iface, name string, value any) error {
	return r.call(path, propertiesInterface+".Set", callTimeout, iface, name, dbus.MakeVariant(value))
}

func (r *blueZRadio) SetAdapterAlias(alias string) error {
	path, err := r.adapterPath()
	if err != nil {
		return err
	}
	return r.setProperty(path, adapterInterface, "Alias", alias)
}

func (r *blueZRadio) SetAdapterConnectable(connectable bool) error {
	path, err := r.adapterPath()
	if err != nil {
		return err
	}
	return r.setProperty(path, adapterInterface, "Connectable", connectable)
}

func (r *blueZRadio) SetDeviceAlias(device bonds.Address, alias string) error {
	path, err := r.devicePath(device)
	if err != nil {
		return err
	}
	return r.setProperty(path, deviceInterface, "Alias", alias)
}

func (r *blueZRadio) SetDeviceTrusted(device bonds.Address, trusted bool) error {
	path, err := r.devicePath(device)
	if err != nil {
		return err
	}
	return r.setProperty(path, deviceInterface, "Trusted", trusted)
}

// OpenWindow puts the radio into the state a first pairing needs, and
// gives bluetoothd the same deadline the request states.
//
// The order matters. The timeouts go on before the two flags, because
// BlueZ starts counting a timeout when the flag turns on, and a
// timeout written afterwards would restart the count. Discovery starts
// last, because the scan fills status.seen and it is the part a person
// waits on.
func (r *blueZRadio) OpenWindow(window time.Duration) error {
	path, err := r.adapterPath()
	if err != nil {
		return err
	}
	if err := r.registerAgent(); err != nil {
		return err
	}
	seconds := uint32(window / time.Second)
	if err := r.setProperty(path, adapterInterface, "PairableTimeout", seconds); err != nil {
		return err
	}
	if err := r.setProperty(path, adapterInterface, "DiscoverableTimeout", seconds); err != nil {
		return err
	}
	if err := r.setProperty(path, adapterInterface, "Pairable", true); err != nil {
		return err
	}
	if err := r.setProperty(path, adapterInterface, "Discoverable", true); err != nil {
		return err
	}
	// A discovery that is already running returns
	// org.bluez.Error.InProgress, and the operator re-asserts the window
	// on every pass, so that error is the ordinary case and not a
	// failure.
	if err := r.call(path, adapterInterface+".StartDiscovery", callTimeout); err != nil && !inProgress(err) {
		return err
	}
	return nil
}

// CloseWindow returns the radio to its idle state: not scanning, not
// discoverable, not pairable, and with no agent registered.
//
// Every step runs even when an earlier one failed, because each one
// closes a different exposure and a failure to stop the scan is no
// reason to leave the radio pairable.
func (r *blueZRadio) CloseWindow() error {
	path, err := r.adapterPath()
	if err != nil {
		return err
	}
	var failure error
	record := func(err error) {
		if err != nil && failure == nil {
			failure = err
		}
	}
	// A stop with no discovery running returns
	// org.bluez.Error.Failed, which is the ordinary result after the
	// adapter's own timeout already ended the scan.
	if err := r.call(path, adapterInterface+".StopDiscovery", callTimeout); err != nil && !notRunning(err) {
		record(err)
	}
	record(r.setProperty(path, adapterInterface, "Discoverable", false))
	record(r.setProperty(path, adapterInterface, "Pairable", false))
	record(r.unregisterAgent())
	return failure
}

func (r *blueZRadio) Pair(device bonds.Address) error {
	path, err := r.devicePath(device)
	if err != nil {
		return err
	}
	return r.call(path, deviceInterface+".Pair", pairTimeout)
}

func (r *blueZRadio) Connect(device bonds.Address) error {
	path, err := r.devicePath(device)
	if err != nil {
		return err
	}
	return r.call(path, deviceInterface+".Connect", connectTimeout)
}

func (r *blueZRadio) Disconnect(device bonds.Address) error {
	path, err := r.devicePath(device)
	if errors.Is(err, ErrNoDevice) {
		// With no device object there is no connection, so the
		// disconnect's goal is already met.
		return nil
	}
	if err != nil {
		return err
	}
	return r.call(path, deviceInterface+".Disconnect", callTimeout)
}

// Remove deletes the bond. bluetoothd removes the device's object, the
// link key from the kernel, and the device's directory under
// /var/lib/bluetooth, so the operator's next pass reads a tree with one
// bond fewer.
func (r *blueZRadio) Remove(device bonds.Address) error {
	adapter, err := r.adapterPath()
	if err != nil {
		return err
	}
	path, err := r.devicePath(device)
	if errors.Is(err, ErrNoDevice) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.call(adapter, adapterInterface+".RemoveDevice", callTimeout, path)
}
