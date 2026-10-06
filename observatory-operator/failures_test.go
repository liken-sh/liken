package main

// The reservations that cannot activate as the inventory stands, and
// the step that names the reason.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// put writes one resource of the example's namespace.
func (w *world) put(kind observatory.Kind, name string, spec map[string]any) {
	w.api.put(kindCollection(kind), map[string]any{
		"apiVersion": observatory.APIVersion, "kind": kind.Name,
		"metadata": map[string]any{"name": name}, "spec": spec,
	})
}

func TestAnInventoryThatCannotRunFailsTheStepThatNeedsIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		change  func(w *world)
		step    observatory.StepName
		message string
	}{
		{
			name: "a driver in no image",
			change: func(w *world) {
				w.put(observatory.FocuserKind, "east-focuser", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_fishcamp_ccd"}})
			},
			step:    observatory.StepStartDevices,
			message: "Focuser east-focuser: indi_fishcamp_ccd: no image of the indi build holds this driver",
		},
		{
			name: "a Switch that does not exist",
			change: func(w *world) {
				w.put(observatory.RotatorKind, "east-rotator", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_rotator"}, "power": map[string]any{"switch": "nowhere", "output": 1}})
			},
			step:    observatory.StepPowerOn,
			message: "a device names the Switch nowhere in spec.power, and no Switch of that name exists",
		},
		{
			name: "a Switch output that does not exist",
			change: func(w *world) {
				w.put(observatory.RotatorKind, "east-rotator", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_rotator"}, "power": map[string]any{"switch": "east-power", "output": 9}})
			},
			step:    observatory.StepPowerOn,
			message: "Switch east-power defines no DIGITAL_OUTPUT_9",
		},
		{
			name: "two devices that INDI names alike on one server",
			change: func(w *world) {
				w.put(observatory.CameraKind, "east-guide", map[string]any{"opticalTrain": "east-guiding", "driver": map[string]any{"name": "indi_simulator_ccd"}})
			},
			step:    observatory.StepStartDevices,
			message: "another device on the same server runs the same driver",
		},
		{
			name: "a telescope in an observatory that does not exist",
			change: func(w *world) {
				w.put(observatory.TelescopeKind, "east", map[string]any{"observatory": "elsewhere"})
			},
			step:    observatory.StepStartSite,
			message: "the Observatory elsewhere of the Telescope east does not exist",
		},
		{
			name: "a name that is no Service name",
			change: func(w *world) {
				w.put(observatory.ReceiverKind, "east.radio", map[string]any{"telescope": "east", "driver": map[string]any{"name": "indi_simulator_receiver"}})
				w.api.deleteNamed(kindCollection(observatory.ReceiverKind), "east-radio")
			},
			step:    observatory.StepPowerOn,
			message: "is not a DNS label",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.change(w)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				r := w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
				step := stepOf(r, c.step)
				if step.State != observatory.StepFailed || !strings.Contains(step.Message, c.message) {
					t.Errorf("%s = %+v, want Failed with %q", c.step, step, c.message)
				}
			})
		})
	}
}

// A telescope with no devices has nothing for a server to run.
func TestATelescopeWithNoDevicesFailsPowerOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.TelescopeKind, "south", map[string]any{"observatory": "lab"})
		w.reserve("south-tonight", map[string]any{"telescope": "south", "holder": "desktop"})
		r := w.phase("south-tonight", observatory.ReservationFailed, 15*time.Minute)
		if step := stepOf(r, observatory.StepPowerOn); step.Message != "the Telescope south has no devices" {
			t.Errorf("PowerOn = %+v", step)
		}
	})
}

// The notes of a step name what a driver lacks, and the step goes on.
func TestADriverThatLacksAPropertyIsNotedAndSkipped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.CameraKind, "east-guide", map[string]any{"opticalTrain": "east-guiding", "driver": map[string]any{"name": "indi_simulator_guide"}, "temperature": -5, "offset": 3})
		w.put(observatory.FilterWheelKind, "east-wheel", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_wheel"},
			"filters": []any{"L", "R", "G", "B", "Ha", "OIII", "SII", "Dark", "Spare"}})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)
		notes := map[observatory.StepName][]string{
			observatory.StepConfigure: {"Camera east-guide defines no CCD_OFFSET", "FilterWheel east-wheel has 8 slots, so 1 filters have no slot"},
			observatory.StepPrepare:   {"Camera east-guide has no cooler: its driver's CCD_TEMPERATURE is read-only"},
		}
		for name, want := range notes {
			for _, note := range want {
				if message := stepOf(r, name).Message; !strings.Contains(message, note) {
					t.Errorf("%s: %q does not note %q", name, message, note)
				}
			}
		}
	})
}

