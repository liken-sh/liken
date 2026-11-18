package main

import (
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

// untilSample waits until a scrape of the operator contains every line,
// and returns the scrape.
func (w *world) untilSample(limit time.Duration, lines ...string) string {
	w.t.Helper()
	var body string
	w.until(limit, "the scrape has no "+lines[0], func() bool {
		body = scrape(w.t, w.n.metrics)
		for _, line := range lines {
			if !hasSample(body, line) {
				return false
			}
		}
		return true
	})
	return body
}

func TestAReadyReservationSetsTheSeriesOfItsTelescope(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.untilSample(time.Minute,
			`observatory_telescope_phase{namespace="observatory",phase="Idle",telescope="east"} 1`,
			`observatory_device_connected{kind="Camera",name="east-main",namespace="observatory"} 0`,
			`observatory_device_claimed{kind="Camera",name="east-main",namespace="observatory"} 0`,
		)

		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		body := w.untilSample(time.Minute,
			`observatory_telescope_phase{namespace="observatory",phase="Ready",telescope="east"} 1`,
			`observatory_device_connected{kind="Camera",name="east-main",namespace="observatory"} 1`,
			`observatory_guider_state{guider="east",namespace="observatory",state="Stopped"} 1`,
		)
		for _, line := range []string{
			`observatory_telescope_phase{namespace="observatory",phase="Idle",telescope="east"} 0`,
			`observatory_telescope_phase{namespace="observatory",phase="Idle",telescope="west"} 1`,
			`observatory_activation_duration_seconds_count{namespace="observatory",telescope="east"} 1`,
			`observatory_device_claimed{kind="Camera",name="east-main",namespace="observatory"} 1`,
			`observatory_device_connected{kind="Dome",name="lab",namespace="observatory"} 1`,
			`observatory_device_claimed{kind="Camera",name="west-main",namespace="observatory"} 0`,
			`observatory_observation_valid{namespace="observatory",source="Telescope/east"} 1`,
			`observatory_observation_valid{namespace="observatory",source="Observatory/lab"} 1`,
			`observatory_observation_valid{namespace="observatory",source="Guider/east"} 1`,
			`observatory_guider_state{guider="east",namespace="observatory",state="Guiding"} 0`,
			`observatory_step_failures_total{namespace="observatory",step="Connect",telescope="east"} 0`,
			`observatory_device_disconnects_total{kind="Camera",name="east-main",namespace="observatory"} 0`,
		} {
			requireSample(t, body, line)
		}
		if at, ok := valueOf(body, `observatory_observation_last_success_timestamp_seconds{namespace="observatory",source="Telescope/east"}`); !ok || at < float64(time.Now().Add(-time.Minute).Unix()) {
			t.Errorf("the last success timestamp of Telescope/east is %v (present: %v), want within the last minute", at, ok)
		}
		if took, _ := valueOf(body, `observatory_activation_duration_seconds_sum{namespace="observatory",telescope="east"}`); took <= 0 {
			t.Errorf("the activation duration sum is %v s, want more than 0", took)
		}
		for _, kind := range []string{"Reservation", "Camera", "Telescope", "Observatory"} {
			if passes, _ := valueOf(body, `observatory_reconcile_duration_seconds_count{kind="`+kind+`"}`); passes == 0 {
				t.Errorf("reconcile passes of %s are counted as 0, want at least 1", kind)
			}
		}
	})
}

func TestTheCameraTemperatureFollowsItsDriver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.report("CCD Simulator", "CCD_TEMPERATURE", "CCD_TEMPERATURE_VALUE", "-12")
		w.untilSample(time.Minute, `observatory_camera_temperature_celsius{name="east-main",namespace="observatory"} -12`)
	})
}

// A person disconnects the device while a session claims it, so the
// disconnect counts as a drop and the connected gauge falls to 0.
func TestADriverThatDropsItsConnectionCounts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		client, _ := w.indi.DialContext(t.Context(), "tcp", "east-telescope.observatory.svc:7624")
		defer client.Close()
		go func() { _, _ = io.Copy(io.Discard, client) }()
		_, _ = client.Write([]byte(`<newSwitchVector device="Focuser Simulator" name="CONNECTION"><oneSwitch name="CONNECT">Off</oneSwitch><oneSwitch name="DISCONNECT">On</oneSwitch></newSwitchVector>`))
		body := w.untilSample(time.Minute,
			`observatory_device_connected{kind="Focuser",name="east",namespace="observatory"} 0`,
			`observatory_device_disconnects_total{kind="Focuser",name="east",namespace="observatory"} 1`,
		)
		requireSample(t, body, `observatory_device_claimed{kind="Focuser",name="east",namespace="observatory"} 1`)
	})
}

func TestAFailedStepCountsUnderItsTelescope(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("east-guider")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		body := w.untilSample(time.Minute,
			`observatory_step_failures_total{namespace="observatory",step="StartGuider",telescope="east"} 1`,
			`observatory_telescope_phase{namespace="observatory",phase="Error",telescope="east"} 1`,
		)
		requireSample(t, body, `observatory_step_failures_total{namespace="observatory",step="Connect",telescope="east"} 0`)
		requireNoSample(t, body, "observatory_activation_duration_seconds_count", `telescope="east"`)
	})
}

