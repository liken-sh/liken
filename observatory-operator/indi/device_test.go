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

// Sync returns once the device's driver has answered a getProperties
// that names the device, so a property that the driver defined after
// CONNECTION and before that answer is in the store.
func TestSyncWaitsForEverythingTheDriverWroteBefore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "dustcover")
		server.mute()
		synced := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			synced <- c.Sync(ctx, server.device)
		}()
		request := server.awaitElement("getProperties", 2)
		if request.Device != server.device {
			t.Errorf("getProperties names %q, want %q", request.Device, server.device)
		}
		synctest.Wait()
		select {
		case err := <-synced:
			t.Fatalf("Sync returned %v before the driver answered", err)
		default:
		}
		server.send(`<defSwitchVector device="` + server.device + `" name="CAP_PARK" state="Idle" perm="rw" rule="OneOfMany"><defSwitch name="PARK">Off</defSwitch><defSwitch name="UNPARK">On</defSwitch></defSwitchVector>`)
		server.send(`<defSwitchVector device="` + server.device + `" name="CONNECTION" state="Ok" perm="rw" rule="OneOfMany"><defSwitch name="CONNECT">On</defSwitch><defSwitch name="DISCONNECT">Off</defSwitch></defSwitchVector>`)
		if err := <-synced; err != nil {
			t.Fatal(err)
		}
		if _, ok := c.Property(server.device, "CAP_PARK"); !ok {
			t.Error("CAP_PARK is not in the store after Sync")
		}
	})
}

// An update of CONNECTION is not the driver's answer to getProperties:
// KStars can connect the device again while Sync waits, and the
// driver's Busy and Ok come before the interface's properties. Sync
// waits for the driver to define CONNECTION again, which it does in
// its answer.
func TestSyncWaitsForTheDefinitionOfConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "dustcover")
		server.mute()
		synced := make(chan error, 1)
		go func() { synced <- c.Sync(context.Background(), server.device) }()
		server.awaitElement("getProperties", 2)
		server.send(`<setSwitchVector device="` + server.device + `" name="CONNECTION" state="Busy"/>
	<setSwitchVector device="` + server.device + `" name="CONNECTION" state="Ok"><oneSwitch name="CONNECT">On</oneSwitch><oneSwitch name="DISCONNECT">Off</oneSwitch></setSwitchVector>`)
		synctest.Wait()
		select {
		case err := <-synced:
			t.Fatalf("Sync returned %v on an update of CONNECTION", err)
		default:
		}
		server.send(`<defSwitchVector device="` + server.device + `" name="CONNECTION" state="Ok" perm="rw" rule="OneOfMany"><defSwitch name="CONNECT">On</defSwitch><defSwitch name="DISCONNECT">Off</defSwitch></defSwitchVector>`)
		if err := <-synced; err != nil {
			t.Fatal(err)
		}
	})
}

// A property's revision rises with each definition and each update, so
// a caller can tell whether the property changed since it looked.
func TestARevisionRisesWithEachChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "dustcover")
		if r := c.Revision(server.device, "NOTHING"); r != 0 {
			t.Errorf("the revision of a property that is not defined = %d, want 0", r)
		}
		first := c.Revision(server.device, "CONNECTION")
		server.send(`<setSwitchVector device="` + server.device + `" name="CONNECTION" state="Busy"/>`)
		synctest.Wait()
		updated := c.Revision(server.device, "CONNECTION")
		server.send(`<defSwitchVector device="` + server.device + `" name="CONNECTION" state="Ok" perm="rw" rule="OneOfMany"><defSwitch name="CONNECT">Off</defSwitch><defSwitch name="DISCONNECT">On</defSwitch></defSwitchVector>`)
		synctest.Wait()
		if defined := c.Revision(server.device, "CONNECTION"); first == 0 || updated <= first || defined <= updated {
			t.Errorf("revisions = %d, %d, %d, want each one higher", first, updated, defined)
		}
	})
}

func TestSyncFails(t *testing.T) {
	cases := []struct {
		name  string
		after func(*replayServer)
		want  error
	}{
		{"when the connection ends", func(s *replayServer) { s.drop() }, ErrDisconnected},
		{"when the device goes", func(s *replayServer) { s.send(`<delProperty device="` + s.device + `"/>`) }, ErrDeleted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, server := connectedBaseline(t, "dustcover")
				server.mute()
				synced := make(chan error, 1)
				go func() { synced <- client.Sync(context.Background(), server.device) }()
				server.awaitElement("getProperties", 2)
				c.after(server)
				if err := <-synced; !errors.Is(err, c.want) {
					t.Errorf("Sync = %v, want %v", err, c.want)
				}
			})
		})
	}
}

func TestSyncNeedsTheDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := connectedBaseline(t, "dustcover")
		if err := c.Sync(context.Background(), "Nobody"); !errors.Is(err, ErrNotDefined) {
			t.Errorf("Sync = %v, want ErrNotDefined", err)
		}
	})
}
