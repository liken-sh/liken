package main

// The radio interface collects the D-Bus calls that change bluetoothd:
// the pairing window, pairing, trust, aliases, connections, adapter
// power, and bond removal. Tests replace it with fakeRadio, so they run
// the controllers without a bus.
//
// Any device nearby can pair with an adapter that is pairable and
// discoverable. So OpenWindow also sets BlueZ's PairableTimeout and
// DiscoverableTimeout, and bluetoothd closes the window at that
// deadline even when the operator has stopped.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

const (
	agentManagerInterface = "org.bluez.AgentManager1"
	batteryInterface      = "org.bluez.Battery1"
	agentInterface        = "org.bluez.Agent1"
	propertiesInterface   = "org.freedesktop.DBus.Properties"

	// agentPath is the object that this program exports for bluetoothd
	// to call during a pairing.
	agentPath = dbus.ObjectPath("/sh/liken/bluetooth/agent")

	// agentCapability tells BlueZ that the machine has no display and
	// no keypad, so BlueZ pairs with the Just Works method. A game
	// controller pairs that way. A device that needs a passkey cannot
	// pair through this operator.
	agentCapability = "NoInputNoOutput"
)

const (
	// pairTimeout bounds Device1.Pair, which returns when the bond
	// completes or bluetoothd gives up. Pair runs on the reconcile
	// loop's goroutine, so a call with no bound would stop every pass.
	// bluetoothd's own bonding timeout is shorter than this on every
	// path that reaches the radio link.
	pairTimeout = 30 * time.Second

	// connectTimeout bounds Device1.Connect, which returns when the link
	// is up or the page failed. Paging a speaker that is switched off
	// takes several seconds. Connect runs outside the reconcile loop, so
	// this bound delays one attempt and no pass.
	connectTimeout = 30 * time.Second

	// callTimeout bounds every other call. bluetoothd runs in the same
	// pod, so a call that takes seconds means bluetoothd has failed.
	callTimeout = 10 * time.Second
)

// ErrNoDevice reports that bluetoothd has no device object for an
// address. That is the normal result for a device that was never paired
// and is out of range, because BlueZ deletes an unpaired device's object
// after its TemporaryTimeout. BlueZ keeps the object of a paired device.
var ErrNoDevice = errors.New("bluetoothd has no object for the device")

// adapterState is the adapter's properties, as bluetoothd reports them.
//
// Connectable is the page scan. When it is false, the adapter refuses
// incoming connections, so a bonded controller cannot reconnect when a
// person presses its button. Discoverable is the inquiry scan, which
// only a first pairing needs.
type adapterState struct {
	Address      bonds.Address
	Alias        string
	Powered      bool
	Connectable  bool
	Discovering  bool
	Discoverable bool
	Pairable     bool
}

// deviceState is one device object, as bluetoothd reports it.
// bluetoothd has an object for each device that the adapter detected
// recently and for each device that it has a bond with. Paired tells
// them apart.
//
// UUIDs are the profile UUIDs from the SDP browse. classify.go reads
// them to tell an audio sink from an input device.
//
// AddressType is "public" or "random". Only a Low Energy device has a
// random address. Battery is nil when the device reports no level.
type deviceState struct {
	Address     bonds.Address
	Name        string
	Alias       string
	Icon        string
	AddressType string
	Paired      bool

	// Bonded is true when BlueZ has stored the link key. Paired alone
	// does not guarantee that.
	Bonded bool

	Connected bool
	Trusted   bool
	UUIDs     []string
	Battery   *deviceBattery
}

// deviceBattery is one device's org.bluez.Battery1 interface.
//
// BlueZ takes Percentage from the GATT Battery Service of a Low Energy
// device, from a HID battery report, or from a battery provider that a
// sound server registers. Source records which of these it used.
type deviceBattery struct {
	Percentage int
	Source     string
}

// radioSnapshot is one read of bluetoothd's whole object tree. Each
// pass reads a new one, instead of keeping state from D-Bus signals.
type radioSnapshot struct {
	Adapter adapterState
	Devices []deviceState
}

func (s radioSnapshot) device(address bonds.Address) (deviceState, bool) {
	for _, device := range s.Devices {
		if device.Address == address {
			return device, true
		}
	}
	return deviceState{}, false
}

