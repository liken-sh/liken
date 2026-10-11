package main

// One INDI device serves several resources: the example's filter wheel
// is the main camera's, and a Flip-Flat is a dust cap and a flat panel
// in one driver (fakevariants_test.go). The served resource acts
// through its owner's device, starts with its owner, and stops before
// it.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The CCD simulator serves INDI's filter interface beside its own, so
// the example's FilterWheel names the main camera and runs no driver.
// The camera's ACTIVE_FILTER then names the camera itself, which is
// how INDI's camera reads the filter of its own wheel. The fake camera
// starts naming another device, so the name it ends with is the one
// the operator wrote.
func TestAFilterWheelThatACameraServesIsReady(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.presetText("CCD Simulator", "ACTIVE_DEVICES", "ACTIVE_FILTER", "Filter Simulator")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()

		wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		endpoint := observatory.Endpoint{Service: "east-telescope", Host: "east-telescope.observatory.svc", Port: 7624}
		if s := wheel.Status; s.Phase != observatory.DeviceConnected || s.IndiDevice != "CCD Simulator" || s.Endpoint == nil || *s.Endpoint != endpoint || s.Pod != "" {
			t.Errorf("filter wheel = %s on %q at %+v, pod %q", s.Phase, s.IndiDevice, s.Endpoint, s.Pod)
		}
		if wheel.Status.Readings.Filter != "Luminance" {
			t.Errorf("filter wheel readings = %+v", wheel.Status.Readings)
		}
		if !holdsFinalizer(wheel.Metadata) {
			t.Error("the filter wheel holds no finalizer while its camera runs")
		}
		if pods := w.api.names(podsCollection); slices.Contains(pods, "east-filterwheel") {
			t.Errorf("pods = %v, want none for the filter wheel", pods)
		}
		changes := strings.Join(w.indi.changes(), "\n")
		for _, want := range []string{
			"east-telescope CCD Simulator.FILTER_NAME FILTER_SLOT_NAME_1=Luminance",
			"ACTIVE_FILTER=CCD Simulator",
		} {
			if !strings.Contains(changes, want) {
				t.Errorf("changes = %s\nwant %q", changes, want)
			}
		}
		if connect := stepOf(r, observatory.StepConnect).Summary; !strings.Contains(connect, "Camera east-main with FilterWheel east") {
			t.Errorf("Connect = %q", connect)
		}
	})
}

// A resource that an INDI device does not serve reports Error, and the
// telescope still starts. A guide camera has no wheel, and an SBIG
// camera sets INDI's FILTER bit with no wheel attached: the bit is a
// hint, and FILTER_SLOT decides.
func TestAServedResourceWhoseOwnerLacksTheInterfaceReportsError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		driver map[string]any
		device string
	}{
		{"a camera with no wheel", map[string]any{"name": "indi_simulator_guide"}, "Guide Simulator"},
		{"a camera that claims a wheel it lacks", map[string]any{"name": "indi_fake_sbig", "image": variantImage}, "SBIG Simulator"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.put(observatory.CameraKind, "east-guide", map[string]any{"opticalTrain": "east-guiding", "driver": c.driver})
				w.put(observatory.FilterWheelKind, "east-guide", map[string]any{"opticalTrain": "east-guiding",
					"device": map[string]any{"kind": "Camera", "name": "east-guide"}, "filters": []any{"L"}})
				r := readyOf(w)

				lacks := "INDI device " + c.device + " defines no FILTER_SLOT after it connects, so it serves no FilterWheel"
				wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east-guide")
				if ready := conditionOf(wheel.Status.Conditions, observatory.ConditionReady); wheel.Status.Phase != observatory.DeviceError || ready.Message != "Failed: "+lacks {
					t.Errorf("filter wheel = %s, Ready = %+v", wheel.Status.Phase, ready)
				}
				camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-guide")
				if camera.Status.Phase != observatory.DeviceConnected {
					t.Errorf("camera = %s", camera.Status.Phase)
				}
				if connect := stepText(stepOf(r, observatory.StepConnect)); !strings.Contains(connect, "FilterWheel east-guide is not served: "+lacks) {
					t.Errorf("Connect = %q", connect)
				}
				if changes := strings.Join(w.indi.changes(), "\n"); strings.Contains(changes, c.device+".FILTER_NAME") {
					t.Errorf("changes = %s, want no FILTER_NAME", changes)
				}
			})
		})
	}
}

