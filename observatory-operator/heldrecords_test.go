package main

// What a session records about the devices it started, so an edit or
// an eviction during the session does not change them: the output each
// device draws from, and the set of devices a server runs.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The focuser draws from output 1 of Switch lab when east-tonight
// starts. An edit of its spec.power during the session takes effect
// when the session ends: the release switches off output 1, which the
// session switched on, and not the output that the spec names now. A
// focuser pod that an eviction deleted after the edit comes back with
// the same record.
func TestAReleaseSwitchesOffTheOutputThatItsSessionSwitchedOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		edit  func(w *world)
		never string
	}{
		{"another output", func(w *world) { w.powerFrom(observatory.FocuserKind, "east", "lab", 2) }, labOutput("2", "OFF")},
		{"another Switch", func(w *world) { w.powerFrom(observatory.FocuserKind, "east", "east", 4) },
			"east-telescope Simulator IO.DIGITAL_OUTPUT_4 OFF=On ON=Off"},
		{"another output, then an eviction", func(w *world) {
			w.powerFrom(observatory.FocuserKind, "east", "lab", 2)
			before, _ := w.api.object(podsCollection, "east-focuser")
			w.api.deleteNamed(podsCollection, "east-focuser")
			w.until(time.Minute, "the focuser's pod is not back", func() bool {
				p, ok := w.api.object(podsCollection, "east-focuser")
				return ok && podUID(p) != podUID(before)
			})
		}, labOutput("2", "OFF")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t, false)
				c.edit(w)
				w.settle()
				from := len(w.indi.changes())

				w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
				w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

				sent := w.indi.changes()[from:]
				if !slices.Contains(sent, labOutput("1", "OFF")) || slices.Contains(sent, c.never) {
					t.Errorf("changes = %q, want %q and not %q", sent, labOutput("1", "OFF"), c.never)
				}
			})
		})
	}
}

// A server pod that an eviction deleted during a session comes back
// with the devices whose pods carry its label, and with the devices
// that hold the finalizer, such as a camera whose pod the eviction
// deleted too. A device placed on the server during the session still
// waits for the next one. When every pod of the server is gone,
// nothing records the session's set, and the new pod takes the devices
// that the specs place there, as a new session would. The operator is
// down while the pods go, so it reads every eviction at once, as it
// would after a node fails.
func TestAServerCreatedAgainKeepsItsSessionsDevices(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		evicted []string
		starts  bool
	}{
		{"the server's pod", []string{"west-telescope"}, false},
		{"the server's pod and the camera's", []string{"west-telescope", "west-main-camera"}, false},
		{"every pod of the server", []string{"west-telescope", "west-mount", "west-main-camera"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := westReadyWorld(t)
				w.put(observatory.FocuserKind, "extra", map[string]any{"opticalTrain": "west-imaging", "driver": map[string]any{"name": "indi_simulator_focus"}})
				w.settle()
				before, _ := w.api.object(podsCollection, "west-telescope")

				w.halt()
				for _, name := range c.evicted {
					w.api.deleteNamed(podsCollection, name)
				}
				w.start()
				w.until(5*time.Minute, "west's server is not back with its devices", func() bool {
					p, ok := w.api.object(podsCollection, "west-telescope")
					connected := w.indi.connected("west-telescope")
					return ok && podUID(p) != podUID(before) && slices.Contains(connected, "CCD Simulator") && slices.Contains(connected, "Telescope Simulator")
				})
				time.Sleep(time.Minute)
				synctest.Wait()

				after, _ := w.api.object(podsCollection, "west-telescope")
				started := slices.Contains(w.api.names(podsCollection), "extra-focuser") || slices.Contains(links(after), "extra-focuser")
				if started != c.starts {
					t.Errorf("the extra focuser started = %t, want %t: west runs %v", started, c.starts, links(after))
				}
			})
		})
	}
}

// A Telescope whose spec.observatory a person edits during its session
// still belongs to the observatory that the session started with: the
// release parks the dome there and stops the observatory's server.
func TestAReleaseKeepsTheObservatoryItsSessionStartedWith(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.amend(observatory.TelescopeKind, "east", func(spec map[string]any) { spec["observatory"] = "lab2" })
		w.settle()
		from := len(w.indi.changes())

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 15*time.Minute)

		if sent := w.indi.changes()[from:]; !slices.Contains(sent, "lab-observatory Dome Simulator.DOME_PARK PARK=On UNPARK=Off") {
			t.Errorf("changes = %q, want the dome parked", sent)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release = %v, want none", pods)
		}
	})
}

