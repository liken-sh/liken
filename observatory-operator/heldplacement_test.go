package main

// While a session holds a server, the server runs the devices it
// started. A spec edit that places one of them elsewhere takes effect
// when the session ends, and a device placed on a held server after it
// started waits for the server's next session.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// moveDevice replaces a device's parent field, or removes it when
// parent is "", and keeps the rest of its spec.
func (w *world) moveDevice(kind observatory.Kind, name, field, parent string) {
	object, _ := w.api.object(kindCollection(kind), name)
	spec := object["spec"].(map[string]any)
	for _, f := range []string{"observatory", "telescope", "opticalTrain"} {
		delete(spec, f)
	}
	if parent != "" {
		spec[field] = parent
	}
	w.put(kind, name, spec)
}

// heldWorld runs the example with the east focuser powered from output
// 1 of an observatory Switch lab, which no other device names and both
// telescopes reach, and with east-tonight Ready. With west, a
// reservation west-tonight of the west telescope is Ready too.
func heldWorld(t *testing.T, west bool) *world {
	w := startWorld(t)
	w.put(observatory.SwitchKind, "lab", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_io"}})
	w.powerFrom(observatory.FocuserKind, "east", "lab", 1)
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	if west {
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
	}
	w.settle()
	return w
}

// touches answers the changes that a client sent to an INDI device
// from the index from of the record of changes.
func touches(w *world, from int, device string) []string {
	return slices.DeleteFunc(slices.Clone(w.indi.changes()[from:]), func(c string) bool {
		return !strings.Contains(c, " "+device+".")
	})
}

// A spec edit that would move the focuser while east-tonight holds it
// changes nothing that runs: the focuser stays connected on east in the
// same pod, its output stays on, and west does not start it. Its
// ParentFound condition says where it goes, and posts one Event. A
// train deleted under it leaves it the same way, and ParentFound names
// the missing train.
func TestASessionKeepsADeviceThatASpecEditMoves(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		west    bool
		edit    func(w *world)
		reason  string
		message string
	}{
		{"to the shelf", false, func(w *world) { w.moveDevice(observatory.FocuserKind, "east", "", "") },
			"MovePending", "Moves to the shelf when Reservation east-tonight ends"},
		{"to an idle telescope's train", false, func(w *world) { w.moveDevice(observatory.FocuserKind, "east", "opticalTrain", "west-imaging") },
			"MovePending", "Moves to OpticalTrain west-imaging when Reservation east-tonight ends"},
		{"to a Ready telescope's train", true, func(w *world) { w.moveDevice(observatory.FocuserKind, "east", "opticalTrain", "west-imaging") },
			"MovePending", "Moves to OpticalTrain west-imaging when Reservation east-tonight ends"},
		{"its train deleted", false, func(w *world) { w.api.deleteNamed(kindCollection(observatory.OpticalTrainKind), "east-imaging") },
			"ParentMissing", "Missing OpticalTrain east-imaging"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t, c.west)
				before, _ := w.api.object(podsCollection, "east-focuser")
				from := len(w.indi.changes())

				c.edit(w)
				time.Sleep(5 * time.Minute)
				synctest.Wait()

				if got := touches(w, from, "Focuser Simulator"); len(got) != 0 {
					t.Errorf("changes to the focuser = %q, want none", got)
				}
				if got := touches(w, from, "Simulator IO"); len(got) != 0 {
					t.Errorf("changes to the Switches = %q, want none", got)
				}
				after, running := w.api.object(podsCollection, "east-focuser")
				if !running || podUID(after) != podUID(before) || after["metadata"].(map[string]any)["labels"].(map[string]any)[labelServer] != "east-telescope" {
					t.Errorf("the focuser's pod changed or went: %v", after)
				}
				if got := w.indi.connected("west-telescope"); slices.Contains(got, "Focuser Simulator") {
					t.Errorf("west connected %q, want no focuser", got)
				}
				focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
				if s := focuser.Status; s.Phase != observatory.DeviceConnected || s.Endpoint == nil || s.Endpoint.Service != "east-telescope" {
					t.Errorf("focuser status = phase %s, endpoint %+v", s.Phase, s.Endpoint)
				}
				parent := conditionOf(focuser.Status.Conditions, observatory.ConditionParentFound)
				if parent.Reason != c.reason || parent.Message != c.message {
					t.Errorf("ParentFound = %s %q, want %s %q", parent.Reason, parent.Message, c.reason, c.message)
				}
				events := slices.DeleteFunc(typedEvents(w.api, observatory.FocuserKind, "east"), func(e string) bool { return !strings.HasSuffix(e, ": "+c.message) })
				if len(events) != 1 {
					t.Errorf("Events that say %q = %q, want one", c.message, events)
				}
			})
		})
	}
}

