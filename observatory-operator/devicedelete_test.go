package main

// Tests of deleting a device. When a person deletes a device while its
// server runs, the operator's finalizer keeps the object until the
// operator has run the device's deactivation and deleted its pod and
// its claim. A device with no pod has no finalizer, so a delete removes
// it immediately.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// parkCap is the change that closes the east telescope's dust cap.
const parkCap = "east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off"

// warmCamera is the change that warms the sensor of the east
// telescope's main camera from its cooled setpoint.
const warmCamera = "east-telescope CCD Simulator.CCD_TEMPERATURE CCD_TEMPERATURE_VALUE=5"

// outputsOff are the changes that switch off the outputs of the east
// Switch that power the devices of the east-imaging train.
var outputsOff = []string{
	"east-telescope Simulator IO.DIGITAL_OUTPUT_1 OFF=On ON=Off",
	"east-telescope Simulator IO.DIGITAL_OUTPUT_2 OFF=On ON=Off",
	"east-telescope Simulator IO.DIGITAL_OUTPUT_3 OFF=On ON=Off",
}

// disconnectCamera is the change that disconnects the east telescope's
// main camera in the Disconnect step.
const disconnectCamera = "east-telescope CCD Simulator.CONNECTION CONNECT=Off DISCONNECT=On"

// claimDustCap adds a claim to the east dust cap, as a dust cap on real
// hardware has, and keeps the rest of its spec.
func (w *world) claimDustCap() {
	object, _ := w.api.object(kindCollection(observatory.DustCapKind), "east")
	spec := object["spec"].(map[string]any)
	spec["claim"] = map[string]any{"devices": map[string]any{"requests": []any{map[string]any{"name": "cap", "exactly": map[string]any{"deviceClassName": "usb.liken.sh"}}}}}
	w.put(observatory.DustCapKind, "east", spec)
}

// gone waits until the resource no longer exists in the API server.
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
		if slices.Contains(w.api.names(podsCollection), "east-dustcap") || slices.Contains(w.api.names(templatesCollection), "east-dustcap") {
			t.Errorf("pods = %v, claims = %v, want neither to hold east-dustcap", w.api.names(podsCollection), w.api.names(templatesCollection))
		}
		deleted := "Normal PodDeleted: Deleted pod east-dustcap because the DustCap was deleted while Reservation east-tonight is Ready"
		if got := typedEvents(w.api, observatory.DustCapKind, "east"); !slices.Contains(got, deleted) {
			t.Errorf("dust cap Events = %q, want %q", got, deleted)
		}
	})
}

// A deactivation that fails posts a ProcedureFailed Event, and the
// delete still completes, so a broken driver cannot block a delete
// forever.
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

// A device whose pod does not run has no finalizer, so a delete
// removes it immediately. This covers a device that no Reservation has
// started, and one whose Reservation is Released.
func TestADeviceWithNoPodIsDeletedAtOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		before func(w *world)
		kind   observatory.Kind
		device string
	}{
		{"a device that no telescope uses", func(*world) {}, observatory.FocuserKind, "spare"},
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
					t.Errorf("%s %s still exists after its delete, want it gone", c.kind.Name, c.device)
				}
			})
		})
	}
}

// A device deleted while the operator is down stays until the operator
// returns, because of the finalizer. The returned operator runs its
// deactivation and then removes the finalizer.
func TestADeviceDeletedWhileTheOperatorIsDownGoesWhenItReturns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.halt()
		w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
		if _, ok := w.api.object(kindCollection(observatory.DustCapKind), "east"); !ok {
			t.Fatal("the dust cap was removed while the operator was down, want it kept by its finalizer")
		}
		w.start()
		w.gone(observatory.DustCapKind, "east", 5*time.Minute)
		if !slices.Contains(w.indi.changes(), parkCap) {
			t.Errorf("changes = %q, want %q", w.indi.changes(), parkCap)
		}
	})
}

// A device deleted during activation, before the Activation step ran
// its activation, is removed with no procedure, because its cap was
// never opened.
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

