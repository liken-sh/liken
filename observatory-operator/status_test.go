package main

// The status of every resource while a reservation holds the east
// telescope: what each device reports, and what each resource above
// the devices builds of the tree.

import (
	"io"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func readyWorld(t *testing.T) *world {
	w := startWorld(t)
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	// The status writer runs once a second.
	time.Sleep(2 * statusWindow)
	synctest.Wait()
	return w
}

func TestADeviceReportsWhatItsDriverReports(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		s := camera.Status
		if s.Phase != observatory.DeviceConnected || s.IndiDevice != "CCD Simulator" || s.Pod != "east-main-camera" || s.Node != "node-1" ||
			s.Driver != "indi_simulator_ccd" || !strings.HasPrefix(s.Image, "ghcr.io/liken-sh/indi-simulators:") {
			t.Errorf("camera status = %+v", s)
		}
		if s.Readings.Temperature == nil || *s.Readings.Temperature != -10 {
			t.Errorf("temperature = %v, want the setpoint the driver reports", s.Readings.Temperature)
		}
		names := map[string]observatory.Property{}
		for _, p := range s.Properties {
			names[p.Name] = p
		}
		gain := names["CCD_GAIN"]
		if gain.Type != "Number" || gain.Permission != "rw" || len(gain.Members) != 1 || gain.Members[0].Value != "100" || gain.Members[0].Max == nil {
			t.Errorf("CCD_GAIN = %+v", gain)
		}
		if _, ok := names["CCD1"]; !ok {
			t.Error("the BLOB property CCD1 is missing from the list")
		}
		if c := conditionOf(s.Conditions, observatory.ConditionReady); c.Status != observatory.ConditionTrue {
			t.Errorf("Ready = %+v", c)
		}
		if c := conditionOf(s.Conditions, observatory.ConditionParentFound); c.Status != observatory.ConditionTrue {
			t.Errorf("ParentFound = %+v", c)
		}

		wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		if wheel.Status.Readings.Slot == nil || wheel.Status.Readings.Filter != "Luminance" {
			t.Errorf("filter wheel readings = %+v", wheel.Status.Readings)
		}
		power, _ := decode[observatory.Switch](t, w.api, kindCollection(observatory.SwitchKind), "east")
		if !slices.Equal(power.Status.Readings.On, []int32{1, 2, 3}) {
			t.Errorf("outputs on = %v", power.Status.Readings.On)
		}
		mount, _ := decode[observatory.Mount](t, w.api, kindCollection(observatory.MountKind), "east")
		if mount.Status.Readings.Parked == nil || *mount.Status.Readings.Parked || mount.Status.Readings.RightAscension == nil {
			t.Errorf("mount readings = %+v", mount.Status.Readings)
		}

		// A cover that moves reports Moving.
		w.indi.setState("east-telescope", "Dust Cover Simulator", "CAP_PARK", "Busy")
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		cap, _ := decode[observatory.DustCap](t, w.api, kindCollection(observatory.DustCapKind), "east")
		if cap.Status.Readings.Cover != observatory.CoverMoving {
			t.Errorf("cover = %q, want Moving", cap.Status.Readings.Cover)
		}

		// The west telescope has no reservation, so its devices are
		// Idle.
		west, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "west-main")
		if west.Status.Phase != observatory.DeviceIdle || west.Status.Pod != "" || west.Status.Properties != nil {
			t.Errorf("west camera = %+v", west.Status)
		}
	})
}

