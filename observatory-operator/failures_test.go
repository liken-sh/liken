package main

// How a reservation's steps fail, and how the operator recovers from a
// write that the API server refuses and from a device that comes back.

import (
	"slices"
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
				w.put(observatory.FocuserKind, "east", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_fishcamp_ccd"}})
			},
			step:    observatory.StepStartDevices,
			message: "Focuser east: indi_fishcamp_ccd: no image of the indi build includes this driver",
		},
		{
			name: "a Switch that does not exist",
			change: func(w *world) {
				w.put(observatory.RotatorKind, "east", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_rotator"}, "power": map[string]any{"switch": "nowhere", "output": 1}})
			},
			step:    observatory.StepPowerOn,
			message: "missing Switch nowhere, which spec.power names",
		},
		{
			name: "a Switch that is not installed",
			change: func(w *world) {
				w.put(observatory.SwitchKind, "spare", map[string]any{"driver": map[string]any{"name": "indi_simulator_io"}})
				w.put(observatory.RotatorKind, "east", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_rotator"}, "power": map[string]any{"switch": "spare", "output": 1}})
			},
			step:    observatory.StepPowerOn,
			message: "the Switch spare, which spec.power names, is not installed: set its spec.telescope or spec.observatory",
		},
		{
			name: "a Switch output that does not exist",
			change: func(w *world) {
				w.put(observatory.RotatorKind, "east", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_rotator"}, "power": map[string]any{"switch": "east", "output": 9}})
			},
			step:    observatory.StepPowerOn,
			message: "no DIGITAL_OUTPUT_9 on Switch east",
		},
		{
			name: "a telescope in an observatory that does not exist",
			change: func(w *world) {
				w.put(observatory.TelescopeKind, "east", map[string]any{"observatory": "elsewhere"})
			},
			step:    observatory.StepStartSite,
			message: "missing Observatory elsewhere of Telescope east",
		},
		{
			name: "a name that is no Service name",
			change: func(w *world) {
				w.put(observatory.ReceiverKind, "east.radio", map[string]any{"telescope": "east", "driver": map[string]any{"name": "indi_simulator_receiver"}})
				w.api.deleteNamed(kindCollection(observatory.ReceiverKind), "east")
			},
			step:    observatory.StepPowerOn,
			message: `Receiver east.radio: the pod and Service name "east.radio-receiver" is not a DNS label`,
		},
		{
			name: "two devices of the telescope with one driver",
			change: func(w *world) {
				w.put(observatory.FocuserKind, "guide", map[string]any{"opticalTrain": "east-guiding", "driver": map[string]any{"name": "indi_simulator_focus"}})
			},
			step:    observatory.StepPowerOn,
			message: "Focuser east and Focuser guide share driver indi_simulator_focus on server east-telescope: INDI gives both devices the same name",
		},
		{
			name: "two devices of the observatory with one driver",
			change: func(w *world) {
				w.put(observatory.WeatherStationKind, "roof", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_weather"}})
			},
			step:    observatory.StepStartSite,
			message: "WeatherStation lab and WeatherStation roof share driver indi_simulator_weather on server lab-observatory: INDI gives both devices the same name",
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
				if step.State != observatory.StepFailed || !strings.Contains(step.Summary, c.message) {
					t.Errorf("%s = %+v, want Failed with %q", c.step, step, c.message)
				}
			})
		})
	}
}

// A telescope with no devices gives the INDI server no driver to run.
func TestATelescopeWithNoDevicesFailsPowerOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.TelescopeKind, "south", map[string]any{"observatory": "lab"})
		w.reserve("south-tonight", map[string]any{"telescope": "south", "holder": "desktop"})
		r := w.phase("south-tonight", observatory.ReservationFailed, 15*time.Minute)
		if step := stepOf(r, observatory.StepPowerOn); step.Summary != "Failed: no devices on Telescope south" {
			t.Errorf("the PowerOn step is %+v, want Summary \"Failed: no devices on Telescope south\"", step)
		}
	})
}

// When a driver lacks a property that a setting needs, the step notes
// it, skips that setting, and continues.
func TestADriverThatLacksAPropertyIsNotedAndSkipped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.CameraKind, "east-guide", map[string]any{"opticalTrain": "east-guiding", "driver": map[string]any{"name": "indi_simulator_guide"}, "activation": []any{map[string]any{"cool": map[string]any{"celsius": -5}}}, "offset": 3})
		w.put(observatory.FilterWheelKind, "east", map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "Camera", "name": "east-main"},
			"filters": []any{"L", "R", "G", "B", "Ha", "OIII", "SII", "Dark", "Spare"}})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)
		notes := map[observatory.StepName][]string{
			observatory.StepConfigure:  {"no CCD_OFFSET on Camera east-guide", "no member for 1 of the values of FILTER_NAME on FilterWheel east (8 members)"},
			observatory.StepActivation: {"Camera east-guide cool: -5 °C within 0.5 °C Skipped: No cooler on Camera east-guide: CCD_TEMPERATURE is read-only"},
		}
		for name, want := range notes {
			for _, note := range want {
				if message := stepText(stepOf(r, name)); !strings.Contains(message, note) {
					t.Errorf("the %s step text is %q, want it to contain %q", name, message, note)
				}
			}
		}
	})
}