// A device that has a deactivation and no activation runs its
// deactivation when it is deleted during a session. A person turned on
// the flat panel's light, and the delete turns it off.
func TestADeletedDeviceWithNoActivationRunsItsDeactivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.ask("east-telescope", "Light Panel Simulator", "FLAT_LIGHT_CONTROL", "FLAT_LIGHT_ON=On", "FLAT_LIGHT_OFF=Off")
		w.settle()
		w.api.deleteNamed(kindCollection(observatory.FlatPanelKind), "east")
		w.gone(observatory.FlatPanelKind, "east", 5*time.Minute)
		if n := startedRuns(w.api, observatory.FlatPanelKind, "east", observatory.TriggerDeactivation); n != 1 {
			t.Errorf("the deactivation started %d times, want once", n)
		}
	})
}

// A deleted device still reports phase Connected, with its readings,
// while its deactivation runs on its old server, so a person who
// watches the delete sees the cap close. It reports Disconnecting once
// its driver stops.
func TestADeletedDeviceReadsConnectedWhileItsDeactivationRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("Dust Cover Simulator", "CAP_PARK")
		w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
		w.until(time.Minute, "the deactivation does not start", func() bool {
			return startedRuns(w.api, observatory.DustCapKind, "east", observatory.TriggerDeactivation) == 1
		})
		time.Sleep(2 * statusWindow)
		w.settle()
		object, _ := decode[map[string]any](t, w.api, kindCollection(observatory.DustCapKind), "east")
		status, _ := object["status"].(map[string]any)
		if phase := status["phase"]; phase != string(observatory.DeviceConnected) {
			t.Errorf("phase = %v, want Connected", phase)
		}
		if ready := w.stateOf(observatory.DustCapKind, "east", observatory.ConditionReady); ready != "True Connected: Connected on east-telescope" {
			t.Errorf("Ready = %q, want the server it left", ready)
		}
		if open := w.stateOf(observatory.DustCapKind, "east", observatory.ConditionOpen); !strings.HasPrefix(open, "True Open:") {
			t.Errorf("Open = %q, want True Open", open)
		}
	})
}

// A dome deleted while a mount is unparked does not park, because its
// lock refuses: the dome would close onto the mount. The failure
// message states the lock, as a refusal in a trigger does.
func TestADomeDeletedOverAnUnparkedMountNamesItsLock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.deleteNamed(kindCollection(observatory.DomeKind), "lab")
		w.gone(observatory.DomeKind, "lab", 5*time.Minute)
		failed := "Warning ProcedureFailed: Procedure deactivation failed: state: Parked: Dome lab refused to park, because Mount east is unparked or moving. In Observatory lab, a dome does not park until every mount parks"
		if got := typedEvents(w.api, observatory.DomeKind, "lab"); !slices.Contains(got, failed) {
			t.Errorf("dome Events = %q, want %q", got, failed)
		}
	})
}

// A dust cap deleted together with its Reservation, as `kubectl delete
// -f` of a whole observatory does, closes in the Reservation's
// Deactivation step before the object is removed. This holds whether
// the cap runs its own driver or a Flip-Flat serves it. The test keeps
// the StopWork step waiting on an exposure until the cap's delete
// arrives, so the Deactivation step finds the cap already deleted.
func TestADeviceDeletedWithItsReservationRunsItsDeactivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(w *world)
		close string
	}{
		{"a dust cap that runs its driver", func(*world) {}, parkCap},
		{"a dust cap that a Flip-Flat serves", func(w *world) { w.flipFlat() }, closeFlipFlat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.setup(w)
				readyOf(w)
				w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Busy")
				w.indi.hold("CCD Simulator", "CCD_ABORT_EXPOSURE")
				w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
				w.until(time.Minute, "the StopWork step does not abort the exposure", func() bool {
					return w.indi.count("east-telescope", "CCD Simulator.CCD_ABORT_EXPOSURE") > 0
				})
				w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
				time.Sleep(time.Minute)
				synctest.Wait()
				w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Ok")
				w.gone(observatory.DustCapKind, "east", 10*time.Minute)
				if !slices.Contains(w.indi.changes(), c.close) {
					t.Errorf("changes = %q\nwant %q before the dust cap went", w.indi.changes(), c.close)
				}
				if n := startedRuns(w.api, observatory.DustCapKind, "east", observatory.TriggerDeactivation); n != 1 {
					t.Errorf("the deactivation started %d times, want once", n)
				}
			})
		})
	}
}

