package main

// A Bluetooth speaker has no device in the kernel. bluetoothd records
// which speakers are paired, and PipeWire makes a sink for a speaker
// only while PipeWire is connected to bluetoothd's D-Bus and has
// registered a media endpoint there. The Bluetooth operator publishes
// that D-Bus, the media bus, as a DRA device. This pod's ResourceClaim
// allocates it, and the claim's CDI edits mount the directory of the
// bus socket and set DBUS_SYSTEM_BUS_ADDRESS to the socket inside it.
//
// A D-Bus signal only starts a reconcile pass, and the pass reads
// bluetoothd's whole object tree again. The operator keeps no cache
// built from signal payloads, because such a cache can drift from
// bluetoothd.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezService     = "org.bluez"
	deviceInterface  = "org.bluez.Device1"
	adapterInterface = "org.bluez.Adapter1"
)

// busAddressVariable holds the address of the media bus. The claim's
// CDI edits set it in every container that names the claim, so when it
// is unset, the claim allocated no media bus and the Bluetooth code
// does not run.
const busAddressVariable = "DBUS_SYSTEM_BUS_ADDRESS"

// audioSinkUUID is the AudioSink service class, 0x110b in the Bluetooth
// base UUID. A device that lists it accepts an A2DP stream.
const audioSinkUUID = "0000110b-0000-1000-8000-00805f9b34fb"

// busReadyTimeout bounds the wait for the Bluetooth media bus at
// startup. bluetooth-operator's pod serves the bus and can be
// restarting while this pod starts.
const busReadyTimeout = 30 * time.Second

// busRetryDelay is the interval between attempts to connect to the
// media bus. Nothing announces that the socket exists, so the attempts
// poll.
const busRetryDelay = time.Second

// speaker is a paired A2DP device. A map of speakers uses the speaker's
// MAC address as the key, in lowercase with colons.
type speaker struct {
	Name      string
	Connected bool
}

// ErrNoAdapter means that bluetoothd's object tree has no adapter. That
// happens before bluetoothd creates the adapter and after the adapter
// goes away. The tree then has no device objects either, but the
// speakers are still paired, so the caller must not remove the
// speakers it published.
var ErrNoAdapter = errors.New("bluetoothd published no adapter")

func bluetoothEnabled() bool {
	return os.Getenv(busAddressVariable) != ""
}

func pairedSpeakers(conn *dbus.Conn) (map[string]speaker, error) {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := conn.Object(bluezService, "/").
		Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).
		Store(&objects)
	if err != nil {
		return nil, fmt.Errorf("reading BlueZ's managed objects: %w", err)
	}
	return speakersFrom(objects)
}

// speakersFrom returns the paired A2DP speakers in a GetManagedObjects
// result. It reads only Device1 objects, because after PipeWire
// registers its media endpoint, the Adapter1 object lists AudioSink
// too, and the adapter is this machine's own radio. A device with
// Paired false was found in a scan, and bluetoothd stores no link key
// for it, so the operator does not offer it.
func speakersFrom(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) (map[string]speaker, error) {
	adapters := 0
	speakers := map[string]speaker{}
	for _, interfaces := range objects {
		if _, ok := interfaces[adapterInterface]; ok {
			adapters++
		}
		properties, ok := interfaces[deviceInterface]
		if !ok {
			continue
		}
		address := normalizeMAC(variantString(properties, "Address"))
		if !validMAC(address) {
			continue
		}
		paired, _ := properties["Paired"].Value().(bool)
		if !paired {
			continue
		}
		uuids, _ := properties["UUIDs"].Value().([]string)
		if !acceptsA2DP(uuids) {
			continue
		}
		connected, _ := properties["Connected"].Value().(bool)
		speakers[address] = speaker{
			Name:      variantString(properties, "Alias"),
			Connected: connected,
		}
	}
	if adapters == 0 {
		return nil, ErrNoAdapter
	}
	return speakers, nil
}

