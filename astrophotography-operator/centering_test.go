package main

// Centering after the slew (plan 02): the rounds of a solve, a sync,
// and a slew again, until the solved center is within the target's
// tolerance. The fake solver answers each round with a center a test
// scripts, the way a mount that the sync corrects lands closer each
// round.

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// m42Position is the J2000 position of the tests' target.
func m42Position() sky.Position {
	p, _ := targetPosition(target("m42"))
	return p
}

// northOf answers a solution whose center is some arcseconds north of
// a position, at the plate scale of the session's 480 mm tube.
func northOf(p sky.Position, arcseconds float64) solver.SolveAnswer {
	return solver.SolveAnswer{
		Solved: true, Center: &sky.Position{RA: p.RA, Dec: p.Dec + arcseconds/3600},
		Scale: 2.2345, Rotation: 180, Parity: solver.ParityNegative, Index: 4207,
	}
}

// centeringOn is a target's centering as the API server stores it,
// with the CRD's defaults.
func centeringOn(solverName string) astrophotography.Centering {
	return astrophotography.Centering{Solver: solverName, Tolerance: 30, Exposure: "5s", Attempts: 5}
}

// centeringTarget is the target m42, of one Luminance frame, that
// centers as c states.
func centeringTarget(c astrophotography.Centering) *astrophotography.Target {
	x := target("m42", lights(1))
	x.Spec.Centering = &c
	return x
}

// centeringNight starts a night whose target m42, of one Luminance
// frame, centers with Solver lab, and makes the reservation Ready.
func centeringNight(t *testing.T, c astrophotography.Centering, answers ...solver.SolveAnswer) *night {
	t.Helper()
	w := centeringWorld(t, c, answers...)
	w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
	return w
}

// centeringWorld starts the operator with the target m42, of one
// Luminance frame, that centers as c states. The CCD reports pixels of
// 5.2 µm, as the simulator does after it connects, and the Solver lab
// is Ready. Each solve answers the next of answers. The test makes the
// reservation Ready.
func centeringWorld(t *testing.T, c astrophotography.Centering, answers ...solver.SolveAnswer) *night {
	t.Helper()
	w := newWorld(t)
	w.indi.Report(ccdINDI, "CCD_INFO", "CCD_PIXEL_SIZE", "5.2")
	w.worker.peers.set(func(f *fakePeers) { f.reach, f.answers = 0, answers })
	w.api.put(solversPath, solverObject("lab", nil))
	w.api.put(podsPath, podFixture("lab-solver-0", "pod-0", workloadLabels(roleSolver, "lab"), true, ""))
	w.put(targetsPath, centeringTarget(c))
	w.configure = func(o *operator) { o.workerImage = testWorkerImage }
	w.playCamera(t, saves)
	w.start(t)
	waitUntil(t, func() bool {
		s, held := objectOf[astrophotography.Solver](t, w.api, solversPath, ns, "lab")
		return held && readyOf(s).Status == conditions.True
	})
	return w
}

// mountWrites answers the mount's changes after the first slew, as
// "<property> <member>=<value> ...".
func mountWrites(w *night) []string {
	var out []string
	for _, change := range w.indi.Changes() {
		if rest, ok := strings.CutPrefix(change, mountINDI+"."); ok {
			out = append(out, rest)
		}
	}
	first := slices.IndexFunc(out, func(c string) bool { return strings.HasPrefix(c, "EQUATORIAL_EOD_COORD") })
	return out[first+1:]
}

// shapes answers each write as its property, with the switch it turns
// On for a write to ON_COORD_SET.
func shapes(writes []string) []string {
	var out []string
	for _, write := range writes {
		name, _, _ := strings.Cut(write, " ")
		for _, on := range []string{"SYNC", "TRACK", "SLEW"} {
			if name == "ON_COORD_SET" && member([]string{write}, name, on) == "On" {
				name += " " + on + "=On"
			}
		}
		out = append(out, name)
	}
	return out
}

func syncs(w *night) int {
	return len(slices.DeleteFunc(shapes(mountWrites(w)), func(c string) bool { return c != "ON_COORD_SET SYNC=On" }))
}

// near reports whether the errors of the rounds are the scripted ones,
// within the tenth of an arcsecond that the runner rounds to.
func near(got, want []float64) bool {
	return slices.EqualFunc(got, want, func(a, b float64) bool { return math.Abs(a-b) <= 0.1 })
}

// The runner centers until the error is within the tolerance, syncs
// and slews again after each round outside it, and records the last
// round in status.centering with the error of each round.
func TestCenteringEndsWithinTheTolerance(t *testing.T) {
	cases := []struct {
		name    string
		answers []float64
		rounds  int32
		syncs   int
		message string
	}{
		{"the first round", []float64{12}, 1, 0, "centered within 12.0 arcseconds in 1 round"},
		{"the third round", []float64{1200, 95, 10}, 3, 2, "centered within 10.0 arcseconds in 3 rounds"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var answers []solver.SolveAnswer
				for _, arcseconds := range c.answers {
					answers = append(answers, northOf(m42Position(), arcseconds))
				}
				w := centeringNight(t, centeringOn("lab"), answers...)
				waitUntil(t, w.inFlight(t, "m42"))
				x := w.targetNow(t, "m42")
				imaging, _ := conditions.Find(x.Status.Conditions, astrophotography.ConditionImaging)
				if !strings.HasSuffix(imaging.Message, ", unguided because the telescope has no Guider, "+c.message+".") {
					t.Errorf("Imaging says %q", imaging.Message)
				}
				got := x.Status.Centering
				last := c.answers[len(c.answers)-1]
				if got == nil || got.Reservation != tonight || got.Rounds != c.rounds || got.Error == nil || math.Abs(*got.Error-last) > 0.1 {
					t.Fatalf("status.centering %+v", got)
				}
				if want := (astrophotography.Solution{Center: astrophotography.Coordinates{RA: "05:35:17.3", Dec: sky.FormatDec(m42Position().Dec + last/3600)}, Rotation: 180, Scale: 2.2345}); *got.Solution != want {
					t.Errorf("solution %+v, want %+v", *got.Solution, want)
				}
				if !near(got.RoundErrors, c.answers) {
					t.Errorf("the errors of the rounds are %v, want %v", got.RoundErrors, c.answers)
				}
				if n := syncs(w); n != c.syncs {
					t.Errorf("%d syncs, want %d", n, c.syncs)
				}
				body := scrape(t, w.o.metrics)
				requireSample(t, body, "astrophotography_centering_rounds_count 1")
				requireSample(t, body, "astrophotography_centering_rounds_sum "+strconv.Itoa(int(c.rounds)))
				requireSample(t, body, `astrophotography_centering_error_arcseconds{namespace="observatory",target="m42"} `+strconv.FormatFloat(last, 'g', -1, 64))
			})
		})
	}
}

