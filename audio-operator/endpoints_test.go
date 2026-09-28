package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// a2dpSourceUUID is the profile the pod's a2dp_source role
// registers, and the one the adapter advertises while WirePlumber
// holds its endpoints. The tests name it here because the code under
// test names no profile at all.
const a2dpSourceUUID = "0000110a-0000-1000-8000-00805f9b34fb"

// managedObjects builds one BlueZ managed-object tree. The tests
// state an adapter as the two lists that matter: what it advertises,
// and what media profiles bluetoothd hosts on it.
func managedObjects(advertised, hosted []string) map[dbus.ObjectPath]map[string]map[string]dbus.Variant {
	adapter := map[string]map[string]dbus.Variant{
		adapterInterface: {"UUIDs": dbus.MakeVariant(advertised)},
	}
	if hosted != nil {
		adapter[mediaInterface] = map[string]dbus.Variant{
			"SupportedUUIDs": dbus.MakeVariant(hosted),
		}
	}
	return map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez/hci0": adapter,
	}
}

// The two states the probe exists to tell apart, in the exact form
// bluetoothd reported them on a machine with one radio and one
// speaker. The healthy tree carries AudioSource because WirePlumber
// registered an endpoint for it. The broken tree is the same radio
// after the Bluetooth pod restarted, with every UUID that bluetoothd
// publishes for itself and none that a client registers.
func TestEndpointsFromReadsTheRegistration(t *testing.T) {
	hosted := []string{a2dpSourceUUID, audioSinkUUID}
	cases := []struct {
		name       string
		advertised []string
		hosted     []string
		adapters   int
		registered bool
	}{
		{
			name:       "a registered endpoint",
			advertised: []string{a2dpSourceUUID, "0000110c-0000-1000-8000-00805f9b34fb"},
			hosted:     hosted,
			adapters:   1,
			registered: true,
		},
		{
			name: "no registered endpoint",
			advertised: []string{
				"0000110c-0000-1000-8000-00805f9b34fb",
				"0000110e-0000-1000-8000-00805f9b34fb",
				"00001200-0000-1000-8000-00805f9b34fb",
			},
			hosted:     hosted,
			adapters:   1,
			registered: false,
		},
		{
			// A role change from a2dp_source to a2dp_sink advertises
			// AudioSink instead, and the check passes with no edit.
			name:       "a different role registered",
			advertised: []string{audioSinkUUID},
			hosted:     hosted,
			adapters:   1,
			registered: true,
		},
		{
			// An adapter with no Media1 hosts no media profile, so
			// nothing about it can be missing.
			name:       "an adapter that hosts no media profile",
			advertised: []string{a2dpSourceUUID},
			hosted:     nil,
			adapters:   1,
			registered: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapters, registered := endpointsFrom(managedObjects(c.advertised, c.hosted))
			if adapters != c.adapters {
				t.Errorf("adapters = %d, want %d", adapters, c.adapters)
			}
			if registered != c.registered {
				t.Errorf("registered = %v, want %v", registered, c.registered)
			}
		})
	}
}

// A tree with no adapter is a radio that is unplugged, and the probe
// passes on it rather than restarting a container that would find
// nothing to register.
func TestEndpointsFromCountsNoAdapter(t *testing.T) {
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez": {"org.freedesktop.DBus.ObjectManager": {}},
	}
	adapters, registered := endpointsFrom(objects)
	if adapters != 0 {
		t.Errorf("adapters = %d, want 0", adapters)
	}
	if registered {
		t.Error("a tree with no adapter reported a registration")
	}
}

// A second radio that holds the registration answers for the tree. A
// machine with two adapters runs one Bluetooth operator pod, and a
// live connection on either is a WirePlumber that still holds its
// bus.
func TestEndpointsFromReadsEitherAdapter(t *testing.T) {
	objects := managedObjects(nil, []string{a2dpSourceUUID})
	objects["/org/bluez/hci1"] = map[string]map[string]dbus.Variant{
		adapterInterface: {"UUIDs": dbus.MakeVariant([]string{a2dpSourceUUID})},
		mediaInterface:   {"SupportedUUIDs": dbus.MakeVariant([]string{a2dpSourceUUID})},
	}
	adapters, registered := endpointsFrom(objects)
	if adapters != 2 {
		t.Errorf("adapters = %d, want 2", adapters)
	}
	if !registered {
		t.Error("a registration on the second adapter was not read")
	}
}

// hostedProfiles states what the failure line counts, so a reader of
// kubectl describe learns what the check looked for.
func TestHostedProfilesCountsWhatBlueZHosts(t *testing.T) {
	objects := managedObjects(nil, []string{a2dpSourceUUID, audioSinkUUID})
	if got := hostedProfiles(objects); got != 2 {
		t.Errorf("hostedProfiles = %d, want 2", got)
	}
}

func TestOverlapsFoldsCase(t *testing.T) {
	cases := []struct {
		name       string
		advertised []string
		hosted     []string
		want       bool
	}{
		{"same case", []string{a2dpSourceUUID}, []string{a2dpSourceUUID}, true},
		{"upper against lower", []string{"0000110A-0000-1000-8000-00805F9B34FB"}, []string{a2dpSourceUUID}, true},
		{"padded", []string{" " + a2dpSourceUUID + " "}, []string{a2dpSourceUUID}, true},
		{"no shared value", []string{audioSinkUUID}, []string{a2dpSourceUUID}, false},
		{"both empty", nil, nil, false},
		{"empty strings never match", []string{""}, []string{""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := overlaps(c.advertised, c.hosted); got != c.want {
				t.Errorf("overlaps = %v, want %v", got, c.want)
			}
		})
	}
}