func acceptsA2DP(uuids []string) bool {
	return slices.ContainsFunc(uuids, func(uuid string) bool {
		return strings.EqualFold(strings.TrimSpace(uuid), audioSinkUUID)
	})
}

func variantString(properties map[string]dbus.Variant, key string) string {
	value, _ := properties[key].Value().(string)
	return value
}

// watchBlueZ returns a channel that receives a value after each signal
// that can change the paired speakers or their connections:
// InterfacesAdded for a new device, InterfacesRemoved for an unpairing,
// and PropertiesChanged on a Device1 object for a connect or a
// disconnect.
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

// relayBlueZSignals sends a value on the returned channel for each
// signal, and drops the value when the channel's buffer of one is
// already full. The reader reads the whole tree again, so one value
// covers any number of signals.
//
// release unregisters signals from the connection when the relay ends,
// because godbus blocks on a registered channel that nobody reads, and
// then delivers no other signal on the connection.
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

// mediaBus holds the latest connection to the media bus. The
// dbus-daemon runs in the Bluetooth operator's pod, so a restart of
// that pod closes the connection, and subscribe stores a connection to
// the new daemon. speakers returns errNoBus before the first
// subscription, and an error from the read while the stored connection
// is closed.
type mediaBus struct {
	mu   sync.Mutex
	conn *dbus.Conn
}

var errNoBus = errors.New("no connection to the media bus is open")

func (b *mediaBus) speakers() (map[string]speaker, error) {
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		return nil, errNoBus
	}
	return pairedSpeakers(conn)
}

// subscribe subscribes to bluetoothd's signals before it stores the
// connection for speakers, so a change during the first read still
// produces a signal. dbus.SystemBus returns its cached connection while
// that connection is open, so the first call returns the connection
// that waitForBus opened, and a call after the bus closes dials again.
//
// godbus does not register a signal channel on a closed connection, and
// never closes that channel. A bus that closed before watchBlueZ
// registered would leave a relay that never delivers and never ends, so
// subscribe checks conn.Connected after registering.
func (b *mediaBus) subscribe(ctx context.Context) (<-chan struct{}, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, err
	}
	relay, stop := context.WithCancel(ctx)
	changed, err := watchBlueZ(relay, conn)
	if err != nil {
		stop()
		return nil, err
	}
	if !conn.Connected() {
		stop()
		return nil, errors.New("the media bus closed during the subscription")
	}
	// When the bus closes first, godbus closes the signal channel, and
	// the relay ends without stop.
	context.AfterFunc(ctx, stop)
	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()
	return changed, nil
}

func followBus(ctx context.Context, bus *mediaBus) <-chan struct{} {
	return resubscribe(ctx, bus.subscribe, busRetryDelay, func(line string) { fmt.Println(line) })
}

// resubscribe keeps a subscription open until ctx ends. The channel
// that subscribe returns closes when its subscription fails, and
// resubscribe then calls subscribe every retry until one succeeds.
//
// The returned channel receives a value for each event, and one when
// each new subscription opens, so the next pass reads the whole state
// again and finds the changes that nothing delivered while no
// subscription was open.
func resubscribe(ctx context.Context, subscribe func(context.Context) (<-chan struct{}, error),
	retry time.Duration, report func(string)) <-chan struct{} {
	out := make(chan struct{}, 1)
	wake := func() {
		select {
		case out <- struct{}{}:
		default:
		}
	}
	go func() {
		defer close(out)
		for {
			events, err := subscribeUntilOpen(ctx, subscribe, retry, report)
			if err != nil {
				return
			}
			wake()
			for range events {
				wake()
			}
			if ctx.Err() != nil {
				return
			}
			report("the Bluetooth media bus closed, so the operator connects again")
		}
	}()
	return out
}