// StopWork ends an exposure and a slew, and sends nothing to a device
// that is idle.
func TestStopWorkAbortsWhatMoves(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Busy")
		w.indi.setState("east-telescope", "Telescope Simulator", "EQUATORIAL_EOD_COORD", "Busy")
		synctest.Wait()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		for _, property := range []string{"CCD Simulator.CCD_ABORT_EXPOSURE", "Telescope Simulator.TELESCOPE_ABORT_MOTION"} {
			if n := w.indi.count("east-telescope", property); n != 1 {
				t.Errorf("%s received %d changes, want 1", property, n)
			}
		}
		if n := w.indi.count("east-telescope", "Guide Simulator.CCD_ABORT_EXPOSURE"); n != 0 {
			t.Errorf("the idle guide camera received %d aborts, want 0", n)
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
		if step := stepOf(r, observatory.StepStopSite); step.State != observatory.StepSkipped || step.Summary != "In use by Reservation west-tonight" {
			t.Errorf("the StopSite step is %+v, want Skipped with Summary \"In use by Reservation west-tonight\"", step)
		}
		if _, ok := w.api.object(podsCollection, "lab-observatory"); !ok {
			t.Error("the lab-observatory pod is gone, want it kept while west-tonight holds its telescope")
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "west-tonight")
		w.until(10*time.Minute, "the observatory's server runs", func() bool {
			_, ok := w.api.object(podsCollection, "lab-observatory")
			return !ok
		})
	})
}

// When a person removes a Reservation's finalizer, the API server
// deletes it at once, and the operator deletes the pods and Services
// that the Reservation started.
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

// A create or a delete that the API server refuses is retried, and the
// step continues when a retry succeeds.
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

// Stopping the operator ends the retries of a refused write at once,
// without waiting for the delay before the next retry. Here the runner
// of a Ready reservation creates the focuser's pod again, and the API
// server refuses the pod's ResourceClaimTemplate.
func TestAStopEndsARefusedWriteAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimFocuser()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.api.refuse(1000)
		tries := w.api.creates(templatesCollection)
		w.api.deleteNamed(podsCollection, "east-focuser")
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if w.api.creates(templatesCollection) == tries {
			t.Fatal("the runner created no new claim after the pod was deleted, want one")
		}
		stopped := time.Now()
		w.halt()
		if waited := time.Since(stopped); waited != 0 {
			t.Errorf("halting waited %v, want no wait", waited)
		}
	})
}

// A device that sets a property to Alert after a change fails the step
// that sent the change, and the step's summary names the device and the
// property.
func TestADeviceThatRefusesAChangeFailsItsStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		device, property string
		step             observatory.StepName
		// ready makes the device refuse the change only after the
		// reservation is Ready, and then deletes the reservation, so the
		// refusal fails a step of the teardown.
		ready bool
	}{
		{"Weather Simulator", "CONNECTION", observatory.StepStartSite, false},
		{"Dome Simulator", "DOME_SHUTTER_PARK_POLICY", observatory.StepStartSite, false},
		{"Simulator IO", "CONNECTION", observatory.StepPowerOn, false},
		{"Simulator IO", "DIGITAL_OUTPUT_2", observatory.StepPowerOn, false},
		{"Telescope Simulator", "CONNECTION", observatory.StepConnect, false},
		{"Telescope Simulator", "GEOGRAPHIC_COORD", observatory.StepConfigure, false},
		{"GPS Simulator", "GEOGRAPHIC_COORD", observatory.StepConfigure, false},
		{"CCD Simulator", "FILTER_NAME", observatory.StepConfigure, false},
		{"Guide Simulator", "ACTIVE_DEVICES", observatory.StepConfigure, false},
		{"CCD Simulator", "CCD_GAIN", observatory.StepConfigure, false},
		{"CCD Simulator", "CCD_OFFSET", observatory.StepConfigure, false},
		{"CCD Simulator", "SCOPE_INFO", observatory.StepConfigure, false},
		{"CCD Simulator", "CCD_TEMPERATURE", observatory.StepActivation, false},
		{"Telescope Simulator", "TELESCOPE_PARK", observatory.StepDeactivation, true},
		{"Dust Cover Simulator", "CAP_PARK", observatory.StepDeactivation, true},
		{"Dome Simulator", "DOME_PARK", observatory.StepDeactivation, true},
		{"CCD Simulator", "CONNECTION", observatory.StepDisconnect, true},
		{"Simulator IO", "DIGITAL_OUTPUT_1", observatory.StepPowerOff, true},
		{"Simulator IO", "CONNECTION", observatory.StepPowerOff, true},
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
				if step.State != observatory.StepFailed || !strings.Contains(step.Summary, want) {
					t.Errorf("%s = %+v, want Failed with %q", c.step, step, want)
				}
				if c.ready {
					if safe := conditionOf(r.Status.Conditions, observatory.ConditionSafeToPowerOff); safe.Status != observatory.ConditionFalse || !strings.Contains(safe.Message, string(c.step)) {
						t.Errorf("the SafeToPowerOff condition is %+v, want False with a message that names %s", safe, c.step)
					}
				}
			})
		})
	}
}