func TestTheGuiderSeriesFollowPHD2(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.guiders.prepare = func(s *phd2test.Server) { s.Scale = phd2test.Pointer(2.5) }
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		phd2 := w.guiders.phd2("east-guider")
		phd2.Broadcast(phd2test.Event("StartGuiding", nil))
		phd2.Broadcast(phd2test.GuideStep(1, 0.3, -0.4))
		phd2.Broadcast(phd2test.GuideStep(2, -0.3, 0.4))
		body := w.untilSample(time.Minute,
			`observatory_guider_state{guider="east",namespace="observatory",state="Guiding"} 1`,
			`observatory_guiding_error_arcseconds{axis="total",guider="east",namespace="observatory"} 1.25`,
		)
		requireSample(t, body, `observatory_guiding_error_arcseconds{axis="ra",guider="east",namespace="observatory"} 0.75`)
		requireSample(t, body, `observatory_guiding_error_arcseconds{axis="dec",guider="east",namespace="observatory"} 1`)
		requireSample(t, body, `observatory_guider_state{guider="east",namespace="observatory",state="Stopped"} 0`)

		phd2.Broadcast(phd2test.Event("StarLost", nil))
		w.untilSample(time.Minute,
			`observatory_guider_state{guider="east",namespace="observatory",state="LostLock"} 1`,
			`observatory_guider_state{guider="east",namespace="observatory",state="Guiding"} 0`,
		)
	})
}

// When the connection to a server ends, the observation is invalid.
// The devices on that server keep their last connected value, because
// the operator cannot tell whether they dropped.
func TestAConnectionThatEndsIsAnInvalidObservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		// The server refuses the next three dials, so the connection
		// stays down through several redial pauses, long enough for the
		// status writer to run.
		w.indi.listenLate("east-telescope", 3)
		w.indi.cut("east-telescope")
		w.untilSample(time.Minute, `observatory_observation_valid{namespace="observatory",source="Telescope/east"} 0`)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		body := scrape(t, w.n.metrics)
		requireSample(t, body, `observatory_observation_valid{namespace="observatory",source="Telescope/east"} 0`)
		requireSample(t, body, `observatory_device_connected{kind="Camera",name="east-main",namespace="observatory"} 1`)
		w.untilSample(time.Minute, `observatory_observation_valid{namespace="observatory",source="Telescope/east"} 1`)
	})
}

// The gauges of a resource that a person deleted disappear from the
// scrape.
func TestTheSeriesOfADeletedDeviceLeaveTheScrape(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.untilSample(time.Minute, `observatory_device_claimed{kind="Focuser",name="spare",namespace="observatory"} 0`)
		w.api.deleteNamed(kindCollection(observatory.FocuserKind), "spare")
		w.until(time.Minute, "the spare focuser's series stay", func() bool {
			return len(samples(scrape(t, w.n.metrics), "observatory_device_claimed")) > 0 &&
				!hasSample(scrape(t, w.n.metrics), `observatory_device_claimed{kind="Focuser",name="spare",namespace="observatory"} 0`)
		})
		body := scrape(t, w.n.metrics)
		requireNoSample(t, body, "observatory_device_connected", `name="spare"`)
		requireSample(t, body, `observatory_device_claimed{kind="Focuser",name="east",namespace="observatory"} 0`)
	})
}

// When a session ends, the operator stops its servers and its guider,
// so their observation series and the guider's series leave the
// scrape, and its devices are neither claimed nor connected.
func TestAReleaseDeletesTheSeriesOfWhatItStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.untilSample(time.Minute, `observatory_guider_state{guider="east",namespace="observatory",state="Stopped"} 1`)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the Reservation is still there", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		body := w.untilSample(time.Minute,
			`observatory_telescope_phase{namespace="observatory",phase="Idle",telescope="east"} 1`,
			`observatory_device_claimed{kind="Camera",name="east-main",namespace="observatory"} 0`,
			`observatory_device_connected{kind="Camera",name="east-main",namespace="observatory"} 0`,
		)
		w.until(time.Minute, "the observation series of the servers stay", func() bool {
			return len(samples(scrape(t, w.n.metrics), "observatory_observation_valid")) == 0
		})
		body = scrape(t, w.n.metrics)
		requireNoSample(t, body, "observatory_guider_state", `guider="east"`)
		// Deactivation disconnects the devices deliberately, so no drop
		// is counted.
		requireSample(t, body, `observatory_device_disconnects_total{kind="Camera",name="east-main",namespace="observatory"} 0`)
	})
}

// When the operator of a namespace stops, it deletes every gauge of
// the namespace and keeps the counters.
func TestAStoppedNamespaceLeavesNoGauges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.halt()
		body := scrape(t, w.n.metrics)
		requireNoSample(t, body, "observatory_device_claimed", `namespace="observatory"`)
		requireNoSample(t, body, "observatory_telescope_phase", `namespace="observatory"`)
		requireNoSample(t, body, "observatory_observation_valid", `namespace="observatory"`)
		requireNoSample(t, body, "observatory_guider_state", `namespace="observatory"`)
		requireSample(t, body, `observatory_step_failures_total{namespace="observatory",step="Connect",telescope="east"} 0`)
		requireSample(t, body, `observatory_activation_duration_seconds_count{namespace="observatory",telescope="east"} 1`)
	})
}
