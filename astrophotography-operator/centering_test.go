package main

// The fake solver returns a scripted center for each round of centering,
// usually closer to the target each round, as for a mount that each sync
// corrects.

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

// northOf returns a solution whose center is the given number of
// arcseconds north of a position, at the plate scale of a 480 mm
// tube.
func northOf(p sky.Position, arcseconds float64) solver.SolveResponse {
	return solver.SolveResponse{
		Solved: true, Center: &sky.Position{RA: p.RA, Dec: p.Dec + arcseconds/3600},
		Scale: 2.2345, Rotation: 180, Parity: solver.ParityNegative, Index: 4207,
	}
}

// centeringOn is a target's centering with the defaults that the CRD
// applies.
func centeringOn(solverName string) astrophotography.Centering {
	return astrophotography.Centering{Solver: solverName, Tolerance: 30, Exposure: "5s", Attempts: 5}
}

// centeringTarget is the target m42, with one Luminance frame, that
// centers as c says.
func centeringTarget(c astrophotography.Centering) *astrophotography.Target {
	x := target("m42", lights(1))
	x.Spec.Centering = &c
	return x
}

// centeringNight starts centeringWorld and makes the reservation Ready.
func centeringNight(t *testing.T, c astrophotography.Centering, solutions ...solver.SolveResponse) *runnerWorld {
	t.Helper()
	w := centeringWorld(t, c, solutions...)
	w.put(reservationsPath, reservationIn(observatory.ReservationReady))
	return w
}

// centeringWorld starts the operator with the target m42, which has one
// Luminance frame and centers as c says. The CCD reports 5.2 µm pixels,
// as the CCD simulator does after it connects, and the Solver lab is
// Ready. Each solve returns the next of solutions. The reservation is not
// Ready yet, so a test can set up fakes before the night starts.
func centeringWorld(t *testing.T, c astrophotography.Centering, solutions ...solver.SolveResponse) *runnerWorld {
	t.Helper()
	w := newWorld(t)
	w.indi.Report(ccdINDI, "CCD_INFO", "CCD_PIXEL_SIZE", "5.2")
	w.worker.peers.set(func(f *fakePeers) { f.reach, f.solutions = 0, solutions })
	w.api.put(solversPath, solverObject("lab", nil))
	w.api.put(podsPath, podFixture("lab-solver-0", "pod-0", workloadLabels(roleSolver, "lab"), true, ""))
	w.put(targetsPath, centeringTarget(c))
	w.configure = func(o *operator) { o.workerImage = testWorkerImage }
	w.playCamera(t, saves)
	w.start(t)
	waitUntil(t, func() bool {
		s, found := objectOf[astrophotography.Solver](t, w.api, solversPath, ns, "lab")
		return found && readyOf(s).Status == conditions.True
	})
	return w
}

// mountWrites returns the changes sent to the mount after the first
// write of EQUATORIAL_EOD_COORD, which starts the first slew, each as
// "<property> <member>=<value> ...".
func mountWrites(w *runnerWorld) []string {
	var out []string
	for _, change := range w.indi.Changes() {
		if rest, ok := strings.CutPrefix(change, mountINDI+"."); ok {
			out = append(out, rest)
		}
	}
	first := slices.IndexFunc(out, func(c string) bool { return strings.HasPrefix(c, "EQUATORIAL_EOD_COORD") })
	return out[first+1:]
}

// writeSummaries reduces each write to its property name. A write to
// ON_COORD_SET also gets the member it turns On.
func writeSummaries(writes []string) []string {
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

func syncs(w *runnerWorld) int {
	return len(slices.DeleteFunc(writeSummaries(mountWrites(w)), func(c string) bool { return c != "ON_COORD_SET SYNC=On" }))
}

// near reports whether the errors of the rounds match the scripted
// ones, within the 0.1 arcsecond that the runner rounds to.
func near(got, want []float64) bool {
	return slices.EqualFunc(got, want, func(a, b float64) bool { return math.Abs(a-b) <= 0.1 })
}

// The runner syncs and slews again after each round whose error is more
// than the tolerance. status.centering has the last round's solution and
// the error of every round.
func TestCenteringEndsWithinTheTolerance(t *testing.T) {
	cases := []struct {
		name    string
		errors  []float64
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
				var solutions []solver.SolveResponse
				for _, arcseconds := range c.errors {
					solutions = append(solutions, northOf(m42Position(), arcseconds))
				}
				w := centeringNight(t, centeringOn("lab"), solutions...)
				waitUntil(t, w.inFlight(t, "m42"))
				x := w.targetNow(t, "m42")
				imaging, _ := conditions.Find(x.Status.Conditions, astrophotography.ConditionImaging)
				if !strings.HasSuffix(imaging.Message, ", unguided because the telescope has no Guider, "+c.message) {
					t.Errorf("the Imaging message is %q, want it to end with %q", imaging.Message, c.message+".")
				}
				got := x.Status.Centering
				last := c.errors[len(c.errors)-1]
				if got == nil || got.Reservation != tonight || got.Rounds != c.rounds || got.Error == nil || math.Abs(*got.Error-last) > 0.1 {
					t.Fatalf("status.centering is %+v, want reservation %q, %d rounds, and an error near %g", got, tonight, c.rounds, last)
				}
				if want := (astrophotography.Solution{Center: astrophotography.Coordinates{RA: "05:35:17.3", Dec: sky.FormatDec(m42Position().Dec + last/3600)}, Rotation: 180, Scale: 2.2345}); *got.Solution != want {
					t.Errorf("solution %+v, want %+v", *got.Solution, want)
				}
				if !near(got.RoundErrors, c.errors) {
					t.Errorf("the errors of the rounds are %v, want %v", got.RoundErrors, c.errors)
				}
				if n := syncs(w); n != c.syncs {
					t.Errorf("the runner synced the mount %d times, want %d", n, c.syncs)
				}
				body := scrape(t, w.o.metrics)
				requireSample(t, body, "astrophotography_centering_rounds_count 1")
				requireSample(t, body, "astrophotography_centering_rounds_sum "+strconv.Itoa(int(c.rounds)))
				requireSample(t, body, `astrophotography_centering_error_arcseconds{namespace="observatory",target="m42"} `+strconv.FormatFloat(last, 'g', -1, 64))
			})
		})
	}
}

