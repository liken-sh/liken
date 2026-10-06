package indi

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
)

// domeParked is the dome simulator's DOME_PARK as the dome reports it
// once it has parked.
var domeParked = Property{
	Device: "Dome Simulator", Name: "DOME_PARK", Type: SwitchType, State: Ok,
	Members: []Member{{Name: "PARK", Switch: true}, {Name: "UNPARK"}},
}

func TestRelaySendsTheReportOfADeviceTheServerLacks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedBaseline(t, "telescope")
		if err := c.Relay(domeParked); err != nil {
			t.Fatal(err)
		}
		got := server.await("DOME_PARK")
		if got.XMLName.Local != "setSwitchVector" || got.Device != "Dome Simulator" || got.State != "Ok" {
			t.Errorf("sent <%s device=%q state=%q>, want <setSwitchVector device=\"Dome Simulator\" state=\"Ok\">", got.XMLName.Local, got.Device, got.State)
		}
		if want := []string{"PARK=On", "UNPARK=Off"}; !slices.Equal(values(got), want) {
			t.Errorf("sent %v, want %v", values(got), want)
		}
		if _, ok := c.Property("Dome Simulator", "DOME_PARK"); ok {
			t.Error("the store holds the relayed property, want only what the server reports")
		}
	})
}

func TestRelayRefusesALight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := connectedBaseline(t, "telescope")
		light := Property{Device: "Dome Simulator", Name: "DOME_STATUS", Type: LightType}
		if err := c.Relay(light); !errors.Is(err, ErrInvalid) {
			t.Errorf("Relay = %v, want ErrInvalid", err)
		}
	})
}

func TestRelayRefusesWithNoConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClient("127.0.0.1:1")
		if err := c.Relay(domeParked); !errors.Is(err, ErrNotConnected) {
			t.Errorf("Relay = %v, want ErrNotConnected", err)
		}
	})
}
