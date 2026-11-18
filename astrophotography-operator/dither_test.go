package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// dithered returns a target of five frames that dithers after every
// `every` frames, by up to 5 pixels in right ascension only.
func dithered(every int32) *astrophotography.Target {
	m42 := target("m42", lights(5))
	m42.Spec.Dither = &astrophotography.Dither{Every: every, Pixels: 5, RAOnly: true}
	return m42
}

func TestTheRunnerDithersEveryNFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, dithered(2))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if !strings.Contains(imaging.Message, "guided by Guider east, dithered every 2 frames, not centered") {
			t.Errorf("the Imaging message is %q, want it to contain \"guided by Guider east, dithered every 2 frames, not centered\"", imaging.Message)
		}
		w.done(t, "m42")

		phd2 := w.phd2.current()
		if got, want := guidingCalls(phd2), "loop find_star guide dither dither"; got != want {
			t.Errorf("PHD2 received %q, want %q", got, want)
		}
		var params map[string]any
		_ = json.Unmarshal(phd2.Params("dither"), &params)
		if params["amount"] != 5.0 || params["raOnly"] != true {
			t.Errorf("the dither params are %v, want amount 5 and raOnly true", params)
		}
		body := scrape(t, w.o.metrics)
		requireSample(t, body, `astrophotography_dithers_total{namespace="observatory",target="m42"} 2`)
		requireSample(t, body, `astrophotography_settle_seconds_count{operation="dither"} 2`)
		want := []string{"Slewing", "Guiding", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("Events on the target %q, want %q", got, want)
		}
	})
}

// The frame that completes the Target's work gets no dither, because
// no later frame of the Target would use the new lock position.
func TestNoDitherAfterTheLastFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m42 := dithered(2)
		m42.Spec.Sequences = []astrophotography.Sequence{lights(4)}
		w := guidedNight(t, m42)
		w.start(t)
		w.done(t, "m42")

		if got, want := guidingCalls(w.phd2.current()), "loop find_star guide dither"; got != want {
			t.Errorf("PHD2 received %q, want %q", got, want)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_dithers_total{namespace="observatory",target="m42"} 1`)
	})
}

// The Imaging message says "dithered after each frame" when every is 1.
func TestADitherAfterEachFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, dithered(1))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if !strings.Contains(imaging.Message, "guided by Guider east, dithered after each frame,") {
			t.Errorf("the Imaging message is %q, want it to contain \"guided by Guider east, dithered after each frame,\"", imaging.Message)
		}
	})
}
