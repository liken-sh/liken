package main

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
)

// centeringFrame is the request of a centering frame of the CCD simulator:
// the header's position, 6.8° from the stars it drew, and the plate
// scale of a 400 mm tube with pixels of 5.2 µm.
var centeringFrame = solveRequest{
	Namespace: "observatory", Reservation: "tonight", Solver: "lab",
	Frame:  "east-main/tonight/_centering/0f3a.fits",
	Center: sky.Position{RA: 35.63, Dec: 53.82},
	Scale:  2.68,
}

// readySolver starts the operator with a Ready Solver lab, whose search
// is given, and a reservation's worker behind the fake peers.
func readySolverWorld(t *testing.T, search map[string]any) *operatorWorld {
	t.Helper()
	w := startWorkloads(t, nil)
	spec := map[string]any{}
	if search != nil {
		spec["search"] = search
	}
	w.api.put(solversPath, solverObject("lab", spec))
	w.readySolverPod("lab-solver-0", "pod-0")
	settle()
	return w
}

// radiiOf answers the radius of each attempt.
func radiiOf(result solveResult) []float64 {
	var out []float64
	for _, a := range result.Attempts {
		out = append(out, a.Radius)
	}
	return out
}

// The ladder widens the search while the frame does not solve: the
// header's position is 6.8° from the stars, so 5° misses and 10°
// solves.
func TestASolveWidensTheSearchUntilTheFrameSolves(t *testing.T) {
	cases := []struct {
		name   string
		search map[string]any
		reach  float64
		radii  []float64
		solved bool
	}{
		{"the first radius reaches", nil, 3, []float64{5}, true},
		{"the second radius reaches", nil, 6.8, []float64{5, 10}, true},
		{"only a blind solve reaches", nil, 20, []float64{5, 10, 0}, true},
		{"no blind solve", map[string]any{"radii": []any{5.0, 10.0}, "blind": false}, 20, []float64{5, 10}, false},
		{"radii of the Solver's own", map[string]any{"radii": []any{2.0, 8.0}, "blind": false}, 6.8, []float64{2, 8}, true},
		{"nothing solves", nil, -1, []float64{5, 10, 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := readySolverWorld(t, c.search)
				w.peers.set(func(f *fakePeers) { f.reach = c.reach })
				result, err := w.o.plateSolve(t.Context(), centeringFrame)
				if err != nil || !slices.Equal(radiiOf(result), c.radii) || result.Answer.Solved != c.solved {
					t.Errorf("attempts %v, solved %v, %v; want %v, solved %v", radiiOf(result), result.Answer.Solved, err, c.radii, c.solved)
				}
			})
		})
	}
}

// The worker gets the frame, the expected center, the radius, and the
// plate scale ±10%, and the answer carries the solution and what the
// extraction measured.
func TestASolveCarriesTheHintAndTheSolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := readySolverWorld(t, nil)
		w.peers.set(func(f *fakePeers) { f.reach = 3 })
		result, err := w.o.plateSolve(t.Context(), centeringFrame)
		if err != nil {
			t.Fatal(err)
		}
		var request worker.ExtractRequest
		w.peers.set(func(f *fakePeers) { request = f.extracts[0] })
		hint := request.Hint
		if request.Frame != centeringFrame.Frame || hint.Center != centeringFrame.Center || hint.Radius != 5 ||
			math.Abs(hint.ScaleLow-2.412) > 1e-9 || math.Abs(hint.ScaleHigh-2.948) > 1e-9 {
			t.Errorf("the worker got %+v", request)
		}
		if result.Answer.Center.RA != 29.6070 || result.Stars != 1675 || result.MedianFWHM != 4.93 || !result.Attempts[0].Solved {
			t.Errorf("result %+v", result)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_solves_total{result="solved"} 1`)
		if !strings.Contains(w.logs.String(), "solving "+centeringFrame.Frame+" in observatory with a radius of 5 degrees: 1675 stars, solved true") {
			t.Errorf("log %q", w.logs.String())
		}
	})
}

// A frame with no star cannot solve at any radius, so the operator asks
// the solver nothing.
func TestAFrameWithNoStarIsNotSolved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := readySolverWorld(t, nil)
		w.peers.set(func(f *fakePeers) { f.stars = 0 })
		result, err := w.o.plateSolve(t.Context(), centeringFrame)
		if err != nil || len(result.Attempts) != 1 || result.Answer.Solved || w.peers.count("POST lab-solver.observatory.svc:8080/solve") != 0 {
			t.Errorf("result %+v, %v", result, err)
		}
	})
}

func TestASolveFails(t *testing.T) {
	cases := []struct {
		name    string
		request solveRequest
		change  func(f *fakePeers)
		want    string
		ready   bool
	}{
		{"a Solver that does not exist", solveRequest{Namespace: "observatory", Solver: "other"}, nil, "no Solver other", true},
		{"a worker that fails", centeringFrame, func(f *fakePeers) { f.failExtract = true }, "cannot open the frame", false},
		{"a solver that fails", centeringFrame, func(f *fakePeers) { f.failSolve = true }, "Failed to load index file", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := readySolverWorld(t, nil)
				if c.change != nil {
					w.peers.set(c.change)
				}
				_, err := w.o.plateSolve(t.Context(), c.request)
				if err == nil || !strings.Contains(err.Error(), c.want) || errors.Is(err, errSolverNotReady) != c.ready {
					t.Errorf("answered %v, want an error with %q", err, c.want)
				}
			})
		})
	}
}

// A Solver that is not Ready answers errSolverNotReady with its reason,
// so the runner's CenteringFailed says why.
func TestASolverThatIsNotReadyRefusesASolve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		settle()
		_, err := w.o.plateSolve(t.Context(), centeringFrame)
		if !errors.Is(err, errSolverNotReady) || !strings.Contains(err.Error(), "PodNotReady") {
			t.Errorf("answered %v", err)
		}
	})
}

func TestTheSearchOfASolver(t *testing.T) {
	cases := []struct {
		name   string
		blind  bool
		radii  []float64
		expect []float64
	}{
		{"the CRD's default", true, []float64{5, 10}, []float64{5, 10, 0}},
		{"no blind solve", false, []float64{3}, []float64{3}},
	}
	for _, c := range cases {
		got := searchRadii(astrophotography.Search{Radii: c.radii, Blind: c.blind})
		if !slices.Equal(got, c.expect) {
			t.Errorf("%s: %v, want %v", c.name, got, c.expect)
		}
	}
}
