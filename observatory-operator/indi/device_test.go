package indi

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

var definitionOrDeletion = regexp.MustCompile(`<(def(?:Number|Switch|Text|Light|BLOB)Vector|delProperty) device="[^"]+"(?: name="([^"]*)")?`)

// expectedProperties applies the definitions and deletions of
// transcripts in order, with a regular expression rather than with the
// client, and returns the properties that remain, sorted. A
// delProperty with no name attribute deletes the device. The receiver
// simulator sends one with an empty name, which deletes nothing.
func expectedProperties(transcripts ...[]byte) []string {
	var names []string
	for _, data := range transcripts {
		for _, match := range definitionOrDeletion.FindAllSubmatch(data, -1) {
			name := string(match[2])
			switch {
			case string(match[1]) != "delProperty":
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			case match[2] == nil:
				names = nil
			default:
				names = slices.DeleteFunc(names, func(n string) bool { return n == name })
			}
		}
	}
	slices.Sort(names)
	return names
}

// propertiesAre is a condition that holds when the device has exactly
// the named properties.
func propertiesAre(device string, want []string) func(*Store) bool {
	return func(s *Store) bool {
		var got []string
		for _, p := range s.Properties(device) {
			got = append(got, p.Name)
		}
		slices.Sort(got)
		return slices.Equal(got, want)
	}
}

func TestConnectAndDisconnectEverySimulator(t *testing.T) {
	for _, simulator := range simulators {
		t.Run(simulator, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, server := connectedBaseline(t, simulator)
				baseline := transcript(t, simulator, "baseline")
				connect := transcript(t, simulator, "connect")
				disconnect := transcript(t, simulator, "disconnect")

				if err := connectDevice(t, c, server.device); err != nil {
					t.Fatal(err)
				}
				p, _ := c.Property(server.device, "CONNECTION")
				if p.State != Ok || !slices.Equal(p.On(), []string{"CONNECT"}) {
					t.Errorf("CONNECTION after ConnectDevice: %s with %v on, want Ok with [CONNECT]", p.State, p.On())
				}
				waitFor(t, c, "the properties of the connected device",
					propertiesAre(server.device, expectedProperties(baseline, connect)))

				if err := disconnectDevice(t, c, server.device); err != nil {
					t.Fatal(err)
				}
				p, _ = c.Property(server.device, "CONNECTION")
				if !slices.Equal(p.On(), []string{"DISCONNECT"}) {
					t.Errorf("CONNECTION after DisconnectDevice has %v on, want [DISCONNECT]", p.On())
				}
				waitFor(t, c, "the properties of the disconnected device",
					propertiesAre(server.device, expectedProperties(baseline, connect, disconnect)))

				for _, r := range server.received() {
					if r.XMLName.Local == "enableBLOB" {
						t.Errorf("the client sent enableBLOB with no caller asking: %+v", r)
					}
				}
			})
		})
	}
}

func TestConnectDeviceSendsNothingWhenConnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "focus")
		if err := connectDevice(t, c, server.device); err != nil {
			t.Fatal(err)
		}
		var sent int
		for _, r := range server.received() {
			if r.Name == "CONNECTION" {
				sent++
			}
		}
		if sent != 1 {
			t.Errorf("the client sent CONNECTION %d times, want once", sent)
		}
	})
}

func TestConnectDeviceWaitsForTheDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := startReplay(t, "focus")
		c := server.newClient()
		result := make(chan error, 1)
		go func() { result <- connectDevice(t, c, server.device) }()
		run(t, c)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}

func TestConnectDeviceGivesUpWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := connectedBaseline(t, "focus")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if err := c.ConnectDevice(ctx, "No Such Device"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("ConnectDevice = %v, want context.DeadlineExceeded", err)
		}
	})
}

func TestConnectDeviceReportsARefusal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "focus")
		server.mute()
		result := make(chan error, 1)
		go func() { result <- connectDevice(t, c, server.device) }()
		server.await("CONNECTION")
		// A driver that cannot open its port refuses the connection so.
		server.send(`<setSwitchVector device="Focuser Simulator" name="CONNECTION" state="Alert">
	<oneSwitch name="CONNECT">Off</oneSwitch><oneSwitch name="DISCONNECT">On</oneSwitch>
	</setSwitchVector>`)
		var alert *AlertError
		if err := <-result; !errors.As(err, &alert) {
			t.Errorf("ConnectDevice = %v, want an AlertError", err)
		}
	})
}

func TestConnectDeviceReportsADriverThatStaysDisconnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "focus")
		server.mute()
		result := make(chan error, 1)
		go func() { result <- connectDevice(t, c, server.device) }()
		server.await("CONNECTION")
		// The driver works on the connection, then gives up with no Alert.
		server.send(`<setSwitchVector device="Focuser Simulator" name="CONNECTION" state="Busy"/>
	<setSwitchVector device="Focuser Simulator" name="CONNECTION" state="Idle">
	<oneSwitch name="CONNECT">Off</oneSwitch><oneSwitch name="DISCONNECT">On</oneSwitch>
	</setSwitchVector>`)
		err := <-result
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("ConnectDevice = %v, want the report that CONNECT stayed Off", err)
		}
	})
}