func TestTheTreeReportsDownward(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		s := east.Status
		if s.Phase != observatory.PhaseReady || s.Server == nil || s.Server.Host != "east-telescope.observatory.svc" || s.Server.Port != 7624 || s.Server.Pod != "east-telescope" {
			t.Errorf("east = %+v", s)
		}
		if s.Reservation == nil || s.Reservation.Name != "east-tonight" || s.Reservation.Holder != "desktop" {
			t.Errorf("reservation = %+v", s.Reservation)
		}
		if !slices.Equal(s.Tubes, []string{"east-guidescope", "east-refractor"}) || len(s.Trains) != 2 || len(s.Trains[1].Devices) != 6 {
			t.Errorf("tubes %v, trains %+v", s.Tubes, s.Trains)
		}
		for _, d := range append(s.Devices, s.Trains[1].Devices...) {
			if d.Phase != observatory.DeviceConnected {
				t.Errorf("%s %s is %s", d.Kind, d.Name, d.Phase)
			}
		}
		if s.Guider == nil || !s.Guider.Ready || s.Guider.Phase != observatory.PhaseReady || s.Guider.State != observatory.GuiderStopped {
			t.Errorf("guider = %+v", s.Guider)
		}
		west, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "west")
		if west.Status.Phase != observatory.PhaseIdle || west.Status.Server != nil {
			t.Errorf("west = %+v", west.Status)
		}

		lab, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
		if lab.Status.Phase != observatory.PhaseReady || !slices.Equal(lab.Status.Telescopes, []string{"east", "west"}) ||
			!slices.Equal(lab.Status.Reservations, []string{"east-tonight"}) || lab.Status.Server == nil || len(lab.Status.Devices) != 3 {
			t.Errorf("lab = %+v", lab.Status)
		}
		if lab.Status.Weather == "" {
			t.Errorf("weather is empty")
		}

		guider, _ := decode[observatory.Guider](t, w.api, kindCollection(observatory.GuiderKind), "east")
		if c := conditionOf(guider.Status.Conditions, observatory.ConditionReady); c.Reason != string(observatory.PhaseReady) || c.Status != observatory.ConditionTrue {
			t.Errorf("guider Ready = %+v", c)
		}
		train, _ := decode[observatory.OpticalTrain](t, w.api, kindCollection(observatory.OpticalTrainKind), "east-imaging")
		if len(train.Status.Devices) != 6 || conditionOf(train.Status.Conditions, observatory.ConditionParentFound).Status != observatory.ConditionTrue {
			t.Errorf("train = %+v", train.Status)
		}
		tube, _ := decode[observatory.OpticalTube](t, w.api, kindCollection(observatory.OpticalTubeKind), "east-refractor")
		if !slices.Equal(tube.Status.Trains, []string{"east-imaging"}) {
			t.Errorf("tube = %+v", tube.Status)
		}
	})
}

// An observatory whose only reservation deactivates is Deactivating,
// not Activating, while its devices disconnect. The dome's driver never
// answers the disconnect, so the release stays in StopSite.
func TestAnObservatoryDeactivatesWithItsReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("Dome Simulator", "CONNECTION")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		var phases []observatory.Phase
		w.until(10*time.Minute, "the Observatory is not Deactivating", func() bool {
			lab, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
			phases = append(phases, lab.Status.Phase)
			return lab.Status.Phase == observatory.PhaseDeactivating
		})
		if slices.Contains(phases, observatory.PhaseActivating) {
			t.Errorf("phases = %v, want no Activating", phases)
		}
		lab, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
		if c := conditionOf(lab.Status.Conditions, observatory.ConditionReady); c.Message != "Deactivating for Reservation east-tonight" {
			t.Errorf("Ready = %+v", c)
		}
	})
}

// A resource whose parent is missing stays, and says which parent is
// missing.
func TestAMissingParentIsReported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    observatory.Kind
		spec    map[string]any
		message string
	}{
		{observatory.FocuserKind, map[string]any{"opticalTrain": "nowhere", "driver": map[string]any{"name": "indi_simulator_focus"}}, "Missing OpticalTrain nowhere"},
		{observatory.MountKind, map[string]any{"telescope": "nowhere", "driver": map[string]any{"name": "indi_simulator_telescope"}}, "Missing Telescope nowhere"},
		{observatory.DomeKind, map[string]any{"observatory": "nowhere", "driver": map[string]any{"name": "indi_simulator_dome"}}, "Missing Observatory nowhere"},
		{observatory.TelescopeKind, map[string]any{"observatory": "nowhere"}, "Missing Observatory nowhere"},
		{observatory.OpticalTrainKind, map[string]any{"telescope": "east", "opticalTube": "nowhere"}, "Missing OpticalTube nowhere"},
		{observatory.OpticalTubeKind, map[string]any{"telescope": "nowhere", "aperture": 1, "focalLength": 1}, "Missing Telescope nowhere"},
		{observatory.GuiderKind, map[string]any{"telescope": "east", "opticalTrain": "nowhere", "pulses": "Mount"}, "Missing OpticalTrain nowhere"},
	}
	for _, c := range cases {
		t.Run(c.kind.Name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.put(c.kind, "stray", c.spec)
				w.until(time.Minute, "the stray "+c.kind.Name+" reports no missing parent", func() bool {
					object, _ := decode[struct {
						Status struct {
							Conditions []observatory.Condition `json:"conditions"`
						} `json:"status"`
					}](t, w.api, kindCollection(c.kind), "stray")
					found := conditionOf(object.Status.Conditions, observatory.ConditionParentFound)
					return found.Status == observatory.ConditionFalse && found.Message == c.message
				})
			})
		})
	}
}

