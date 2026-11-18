package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// a2dpSourceUUID is the UUID of AudioSource, the profile that the pod's
// a2dp_source role registers. The tests define it because the code
// under test names no profile.
const a2dpSourceUUID = "0000110a-0000-1000-8000-00805f9b34fb"

// managedObjects builds a BlueZ managed-object tree with one adapter.
// A nil hosted list leaves out the Media1 interface.
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

// The cases use the UUIDs that bluetoothd reported on a machine with one
// radio and one speaker. While the endpoint is registered, the adapter
// advertises AudioSource. After the Bluetooth pod restarts, the adapter
// advertises only the UUIDs that bluetoothd publishes for itself.
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
			// A deployment that registers a2dp_sink advertises AudioSink,
			// and the check passes.
			name:       "a different role registered",
			advertised: []string{audioSinkUUID},
			hosted:     hosted,
			adapters:   1,
			registered: true,
		},
		{
			// An adapter with no Media1 hosts no media profile, so it never
			// counts as registered, and the probe fails.
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

// A tree with no adapter means that the radio is unplugged. The probe
// passes, because a restart of the container has nothing to register
// with.
func TestEndpointsFromCountsNoAdapter(t *testing.T) {
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez": {"org.freedesktop.DBus.ObjectManager": {}},
	}
	adapters, registered := endpointsFrom(objects)
	if adapters != 0 {
		t.Errorf("adapters = %d, want 0", adapters)
	}
	if registered {
		t.Error("registered = true for a tree with no adapter, want false")
	}
}

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
		t.Error("registered = false with a registration on the second adapter, want true")
	}
}

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
