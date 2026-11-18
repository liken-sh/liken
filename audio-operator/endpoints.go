package main

// The endpoints-registered probe checks that this pod's WirePlumber
// still has its Bluetooth media endpoints registered with bluetoothd.
//
// A media endpoint exists only as long as the D-Bus connection that
// registered it. When the Bluetooth operator's pod restarts, its
// dbus-daemon exits and every connection to the bus closes. libdbus
// sets exit-on-disconnect on a bus connection by default, but
// PipeWire's bluez5 plugin clears it, so WirePlumber keeps running. The
// plugin does not listen for the disconnect, so WirePlumber does not
// register again. The radio then advertises no audio, and every
// container still reports Ready.
//
// A restart of the WirePlumber container alone repairs this, because
// the new process connects to the new dbus-daemon and registers the
// endpoints again. WirePlumber is a native sidecar, so the kubelet
// restarts only that container and leaves PipeWire and the card's sinks
// running. This check is that container's startup and liveness probe in
// deploy/operator.yaml.
//
// A test of the socket would pass, because a new dbus-daemon listens on
// the same path and accepts a new connection. The broken part is the
// file descriptor that WirePlumber still has open on the old socket. So
// the check reads a fact that is true only while a registration is
// live.
//
// bluetoothd adds a media profile's UUID to org.bluez.Adapter1.UUIDs
// when a client registers an endpoint for that profile, and removes it
// when that client's connection closes. org.bluez.Media1.SupportedUUIDs
// lists the media profiles that bluetoothd hosts. When the two lists
// share a UUID, an endpoint is registered.
//
// The check reads both lists from BlueZ and names no profile UUID. The
// pod registers the a2dp_source role, so the shared UUID is
// AudioSource, and a deployment that registered a2dp_sink would share
// AudioSink and pass. A check that named one UUID would fail after that
// change on every run, and the kubelet would restart the container in a
// loop.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// endpointsMode is the argument that the WirePlumber container's
// startup and liveness probes pass to select this check.
const endpointsMode = "endpoints-registered"

const mediaInterface = "org.bluez.Media1"

// probeBusTimeout bounds the connection to the bus. The kubelet counts
// a probe that runs past its timeoutSeconds as failed and shows no
// reason, so the check gives up sooner and prints why.
const probeBusTimeout = 3 * time.Second

// endpointsRegistered exits 1 only when bluetoothd has an adapter and
// no adapter advertises a media profile that bluetoothd hosts, because a
// restart of WirePlumber then registers the endpoints again. Every
// other state exits 0, because a restart would not fix it and a failing
// probe restarts the container again and again:
//
//   - The claim includes no media bus, so WirePlumber registers
//     nothing on this machine.
//   - The bus does not respond, or bluetoothd does not return its
//     managed objects. The Bluetooth operator's pod is down or
//     restarting, and a WirePlumber started while the bus is gone has
//     nothing to register with.
//   - There is no adapter, because the radio is unplugged.
//
// Each exit prints the state it found, because kubectl describe shows a
// failing exec probe as one line, and the person who reads it needs to
// know which state the check found.
func endpointsRegistered() {
	if !bluetoothEnabled() {
		fmt.Printf("the claim includes no Bluetooth media bus, so no endpoints register here\n")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeBusTimeout)
	defer cancel()

	conn, err := waitForBus(ctx, probeBusTimeout)
	if err != nil {
		fmt.Printf("the Bluetooth media bus does not respond, and a restart of this container "+
			"registers nothing while the bus is gone: %v\n", err)
		return
	}

	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err = conn.Object(bluezService, "/").
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).
		Store(&objects)
	if err != nil {
		fmt.Printf("bluetoothd does not return its managed objects, and a restart of this container "+
			"registers nothing until it does: %v\n", err)
		return
	}

	adapters, registered := endpointsFrom(objects)
	switch {
	case adapters == 0:
		fmt.Printf("bluetoothd published no adapter, so there is no radio to advertise on\n")
	case registered:
		fmt.Printf("the adapter advertises a media profile that bluetoothd hosts, " +
			"so WirePlumber's endpoints are registered\n")
	default:
		fatal("the adapter advertises none of the %d media profiles that bluetoothd hosts, "+
			"so WirePlumber's endpoints are gone, and only a restart of this container registers them again",
			hostedProfiles(objects))
	}
}

// endpointsFrom returns how many adapters a managed-object tree has,
// and whether any adapter advertises a media profile that bluetoothd
// hosts. The result covers the whole tree and not each adapter: a
// machine with two radios runs one Bluetooth operator pod, for the
// radio that this claim includes, and a registration on either adapter
// means that WirePlumber still has its connection.
func endpointsFrom(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) (adapters int, registered bool) {
	for _, interfaces := range objects {
		advertised, ok := interfaces[adapterInterface]
		if !ok {
			continue
		}
		adapters++
		hosted, ok := interfaces[mediaInterface]
		if !ok {
			// An adapter with no Media1 hosts no media profile, so it
			// never counts as registered. When it is the only adapter,
			// the probe fails.
			continue
		}
		if overlaps(uuidList(advertised, "UUIDs"), uuidList(hosted, "SupportedUUIDs")) {
			registered = true
		}
	}
	return adapters, registered
}

// hostedProfiles counts the media profiles that the adapters host, for
// the failure message.
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

// uuidList returns a string-array property, or nil when the property
// is missing or is not a string array.
func uuidList(properties map[string]dbus.Variant, key string) []string {
	list, _ := properties[key].Value().([]string)
	return list
}

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

// sameUUID compares two UUIDs as acceptsA2DP does, ignoring case and
// surrounding space, because the case of a UUID has no meaning. An
// empty string matches nothing.
func sameUUID(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && strings.EqualFold(a, b)
}