// The release of east-tonight treats the focuser that a spec edit moved
// to west as its own: it disconnects the focuser, switches its output
// off, and deletes its pod. The next session of west starts it there,
// whether west was idle or held by a session of its own.
func TestAMovedDeviceLeavesWithItsSessionAndStartsInTheNext(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		west bool
		// free ends what holds west before the next session.
		free func(w *world)
	}{
		{"west idle", false, func(*world) {}},
		{"west Ready", true, func(w *world) {
			w.api.deleteNamed(kindCollection(observatory.ReservationKind), "west-tonight")
			w.gone(observatory.ReservationKind, "west-tonight", 10*time.Minute)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t, c.west)
				w.moveDevice(observatory.FocuserKind, "east", "opticalTrain", "west-imaging")
				w.settle()
				from := len(w.indi.changes())

				w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
				w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

				sent := w.indi.changes()[from:]
				for _, want := range []string{"east-telescope Focuser Simulator.CONNECTION CONNECT=Off DISCONNECT=On", labOutput("1", "OFF")} {
					if !slices.Contains(sent, want) {
						t.Errorf("changes = %q, want %q", sent, want)
					}
				}
				if slices.Contains(w.api.names(podsCollection), "east-focuser") {
					t.Error("the focuser's pod stays after the release")
				}

				c.free(w)
				w.reserve("west-later", map[string]any{"telescope": "west", "holder": "desktop"})
				w.phase("west-later", observatory.ReservationReady, 10*time.Minute)
				w.settle()

				if got := w.indi.connected("west-telescope"); !slices.Contains(got, "Focuser Simulator") {
					t.Errorf("west connected %q, want the focuser", got)
				}
				focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
				if s := focuser.Status; s.Phase != observatory.DeviceConnected || s.Endpoint == nil || s.Endpoint.Service != "west-telescope" {
					t.Errorf("focuser status = phase %s, endpoint %+v", s.Phase, s.Endpoint)
				}
				if parent := conditionOf(focuser.Status.Conditions, observatory.ConditionParentFound); parent.Reason != "Found" {
					t.Errorf("ParentFound = %s %q, want Found", parent.Reason, parent.Message)
				}
			})
		})
	}
}

// A served device that a spec edit puts on the shelf during a session
// stays with its owner: it reads the camera's wheel and holds the
// finalizer until the release, which lets it go.
func TestASessionKeepsAServedDeviceThatASpecEditMoves(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		shelveWheel(w)
		time.Sleep(time.Minute)
		synctest.Wait()

		wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		parent := conditionOf(wheel.Status.Conditions, observatory.ConditionParentFound)
		if s := wheel.Status; s.Phase != observatory.DeviceConnected || s.Slot == nil || !holdsFinalizer(wheel.Metadata) ||
			parent.Message != "Moves to the shelf when Reservation east-tonight ends" {
			t.Errorf("during the session, filter wheel = %s, slot %+v, finalizers %v, ParentFound %q", s.Phase, s.Slot, wheel.Metadata.Finalizers, parent.Message)
		}

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
		w.until(time.Minute, "the shelved filter wheel keeps its finalizer", func() bool {
			wheel, _ = decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
			return !holdsFinalizer(wheel.Metadata)
		})
		w.settle()
		wheel, _ = decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		if wheel.Status.Phase != observatory.DeviceInventory {
			t.Errorf("after the release, filter wheel = %s", wheel.Status.Phase)
		}
	})
}