// radio is how the controllers call bluetoothd. blueZRadio implements
// it over D-Bus.
type radio interface {
	// Snapshot reads the adapter and every device object in one call.
	// It returns ErrNoAdapter when bluetoothd has published no adapter,
	// which happens during startup and after the adapter is unplugged.
	Snapshot() (radioSnapshot, error)

	SetAdapterAlias(alias string) error

	SetAdapterConnectable(connectable bool) error

	// SetAdapterPowered powers the adapter on or off. A power-off
	// drops every connection.
	SetAdapterPowered(powered bool) error

	SetDeviceAlias(device bonds.Address, alias string) error
	SetDeviceTrusted(device bonds.Address, trusted bool) error

	// OpenWindow makes the adapter pairable, discoverable, and
	// scanning for the given length of time, with the operator's agent
	// registered. CloseWindow reverses all of that.
	OpenWindow(window time.Duration) error
	CloseWindow() error

	Pair(device bonds.Address) error

	// Connect pages a bonded device and returns when the link is up or
	// the page failed. The caller runs it outside the reconcile loop.
	Connect(device bonds.Address) error

	Disconnect(device bonds.Address) error
	Remove(device bonds.Address) error
}

// blueZRadio calls bluetoothd over the D-Bus connection in this pod.
type blueZRadio struct {
	conn *dbus.Conn

	// agentExported is true once the agent object is exported on the
	// bus. The export lasts for the connection, and registerAgent
	// registers the agent with BlueZ again for each window.
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

// snapshotFrom builds a snapshot from one object tree, without a bus,
// so that tests can call it.
//
// A pod claims one adapter. If the tree has more, snapshotFrom takes
// the one with the lowest object path, so that every read of the same
// tree returns the same adapter whatever the map order.
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
		icon, _ := properties["Icon"].Value().(string)
		addressType, _ := properties["AddressType"].Value().(string)
		paired, _ := properties["Paired"].Value().(bool)
		bonded, _ := properties["Bonded"].Value().(bool)
		connected, _ := properties["Connected"].Value().(bool)
		trusted, _ := properties["Trusted"].Value().(bool)
		uuids, _ := properties["UUIDs"].Value().([]string)
		snapshot.Devices = append(snapshot.Devices, deviceState{
			Address:     parsed,
			Name:        name,
			Alias:       alias,
			Icon:        icon,
			AddressType: addressType,
			Paired:      paired,
			Bonded:      bonded,
			Connected:   connected,
			Trusted:     trusted,
			UUIDs:       uuids,
			Battery:     batteryFrom(interfaces),
		})
	}
	return snapshot, nil
}

// batteryFrom reads org.bluez.Battery1, which BlueZ publishes on the
// device object beside Device1. A device with no battery has no
// Battery1, and neither does a battery device that is not connected,
// so nil is a normal result.
func batteryFrom(interfaces map[string]map[string]dbus.Variant) *deviceBattery {
	properties, ok := interfaces[batteryInterface]
	if !ok {
		return nil
	}
	percentage, ok := properties["Percentage"].Value().(byte)
	if !ok {
		return nil
	}
	source, _ := properties["Source"].Value().(string)
	return &deviceBattery{Percentage: int(percentage), Source: source}
}

// adapterPath and devicePath read the tree to find the object to call,
// instead of building a path from an address. So a device that has no
// object returns ErrNoDevice, which callers handle, instead of a D-Bus
// error that looks like a failure. adapterPath returns the same
// adapter that snapshotFrom chooses.
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

// call runs a method that returns no value.
func (r *blueZRadio) call(path dbus.ObjectPath, method string, timeout time.Duration, args ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return r.conn.Object(bluezService, path).CallWithContext(ctx, method, 0, args...).Err
}

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

func (r *blueZRadio) SetAdapterPowered(powered bool) error {
	path, err := r.adapterPath()
	if err != nil {
		return err
	}
	return r.setProperty(path, adapterInterface, "Powered", powered)
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

// OpenWindow writes the timeouts before the Pairable and Discoverable
// flags, because BlueZ starts counting a timeout when its flag turns
// on, and a timeout written afterwards would restart the count.
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
	// The operator calls OpenWindow on every pass of an open window, so
	// discovery is often running already, and BlueZ then returns
	// org.bluez.Error.InProgress.
	if err := r.call(path, adapterInterface+".StartDiscovery", callTimeout); err != nil && !inProgress(err) {
		return err
	}
	return nil
}

// CloseWindow runs every step even when an earlier step failed, so
// that a failure to stop discovery does not leave the adapter pairable.
// It returns the first error.
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
	// notRunning accepts the error that StopDiscovery returns when no
	// discovery is running.
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
		// A device with no object has no connection to close.
		return nil
	}
	if err != nil {
		return err
	}
	return r.call(path, deviceInterface+".Disconnect", callTimeout)
}

// Remove calls RemoveDevice, which deletes the device object and its
// stored bond. A device with no object has no bond to delete.
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
