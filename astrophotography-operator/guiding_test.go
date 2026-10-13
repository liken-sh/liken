package main

// Guiding with PHD2 (plan 01, steps 2 and 6): the runner stops PHD2
// before the slew, guides before the first frame and waits for
// SettleDone, and exposes unguided only when the session has no
// Guider.

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

const guidersPath = "/apis/observatory.liken.sh/v1alpha1/guiders"

// switchboard is the operator's dialer of PHD2. It records each address
// the runner dials, and connects it to the fake PHD2 of the moment, so
// a test can replace a PHD2 that exited with a new one, as the guider's
// pod does when it starts again.
type switchboard struct {
	mu     sync.Mutex
	server *phd2test.Server
	dialed []string
}

// newSwitchboard connects to a PHD2 that runs, idle, with its camera
// and mount connected, as observatory-operator's StartGuider leaves it.
func newSwitchboard() *switchboard { return &switchboard{server: connectedPHD2()} }

func connectedPHD2() *phd2test.Server {
	s := phd2test.New()
	s.Set(func(s *phd2test.Server) { s.Equipment = true })
	return s
}

func (b *switchboard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	b.mu.Lock()
	b.dialed = append(b.dialed, address)
	server := b.server
	b.mu.Unlock()
	return server.DialContext(ctx, network, address)
}

// now answers the fake PHD2 of the moment.
func (b *switchboard) now() *phd2test.Server {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.server
}

// replace connects later dials to another PHD2.
func (b *switchboard) replace(server *phd2test.Server) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.server = server
}

func (b *switchboard) addresses() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.dialed)
}

// guidingCalls answers the methods a PHD2 received that move the guiding,
// in order, with the reads of its state left out.
func guidingCalls(s *phd2test.Server) string {
	var out []string
	for _, method := range strings.Fields(s.Received()) {
		if !strings.HasPrefix(method, "get_") {
			out = append(out, method)
		}
	}
	return strings.Join(out, " ")
}

// guider is the Guider east, whose guide camera is on the train
// east-guide, with PHD2's event server at endpoint.
func guider(endpoint *observatory.Endpoint) *observatory.Guider {
	return &observatory.Guider{
		Metadata: meta("east"),
		Spec:     observatory.GuiderSpec{Telescope: "east", OpticalTrain: "east-guide", Pulses: observatory.PulsesMount},
		Status:   observatory.GuiderStatus{Endpoint: endpoint},
	}
}

var guiderEndpoint = &observatory.Endpoint{Service: "east-guider", Host: "east-guider.observatory.svc", Port: 4400}

// guided adds the Guider east to a reservation's session, with its
// guide camera, as observatory-operator records them.
func guided(res *observatory.Reservation) *observatory.Reservation {
	res.Status.Session.Devices["Camera/east-guide"] = observatory.SessionDevice{OpticalTrain: "east-guide", Guides: true}
	res.Status.Session.Guiders = map[string]observatory.SessionGuider{"east": {UID: ns + "/east", GuiderSpec: guider(nil).Spec}}
	return res
}

// guidedNight is a world whose telescope guides with the Guider east.
func guidedNight(t *testing.T, targets ...*astrophotography.Target) *night {
	w := newWorld(t)
	w.put(guidersPath, guider(guiderEndpoint))
	for _, target := range targets {
		w.put(targetsPath, target)
	}
	w.put(reservationsPath, guided(readyOrNot(observatory.ReservationReady)))
	w.playCamera(t, saves)
	return w
}

// done waits until the runner released the target with SequencesDone.
func (w *night) done(t *testing.T, name string) {
	t.Helper()
	waitUntil(t, func() bool { return w.imaging(t, name) == "False SequencesDone" })
}

