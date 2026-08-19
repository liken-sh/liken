package main

// The Bluetooth half of this operator: the switch that turns it on,
// the WirePlumber fragment that enables the bluez monitor, and the
// read of bluetoothd's paired set.
//
// A Bluetooth speaker creates nothing in the kernel. The paired set
// is only in bluetoothd, and the sink exists only while a sound
// server holds bluetoothd's bus and keeps a media endpoint
// registered. The Bluetooth operator publishes that bus as a DRA
// device, this pod's claim allocates it, and the claim's CDI
// delivery is a mount of the bus socket's directory plus
// DBUS_SYSTEM_BUS_ADDRESS naming the socket inside it.
//
// Every signal here says only that something changed, and each one
// starts a pass that re-reads the whole managed-object tree. A cache
// built from signal payloads can fall out of step with the daemon; a
// full re-read stays correct. The Bluetooth operator reads the same
// daemon the same way, for the same reason.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezService     = "org.bluez"
	deviceInterface  = "org.bluez.Device1"
	adapterInterface = "org.bluez.Adapter1"
)

// busAddressVariable is both the address of the delivered bus and
// the switch. The Bluetooth media bus's CDI delivery sets it in
// every container that names the claim, so when it is unset, the
// claim allocated no bus and none of this code runs.
const busAddressVariable = "DBUS_SYSTEM_BUS_ADDRESS"

// audioSinkUUID is the Bluetooth SIG's AudioSink profile, assigned
// number 0x110b in the base UUID. A paired device that advertises it
// accepts an A2DP stream, and that is what makes it a speaker to
// this operator.
const audioSinkUUID = "0000110b-0000-1000-8000-00805f9b34fb"

// busReadyTimeout bounds the wait for the delivered socket. Another
// pod serves it and can be restarting while this one starts.
const busReadyTimeout = 30 * time.Second

// busRetryDelay is how often that wait asks again. The bus raises no
// event before it exists, so the wait has to poll.
const busRetryDelay = time.Second

// speaker is one paired A2DP sink as bluetoothd reports it, keyed
// elsewhere by the normalized peer MAC.
type speaker struct {
	Name      string
	Connected bool
}

// ErrNoAdapter separates "no speaker is paired" from "there is
// nothing to ask yet". bluetoothd answers with an empty tree in both
// cases, and the second must never retract a published speaker.
var ErrNoAdapter = errors.New("bluetoothd published no adapter")

// bluetoothEnabled reports whether the claim delivered a media bus.
func bluetoothEnabled() bool {
	return os.Getenv(busAddressVariable) != ""
}

// pairedSpeakers reads every paired A2DP sink from bluetoothd in one
// round trip, keyed by the normalized peer MAC.
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

// speakersFrom reads the paired A2DP sinks out of one managed-object
// tree. It is separate from the call so the rules test without a
// bus.
//
// Only an org.bluez.Device1 object can become a speaker. The adapter
// object carries the AudioSink UUID too once the endpoint registers,
// and it is the machine's own radio, not a peer.
//
// A device with Paired false is one bluetoothd detected on the air
// and holds no link key for, so it is not this machine's to offer.
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

// acceptsA2DP reports whether the browsed profile UUIDs include
// AudioSink, the one profile that makes a paired device a speaker
// this operator can play into.
func acceptsA2DP(uuids []string) bool {
	return slices.ContainsFunc(uuids, func(uuid string) bool {
		return strings.EqualFold(strings.TrimSpace(uuid), audioSinkUUID)
	})
}

// variantString reads one string property, and gives an empty string
// for a property bluetoothd did not report.
func variantString(properties map[string]dbus.Variant, key string) string {
	value, _ := properties[key].Value().(string)
	return value
}

// watchBlueZ reports whenever the paired set or a connection state
// may have changed: InterfacesAdded when bluetoothd creates a device
// object, InterfacesRemoved for an unpairing, and PropertiesChanged
// on a device for a connect or a disconnect.
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

// relayBlueZSignals is watchBlueZ's loop over a channel that is
// already subscribed, separate from the subscription so a test can
// drive it without a bus.
//
// The output channel is buffered to one, and a full channel drops
// the signal, because the reader re-reads the whole tree and one
// wake answers a burst.
//
// release unregisters the signal channel from the connection. A
// connection keeps delivering to a registered channel, so a channel
// nobody reads would hold godbus's delivery goroutine forever.
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