// Abort ends an exposure and a slew, and sends nothing to a device that
// is idle.
func TestAbortStopsWhatMoves(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.setState("telescope-east", "CCD Simulator", "CCD_EXPOSURE", "Busy")
		w.indi.setState("telescope-east", "Telescope Simulator", "EQUATORIAL_EOD_COORD", "Busy")
		synctest.Wait()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		for _, property := range []string{"CCD Simulator.CCD_ABORT_EXPOSURE", "Telescope Simulator.TELESCOPE_ABORT_MOTION"} {
			if n := w.indi.count("telescope-east", property); n != 1 {
				t.Errorf("%s received %d changes, want 1", property, n)
			}
		}
		if n := w.indi.count("telescope-east", "Guide Simulator.CCD_ABORT_EXPOSURE"); n != 0 {
			t.Errorf("the idle guide camera received %d aborts", n)
		}
	})
}

// Two telescopes in one observatory share its server, which stops
// after the last reservation ends.
func TestTheObservatorysServerStopsAfterTheLastReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "session"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
		end := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		if step := stepOf(r, observatory.StepStopSite); step.State != observatory.StepSkipped || step.Message != "the server of the Observatory lab stays up for the Reservation west-tonight" {
			t.Errorf("StopSite = %+v", step)
		}
		if _, ok := w.api.object(podsCollection, "observatory-lab"); !ok {
			t.Error("the observatory's server stopped while west holds its telescope")
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "west-tonight")
		w.until(10*time.Minute, "the observatory's server runs", func() bool {
			_, ok := w.api.object(podsCollection, "observatory-lab")
			return !ok
		})
	})
}

// A reservation whose finalizer a person removed goes at once, and the
// operator stops the pods that it left.
func TestThePodsOfAReservationThatVanishedAreStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		object, _ := w.api.object(kindCollection(observatory.ReservationKind), "east-tonight")
		delete(object["metadata"].(map[string]any), "finalizers")
		w.api.mu.Lock()
		w.api.store(kindCollection(observatory.ReservationKind), "east-tonight", object, "DELETED")
		w.api.mu.Unlock()
		w.until(time.Minute, "pods stay", func() bool { return len(w.api.names(podsCollection)) == 0 })
		w.until(time.Minute, "Services stay", func() bool { return len(w.api.names(servicesCollection)) == 0 })
	})
}

// A create or a delete that the API server refuses is tried again, and
// the step goes on when the API server answers.
func TestARefusedWriteIsTriedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.refuse(5)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.api.refuse(3)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
	})
}

