package main

// These tests cover which BlueZ objects are speakers this operator
// can play into, when the declare container enables WirePlumber's
// Bluetooth monitor, and how the operator follows the media bus
// across a restart of the Bluetooth operator's pod.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The UUIDs a test device advertises, in the base form BlueZ prints
// them in.
const (
	inputUUID       = "00001124-0000-1000-8000-00805f9b34fb"
	avrcpTargetUUID = "0000110c-0000-1000-8000-00805f9b34fb"
)

// bluezDevice is one org.bluez.Device1 object as a test states it.
type bluezDevice struct {
	Path      string
	Address   string
	Alias     string
	Paired    bool
	Connected bool
	UUIDs     []string
}

// bluezTree builds one managed-object tree with an adapter in it
// and one device object for each entry.
//
// The adapter advertises AudioSink itself, because a registered
// media endpoint puts that UUID on the adapter, and the adapter is
// the machine's own radio rather than a speaker.
func bluezTree(devices ...bluezDevice) map[dbus.ObjectPath]map[string]map[string]dbus.Variant {
	tree := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez/hci0": {
			adapterInterface: {
				"Address": dbus.MakeVariant("04:4A:11:22:33:44"),
				"Powered": dbus.MakeVariant(true),
				"UUIDs":   dbus.MakeVariant([]string{audioSinkUUID, avrcpTargetUUID}),
			},
		},
	}
	for _, device := range devices {
		tree[dbus.ObjectPath(device.Path)] = map[string]map[string]dbus.Variant{
			deviceInterface: {
				"Address":   dbus.MakeVariant(device.Address),
				"Alias":     dbus.MakeVariant(device.Alias),
				"Paired":    dbus.MakeVariant(device.Paired),
				"Connected": dbus.MakeVariant(device.Connected),
				"UUIDs":     dbus.MakeVariant(device.UUIDs),
			},
		}
	}
	return tree
}

func TestSpeakersFromReadsThePairedAudioSinks(t *testing.T) {
	tree := bluezTree(
		bluezDevice{
			Path:      "/org/bluez/hci0/dev_A0_AB_51_33_B7_12",
			Address:   "A0:AB:51:33:B7:12",
			Alias:     "Kitchen Speaker",
			Paired:    true,
			Connected: true,
			UUIDs:     []string{audioSinkUUID, avrcpTargetUUID},
		},
		bluezDevice{
			Path:    "/org/bluez/hci0/dev_7C_66_00_11_22_33",
			Address: "7C:66:00:11:22:33",
			Alias:   "Bathroom Speaker",
			Paired:  true,
			UUIDs:   []string{audioSinkUUID},
		},
	)

	speakers, err := speakersFrom(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(speakers) != 2 {
		t.Fatalf("speakers = %v, want two", speakers)
	}
	// The key is the address in the lowercase colon form, whatever
	// case BlueZ printed it in, because pw-dump prints the same
	// address on the sink node in the other case.
	connected := speakers["a0:ab:51:33:b7:12"]
	if connected.Name != "Kitchen Speaker" || !connected.Connected {
		t.Errorf("the connected speaker = %+v", connected)
	}
	// A speaker that is switched off is still paired, so it is still
	// this machine's to offer.
	off := speakers["7c:66:00:11:22:33"]
	if off.Name != "Bathroom Speaker" || off.Connected {
		t.Errorf("the speaker that is off = %+v", off)
	}
}

func TestSpeakersFromIgnoresWhatIsNotASpeaker(t *testing.T) {
	cases := []struct {
		name   string
		device bluezDevice
	}{
		{
			// A device BlueZ detected on the air and holds no link key
			// for is not this machine's to offer.
			name: "a speaker that is not paired",
			device: bluezDevice{
				Path:    "/org/bluez/hci0/dev_A0_AB_51_33_B7_12",
				Address: "A0:AB:51:33:B7:12",
				UUIDs:   []string{audioSinkUUID},
			},
		},
		{
			// The same adapter carries the keyboards and the speakers,
			// and this operator publishes only what it can play into.
			name: "a paired keyboard",
			device: bluezDevice{
				Path:    "/org/bluez/hci0/dev_04_4A_11_22_33_44",
				Address: "04:4A:11:22:33:44",
				Paired:  true,
				UUIDs:   []string{inputUUID},
			},
		},
		{
			name: "a paired device that advertises no profile at all",
			device: bluezDevice{
				Path:    "/org/bluez/hci0/dev_7C_66_00_11_22_33",
				Address: "7C:66:00:11:22:33",
				Paired:  true,
			},
		},
		{
			name: "a device object with no usable address",
			device: bluezDevice{
				Path:    "/org/bluez/hci0/dev_nothing",
				Address: "",
				Paired:  true,
				UUIDs:   []string{audioSinkUUID},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			speakers, err := speakersFrom(bluezTree(c.device))
			if err != nil {
				t.Fatal(err)
			}
			if len(speakers) != 0 {
				t.Fatalf("speakers = %v, want none", speakers)
			}
		})
	}
}

// The adapter object is the radio whose media bus this pod claimed,
// and its own UUIDs include AudioSink once the endpoint registers.
// It is not a speaker, so nothing about it enters the slice.
func TestSpeakersFromNeverPublishesTheAdapter(t *testing.T) {
	speakers, err := speakersFrom(bluezTree())
	if err != nil {
		t.Fatal(err)
	}
	if len(speakers) != 0 {
		t.Fatalf("the adapter published as %v", speakers)
	}
}

// bluetoothd publishes its object tree a moment after it claims its
// bus name, and it removes every device object when the adapter
// goes away. An answer with no adapter arrives in both cases, and
// neither one means the paired set is empty.
func TestSpeakersFromReportsATreeWithNoAdapter(t *testing.T) {
	tree := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}
	if _, err := speakersFrom(tree); err != ErrNoAdapter {
		t.Fatalf("error = %v, want %v", err, ErrNoAdapter)
	}
}