// readyOf reserves the east telescope, waits until it is Ready, and
// lets the status writer write once more.
func readyOf(w *world) observatory.Reservation {
	w.t.Helper()
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	time.Sleep(2 * statusWindow)
	synctest.Wait()
	return r
}

// flipFlat makes the east flat panel a Flip-Flat that runs its driver
// and serves the east dust cap, with the cover closed and the light on.
func (w *world) flipFlat() {
	w.put(observatory.FlatPanelKind, "east", map[string]any{"opticalTrain": "east-imaging",
		"driver": map[string]any{"name": "indi_fake_flipflat", "image": variantImage}, "power": map[string]any{"switch": "east", "output": 3},
		"deactivation": []any{map[string]any{"state": "Dark"}}})
	w.put(observatory.DustCapKind, "east", map[string]any{"opticalTrain": "east-imaging",
		"device":     map[string]any{"kind": "FlatPanel", "name": "east"},
		"activation": []any{map[string]any{"state": "Open"}}, "deactivation": []any{map[string]any{"state": "Closed"}}})
	w.indi.preset("Flip-Flat Simulator", "CAP_PARK", "PARK")
	w.indi.preset("Flip-Flat Simulator", "FLAT_LIGHT_CONTROL", "FLAT_LIGHT_ON")
}

// The changes that a Flip-Flat receives.
const (
	openFlipFlat  = "east-telescope Flip-Flat Simulator.CAP_PARK PARK=Off UNPARK=On"
	closeFlipFlat = "east-telescope Flip-Flat Simulator.CAP_PARK PARK=On UNPARK=Off"
	darkFlipFlat  = "east-telescope Flip-Flat Simulator.FLAT_LIGHT_CONTROL FLAT_LIGHT_ON=Off FLAT_LIGHT_OFF=On"
	stopFlipFlat  = "east-telescope Flip-Flat Simulator.CONNECTION CONNECT=Off DISCONNECT=On"
)

// order answers the index of each change in the changes the clients
// sent, or -1 for a change that none sent.
func order(w *world, changes ...string) []int {
	sent := w.indi.changes()
	var out []int
	for _, change := range changes {
		out = append(out, slices.Index(sent, change))
	}
	return out
}

// A Flip-Flat serves a DustCap and a FlatPanel from one pod. Both
// activate and deactivate, and the cap, which the panel serves, closes
// before the panel's deactivation and before the device disconnects.
func TestADustCapAndAFlatPanelOnOneDeviceActivateAndDeactivate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.flipFlat()
		readyOf(w)

		cap, _ := decode[observatory.DustCap](t, w.api, kindCollection(observatory.DustCapKind), "east")
		if open := conditionOf(cap.Status.Conditions, observatory.ConditionOpen); cap.Status.IndiDevice != "Flip-Flat Simulator" || open.Status != observatory.ConditionTrue {
			t.Errorf("dust cap on %q, Open = %+v", cap.Status.IndiDevice, open)
		}
		pods := w.api.names(podsCollection)
		if !slices.Contains(pods, "east-flatpanel") || slices.Contains(pods, "east-dustcap") {
			t.Errorf("pods = %v, want the flat panel's and none for the dust cap", pods)
		}
		if at := order(w, openFlipFlat); at[0] < 0 {
			t.Errorf("changes = %q, want %q", w.indi.changes(), openFlipFlat)
		}

		// The cover moves until the test ends its motion, so the
		// panel's deactivation waits for the cap's, and not by chance.
		w.indi.move("Flip-Flat Simulator", "CAP_PARK")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "the cap does not close", func() bool { return order(w, closeFlipFlat)[0] >= 0 })
		time.Sleep(time.Minute)
		synctest.Wait()
		if at := order(w, darkFlipFlat); at[0] >= 0 {
			t.Errorf("changes = %q\nthe light went off while the cap closed", w.indi.changes())
		}
		w.indi.setState("east-telescope", "Flip-Flat Simulator", "CAP_PARK", "Ok")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
		at := order(w, closeFlipFlat, darkFlipFlat, stopFlipFlat)
		if at[0] < 0 || at[1] < 0 || at[2] < 0 || at[0] > at[1] || at[1] > at[2] {
			t.Errorf("changes = %q\nwant the cap closed, then the light off, then the device disconnected", w.indi.changes())
		}
	})
}

