package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	inputUUID       = "00001124-0000-1000-8000-00805f9b34fb"
	avrcpTargetUUID = "0000110c-0000-1000-8000-00805f9b34fb"
)

type bluezDevice struct {
	Path      string
	Address   string
	Alias     string
	Paired    bool
	Connected bool
	UUIDs     []string
}

// bluezTree returns a GetManagedObjects result with an adapter and a
// device object for each argument. The adapter lists AudioSink, as a
// real adapter does after PipeWire registers its media endpoint.
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
		t.Fatalf("reading the speakers from the tree: %v", err)
	}
	if len(speakers) != 2 {
		t.Fatalf("speakers = %v, want two", speakers)
	}
	// The key is the address in lowercase, whatever case BlueZ printed
	// it in, so that it matches the key that normalizeMAC makes from
	// the address pw-dump prints on the sink node.
	connected := speakers["a0:ab:51:33:b7:12"]
	if connected.Name != "Kitchen Speaker" || !connected.Connected {
		t.Errorf("the connected speaker is %+v, want Kitchen Speaker, connected", connected)
	}
	// A speaker that is switched off is still paired.
	off := speakers["7c:66:00:11:22:33"]
	if off.Name != "Bathroom Speaker" || off.Connected {
		t.Errorf("the speaker that is off is %+v, want Bathroom Speaker, not connected", off)
	}
}

func TestSpeakersFromIgnoresWhatIsNotASpeaker(t *testing.T) {
	cases := []struct {
		name   string
		device bluezDevice
	}{
		{
			// BlueZ found this device in a scan and stores no link key
			// for it.
			name: "a speaker that is not paired",
			device: bluezDevice{
				Path:    "/org/bluez/hci0/dev_A0_AB_51_33_B7_12",
				Address: "A0:AB:51:33:B7:12",
				UUIDs:   []string{audioSinkUUID},
			},
		},
		{
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
				t.Fatalf("reading the speakers from the tree: %v", err)
			}
			if len(speakers) != 0 {
				t.Fatalf("speakers = %v, want none", speakers)
			}
		})
	}
}

func TestSpeakersFromNeverPublishesTheAdapter(t *testing.T) {
	speakers, err := speakersFrom(bluezTree())
	if err != nil {
		t.Fatalf("reading the speakers from the tree: %v", err)
	}
	if len(speakers) != 0 {
		t.Fatalf("the adapter published as %v, want no speaker", speakers)
	}
}

func TestSpeakersFromReportsATreeWithNoAdapter(t *testing.T) {
	tree := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}
	if _, err := speakersFrom(tree); err != ErrNoAdapter {
		t.Fatalf("error = %v, want %v", err, ErrNoAdapter)
	}
}

// monitorDirectory makes writeMonitorConfig write into a temporary
// directory.
func monitorDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wireplumber.conf.d")
	wireplumberConfigDir = dir
	t.Cleanup(func() { wireplumberConfigDir = "/etc/wireplumber/wireplumber.conf.d" })
	return dir
}

func TestWriteMonitorConfigFollowsTheDeliveredBus(t *testing.T) {
	cases := []struct {
		name    string
		address string
		wrote   bool
	}{
		{
			name:    "the claim includes a media bus",
			address: "unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket",
			wrote:   true,
		},
		{name: "the claim includes none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := monitorDirectory(t)
			t.Setenv(busAddressVariable, c.address)

			wrote, err := writeMonitorConfig()
			if err != nil {
				t.Fatalf("writing the monitor fragment: %v", err)
			}
			if wrote != c.wrote {
				t.Fatalf("wrote = %v, want %v", wrote, c.wrote)
			}
			written, err := os.ReadFile(filepath.Join(dir, monitorFragmentName))
			if !c.wrote {
				if !os.IsNotExist(err) {
					t.Fatalf("a pod with no bus wrote %s: %v", written, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("reading the monitor fragment: %v", err)
			}
			if string(written) != monitorConfig {
				t.Fatalf("the file contains something other than the fragment:\n%s", written)
			}
		})
	}
}

// WirePlumber and PipeWire's bluez5 plugin ignore a misspelled setting
// name and report no error, so the test fixes the text of each setting.
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
	for _, role := range []string{"hsp_hs", "hsp_ag", "hfp_hf", "hfp_ag"} {
		if strings.Contains(monitorConfig, role) {
			t.Errorf("the fragment enables the headset role %q", role)
		}
	}
}

// fakeSubscriptions returns the next channel in next on each call, and
// an error when that entry is nil or next is empty.
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

// When a subscription's channel closes, as it does when the Bluetooth
// operator's pod restarts, resubscribe tries again every busRetryDelay
// and sends a value when a new subscription opens.
func TestTheMediaBusIsFollowedAcrossARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := make(chan struct{}, 1), make(chan struct{}, 1)
		bus := &fakeSubscriptions{next: []chan struct{}{first, nil, second}}
		// resubscribe's goroutine ranges over second and does not read
		// ctx until second closes, so the bubble ends only after this
		// close.
		t.Cleanup(func() { close(second) })

		out := resubscribe(t.Context(), bus.subscribe, busRetryDelay, func(string) {})
		waitForWake(t, out)

		first <- struct{}{}
		waitForWake(t, out)

		close(first)
		closed := time.Now()
		waitForWake(t, out)
		if waited := time.Since(closed); waited != busRetryDelay {
			t.Errorf("the refused subscription was tried again after %v, want %v", waited, busRetryDelay)
		}
		second <- struct{}{}
		waitForWake(t, out)
		bus.mu.Lock()
		defer bus.mu.Unlock()
		if bus.calls != 3 {
			t.Errorf("the bus was subscribed %d times, want 3: the first, the refused one, and the one after it", bus.calls)
		}
	})
}

func TestTheMediaBusFollowerEndsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		bus := &fakeSubscriptions{}

		out := resubscribe(ctx, bus.subscribe, busRetryDelay, func(string) {})
		cancel()

		if _, open := <-out; open {
			t.Fatal("the follower woke the loop with no subscription open")
		}
	})
}

func TestAMediaBusWithNoConnectionReturnsAnError(t *testing.T) {
	speakers, err := (&mediaBus{}).speakers()
	if err == nil || speakers != nil {
		t.Errorf("speakers() = %v, %v, want nil and an error", speakers, err)
	}
}