// A camera whose exposure does not abort fails StopWork at the step's
// deadline.
func TestAnExposureThatDoesNotAbortFailsStopWork(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Busy")
		w.indi.hold("CCD Simulator", "CCD_ABORT_EXPOSURE")
		synctest.Wait()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		r := w.phase("east-tonight", observatory.ReservationFailed, 10*time.Minute)
		if step := stepOf(r, observatory.StepStopWork); step.State != observatory.StepFailed || step.Summary != "Timed out after 2 min: aborting Camera east-main" {
			t.Errorf("the StopWork step is %+v, want Failed with Summary \"Timed out after 2 min: aborting Camera east-main\"", step)
		}
	})
}

// A status write or an Event that the API server refuses is not lost.
// The next status write sends the same fields again, and the recorder sends
// the Event again.
func TestRefusedStatusWritesAndEventsAreWrittenLater(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.mu.Lock()
		w.api.statusRefusals = 40
		w.api.mu.Unlock()
		// The recorder sends each Event up to three times, so two
		// refusals delay the first Event and lose none.
		w.api.recorded.Refuse(2)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		if camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main"); camera.Status.Phase != observatory.DeviceConnected {
			t.Errorf("the Camera phase is %q, want %q", camera.Status.Phase, observatory.DeviceConnected)
		}
		// The runner's status writes can use up every refusal above.
		// While the reservation is Ready, only the status writer writes,
		// so this refusal reaches it.
		w.api.mu.Lock()
		w.api.statusRefusals = 1
		w.api.mu.Unlock()
		w.indi.report("CCD Simulator", "CCD_TEMPERATURE", "CCD_TEMPERATURE_VALUE", "-12")
		time.Sleep(3 * statusWindow)
		synctest.Wait()
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		if s := camera.Status.Temperature; s == nil || s.Value == nil || *s.Value != -12 {
			t.Errorf("temperature = %s after a refused write, want -12", mustJSON(s))
		}
		// The recorder sends a refused Event again after 10 s.
		time.Sleep(time.Minute)
		synctest.Wait()
		reasons := w.api.eventReasons()
		if !slices.Contains(reasons, string(observatory.StepWait)) || !slices.Contains(reasons, string(observatory.ReservationReady)) || strings.Contains(w.logs.String(), "Event") {
			t.Errorf("Events %q, and the log says:\n%s\nwant every Event written and no Event lost", reasons, w.logs.String())
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
		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.until(time.Minute, "the camera shows no fault", func() bool {
			camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
			ready := conditionOf(camera.Status.Conditions, observatory.ConditionReady)
			return camera.Status.Phase == observatory.DeviceError && strings.Contains(ready.Message, "CCD Simulator.CCD_GAIN is Alert")
		})
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("the Reservation phase is %s, want %s", r.Status.Phase, observatory.ReservationReady)
		}
	})
}

// A device whose setup failed when it came back clears the fault when
// it comes back again and its setup succeeds.
func TestADeviceThatComesBackAgainClearsItsFault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.refuse("CCD Simulator", "CCD_GAIN")
		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.until(time.Minute, "the camera shows no fault", func() bool {
			camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
			return camera.Status.Phase == observatory.DeviceError
		})
		w.indi.accept("CCD Simulator", "CCD_GAIN")
		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.until(time.Minute, "the camera keeps its fault", func() bool {
			camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
			ready := conditionOf(camera.Status.Conditions, observatory.ConditionReady)
			return camera.Status.Phase == observatory.DeviceConnected && ready.Status == observatory.ConditionTrue
		})
	})
}

// A reservation whose end passed before it got its telescope ends at
// once, and does not delay the reservations that wait.
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
			t.Errorf("the Wait step is %+v, want state %s", wait, observatory.StepSkipped)
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.phase("later", observatory.ReservationReady, 20*time.Minute)
	})
}