// A delete of either resource on a Flip-Flat waits for its
// deactivation. The served cap closes through the panel's driver, which
// keeps running. The panel's delete closes the cap first, because the
// cap's driver is the panel's, and the cap stays, refused, with no
// device to serve it.
func TestADeleteOfEitherResourceOnOneDeviceWaitsForItsDeactivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		deleted observatory.Kind
		// sent lists the changes the device receives, in order.
		sent []string
		// running is whether the Flip-Flat still runs after the delete.
		running bool
	}{
		{"the served dust cap", observatory.DustCapKind, []string{closeFlipFlat}, true},
		{"the flat panel that serves it", observatory.FlatPanelKind, []string{closeFlipFlat, darkFlipFlat}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.flipFlat()
				readyOf(w)
				w.api.deleteNamed(kindCollection(c.deleted), "east")
				if _, ok := w.api.object(kindCollection(c.deleted), "east"); !ok {
					t.Fatalf("the %s went before its deactivation", c.deleted.Name)
				}
				w.gone(c.deleted, "east", 5*time.Minute)

				at := order(w, c.sent...)
				if slices.Contains(at, -1) || !slices.IsSorted(at) {
					t.Errorf("changes = %q\nwant %q in order", w.indi.changes(), c.sent)
				}
				if running := slices.Contains(w.indi.connected("east-telescope"), "Flip-Flat Simulator"); running != c.running {
					t.Errorf("the Flip-Flat runs: %t, want %t", running, c.running)
				}
				if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
					t.Errorf("the reservation is %s", r.Status.Phase)
				}
			})
		})
	}
}

// A served resource names a resource on its own server, because one
// INDI device runs on one server, and one device serves one resource of
// each kind. The tree refuses any other, and the telescope still
// starts.
func TestAServedResourceThatCannotShareItsOwnersDeviceIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    map[string]any
		refusal string
	}{
		{"a camera on another telescope's server", map[string]any{"opticalTrain": "east-guiding", "device": map[string]any{"kind": "Camera", "name": "west-main"}},
			"spec.device names Camera west-main, which runs on server west-telescope, and FilterWheel east-other is on server east-telescope: one INDI device runs on one server"},
		{"a camera that serves a filter wheel already", map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "Camera", "name": "east-main"}},
			"Camera east-main serves FilterWheel east already: one INDI device serves one resource of each kind"},
		{"a resource that does not exist", map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "Camera", "name": "nowhere"}},
			"spec.device names Camera nowhere, which does not exist"},
		{"a resource that is served itself", map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "FilterWheel", "name": "east"}},
			"spec.device names FilterWheel east, which runs no driver of its own: name the resource that runs the driver"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.put(observatory.FilterWheelKind, "east-other", c.spec)
				readyOf(w)
				wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east-other")
				if ready := conditionOf(wheel.Status.Conditions, observatory.ConditionReady); wheel.Status.Phase != observatory.DeviceError || ready.Message != "Failed: "+c.refusal {
					t.Errorf("filter wheel = %s, Ready = %+v", wheel.Status.Phase, ready)
				}
				if holdsFinalizer(wheel.Metadata) {
					t.Error("a refused filter wheel holds the finalizer")
				}
				served, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
				if served.Status.Phase != observatory.DeviceConnected {
					t.Errorf("the example's filter wheel = %s", served.Status.Phase)
				}
			})
		})
	}
}

