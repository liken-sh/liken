package main

// A device that a person deletes while its server runs keeps its
// object, through the operator's finalizer, until the operator ran its
// deactivation and deleted its pod and its claim. A device with no pod
// carries no finalizer, so its delete is instant.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// parkCap is the change that closes the east telescope's dust cap.
const parkCap = "east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off"

// claimDustCap gives the east dust cap a claim, as a dust cap on real
// hardware states one, and keeps the rest of its spec.
func (w *world) claimDustCap() {
	object, _ := w.api.object(kindCollection(observatory.DustCapKind), "east")
	spec := object["spec"].(map[string]any)
	spec["claim"] = map[string]any{"devices": map[string]any{"requests": []any{map[string]any{"name": "cap", "exactly": map[string]any{"deviceClassName": "usb.liken.sh"}}}}}
	w.put(observatory.DustCapKind, "east", spec)
}

// gone waits until a resource is gone from the API server.
func (w *world) gone(kind observatory.Kind, name string, limit time.Duration) {
	w.t.Helper()
	w.until(limit, kind.Name+" "+name+" stays", func() bool {
		_, ok := w.api.object(kindCollection(kind), name)
		return !ok
	})
}

func TestADeviceDeletedDuringASessionRunsItsDeactivationAndGoes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimDustCap()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
		w.gone(observatory.DustCapKind, "east", 5*time.Minute)
		if !slices.Contains(w.indi.changes(), parkCap) {
			t.Errorf("changes = %q, want %q", w.indi.changes(), parkCap)
		}
		if n := startedRuns(w.api, observatory.DustCapKind, "east", observatory.TriggerDeactivation); n != 1 {
			t.Errorf("the deactivation started %d times, want once", n)
		}
		if slices.Contains(w.api.names(podsCollection), "east-dustcap") || slices.Contains(w.api.names(claimsCollection), "east-dustcap") {
			t.Errorf("pods = %v, claims = %v, want neither to hold east-dustcap", w.api.names(podsCollection), w.api.names(claimsCollection))
		}
		deleted := "Normal PodDeleted: Deleted pod east-dustcap, because the DustCap was deleted while Reservation east-tonight is Ready."
		if got := typedEvents(w.api, observatory.DustCapKind, "east"); !slices.Contains(got, deleted) {
			t.Errorf("dust cap Events = %q, want %q", got, deleted)
		}
	})
}

// A deactivation that fails posts ProcedureFailed, and the delete goes
// on, so a broken driver cannot hold a delete forever.
func TestADeletedDevicesFailedDeactivationStillLetsItGo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.refuse("Dust Cover Simulator", "CAP_PARK")
		w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
		w.gone(observatory.DustCapKind, "east", 5*time.Minute)
		failed := "Warning ProcedureFailed: Procedure deactivation failed: state: Closed: DustCap east: indi: Dust Cover Simulator.CAP_PARK is Alert"
		if got := typedEvents(w.api, observatory.DustCapKind, "east"); !slices.Contains(got, failed) {
			t.Errorf("dust cap Events = %q, want %q", got, failed)
		}
		if slices.Contains(w.api.names(podsCollection), "east-dustcap") {
			t.Errorf("pods = %v, want no east-dustcap", w.api.names(podsCollection))
		}
	})
}

// A device whose pod does not run carries no finalizer, so a delete
// removes it at once: one in the inventory, and one whose reservation
// was Released.
func TestADeviceWithNoPodIsDeletedAtOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		before func(w *world)
		kind   observatory.Kind
		device string
	}{
		{"a device on the shelf", func(*world) {}, observatory.FocuserKind, "spare"},
		{"a device of a telescope with no reservation", func(*world) {}, observatory.MountKind, "west"},
		{"a device after its session", func(w *world) {
			end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
			w.phase("east-tonight", observatory.ReservationReleased, 2*time.Hour)
			w.until(time.Minute, "the dust cap keeps its finalizer", func() bool {
				object, _ := decode[deviceObject](t, w.api, kindCollection(observatory.DustCapKind), "east")
				return len(object.Metadata.Finalizers) == 0
			})
		}, observatory.DustCapKind, "east"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.before(w)
				w.api.deleteNamed(kindCollection(c.kind), c.device)
				if _, ok := w.api.object(kindCollection(c.kind), c.device); ok {
					t.Errorf("%s %s stays after its delete", c.kind.Name, c.device)
				}
			})
		})
	}
}

// The finalizer holds a device deleted while the operator is down, and
// the operator that returns runs its deactivation and lets it go.
func TestADeviceDeletedWhileTheOperatorIsDownGoesWhenItReturns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.halt()
		w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
		if _, ok := w.api.object(kindCollection(observatory.DustCapKind), "east"); !ok {
			t.Fatal("the dust cap went while the operator was down")
		}
		w.start()
		w.gone(observatory.DustCapKind, "east", 5*time.Minute)
		if !slices.Contains(w.indi.changes(), parkCap) {
			t.Errorf("changes = %q, want %q", w.indi.changes(), parkCap)
		}
	})
}

// A device deleted during activation, before the Activation step ran
// its activation, goes with no procedure: its cap was never opened.
func TestADeviceDeletedBeforeItsActivationGoesWithNoProcedure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("east-main-camera")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(10*time.Minute, "the dust cap's pod does not start", func() bool {
			return slices.Contains(w.api.names(podsCollection), "east-dustcap")
		})
		w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
		w.api.setPodReady("east-main-camera")
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.gone(observatory.DustCapKind, "east", 5*time.Minute)
		for _, trigger := range []string{observatory.TriggerActivation, observatory.TriggerDeactivation} {
			if n := startedRuns(w.api, observatory.DustCapKind, "east", trigger); n != 0 {
				t.Errorf("the %s started %d times, want none", trigger, n)
			}
		}
		if slices.Contains(w.api.names(podsCollection), "east-dustcap") {
			t.Errorf("pods = %v, want no east-dustcap", w.api.names(podsCollection))
		}
	})
}