// named identifies one resource by its kind and name.
type named struct {
	kind observatory.Kind
	name string
}

// deleteDuringAbort deletes the east Reservation, then deletes the
// resources while an exposure keeps the StopWork step waiting. The
// Deactivation step then finds them deleted, as it does when a person
// deletes them with the Reservation in one command and the operator
// reads the Reservation's delete first.
func (w *world) deleteDuringAbort(resources ...named) {
	w.t.Helper()
	w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Busy")
	w.indi.hold("CCD Simulator", "CCD_ABORT_EXPOSURE")
	w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
	w.until(time.Minute, "the StopWork step does not abort the exposure", func() bool {
		return w.indi.count("east-telescope", "CCD Simulator.CCD_ABORT_EXPOSURE") > 0
	})
	for _, r := range resources {
		w.api.deleteNamed(kindCollection(r.kind), r.name)
	}
	time.Sleep(time.Minute)
	synctest.Wait()
	w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Ok")
}

// When a Reservation is deleted together with a device or the resource
// that places it, as `kubectl delete -f` of the whole inventory does,
// the Reservation's Deactivation step still runs the device's
// deactivation. The steps find each device by the server that its pod
// runs on, so a deleted train does not take its cap, panel, or camera
// out of the steps, whether or not those are deleted too. The Switch
// switches off the outputs of the train's devices even when the Switch
// itself is deleted, and a deleted dome is parked and disconnected
// before its server stops.
func TestADeviceDeletedWithItsPlaceRunsItsDeactivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		deletes []named
		runs    []named
		changes []string
	}{
		{"a dust cap and a camera with their train",
			[]named{{observatory.OpticalTrainKind, "east-imaging"}, {observatory.DustCapKind, "east"}, {observatory.CameraKind, "east-main"}},
			[]named{{observatory.DustCapKind, "east"}, {observatory.CameraKind, "east-main"}}, append([]string{parkCap, warmCamera, disconnectCamera}, outputsOff...)},
		{"a train whose camera and panel stay",
			[]named{{observatory.OpticalTrainKind, "east-imaging"}, {observatory.DustCapKind, "east"}},
			[]named{{observatory.DustCapKind, "east"}, {observatory.CameraKind, "east-main"}, {observatory.FlatPanelKind, "east"}}, append([]string{parkCap, warmCamera, disconnectCamera}, outputsOff...)},
		{"a Switch whose telescope stays",
			[]named{{observatory.SwitchKind, "east"}},
			nil, outputsOff},
		{"a dome whose observatory stays",
			[]named{{observatory.DomeKind, "lab"}},
			[]named{{observatory.DomeKind, "lab"}}, []string{"lab-observatory Dome Simulator.DOME_PARK PARK=On UNPARK=Off", "lab-observatory Dome Simulator.CONNECTION CONNECT=Off DISCONNECT=On"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				readyOf(w)
				w.deleteDuringAbort(c.deletes...)
				for _, d := range c.deletes {
					w.gone(d.kind, d.name, 10*time.Minute)
				}
				w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
				for _, change := range c.changes {
					if !slices.Contains(w.indi.changes(), change) {
						t.Errorf("changes = %q\nwant %q", w.indi.changes(), change)
					}
				}
				for _, d := range c.runs {
					if n := startedRuns(w.api, d.kind, d.name, observatory.TriggerDeactivation); n != 1 {
						t.Errorf("the deactivation of %s %s started %d times, want once", d.kind.Name, d.name, n)
					}
				}
			})
		})
	}
}