// servedOn builds a device on the east telescope that names in
// spec.device the resource whose driver serves it.
func servedOn(kind observatory.Kind, name string, by *device) *device {
	d := &device{kind: kind}
	d.object.Metadata = observatory.ObjectMeta{Name: name, Namespace: "observatory", UID: "uid-" + name}
	d.object.Spec.Telescope = "east"
	d.object.Spec.Device = &observatory.ServedBy{Kind: by.kind.Name, Name: by.name()}
	return d
}

// A device powers the others when its INDI device serves a Switch,
// whichever resource runs its driver, so a power box that a Focuser
// owns connects in PowerOn with the Switch it serves.
func TestTheDevicesThatServeASwitchPowerTheOthers(t *testing.T) {
	t.Parallel()
	installed := func(d *device) *device {
		d.object.Spec.Telescope = "east"
		return d
	}
	relays := installed(simulator(observatory.SwitchKind, "east", "indi_simulator_io"))
	box := installed(simulator(observatory.FocuserKind, "east-box", "indi_pegasus_ppb"))
	boxOutputs := servedOn(observatory.SwitchKind, "east-box", box)
	mount := installed(simulator(observatory.MountKind, "east", "indi_simulator_telescope"))
	tr := &tree{devices: []*device{relays, box, boxOutputs, mount}, pods: map[string]*pod{}}
	switches, others := tr.partition(tr.devicesOn(serverRef{observatory.TelescopeKind, "east"}))
	if got := names(switches); got != "Switch east, Focuser east-box, Switch east-box" {
		t.Errorf("the devices that power the others = %q", got)
	}
	if got := names(others); got != "Mount east" {
		t.Errorf("the others = %q", got)
	}
}

// A served resource that joins a Ready session gets the finalizer and
// its settings through its owner's device, which runs already.
func TestAServedResourceThatJoinsAReadySessionGetsItsSettings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.deleteNamed(kindCollection(observatory.FilterWheelKind), "east")
		w.gone(observatory.FilterWheelKind, "east", 5*time.Minute)
		w.put(observatory.FilterWheelKind, "east-new", map[string]any{"opticalTrain": "east-imaging",
			"device": map[string]any{"kind": "Camera", "name": "east-main"}, "filters": []any{"Clear", "Red"}})
		want := "east-telescope CCD Simulator.FILTER_NAME FILTER_SLOT_NAME_1=Clear FILTER_SLOT_NAME_2=Red"
		w.until(time.Minute, "the new filter wheel's names do not reach the camera", func() bool {
			return slices.ContainsFunc(w.indi.changes(), func(c string) bool { return strings.HasPrefix(c, want) })
		})
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east-new")
		if wheel.Status.Phase != observatory.DeviceConnected || !holdsFinalizer(wheel.Metadata) {
			t.Errorf("filter wheel = %s, finalizers %v", wheel.Status.Phase, wheel.Metadata.Finalizers)
		}
	})
}

// A served resource that joins a driver that never answers its settings
// holds the runner no longer than reapplyLimit: it reports the failure,
// and the runner goes on to create a pod that is gone.
func TestAServedResourceThatJoinsADriverThatNeverAnswersDoesNotHoldTheRunner(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.deleteNamed(kindCollection(observatory.FilterWheelKind), "east")
		w.gone(observatory.FilterWheelKind, "east", 5*time.Minute)
		w.indi.hold("CCD Simulator", "FILTER_NAME")
		before := w.indi.count("east-telescope", "CCD Simulator.FILTER_NAME")
		w.put(observatory.FilterWheelKind, "east-new", map[string]any{"opticalTrain": "east-imaging",
			"device": map[string]any{"kind": "Camera", "name": "east-main"}, "filters": []any{"Clear", "Red"}})
		w.until(time.Minute, "the new filter wheel's names are not sent", func() bool {
			return w.indi.count("east-telescope", "CCD Simulator.FILTER_NAME") > before
		})
		w.api.deleteNamed(podsCollection, "east-focuser")
		w.until(2*reapplyLimit, "the runner does not create the focuser's pod again", func() bool {
			return slices.Contains(w.api.names(podsCollection), "east-focuser")
		})
		w.until(time.Minute, "the new filter wheel shows no fault", func() bool {
			wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east-new")
			return wheel.Status.Phase == observatory.DeviceError
		})
	})
}