// The Imaging condition changes to Centering once, however many rounds
// the centering takes, so the night posts one Event for it.
func TestCenteringPostsOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringNight(t, centeringOn("lab"), northOf(m42Position(), 1200), northOf(m42Position(), 95), northOf(m42Position(), 10))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		want := []string{"Slewing", "Centering", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("the Events on the target are %q, want %q", got, want)
		}
	})
}

// The sync writes the solved center at the equinox of the date between
// ON_COORD_SET SYNC and TRACK, so the second write of the target is a
// slew and not a second sync.
func TestSyncPrecedesTheSecondSlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		solved := northOf(m42Position(), 1200)
		w := centeringNight(t, centeringOn("lab"), solved, northOf(m42Position(), 10))
		waitUntil(t, w.inFlight(t, "m42"))
		writes := mountWrites(w)
		want := []string{"ON_COORD_SET SYNC=On", "EQUATORIAL_EOD_COORD", "ON_COORD_SET TRACK=On", "EQUATORIAL_EOD_COORD"}
		if got := writeSummaries(writes); !slices.Equal(got, want) {
			t.Fatalf("the mount received %q, want %q", writes, want)
		}
		now := time.Now()
		for i, want := range []sky.Position{sky.ToDate(*solved.Center, now), sky.ToDate(m42Position(), now)} {
			ra, _ := strconv.ParseFloat(member(writes[2*i+1:2*i+2], "EQUATORIAL_EOD_COORD", "RA"), 64)
			dec, _ := strconv.ParseFloat(member(writes[2*i+1:2*i+2], "EQUATORIAL_EOD_COORD", "DEC"), 64)
			if sky.Separation(sky.Position{RA: ra * 15, Dec: dec}, want) > 1.0/3600 {
				t.Errorf("write %d set %f h, %f°, want %f h, %f°", 2*i+1, ra, dec, want.RAHours(), want.Dec)
			}
		}
	})
}

// alignmentModel responds to each write to EQUATORIAL_EOD_COORD, which
// the test holds back on the fake INDI server, the way the telescope simulator
// does. After a slew it reports the values sent. After a sync it reports
// a position 2 arcseconds east of the values sent.
type alignmentModel struct {
	w        *runnerWorld
	reported int
}

func (m *alignmentModel) report() {
	sync, writes := false, 0
	for _, change := range m.w.indi.Changes() {
		write, ok := strings.CutPrefix(change, mountINDI+".")
		switch {
		case !ok:
		case strings.HasPrefix(write, "ON_COORD_SET "):
			sync = member([]string{write}, "ON_COORD_SET", "SYNC") == "On"
		case strings.HasPrefix(write, "EQUATORIAL_EOD_COORD "):
			if writes++; writes <= m.reported {
				continue
			}
			m.reported = writes
			ra, _ := strconv.ParseFloat(member([]string{write}, "EQUATORIAL_EOD_COORD", "RA"), 64)
			if sync {
				ra += 2.0 / 3600 / 15
			}
			m.w.indi.Report(mountINDI, "EQUATORIAL_EOD_COORD", "RA", strconv.FormatFloat(ra, 'g', -1, 64))
			m.w.indi.Report(mountINDI, "EQUATORIAL_EOD_COORD", "DEC", member([]string{write}, "EQUATORIAL_EOD_COORD", "DEC"))
		}
	}
}

// A mount that reports a different position after a sync than the one
// sent still centers the target, because the sync ends on the mount's
// first report and does not time out waiting for the values sent.
func TestSyncEndsOnTheMountsFirstReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringWorld(t, centeringOn("lab"), northOf(m42Position(), 1200), northOf(m42Position(), 10))
		w.indi.Hold(mountINDI, "EQUATORIAL_EOD_COORD")
		model := &alignmentModel{w: w}
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		waitUntil(t, func() bool {
			model.report()
			imaging := w.imaging(t, "m42")
			return imaging == "True Exposing" || strings.HasPrefix(imaging, "False")
		})
		if got := w.imaging(t, "m42"); got != "True Exposing" {
			t.Fatalf("the Imaging condition is %q, want True Exposing: %s", got, w.imagingMessage(t, "m42"))
		}
		if n := syncs(w); n != 1 {
			t.Errorf("the runner synced the mount %d times, want 1", n)
		}
	})
}
