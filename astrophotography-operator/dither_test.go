package main

// Dithering (plan 01, step 7.9): after every dither.every frames, PHD2
// moves its lock position and the runner waits for SettleDone before the
// next frame. A dither changes no condition, so it posts no Event.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// dithered is a target of five frames that dithers every two by up to
// five pixels in right ascension alone.
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
			t.Errorf("Imaging says %q", imaging.Message)
		}
		w.done(t, "m42")

		phd2 := w.phd2.now()
		if got := guidingCalls(phd2); got != "loop find_star guide dither dither" {
			t.Errorf("PHD2 received %q", got)
		}
		var params map[string]any
		_ = json.Unmarshal(phd2.Params("dither"), &params)
		if params["amount"] != 5.0 || params["raOnly"] != true {
			t.Errorf("dither params %v", params)
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

// The frame that completes the target's work gets no dither, because no
// frame of the target follows it to use the new lock position.
func TestNoDitherAfterTheLastFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m42 := dithered(2)
		m42.Spec.Sequences = []astrophotography.Sequence{lights(4)}
		w := guidedNight(t, m42)
		w.start(t)
		w.done(t, "m42")

		if got := guidingCalls(w.phd2.now()); got != "loop find_star guide dither" {
			t.Errorf("PHD2 received %q", got)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_dithers_total{namespace="observatory",target="m42"} 1`)
	})
}

// A dither of every frame says so in words.
func TestADitherAfterEachFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, dithered(1))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if !strings.Contains(imaging.Message, "guided by Guider east, dithered after each frame,") {
			t.Errorf("Imaging says %q", imaging.Message)
		}
	})
}