// The pod of a device that a spec edit moved keeps the device on the
// server in its label, also when that server's pod is gone: the
// runner creates the server again with the device, and the other
// server, which the spec names, never takes the pod. Here the operator
// is down while a node's eviction deletes east's server pod and a
// person moves the focuser to west.
func TestAServerCreatedAgainKeepsADeviceThatASpecEditMoved(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := heldWorld(t, true)
		before, _ := w.api.object(podsCollection, "east-focuser")

		w.halt()
		w.api.deleteNamed(podsCollection, "east-telescope")
		w.moveDevice(observatory.FocuserKind, "east", "opticalTrain", "west-imaging")
		w.start()
		w.until(5*time.Minute, "east's server is not back with the focuser", func() bool {
			return slices.Contains(w.indi.connected("east-telescope"), "Focuser Simulator")
		})
		time.Sleep(time.Minute)
		synctest.Wait()

		server, _ := w.api.object(podsCollection, "east-telescope")
		after, running := w.api.object(podsCollection, "east-focuser")
		if !running || podUID(after) != podUID(before) || !slices.Contains(links(server), "east-focuser") {
			t.Errorf("east runs %v, and the focuser's pod changed or went: %v", links(server), after)
		}
		if got := w.indi.connected("west-telescope"); slices.Contains(got, "Focuser Simulator") {
			t.Errorf("west connected %q, want no focuser", got)
		}
	})
}

// westReadyWorld runs the example with the west telescope reserved and
// Ready, and the east telescope free.
func westReadyWorld(t *testing.T) *world {
	w := startWorld(t)
	w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
	w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
	return w
}

// A focuser placed in west's train while west-tonight holds the server
// does not start: it gets no pod, its driver does not start, and it
// reads Idle with a message that names the session it waits for. The
// next session of west starts it.
func TestADevicePlacedOnAHeldServerWaitsForItsNextSession(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, device, pod string
		place             func(w *world)
	}{
		{"taken off the shelf", "spare", "spare-focuser", func(w *world) {
			w.moveDevice(observatory.FocuserKind, "spare", "opticalTrain", "west-imaging")
		}},
		{"created", "extra", "extra-focuser", func(w *world) {
			w.put(observatory.FocuserKind, "extra", map[string]any{"opticalTrain": "west-imaging", "driver": map[string]any{"name": "indi_simulator_focus"}})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := westReadyWorld(t)

				c.place(w)
				time.Sleep(time.Minute)
				synctest.Wait()

				if slices.Contains(w.api.names(podsCollection), c.pod) {
					t.Errorf("pods = %q, want no %s", w.api.names(podsCollection), c.pod)
				}
				after, _ := w.api.object(podsCollection, "west-telescope")
				if slices.Contains(links(after), c.pod) || slices.Contains(w.indi.connected("west-telescope"), "Focuser Simulator") {
					t.Errorf("west runs %v and connected %q, want no focuser", links(after), w.indi.connected("west-telescope"))
				}
				focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), c.device)
				ready := conditionOf(focuser.Status.Conditions, observatory.ConditionReady)
				want := "Starts at the next reservation, after Reservation west-tonight ends"
				if focuser.Status.Phase != observatory.DeviceIdle || ready.Message != want || holdsFinalizer(focuser.Metadata) {
					t.Errorf("focuser = %s %q, finalizers %v; want Idle %q and no finalizer", focuser.Status.Phase, ready.Message, focuser.Metadata.Finalizers, want)
				}

				w.api.deleteNamed(kindCollection(observatory.ReservationKind), "west-tonight")
				w.gone(observatory.ReservationKind, "west-tonight", 10*time.Minute)
				w.reserve("west-later", map[string]any{"telescope": "west", "holder": "desktop"})
				w.phase("west-later", observatory.ReservationReady, 10*time.Minute)

				if got := w.indi.connected("west-telescope"); !slices.Contains(got, "Focuser Simulator") {
					t.Errorf("west connected %q, want the focuser", got)
				}
			})
		})
	}
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
		{name: "west Ready", world: func(t *testing.T) *world {
			w := readyWorld(t)
			w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
			w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
			return w
		}},
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
				w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

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