// monitorDirectory points the fragment write at a directory the
// test owns.
func monitorDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wireplumber.conf.d")
	wireplumberConfigDir = dir
	t.Cleanup(func() { wireplumberConfigDir = "/etc/wireplumber/wireplumber.conf.d" })
	return dir
}

// The delivered bus address is the switch: a pod whose claim
// allocated a media bus gets the fragment, and a pod whose claim
// did not writes nothing and runs as it did before Bluetooth
// existed here.
func TestWriteMonitorConfigFollowsTheDeliveredBus(t *testing.T) {
	cases := []struct {
		name    string
		address string
		wrote   bool
	}{
		{
			name:    "the claim delivered a media bus",
			address: "unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket",
			wrote:   true,
		},
		{name: "the claim delivered none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := monitorDirectory(t)
			t.Setenv(busAddressVariable, c.address)

			wrote, err := writeMonitorConfig()
			if err != nil {
				t.Fatal(err)
			}
			if wrote != c.wrote {
				t.Fatalf("wrote = %v, want %v", wrote, c.wrote)
			}
			written, err := os.ReadFile(filepath.Join(dir, monitorDropInName))
			if !c.wrote {
				if !os.IsNotExist(err) {
					t.Fatalf("a pod with no bus wrote %s: %v", written, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(written) != monitorConfig {
				t.Fatalf("the file holds something other than the fragment:\n%s", written)
			}
		})
	}
}

// The fragment states four things, and each one is a name read from
// the wireplumber 0.5 configuration or the pipewire bluez5 plugin.
// A name that drifts here turns the monitor off with no error
// anywhere, so the test pins each one.
func TestMonitorConfigStatesTheProfileAndTheRoles(t *testing.T) {
	lines := []string{
		"main-embedded = {",
		"hardware.bluetooth = required",
		"monitor.bluez = required",
		"monitor.bluez-midi = disabled",
		"bluez5.roles = [ a2dp_source ]",
		`bluez5.hfphsp-backend = "none"`,
	}
	for _, line := range lines {
		if !strings.Contains(monitorConfig, line) {
			t.Errorf("the fragment does not state %q", line)
		}
	}
	// HFP and HSP need an SCO socket in the host network
	// namespace, which this pod does not have, so no headset role may
	// ever appear in the list.
	for _, role := range []string{"hsp_hs", "hsp_ag", "hfp_hf", "hfp_ag"} {
		if strings.Contains(monitorConfig, role) {
			t.Errorf("the fragment enables the headset role %q", role)
		}
	}
}

// fakeSubscriptions hands out one scripted subscription per call: a
// channel, or an error when the channel is nil.
type fakeSubscriptions struct {
	mu    sync.Mutex
	calls int
	next  []chan struct{}
}

func (f *fakeSubscriptions) subscribe(context.Context) (<-chan struct{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.next) == 0 {
		return nil, errors.New("the bus refused the connection")
	}
	events := f.next[0]
	f.next = f.next[1:]
	if events == nil {
		return nil, errors.New("the bus refused the connection")
	}
	return events, nil
}

// A media bus that closes, as it does when the Bluetooth operator's
// pod restarts, is connected again, and the loop wakes once the new
// subscription is open. The wake is what makes the next pass read the
// whole paired set from the new bus, because the subscription missed
// every change while it was closed. A connection that fails is tried
// again.
func TestTheMediaBusIsFollowedAcrossARestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := make(chan struct{}, 1), make(chan struct{}, 1)
	bus := &fakeSubscriptions{next: []chan struct{}{first, nil, second}}

	out := resubscribe(ctx, bus.subscribe, time.Millisecond, func(string) {})
	waitForWake(t, out, testLimit)

	first <- struct{}{}
	waitForWake(t, out, testLimit)

	close(first)
	waitForWake(t, out, testLimit)
	second <- struct{}{}
	waitForWake(t, out, testLimit)
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if bus.calls != 3 {
		t.Errorf("the bus was subscribed %d times, want 3: the first, the refused one, and the one after it", bus.calls)
	}
}

// The subscription ends with its context, and only then.
func TestTheMediaBusFollowerEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bus := &fakeSubscriptions{}

	out := resubscribe(ctx, bus.subscribe, time.Millisecond, func(string) {})
	cancel()

	select {
	case _, open := <-out:
		if open {
			t.Fatal("the follower woke the loop with no subscription open")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the follower did not end with its context")
	}
}

// A pass that runs while the bus is closed reads no paired set, and
// the error keeps the speakers the last read found.
func TestAClosedMediaBusAnswersAnError(t *testing.T) {
	speakers, err := (&mediaBus{}).speakers()
	if err == nil || speakers != nil {
		t.Errorf("speakers = %v, %v; want an error", speakers, err)
	}
}
