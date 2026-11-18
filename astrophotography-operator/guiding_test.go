package main

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

// phd2Dialer is the dialer that the operator uses to reach PHD2. It
// records each address the runner dials and connects it to the current
// fake PHD2. A test can replace the fake, as when the Guider's pod
// restarts after PHD2 exits.
type phd2Dialer struct {
	mu     sync.Mutex
	server *phd2test.Server
	dialed []string
}

// newPHD2Dialer returns a dialer connected to an idle PHD2 whose
// camera and mount are connected, which is the state that
// observatory-operator's StartGuider leaves PHD2 in.
func newPHD2Dialer() *phd2Dialer { return &phd2Dialer{server: connectedPHD2()} }

func connectedPHD2() *phd2test.Server {
	s := phd2test.New()
	s.Set(func(s *phd2test.Server) { s.Equipment = true })
	return s
}

func (b *phd2Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	b.mu.Lock()
	b.dialed = append(b.dialed, address)
	server := b.server
	b.mu.Unlock()
	return server.DialContext(ctx, network, address)
}

func (b *phd2Dialer) current() *phd2test.Server {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.server
}

func (b *phd2Dialer) replace(server *phd2test.Server) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.server = server
}

func (b *phd2Dialer) addresses() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.dialed)
}

// guidingCalls returns the methods that the fake PHD2 received, in
// order, except the get_ methods, which only read state.
func guidingCalls(s *phd2test.Server) string {
	var out []string
	for _, method := range strings.Fields(s.Received()) {
		if !strings.HasPrefix(method, "get_") {
			out = append(out, method)
		}
	}
	return strings.Join(out, " ")
}

func guider(endpoint *observatory.Endpoint) *observatory.Guider {
	return &observatory.Guider{
		Metadata: meta("east"),
		Spec:     observatory.GuiderSpec{Telescope: "east", OpticalTrain: "east-guide", Pulses: observatory.PulsesMount},
		Status:   observatory.GuiderStatus{Endpoint: endpoint},
	}
}

var guiderEndpoint = &observatory.Endpoint{Service: "east-guider", Host: "east-guider.observatory.svc", Port: 4400}

// guided adds the Guider east and its guide camera to the reservation's
// session, as observatory-operator writes them.
func guided(res *observatory.Reservation) *observatory.Reservation {
	res.Status.Session.Devices["Camera/east-guide"] = observatory.SessionDevice{OpticalTrain: "east-guide", Guides: true}
	res.Status.Session.Guiders = map[string]observatory.SessionGuider{"east": {UID: ns + "/east", GuiderSpec: guider(nil).Spec}}
	return res
}

func guidedNight(t *testing.T, targets ...*astrophotography.Target) *runnerWorld {
	w := newWorld(t)
	w.put(guidersPath, guider(guiderEndpoint))
	for _, target := range targets {
		w.put(targetsPath, target)
	}
	w.put(reservationsPath, guided(reservationIn(observatory.ReservationReady)))
	w.playCamera(t, saves)
	return w
}

func (w *runnerWorld) done(t *testing.T, name string) {
	t.Helper()
	waitUntil(t, func() bool { return w.imaging(t, name) == "False SequencesDone" })
}

// The runner dials the Guider's status.endpoint, guides with loop,
// find_star, and guide without recalibrate, and posts one Guiding Event.
func TestTheRunnerGuidesBeforeTheFirstFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(2)))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if want := "Reservation east-tonight exposes Luminance 10s, guided by Guider east, not centered"; imaging.Message != want {
			t.Errorf("the Imaging message is %q, want %q", imaging.Message, want)
		}
		w.done(t, "m42")

		phd2 := w.phd2.current()
		if got := guidingCalls(phd2); got != "loop find_star guide" {
			t.Errorf("PHD2 received %q, want loop find_star guide", got)
		}
		var params map[string]any
		_ = json.Unmarshal(phd2.Params("guide"), &params)
		settle, _ := json.Marshal(params["settle"])
		if string(settle) != `{"pixels":1.5,"time":8,"timeout":60}` || params["recalibrate"] != false {
			t.Errorf("guide params are %v, want settle {pixels 1.5, time 8, timeout 60} and recalibrate false", params)
		}
		if got := w.phd2.addresses(); got[0] != "east-guider.observatory.svc:4400" {
			t.Errorf("the runner dialed %q, want east-guider.observatory.svc:4400", got)
		}
		want := []string{"Slewing", "Guiding", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("Events on the target %q, want %q", got, want)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_settle_seconds_count{operation="guide"} 1`)
	})
}

func TestTheFirstFrameWaitsForSettleDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.current().Set(func(s *phd2test.Server) { s.HoldSettle = true })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Guiding" })
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if frames := w.worker.outboxFrames(); len(frames) != 0 || w.targetNow(t, "m42").Status.Exposure != nil {
			t.Errorf("the worker holds %q and the Target has an exposure before SettleDone, want none", frames)
		}
		w.phd2.current().Settle("")
		w.done(t, "m42")
		if frames := w.worker.outboxFrames(); len(frames) != 1 {
			t.Errorf("the worker holds %d frames after SettleDone, want 1", len(frames))
		}
	})
}

// When PHD2 is already guiding as the runner starts, for example after
// an operator restart, the runner sends stop_capture before the slew and
// guides again after it.
func TestTheRunnerStopsGuidingBeforeTheSlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.current().Set(func(s *phd2test.Server) { s.AppState, s.Calibrated = "Guiding", true })
		w.start(t)
		w.done(t, "m42")
		if got := guidingCalls(w.phd2.current()); got != "stop_capture loop find_star guide" {
			t.Errorf("PHD2 received %q, want stop_capture loop find_star guide", got)
		}
	})
}

// When PHD2 does not respond to stop_capture, the runner does not slew,
// and sets Imaging to False with GuidingFailed.
func TestAPHD2ThatDoesNotStopKeepsTheMountStill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.current().Set(func(s *phd2test.Server) { s.AppState, s.Hold = "Guiding", "stop_capture" })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		if slews := slices.DeleteFunc(w.indi.Changes(), func(c string) bool { return !isSlew(c) }); len(slews) != 0 {
			t.Errorf("the mount slewed with %q, want no slew", slews)
		}
	})
}

// A session with no Guider exposes unguided, and does not dither even
// when the Target sets spec.dither.
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
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, w.inFlight(t, "m42"))
				imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
				if want := "Reservation east-tonight exposes Luminance 10s, " + c.says + ", not centered"; imaging.Message != want {
					t.Errorf("the Imaging message is %q, want %q", imaging.Message, want)
				}
				w.done(t, "m42")
				if dialed := w.phd2.addresses(); len(dialed) != 0 {
					t.Errorf("the runner dialed PHD2 at %q, want no connection", dialed)
				}
			})
		})
	}
}