// The Imaging condition turns to Centering once, however many rounds
// the centering takes, so the night posts one Event for it.
func TestCenteringPostsOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringNight(t, centeringOn("lab"), northOf(m42Position(), 1200), northOf(m42Position(), 95), northOf(m42Position(), 10))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		want := []string{"Slewing", "Centering", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("Events on the target %q, want %q", got, want)
		}
	})
}

// A sync tells the mount that it points at the solved center: the
// runner sets ON_COORD_SET to SYNC, writes the solved center at the
// equinox of the date, and sets ON_COORD_SET back to TRACK before it
// slews to the target again, so the slew is no second sync.
func TestASyncComesBeforeTheSlewAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		solved := northOf(m42Position(), 1200)
		w := centeringNight(t, centeringOn("lab"), solved, northOf(m42Position(), 10))
		waitUntil(t, w.inFlight(t, "m42"))
		writes := mountWrites(w)
		want := []string{"ON_COORD_SET SYNC=On", "EQUATORIAL_EOD_COORD", "ON_COORD_SET TRACK=On", "EQUATORIAL_EOD_COORD"}
		if got := shapes(writes); !slices.Equal(got, want) {
			t.Fatalf("the mount got %q, want %q", writes, want)
		}
		now := time.Now()
		for i, want := range []sky.Position{sky.ToDate(*solved.Center, now), sky.ToDate(m42Position(), now)} {
			ra, _ := strconv.ParseFloat(member(writes[2*i+1:2*i+2], "EQUATORIAL_EOD_COORD", "RA"), 64)
			dec, _ := strconv.ParseFloat(member(writes[2*i+1:2*i+2], "EQUATORIAL_EOD_COORD", "DEC"), 64)
			if sky.Separation(sky.Position{RA: ra * 15, Dec: dec}, want) > 1.0/3600 {
				t.Errorf("write %d went to %f h, %f°, want %f h, %f°", 2*i+1, ra, dec, want.RAHours(), want.Dec)
			}
		}
	})
}

// alignmentModel answers the writes of the mount's EQUATORIAL_EOD_COORD,
// which the test holds, the way the telescope simulator does: a slew
// with the values sent, and a sync with the position that its alignment
// model computes from the sync, 2 arcseconds east of the values sent. A
// mount reports where it computes that it points, so its answer to a
// sync need not carry the values sent.
type alignmentModel struct {
	w        *night
	answered int
}

// answer reports the position of each write that it has not answered.
func (m *alignmentModel) answer() {
	sync, writes := false, 0
	for _, change := range m.w.indi.Changes() {
		write, ok := strings.CutPrefix(change, mountINDI+".")
		switch {
		case !ok:
		case strings.HasPrefix(write, "ON_COORD_SET "):
			sync = member([]string{write}, "ON_COORD_SET", "SYNC") == "On"
		case strings.HasPrefix(write, "EQUATORIAL_EOD_COORD "):
			if writes++; writes <= m.answered {
				continue
			}
			m.answered = writes
			ra, _ := strconv.ParseFloat(member([]string{write}, "EQUATORIAL_EOD_COORD", "RA"), 64)
			if sync {
				ra += 2.0 / 3600 / 15
			}
			m.w.indi.Report(mountINDI, "EQUATORIAL_EOD_COORD", "RA", strconv.FormatFloat(ra, 'g', -1, 64))
			m.w.indi.Report(mountINDI, "EQUATORIAL_EOD_COORD", "DEC", member([]string{write}, "EQUATORIAL_EOD_COORD", "DEC"))
		}
	}
}

// A sync ends on the mount's answer, whatever position the answer
// carries, so a mount with an alignment model, such as the telescope
// simulator, centers the target instead of failing the sync after the
// limit of a setting.
func TestASyncEndsOnTheMountsAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringWorld(t, centeringOn("lab"), northOf(m42Position(), 1200), northOf(m42Position(), 10))
		w.indi.Hold(mountINDI, "EQUATORIAL_EOD_COORD")
		model := &alignmentModel{w: w}
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		waitUntil(t, func() bool {
			model.answer()
			imaging := w.imaging(t, "m42")
			return imaging == "True Exposing" || strings.HasPrefix(imaging, "False")
		})
		if got := w.imaging(t, "m42"); got != "True Exposing" {
			t.Fatalf("Imaging is %q: %s", got, w.imagingMessage(t, "m42"))
		}
		if n := syncs(w); n != 1 {
			t.Errorf("%d syncs, want 1", n)
		}
	})
}
