package main

// The check that answers whether this pod's WirePlumber still holds
// its media endpoints on the delivered bus.
//
// A media endpoint is registered over a D-Bus connection, and it
// lives exactly as long as that connection. When the Bluetooth
// operator's pod restarts, its dbus-daemon exits and every
// connection to the bus dies with it. PipeWire's bluez5 plugin
// clears exit-on-disconnect, which libdbus sets for a bus connection
// by default, so WirePlumber survives the loss instead of ending the
// way a D-Bus client normally does. It then registers nothing again,
// because the plugin listens for no disconnect. The radio advertises
// no audio from that moment, every container still reports Ready,
// and nothing in the pod notices.
//
// The repair is a restart of the WirePlumber container alone, which
// re-opens the socket and registers the endpoints again. WirePlumber
// is a native sidecar, so the kubelet restarts that one container
// and leaves PipeWire and the card's sinks running. This check is
// what asks the kubelet for that restart, as the liveness probe in
// deploy/operator.yaml.
//
// The check cannot test the socket. The socket is healthy: a new
// dbus-daemon is listening on the same path, and a fresh connection
// to it succeeds. What is broken is the file descriptor WirePlumber
// still holds to the socket that went away. So the check reads a
// fact that is true only while a registration is live.
//
// That fact is on the adapter. bluetoothd adds a media profile's
// UUID to org.bluez.Adapter1.UUIDs when a client registers an
// endpoint for it, and removes it when that client's connection
// ends. org.bluez.Media1.SupportedUUIDs names the media profiles
// this bluetoothd hosts. An overlap between the two means some
// endpoint is registered. No overlap means none is.
//
// Asking BlueZ for both lists keeps a profile number out of this
// file. The pod registers the a2dp_source role today, so the overlap
// is AudioSource, and a deployment that registered a2dp_sink instead
// would overlap on AudioSink and pass here unchanged. A check that
// named one profile would turn that configuration change into a
// permanent restart loop.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// endpointsMode is the argument that selects this check. The
// liveness and startup probes on the WirePlumber container name it,
// and no other caller has a reason to.
const endpointsMode = "endpoints-registered"

// mediaInterface holds the media profiles bluetoothd hosts, one
// object per adapter beside org.bluez.Adapter1.
const mediaInterface = "org.bluez.Media1"

// probeBusTimeout bounds the connection this check opens. The probe
// carries its own timeout, and the kubelet counts a probe that
// exceeds it as a failure, so this check has to give up first and
// report the reason itself.
const probeBusTimeout = 3 * time.Second

// endpointsRegistered is the probe. It exits 0 when a restart of
// WirePlumber would repair nothing, and 1 when the endpoints are
// gone and a restart is the repair.
//
// Only one state exits 1: an adapter is present and no media profile
// it hosts is advertised. Every other state passes, because a
// restart would not fix it and a probe that fails restarts this
// container forever.
//
//   - No delivered bus. The pod's claim allocated no media bus, so
//     this machine's WirePlumber registers nothing and never should.
//   - No answer from the bus. The Bluetooth operator's pod is down or
//     restarting. Its own kubelet restarts it, and a WirePlumber that
//     restarts while the bus is missing finds nothing to register.
//   - No adapter. The radio is unplugged, so there is nothing to
//     advertise on.
//
// Each line names the state it found, because a failing exec probe
// reaches a reader as one terse line in kubectl describe, and the
// next question is always which of these the check saw.
func endpointsRegistered() {
	if !bluetoothEnabled() {
		fmt.Printf("the claim delivered no Bluetooth media bus; nothing registers endpoints here\n")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeBusTimeout)
	defer cancel()

	conn, err := waitForBus(ctx, probeBusTimeout)
	if err != nil {
		fmt.Printf("the delivered bus does not answer: %v; "+
			"a restart of this container registers nothing while it is gone\n", err)
		return
	}

	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err = conn.Object(bluezService, "/").
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).
		Store(&objects)
	if err != nil {
		fmt.Printf("reading BlueZ's managed objects: %v; "+
			"a restart of this container registers nothing while bluetoothd is silent\n", err)
		return
	}

	adapters, registered := endpointsFrom(objects)
	switch {
	case adapters == 0:
		fmt.Printf("bluetoothd published no adapter; there is no radio to advertise on\n")
	case registered:
		fmt.Printf("the adapter advertises a media profile that bluetoothd hosts; " +
			"WirePlumber holds its endpoints\n")
	default:
		fatal("the adapter advertises none of the %d media profile(s) bluetoothd hosts; "+
			"WirePlumber's endpoints are gone and only a restart of this container registers them again",
			hostedProfiles(objects))
	}
}

// endpointsFrom reads the two lists out of one managed-object tree
// and reports how many adapters it found and whether any of them
// advertises a media profile. It is separate from the call so the
// rules test without a bus.
//
// The answer is per tree, not per adapter. A machine with two radios
// runs one Bluetooth operator pod for the one this claim delivered,
// and a registration on either is a WirePlumber that still holds its
// connection.
func endpointsFrom(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) (adapters int, registered bool) {
	for _, interfaces := range objects {
		advertised, ok := interfaces[adapterInterface]
		if !ok {
			continue
		}
		adapters++
		hosted, ok := interfaces[mediaInterface]
		if !ok {
			// An adapter with no Media1 hosts no media profile, so
			// nothing about it can be registered or missing.
			continue
		}
		if overlaps(uuidList(advertised, "UUIDs"), uuidList(hosted, "SupportedUUIDs")) {
			registered = true
		}
	}
	return adapters, registered
}

// hostedProfiles counts the media profiles the tree's adapters host,
// so the failure line states what was looked for.
func hostedProfiles(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) int {
	count := 0
	for _, interfaces := range objects {
		if _, ok := interfaces[adapterInterface]; !ok {
			continue
		}
		count += len(uuidList(interfaces[mediaInterface], "SupportedUUIDs"))
	}
	return count
}

// uuidList reads one array-of-string property, and gives an empty
// list for a property the daemon did not report.
func uuidList(properties map[string]dbus.Variant, key string) []string {
	list, _ := properties[key].Value().([]string)
	return list
}

// overlaps reports whether the two UUID lists share a value. BlueZ
// writes both in the same lowercase form, and the comparison folds
// case anyway, because a UUID's case carries no meaning.
func overlaps(advertised, hosted []string) bool {
	for _, a := range advertised {
		for _, h := range hosted {
			if sameUUID(a, h) {
				return true
			}
		}
	}
	return false
}

// sameUUID compares two profile UUIDs the way acceptsA2DP compares
// one, so both readings of a UUID in this package agree.
func sameUUID(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && strings.EqualFold(a, b)
}