// A resource whose device defines its interface only after the step
// that connected it decided that it lacks the interface heals: it
// reports Connected, gets its settings, and runs its activation, as a
// resource that joins a Ready session does.
func TestAResourceWhoseInterfaceArrivesLateHeals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		before           func(w *world)
		device, property string
		kind             observatory.Kind
		want             string
	}{
		{"the camera's filter wheel gets its names", func(*world) {}, "CCD Simulator", "FILTER_SLOT", observatory.FilterWheelKind,
			"east-telescope CCD Simulator.FILTER_NAME FILTER_SLOT_NAME_1=Luminance"},
		{"the Flip-Flat's dust cap opens", func(w *world) { w.flipFlat() }, "Flip-Flat Simulator", "CAP_PARK", observatory.DustCapKind,
			openFlipFlat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.before(w)
				w.indi.withhold(c.device, c.property)
				readyOf(w)
				lacking, _ := decode[deviceObject](t, w.api, kindCollection(c.kind), "east")
				if lacking.Status.Phase != observatory.DeviceError {
					t.Errorf("before the property, %s east is %s, want Error", c.kind.Name, lacking.Status.Phase)
				}
				if slices.ContainsFunc(w.indi.changes(), func(s string) bool { return strings.HasPrefix(s, c.want) }) {
					t.Fatalf("changes = %q, sent before the property", w.indi.changes())
				}

				w.indi.reveal(c.device, c.property)
				w.until(time.Minute, "the resource does not heal", func() bool {
					return slices.ContainsFunc(w.indi.changes(), func(s string) bool { return strings.HasPrefix(s, c.want) })
				})
				time.Sleep(2 * statusWindow)
				synctest.Wait()
				healed, _ := decode[deviceObject](t, w.api, kindCollection(c.kind), "east")
				if healed.Status.Phase != observatory.DeviceConnected {
					t.Errorf("%s east is %s, want Connected", c.kind.Name, healed.Status.Phase)
				}
			})
		})
	}
}

// Of two served resources of one kind on one owner, the one that holds
// the finalizer keeps the device, so one that joins never displaces it,
// and then the older one.
func TestTheServedResourceThatKeepsTheDevice(t *testing.T) {
	t.Parallel()
	early, late := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	wheel := func(name string, created time.Time, held bool, owner *device) *device {
		d := servedOn(observatory.FilterWheelKind, name, owner)
		d.object.Spec.Telescope, d.object.Spec.OpticalTrain = "", "east-imaging"
		d.object.Metadata.CreationTimestamp = &created
		if held {
			d.object.Metadata.Finalizers = []string{observatory.Finalizer}
		}
		return d
	}
	cases := []struct {
		name          string
		a, b          func(owner *device) *device
		winner, loser string
	}{
		{"the one that holds the finalizer", func(o *device) *device { return wheel("a", early, false, o) },
			func(o *device) *device { return wheel("b", late, true, o) }, "b", "a"},
		{"the older one", func(o *device) *device { return wheel("a", late, false, o) },
			func(o *device) *device { return wheel("b", early, false, o) }, "b", "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			camera := simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd")
			camera.object.Spec.OpticalTrain = "east-imaging"
			tr := &tree{devices: []*device{camera, c.a(camera), c.b(camera)}, pods: map[string]*pod{},
				trains: map[string]*observatory.OpticalTrain{"east-imaging": {Spec: observatory.OpticalTrainSpec{Telescope: "east"}}}}
			winner, _ := tr.device(observatory.FilterWheelKind, c.winner)
			loser, _ := tr.device(observatory.FilterWheelKind, c.loser)
			if tr.verdict(winner).owner != camera || tr.verdict(loser).owner != nil {
				t.Errorf("%s served by %v, %s refused with %q", c.winner, tr.verdict(winner).owner, c.loser, tr.verdict(loser).refusal)
			}
		})
	}
}