// subscribeUntilOpen calls subscribe every retry until it succeeds, and
// returns an error only when ctx ends first. It reports the first
// failure of a run and the success that ends the run, so a bus that
// stays down writes one log line.
func subscribeUntilOpen(ctx context.Context, subscribe func(context.Context) (<-chan struct{}, error),
	retry time.Duration, report func(string)) (<-chan struct{}, error) {
	failed := false
	for {
		events, err := subscribe(ctx)
		if err == nil {
			if failed {
				report("subscribed to bluetoothd on the Bluetooth media bus again")
			}
			return events, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !failed {
			report(fmt.Sprintf("subscribing to bluetoothd on the Bluetooth media bus, and trying again every %s: %v", retry, err))
			failed = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retry):
		}
	}
}

// waitForBus connects to the bus at DBUS_SYSTEM_BUS_ADDRESS, and tries
// again every busRetryDelay until the socket accepts a connection or
// timeout passes. The Bluetooth operator's pod serves the socket and
// can be restarting while this pod starts.
func waitForBus(ctx context.Context, timeout time.Duration) (*dbus.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		// dbus.SystemBus caches only a connection that succeeded, so
		// each retry dials again.
		conn, err := dbus.SystemBus()
		if err == nil {
			return conn, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("the media bus accepted no connection within %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(busRetryDelay):
		}
	}
}

// wireplumberConfigDir is where the declare init container writes
// WirePlumber fragments. WirePlumber reads the fragments in
// /usr/share/wireplumber/wireplumber.conf.d before the fragments here,
// so a fragment here overrides the image's. Tests point it at a
// temporary directory.
var wireplumberConfigDir = "/etc/wireplumber/wireplumber.conf.d"

// monitorFragmentName starts with 60 so that it sorts after the image's
// fragments, which start with 50 and 51.
const monitorFragmentName = "60-liken-bluetooth.conf"

// monitorConfig turns on WirePlumber's bluez monitor, which the image's
// fragment turns off, with only the A2DP source role.
const monitorConfig = `# The declare init container writes this file only when the pod's
# claim includes a Bluetooth media bus. It is written again on every
# pod start, so an edit here does not survive a restart.

# The image's fragment sets hardware.bluetooth to disabled in the
# main-embedded profile. WirePlumber reads this fragment after that
# one, so these lines turn the Bluetooth monitor back on. The MIDI
# monitor stays off because a BLE MIDI instrument is an input, not a
# sound output.
wireplumber.profiles = {
  main-embedded = {
    hardware.bluetooth = required
    monitor.bluez = required
    monitor.bluez-midi = disabled
  }
}

# WirePlumber stores no Bluetooth settings, because the pod has no
# state directory to keep them across a restart. It has no headset
# profile to switch to either.
wireplumber.settings = {
  bluetooth.autoswitch-to-headset-profile = false
  bluetooth.use-persistent-storage = false
}

# bluez5.roles lists the roles of this machine, not of the speaker.
# a2dp_source sends audio to a speaker, and a2dp_sink would make this
# machine a speaker for phones. The headset roles stay off because they
# need an SCO socket in the host's network namespace, and this pod
# runs in its own.
monitor.bluez.properties = {
  bluez5.roles = [ a2dp_source ]
  bluez5.hfphsp-backend = "none"
}
`

// writeMonitorConfig writes the fragment when the claim includes a
// media bus, and returns whether it wrote. It runs in the declare init
// container, because WirePlumber reads its configuration only at start,
// and the kubelet starts the other containers only after the init
// container exits.
func writeMonitorConfig() (bool, error) {
	if !bluetoothEnabled() {
		return false, nil
	}
	if err := os.MkdirAll(wireplumberConfigDir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(wireplumberConfigDir, monitorFragmentName)
	if err := os.WriteFile(path, []byte(monitorConfig), 0o644); err != nil {
		return false, err
	}
	fmt.Printf("enabled WirePlumber's Bluetooth monitor in %s\n", path)
	return true, nil
}