// The runner dials the Guider's status.endpoint, guides with loop,
// find_star, and guide, and posts one Event as it starts guiding. The
// settle is 1.5 pixels for 8 seconds, and PHD2 calibrates only when it
// holds no calibration.
func TestTheRunnerGuidesBeforeTheFirstFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(2)))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if want := "Reservation east-tonight exposes Luminance 10s, guided by Guider east, not centered."; imaging.Message != want {
			t.Errorf("Imaging says %q, want %q", imaging.Message, want)
		}
		w.done(t, "m42")

		phd2 := w.phd2.now()
		if got := guidingCalls(phd2); got != "loop find_star guide" {
			t.Errorf("PHD2 received %q", got)
		}
		var params map[string]any
		_ = json.Unmarshal(phd2.Params("guide"), &params)
		settle, _ := json.Marshal(params["settle"])
		if string(settle) != `{"pixels":1.5,"time":8,"timeout":60}` || params["recalibrate"] != false {
			t.Errorf("guide params %v", params)
		}
		if got := w.phd2.addresses(); got[0] != "east-guider.observatory.svc:4400" {
			t.Errorf("dialed %q", got)
		}
		want := []string{"Slewing", "Guiding", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("Events on the target %q, want %q", got, want)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_settle_seconds_count{operation="guide"} 1`)
	})
}

// No frame starts until PHD2 reports SettleDone, because a frame taken
// while the star moves is blurred.
func TestTheFirstFrameWaitsForSettleDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.now().Set(func(s *phd2test.Server) { s.HoldSettle = true })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Guiding" })
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if frames := w.worker.held(); len(frames) != 0 || w.targetNow(t, "m42").Status.Exposure != nil {
			t.Errorf("exposed %q before SettleDone", frames)
		}
		w.phd2.now().Settle("")
		w.done(t, "m42")
		if frames := w.worker.held(); len(frames) != 1 {
			t.Errorf("frames %q, want 1", frames)
		}
	})
}

// A PHD2 that guides when the runner starts, as after a restart of the
// operator, gets stop_capture before the mount moves, and guides again
// after the slew.
func TestTheRunnerStopsGuidingBeforeTheSlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.now().Set(func(s *phd2test.Server) { s.AppState, s.Calibrated = "Guiding", true })
		w.start(t)
		w.done(t, "m42")
		if got := guidingCalls(w.phd2.now()); got != "stop_capture loop find_star guide" {
			t.Errorf("PHD2 received %q", got)
		}
	})
}

// A PHD2 that does not answer stop_capture keeps the mount where it is:
// the runner releases the target with GuidingFailed and does not slew.
func TestAPHD2ThatDoesNotStopKeepsTheMountStill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.now().Set(func(s *phd2test.Server) { s.AppState, s.Hold = "Guiding", "stop_capture" })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		if slews := slices.DeleteFunc(w.indi.Changes(), func(c string) bool { return !isSlew(c) }); len(slews) != 0 {
			t.Errorf("the mount slewed: %q", slews)
		}
	})
}

// A session with no Guider exposes unguided, says so, and does not
// dither even when the target asks for it.
func TestASessionWithNoGuiderExposesUnguided(t *testing.T) {
	cases := []struct {
		name   string
		dither *astrophotography.Dither
		says   string
	}{
		{"no dither", nil, "unguided because the telescope has no Guider"},
		{"a dither", &astrophotography.Dither{Every: 1, Pixels: 5}, "unguided and not dithered because the telescope has no Guider"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				m42 := target("m42", lights(2))
				m42.Spec.Dither = c.dither
				w.put(targetsPath, m42)
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, w.inFlight(t, "m42"))
				imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
				if want := "Reservation east-tonight exposes Luminance 10s, " + c.says + ", not centered."; imaging.Message != want {
					t.Errorf("Imaging says %q, want %q", imaging.Message, want)
				}
				w.done(t, "m42")
				if dialed := w.phd2.addresses(); len(dialed) != 0 {
					t.Errorf("dialed PHD2 at %q", dialed)
				}
			})
		})
	}
}
