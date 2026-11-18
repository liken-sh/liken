package main

// A mount reads DOME_POLICY from its saved configuration on each
// getProperties (fakeconfig_test.go), so the mount stays locked for a
// client that connects later only if the saved configuration has
// DOME_LOCKS On.

import (
	"context"
	"io"
	"testing"
	"testing/synctest"
)

// look connects to a server as another client such as PHD2 or KStars
// does, and sends getProperties.
func (w *indiWorld) look(server string) {
	w.t.Helper()
	conn, err := w.DialContext(context.Background(), "tcp", server+".observatory.svc:7624")
	if err != nil {
		w.t.Fatalf("connecting to %s: %v", server, err)
	}
	if _, err := io.WriteString(conn, `<getProperties version="1.7"/>`+"\n"); err != nil {
		w.t.Fatalf("sending getProperties to %s: %v", server, err)
	}
	synctest.Wait()
	_ = conn.Close()
	synctest.Wait()
}

func TestAClientThatConnectsLaterLeavesTheMountLocked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		w.indi.look("east-telescope")
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "DOME_POLICY"); got != "Ok DOME_LOCKS" {
			t.Fatalf("DOME_POLICY is %q, want Ok with DOME_LOCKS On", got)
		}

		for _, server := range []string{"east-telescope", "west-telescope"} {
			w.indi.ask(server, "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		}
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsParkedToLab)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast("east-telescope", "DOME_PARK", domeParkedToEast)
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=Off", "UNPARK=On")
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "TELESCOPE_PARK"); got != "Alert PARK" {
			t.Errorf("TELESCOPE_PARK is %q, want Alert with PARK On", got)
		}
	})
}