// A status is written once a window at most, however often the driver
// updates.
func TestStatusWritesAreCoalesced(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		before := w.api.statusWrites(kindCollection(observatory.FocuserKind), "east")
		w.indi.mu.Lock()
		server := w.indi.servers["east-telescope"]
		w.indi.mu.Unlock()
		// The focuser moves: fifty updates in five seconds.
		for i := range 50 {
			w.indi.mu.Lock()
			for _, d := range server.drivers {
				if p := d.find("ABS_FOCUS_POSITION"); p != nil {
					p.Members[0].Value = strings.Repeat("1", 1) + string(rune('0'+i%10)) + "000"
					server.broadcast(p.set())
				}
			}
			w.indi.mu.Unlock()
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		writes := w.api.statusWrites(kindCollection(observatory.FocuserKind), "east") - before
		if writes > 7 {
			t.Errorf("%d status writes for 50 updates in 5 seconds, want at most one a second", writes)
		}
		focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
		if focuser.Status.Readings.Position == nil || *focuser.Status.Readings.Position != 19000 {
			t.Errorf("position = %v, want the last update", focuser.Status.Readings.Position)
		}
	})
}

// A device pod that restarts comes back disconnected, and the operator
// connects it and writes its settings again.
func TestADeviceThatComesBackIsSetUpAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		gains := w.indi.count("east-telescope", "CCD Simulator.CCD_GAIN")
		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.until(time.Minute, "the camera is not set up again", func() bool {
			return slices.Contains(w.indi.connected("east-telescope"), "CCD Simulator") &&
				w.indi.count("east-telescope", "CCD Simulator.CCD_GAIN") == gains+1
		})
		// A device that a person disconnects stays disconnected.
		connects := w.indi.count("east-telescope", "Focuser Simulator.CONNECTION")
		client, _ := w.indi.DialContext(t.Context(), "tcp", "east-telescope.observatory.svc:7624")
		defer client.Close()
		go func() { _, _ = io.Copy(io.Discard, client) }()
		_, _ = client.Write([]byte(`<newSwitchVector device="Focuser Simulator" name="CONNECTION"><oneSwitch name="CONNECT">Off</oneSwitch><oneSwitch name="DISCONNECT">On</oneSwitch></newSwitchVector>`))
		time.Sleep(time.Minute)
		synctest.Wait()
		if slices.Contains(w.indi.connected("east-telescope"), "Focuser Simulator") {
			t.Error("the focuser is connected again")
		}
		if n := w.indi.count("east-telescope", "Focuser Simulator.CONNECTION"); n != connects+1 {
			t.Errorf("CONNECTION changes = %d, want only the person's", n-connects)
		}
	})
}

// A server that restarts brings every driver back disconnected, and
// the operator connects each one again.
func TestAServerThatRestartsIsSetUpAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.deleteNamed(podsCollection, "east-telescope")
		w.until(time.Minute, "the devices are not connected again", func() bool {
			return len(w.indi.connected("east-telescope")) == 12
		})
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("the reservation is %s", r.Status.Phase)
		}
	})
}

// A connection that ends while the server's pod stays Ready brings no
// pod event, so the operator dials again after a pause, which doubles
// for each connection that ends early.
func TestAConnectionThatEndsIsOpenedAgainAfterAPause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		for _, pause := range []time.Duration{time.Second, 2 * time.Second} {
			w.indi.cut("east-telescope")
			time.Sleep(pause - time.Millisecond)
			synctest.Wait()
			if n := w.indi.clients("east-telescope"); n != 0 {
				t.Fatalf("%d connections before the pause of %v ended", n, pause)
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			if n := w.indi.clients("east-telescope"); n != 1 {
				t.Fatalf("%d connections after the pause of %v, want 1", n, pause)
			}
		}
	})
}

// indiserver opens its port a moment after its pod is Ready, so the
// operator's first dial of a new server is often refused. That refusal
// is no fault and logs nothing. A server that refuses the dial after
// the pause too is logged.
func TestAServerThatListensLateLogsOnlyARepeatedRefusal(t *testing.T) {
	cases := []struct {
		name  string
		dials int
		want  int
	}{
		{"one refusal", 1, 0},
		{"two refusals", 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.indi.listenLate("west-telescope", c.dials)
				w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
				w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
				refusals := strings.Count(w.logs.String(), "the connection to west-telescope ended: dial tcp: connect: connection refused")
				if refusals != c.want {
					t.Errorf("%d refusals logged, want %d:\n%s", refusals, c.want, w.logs.String())
				}
			})
		})
	}
}

// A device of the observatory that comes back is connected again, and
// the dome's policy is written again.
func TestASiteDeviceThatComesBackIsSetUpAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		policies := w.indi.count("lab-observatory", "Dome Simulator.DOME_SHUTTER_PARK_POLICY")
		w.api.deleteNamed(podsCollection, "lab-dome")
		w.until(time.Minute, "the dome is not set up again", func() bool {
			return slices.Contains(w.indi.connected("lab-observatory"), "Dome Simulator") &&
				w.indi.count("lab-observatory", "Dome Simulator.DOME_SHUTTER_PARK_POLICY") == policies+1
		})
	})
}
