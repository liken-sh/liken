package main

// Fakes of the operator's own pods, the solvers and the workers, behind
// its peer client. One handler answers every host, and tells a solver
// from a worker by the host's name, the way the cluster's DNS does. A
// worker answers each extraction with a star list that holds the hint
// it was given, and a solver solves that list when the hint's radius
// reaches the field: the frame of the CCD simulator whose header
// pointed 6.8° from its stars, which needs 10°.

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
)

// labIndexes is what the lab's solver reads from its claim.
var labIndexes = solver.Indexes{
	{ID: 4207, Files: 12, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}},
	{ID: 4208, Files: 1, ScaleArcmin: astrophotography.ScaleRange{Low: 30, High: 42}},
}

// The solution of the fixture's frame (plan 02).
var simulatorSolution = solver.SolveAnswer{
	Solved: true, Center: &sky.Position{RA: 29.6070, Dec: 59.8193}, Scale: 2.6815, Rotation: 180, Parity: solver.ParityNegative, Index: 4207,
}

type fakePeers struct {
	mu sync.Mutex
	// indexes is every solver's index list.
	indexes solver.Indexes
	// refuseIndexes counts the next index lists that answer 503.
	refuseIndexes int
	// reach is the distance, in degrees, from the hint to the stars: a
	// hint whose radius is at least this solves, and so does a blind
	// one. Below zero, no hint solves.
	reach float64
	// answers holds the solutions of the next solves that reach, in
	// order, as a mount that a centering moves gives a new center each
	// round. With none left, a solve that reaches answers the fixture's
	// frame.
	answers []solver.SolveAnswer
	// solved runs after each solve that reaches, such as a test's
	// delete of the Solver in the middle of a centering.
	solved func()
	// stars is the count of stars each frame holds.
	stars int32
	// failExtract makes every extraction answer 500, and failSolve every
	// solve.
	failExtract bool
	failSolve   bool
	// requests holds each request's method, host, and path, in order.
	requests []string
	// extracts holds each extraction request, in order.
	extracts []worker.ExtractRequest
}

func newFakePeers() *fakePeers {
	return &fakePeers{indexes: labIndexes, reach: 6.8, stars: 1675}
}

func (f *fakePeers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.Host+r.URL.Path)
	switch {
	case strings.Contains(r.Host, "-solver.") && r.URL.Path == solver.PathIndexes:
		if f.refuseIndexes > 0 {
			f.refuseIndexes--
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(f.indexes)
	case strings.Contains(r.Host, "-solver.") && r.URL.Path == solver.PathSolve && f.failSolve:
		http.Error(w, "astrometry-engine: exit status 1: Failed to load index file", http.StatusInternalServerError)
	case strings.Contains(r.Host, "-solver.") && r.URL.Path == solver.PathSolve:
		var hint worker.Hint
		_ = json.NewDecoder(r.Body).Decode(&hint)
		answer := solver.SolveAnswer{}
		if f.reach >= 0 && (hint.Radius == 0 || hint.Radius >= f.reach) {
			answer = simulatorSolution
			if len(f.answers) > 0 {
				answer, f.answers = f.answers[0], f.answers[1:]
			}
			if f.solved != nil {
				f.solved()
			}
		}
		_ = json.NewEncoder(w).Encode(answer)
	case strings.Contains(r.Host, "-astrophotography.") && r.URL.Path == worker.PathExtract:
		var request worker.ExtractRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		f.extracts = append(f.extracts, request)
		if f.failExtract {
			http.Error(w, "source-extractor: exit status 1: cannot open the frame", http.StatusInternalServerError)
			return
		}
		axy, _ := json.Marshal(request.Hint)
		answer := worker.ExtractAnswer{AXY: axy, Stars: f.stars, MedianFWHM: 4.93}
		if f.stars == 0 {
			answer.AXY = nil
		}
		_ = json.NewEncoder(w).Encode(answer)
	default:
		http.NotFound(w, r)
	}
}

// count answers how many requests reached one host and path.
func (f *fakePeers) count(request string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r == request {
			n++
		}
	}
	return n
}

func (f *fakePeers) set(change func(f *fakePeers)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}
