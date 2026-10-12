package main

// A device that moves from one running server to another. Its pod
// belongs to the server in the pod's label until the pod is gone, so
// the server it left stops it, and the server it joined starts a pod
// of its own.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// twoReadyWorld runs the example with both telescopes reserved and
// Ready.
func twoReadyWorld(t *testing.T) *world {
	w := readyWorld(t)
	w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
	w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
	return w
}

// afterAdoption answers the changes that a client sent to a device on
// the server it left, after the first change sent to it on the server
// it joined, from the index from of the record of changes. A change
// there acts on a device that the other server runs already.
func afterAdoption(w *world, from int, left, joined, device string) []string {
	var out []string
	adopted := false
	for _, c := range w.indi.changes()[from:] {
		adopted = adopted || strings.HasPrefix(c, joined+" "+device+".")
		if adopted && strings.HasPrefix(c, left+" "+device+".") {
			out = append(out, c)
		}
	}
	return out
}

// A device that moves from one Ready telescope to another stops on the
// server it left, as any device that leaves does, and then starts in a
// new pod on the server it joined. The server it left never acts on it
// once the other server runs it, whether that server's reservation
// stays Ready or ends in the same apply.
func TestADeviceMovedToAnotherReadyTelescopeRunsOnlyThere(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		then func(w *world)
	}{
		{name: "east stays Ready", then: func(w *world) {}},
		{name: "east ends in the same apply", then: func(w *world) {
			w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
			w.until(10*time.Minute, "east-tonight is still there", func() bool {
				_, ok := w.reservation("east-tonight")
				return !ok
			})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := twoReadyWorld(t)
				moved := len(w.indi.changes())
				before, _ := w.api.object(podsCollection, "east-focuser")

				w.put(observatory.FocuserKind, "east", map[string]any{"opticalTrain": "west-imaging", "driver": map[string]any{"name": "indi_simulator_focus"}})
				c.then(w)
				w.until(5*time.Minute, "the focuser does not connect on west", func() bool {
					return slices.Contains(w.indi.connected("west-telescope"), "Focuser Simulator")
				})
				time.Sleep(2 * statusWindow)
				synctest.Wait()

				if got := afterAdoption(w, moved, "east-telescope", "west-telescope", "Focuser Simulator"); len(got) != 0 {
					t.Errorf("east sent %q to the focuser after west ran it", got)
				}
				// A driver whose pod goes while a shim still lists it
				// restarts against nothing: west ran a pod that east
				// deleted, or west deleted a pod that east still ran.
				if got := w.indi.restarted(); slices.ContainsFunc(got, func(r string) bool { return strings.HasSuffix(r, " east-focuser") }) {
					t.Errorf("restarted drivers = %q, want none of the focuser", got)
				}
				after, _ := w.api.object(podsCollection, "east-focuser")
				if got := after["metadata"].(map[string]any)["labels"].(map[string]any)[labelServer]; got != "west-telescope" || podUID(after) == podUID(before) {
					t.Errorf("the focuser's pod is labeled for %v, and new %t; want a new pod for west-telescope", got, podUID(after) != podUID(before))
				}
				focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
				s := focuser.Status
				if s.Phase != observatory.DeviceConnected || s.Endpoint == nil || s.Endpoint.Service != "west-telescope" {
					t.Errorf("focuser status = phase %s, endpoint %+v", s.Phase, s.Endpoint)
				}
				if r, _ := w.reservation("west-tonight"); r.Status.Phase != observatory.ReservationReady {
					t.Errorf("west-tonight is %s", r.Status.Phase)
				}
			})
		})
	}
}

// A device pod whose server is gone belongs to no server, so the
// server that the tree places the device on replaces it with a pod of
// its own, and runs no driver in the pod that the other server's
// runner deletes when it starts that server again. Here the operator
// is down while a node's eviction deletes east's server pod and a
// person moves the focuser to west.
func TestADevicePodWhoseServerIsGoneIsReplaced(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := twoReadyWorld(t)
		before, _ := w.api.object(podsCollection, "east-focuser")

		w.halt()
		w.api.deleteNamed(podsCollection, "east-telescope")
		w.put(observatory.FocuserKind, "east", map[string]any{"opticalTrain": "west-imaging", "driver": map[string]any{"name": "indi_simulator_focus"}})
		w.start()
		w.until(5*time.Minute, "the focuser does not connect on west", func() bool {
			return slices.Contains(w.indi.connected("west-telescope"), "Focuser Simulator")
		})
		time.Sleep(2 * statusWindow)
		synctest.Wait()

		if got := w.indi.restarted(); slices.ContainsFunc(got, func(r string) bool { return strings.HasSuffix(r, " east-focuser") }) {
			t.Errorf("restarted drivers = %q, want none of the focuser", got)
		}
		after, _ := w.api.object(podsCollection, "east-focuser")
		if got := after["metadata"].(map[string]any)["labels"].(map[string]any)[labelServer]; got != "west-telescope" || podUID(after) == podUID(before) {
			t.Errorf("the focuser's pod is labeled for %v, and new %t; want a new pod for west-telescope", got, podUID(after) != podUID(before))
		}
	})
}

// A Switch that moves to another telescope while its telescope's
// release runs still runs on the server it left until its pod is gone,
// so the release switches its outputs off there, whether or not the
// other telescope's server runs. The mount's park holds the release in
// its Deactivation step while the Switch moves.
func TestASwitchMovedAwayDuringAReleaseSwitchesItsOutputsOff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		world func(t *testing.T) *world
	}{
		{name: "west stopped", world: readyWorld},
		{name: "west Ready", world: twoReadyWorld},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := c.world(t)
				moved := len(w.indi.changes())
				w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
				w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
				w.until(time.Minute, "the park does not run", func() bool {
					r, _ := w.reservation("east-tonight")
					return strings.Contains(stepText(stepOf(r, observatory.StepDeactivation)), "Mount east state: Parked Running")
				})

				w.put(observatory.SwitchKind, "east", map[string]any{"telescope": "west", "driver": map[string]any{"name": "indi_simulator_io"}})
				w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
				parked(w, "Telescope Simulator")
				w.until(10*time.Minute, "east-tonight is still there", func() bool {
					_, ok := w.reservation("east-tonight")
					return !ok
				})

				sent := w.indi.changes()[moved:]
				for _, off := range outputsOff {
					if !slices.Contains(sent, off) {
						t.Errorf("changes = %q, want %q", sent, off)
					}
				}
			})
		})
	}
}

// parked answers a held park: the mount reports itself parked, as the
// driver would have.
func parked(w *world, device string) {
	w.indi.report(device, "TELESCOPE_PARK", "PARK", "On")
	w.indi.report(device, "TELESCOPE_PARK", "UNPARK", "Off")
}
