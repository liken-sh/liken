package main

// A centering frame: the same settings and keywords as a light frame,
// its own FRAMEID and file in the reservation's _centering directory,
// the hint of the solve, and the delete after it.

import (
	"math"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/worker"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// untilExposure answers the changes the INDI server received before
// the first exposure.
func untilExposure(w *night) []string {
	sent := w.indi.Changes()
	first := slices.IndexFunc(sent, func(c string) bool { return strings.HasPrefix(c, ccdINDI+".CCD_EXPOSURE ") })
	return sent[:first+1]
}

func TestACenteringFrameHasItsOwnIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := centeringOn("lab")
		c.Filter = "Red"
		w := centeringNight(t, c, northOf(m42Position(), 12))
		waitUntil(t, w.inFlight(t, "m42"))
		sent := untilExposure(w)
		frameID := member(sent, ccdINDI+".FITS_HEADER", "KEYWORD_VALUE")
		cases := []struct{ property, member, want string }{
			{ccdINDI + ".UPLOAD_SETTINGS", "UPLOAD_DIR", "/outbox/east-main/east-tonight/_centering"},
			{ccdINDI + ".UPLOAD_SETTINGS", "UPLOAD_PREFIX", frameID},
			{ccdINDI + ".CCD_FRAME_TYPE", "FRAME_LIGHT", "On"},
			{wheelINDI + ".FILTER_SLOT", "FILTER_SLOT_VALUE", "1"},
			{ccdINDI + ".CCD_EXPOSURE", "CCD_EXPOSURE_VALUE", "5"},
		}
		for _, c := range cases {
			if got := member(sent, c.property, c.member); got != c.want {
				t.Errorf("%s %s = %q, want %q", c.property, c.member, got, c.want)
			}
		}
		var keywords []string
		for _, name := range changes(sent, 0) {
			if keyword, ok := strings.CutPrefix(name, ccdINDI+".FITS_HEADER "); ok {
				keywords = append(keywords, keyword)
			}
		}
		if want := []string{"INDI_CLEAR", "OBSERVER", "TELESCOP", "PROGRAM", "OBJECT", "FRAMEID"}; !slices.Equal(keywords, want) {
			t.Errorf("keywords %q, want %q", keywords, want)
		}
		if light := w.targetNow(t, "m42").Status.Exposure.FrameID; light == frameID {
			t.Errorf("the light frame took the centering frame's FRAMEID %s", frameID)
		}

		var asked worker.ExtractRequest
		w.worker.peers.set(func(f *fakePeers) { asked = f.extracts[0] })
		hint := asked.Hint
		// 206.265 × 5.2 µm / 480 mm is 2.2345 arcseconds per pixel.
		if asked.Frame != "east-main/east-tonight/_centering/"+frameID+".fits" || hint.Center != m42Position() || hint.Radius != 5 ||
			math.Abs(hint.ScaleLow-0.9*2.2345375) > 1e-6 || math.Abs(hint.ScaleHigh-1.1*2.2345375) > 1e-6 {
			t.Errorf("the worker got %+v", asked)
		}
		if got := w.worker.deletions(); !slices.Equal(got, []string{frameID}) {
			t.Errorf("deleted %q, want the centering frame %s", got, frameID)
		}
		if slices.ContainsFunc(w.worker.held(), func(p string) bool { return strings.Contains(p, "/_centering/") }) {
			t.Errorf("the outbox still holds a centering frame: %q", w.worker.held())
		}
	})
}

// With no filter of its own, a centering frame takes the first
// sequence's filter.
func TestACenteringFrameTakesTheFirstSequencesFilter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringNight(t, centeringOn("lab"), northOf(m42Position(), 12))
		waitUntil(t, w.inFlight(t, "m42"))
		if got := member(untilExposure(w), wheelINDI+".FILTER_SLOT", "FILTER_SLOT_VALUE"); got != "8" {
			t.Errorf("FILTER_SLOT %q, want 8, Luminance", got)
		}
	})
}

// A centering frame that the worker cannot delete stays in the outbox,
// where it costs only its space, and the centering goes on.
func TestACenteringFrameThatStaysDoesNotStopTheCentering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringWorld(t, centeringOn("lab"), northOf(m42Position(), 12))
		w.worker.set(func() { w.worker.refuseDeletes = true })
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := w.targetNow(t, "m42").Status.Centering; got == nil || got.Rounds != 1 {
			t.Errorf("status.centering %+v, want one round", got)
		}
	})
}