// waitForBus connects to the delivered system bus, and retries until
// the socket answers or the timeout passes. The socket belongs to
// the Bluetooth operator's pod, which can be restarting while this
// pod starts. The wait is bounded because a bus that never arrives
// is a failure to report, not one to sit in.
func waitForBus(ctx context.Context, timeout time.Duration) (*dbus.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		// godbus caches the connection it returns and caches nothing
		// on a failure, so a call after a failure opens a new
		// connection.
		conn, err := dbus.SystemBus()
		if err == nil {
			return conn, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("no bus within %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(busRetryDelay):
		}
	}
}

// wireplumberConfigDir is where the declare container writes the
// generated fragment. WirePlumber merges the fragments of
// /usr/share/wireplumber/wireplumber.conf.d first and /etc second,
// so a fragment here overrides the two the image bakes.
//
// It is a variable so the tests can point it at a directory they
// control.
var wireplumberConfigDir = "/etc/wireplumber/wireplumber.conf.d"

// monitorDropInName is the generated fragment's file name. 60 sorts
// it after the image's own 50 and 51 fragments.
const monitorDropInName = "60-liken-bluetooth.conf"

// monitorConfig turns the bluez monitor on for the profile this pod
// runs, and restricts it to the one role a speaker plays. The
// image's baked fragment disables hardware.bluetooth, so this file
// is the whole of what a delivered media bus changes about the pod.
const monitorConfig = `# The declare init container writes this file only when the pod's
# claim delivered a Bluetooth media bus. A pod with no bus has no
# such file and loads no bluez monitor. The file is generated on
# every pod start, so an edit here does not survive one.

# main-embedded inherits main, which marks hardware.bluetooth
# required, and the image's baked fragment disables it. WirePlumber
# reads this fragment after the image's, so this is what turns the
# monitor back on. The MIDI monitor stays off: a BLE MIDI instrument
# is an input, not a sound output.
wireplumber.profiles = {
  main-embedded = {
    hardware.bluetooth = required
    monitor.bluez = required
    monitor.bluez-midi = disabled
  }
}

# The pod keeps no state directory, so nothing restores settings
# across a restart, and there is no headset profile to switch to.
wireplumber.settings = {
  bluetooth.autoswitch-to-headset-profile = false
  bluetooth.use-persistent-storage = false
}

# bluez5.roles is the A2DP-only rule, and it names the roles THIS
# machine plays, not the peer's. a2dp_source is the role that plays
# into a speaker: PipeWire registers /MediaEndpoint/A2DPSource
# endpoints, the radio advertises AudioSource, and a speaker's own
# sink connects to them. Do not write a2dp_sink here because "the
# speaker is a sink": that is the role of receiving audio, it turns
# this machine into a speaker for phones, and a real speaker's card
# then offers no playback profile at all. The single role also
# leaves the LE Audio and ASHA roles off, and the HFP and HSP
# backend reads the same list, finds no headset role, and enables
# nothing. hfphsp-backend states that choice where a reader can see
# it: the headset profiles need an SCO socket in the host's network
# namespace, and this pod has no host network.
monitor.bluez.properties = {
  bluez5.roles = [ a2dp_source ]
  bluez5.hfphsp-backend = "none"
}
`

// writeMonitorConfig writes the fragment when the claim delivered a
// bus, and writes nothing when it did not. It reports whether it
// wrote.
//
// The write must finish before WirePlumber starts, because
// WirePlumber reads its configuration once. The declare init
// container running to completion first is the kubelet's own
// expression of that order.
func writeMonitorConfig() (bool, error) {
	if !bluetoothEnabled() {
		return false, nil
	}
	if err := os.MkdirAll(wireplumberConfigDir, 0o755); err != nil {
		return false, fmt.Errorf("making %s: %w", wireplumberConfigDir, err)
	}
	path := filepath.Join(wireplumberConfigDir, monitorDropInName)
	if err := os.WriteFile(path, []byte(monitorConfig), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("enabled WirePlumber's Bluetooth monitor in %s\n", path)
	return true, nil
}