// A spec edit during a session changes nothing that the session
// started: a guide camera moved to another train keeps its pod beside
// the server and its Guider stays Ready, a driver edit that names
// another device's driver leaves both devices running, and a camera
// moved to another telescope's train keeps its train's settings when
// its driver comes back, and stays in its train's status.
func TestASpecEditDuringASessionChangesNothingItStarted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		edit  func(w *world)
		check func(t *testing.T, w *world, from int, before map[string]string)
	}{
		{"a guide camera moved to another train", func(w *world) {
			w.moveDevice(observatory.CameraKind, "east-guide", "opticalTrain", "west-imaging")
		}, func(t *testing.T, w *world, _ int, before map[string]string) {
			unchanged(t, w, before, "east-guide-camera")
			guider, _ := decode[observatory.Guider](t, w.api, kindCollection(observatory.GuiderKind), "east")
			if guider.Status.Phase != observatory.PhaseReady {
				t.Errorf("the Guider is %s, want Ready", guider.Status.Phase)
			}
		}},
		{"a driver that another device runs", func(w *world) {
			w.amend(observatory.FocuserKind, "east", func(spec map[string]any) { spec["driver"] = map[string]any{"name": "indi_simulator_rotator"} })
		}, func(t *testing.T, w *world, _ int, before map[string]string) {
			unchanged(t, w, before, "east-focuser", "east-rotator")
			for _, kind := range []observatory.Kind{observatory.FocuserKind, observatory.RotatorKind} {
				d, _ := decode[deviceObject](t, w.api, kindCollection(kind), "east")
				if d.Status.Phase != observatory.DeviceConnected {
					t.Errorf("%s east is %s, want Connected", kind.Name, d.Status.Phase)
				}
			}
		}},
		{"a camera moved to another telescope's train, whose pod an eviction deleted", func(w *world) {
			w.moveDevice(observatory.CameraKind, "east-main", "opticalTrain", "west-imaging")
			w.settle()
			w.api.deleteNamed(podsCollection, "east-main-camera")
		}, func(t *testing.T, w *world, from int, _ map[string]string) {
			sent := touches(w, from, "CCD Simulator")
			if !slices.Contains(sent, "east-telescope CCD Simulator.SCOPE_INFO FOCAL_LENGTH=480 APERTURE=80") ||
				slices.ContainsFunc(sent, func(c string) bool { return strings.Contains(c, ".ACTIVE_DEVICES ") }) {
				t.Errorf("changes to the camera = %q, want east's SCOPE_INFO and no new ACTIVE_DEVICES", sent)
			}
			train, _ := decode[observatory.OpticalTrain](t, w.api, kindCollection(observatory.OpticalTrainKind), "east-imaging")
			if !slices.ContainsFunc(train.Status.Devices, func(r observatory.DeviceRef) bool { return r.Name == "east-main" }) {
				t.Errorf("east-imaging lists %+v, want the camera", train.Status.Devices)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				before := map[string]string{}
				for _, name := range w.api.names(podsCollection) {
					p, _ := w.api.object(podsCollection, name)
					before[name] = podUID(p)
				}
				from := len(w.indi.changes())

				c.edit(w)
				time.Sleep(5 * time.Minute)
				synctest.Wait()
				c.check(t, w, from, before)
			})
		})
	}
}

// unchanged fails the test when one of the pods is gone or new.
func unchanged(t *testing.T, w *world, before map[string]string, pods ...string) {
	t.Helper()
	for _, name := range pods {
		if p, ok := w.api.object(podsCollection, name); !ok || podUID(p) != before[name] {
			t.Errorf("pod %s was replaced or deleted", name)
		}
	}
}

// A Switch placed on a telescope during its session never runs there,
// also not during the release, which stops only what the session
// started. The hold on an output keeps the release in PowerOff.
func TestAReleaseStartsNoDriverThatItsSessionDidNot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.put(observatory.SwitchKind, "extra", map[string]any{"telescope": "east", "driver": map[string]any{"name": "indi_simulator_io"}})
		w.settle()
		w.indi.hold("Simulator IO", "DIGITAL_OUTPUT_1")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(5*time.Minute, "PowerOff does not run", func() bool {
			r, _ := w.reservation("east-tonight")
			return stepOf(r, observatory.StepPowerOff).State == observatory.StepRunning
		})
		if p, _ := w.api.object(podsCollection, "east-telescope"); slices.Contains(links(p), "extra-switch") {
			t.Errorf("east-telescope runs %v during the release, want no Switch extra", links(p))
		}
		w.indi.release("Simulator IO", "DIGITAL_OUTPUT_1")
	})
}