// A device that answers a change with Alert fails the step that sent
// the change, and the step's message names the device.
func TestADeviceThatRefusesAChangeFailsItsStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		device, property string
		step             observatory.StepName
		// ready refuses the change after activation, and ends the
		// reservation, so a deactivation step meets the refusal.
		ready bool
	}{
		{"Weather Simulator", "CONNECTION", observatory.StepStartSite, false},
		{"Dome Simulator", "DOME_SHUTTER_PARK_POLICY", observatory.StepStartSite, false},
		{"Simulator IO", "CONNECTION", observatory.StepPowerOn, false},
		{"Simulator IO", "DIGITAL_OUTPUT_2", observatory.StepPowerOn, false},
		{"Telescope Simulator", "CONNECTION", observatory.StepConnect, false},
		{"Telescope Simulator", "GEOGRAPHIC_COORD", observatory.StepConfigure, false},
		{"GPS Simulator", "GEOGRAPHIC_COORD", observatory.StepConfigure, false},
		{"Filter Simulator", "FILTER_NAME", observatory.StepConfigure, false},
		{"CCD Simulator", "ACTIVE_DEVICES", observatory.StepConfigure, false},
		{"CCD Simulator", "CCD_GAIN", observatory.StepConfigure, false},
		{"CCD Simulator", "CCD_OFFSET", observatory.StepConfigure, false},
		{"CCD Simulator", "SCOPE_INFO", observatory.StepConfigure, false},
		{"CCD Simulator", "CCD_TEMPERATURE", observatory.StepPrepare, false},
		{"Telescope Simulator", "TELESCOPE_PARK", observatory.StepSecure, true},
		{"Dust Cover Simulator", "CAP_PARK", observatory.StepSecure, true},
		{"CCD Simulator", "CONNECTION", observatory.StepDisconnect, true},
		{"Simulator IO", "DIGITAL_OUTPUT_1", observatory.StepPowerOff, true},
		{"Simulator IO", "CONNECTION", observatory.StepPowerOff, true},
		{"Dome Simulator", "DOME_PARK", observatory.StepStopSite, true},
		{"Weather Simulator", "CONNECTION", observatory.StepStopSite, true},
	}
	for _, c := range cases {
		t.Run(string(c.step)+"/"+c.device+"/"+c.property, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				if !c.ready {
					w.indi.refuse(c.device, c.property)
				}
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				if c.ready {
					w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
					w.indi.refuse(c.device, c.property)
					w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
				}
				r := w.phase("east-tonight", observatory.ReservationFailed, 30*time.Minute)
				step := stepOf(r, c.step)
				want := c.device + "." + c.property + " is Alert"
				if step.State != observatory.StepFailed || !strings.Contains(step.Message, want) {
					t.Errorf("%s = %+v, want Failed with %q", c.step, step, want)
				}
				if c.ready {
					if safe := conditionOf(r.Status.Conditions, observatory.ConditionSafeToPowerOff); safe.Status != observatory.ConditionFalse || !strings.Contains(safe.Message, string(c.step)) {
						t.Errorf("SafeToPowerOff = %+v", safe)
					}
				}
			})
		})
	}
}

// A camera whose exposure does not end when Abort asks fails Abort at
// its deadline.
func TestAnExposureThatDoesNotAbortFailsAbort(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.setState("telescope-east", "CCD Simulator", "CCD_EXPOSURE", "Busy")
		w.indi.hold("CCD Simulator", "CCD_ABORT_EXPOSURE")
		synctest.Wait()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		r := w.phase("east-tonight", observatory.ReservationFailed, 10*time.Minute)
		if step := stepOf(r, observatory.StepAbort); step.State != observatory.StepFailed || step.Message != "timed out after 2m0s: aborting Camera east-main" {
			t.Errorf("Abort = %+v", step)
		}
	})
}

// A status write or an Event that the API server refuses costs nothing
// but a log line: the next write carries the same facts.
func TestRefusedStatusWritesAndEventsAreWrittenLater(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.mu.Lock()
		w.api.statusRefusals, w.api.eventRefusals = 40, 3
		w.api.mu.Unlock()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		if camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main"); camera.Status.Phase != observatory.DeviceConnected {
			t.Errorf("the camera is %q", camera.Status.Phase)
		}
	})
}

// A device that comes back and refuses its settings keeps the
// reservation Ready, and its status shows the failure.
func TestADeviceThatComesBackAndRefusesItsSettingsShowsTheFault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.refuse("CCD Simulator", "CCD_GAIN")
		w.api.deleteNamed(podsCollection, "camera-east-main")
		w.until(time.Minute, "the camera shows no fault", func() bool {
			camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
			ready := conditionOf(camera.Status.Conditions, observatory.ConditionReady)
			return camera.Status.Phase == observatory.DeviceError && strings.Contains(ready.Message, "CCD Simulator.CCD_GAIN is Alert")
		})
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("the reservation is %s", r.Status.Phase)
		}
	})
}

// A reservation whose end passed before it took its telescope ends at
// once, and does not hold up the reservations that wait.
func TestAReservationThatEndedBeforeItsStartGoesAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		past := time.Now().Add(-time.Hour).UTC()
		w.reserve("yesterday", map[string]any{"telescope": "east", "holder": "a", "start": past.Add(-time.Hour).Format(time.RFC3339), "end": past.Format(time.RFC3339)})
		w.reserve("later", map[string]any{"telescope": "east", "holder": "b"})
		r := w.phase("yesterday", observatory.ReservationReleased, time.Minute)
		if wait := stepOf(r, observatory.StepWait); wait.State != observatory.StepSkipped {
			t.Errorf("Wait = %+v", wait)
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.phase("later", observatory.ReservationReady, 20*time.Minute)
	})
}
