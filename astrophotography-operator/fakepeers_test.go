package main

// fakePeers fakes the solver and worker pods that the operator calls
// through its HTTP client. One handler serves every host and picks the
// peer by the host name. A solver returns its index list only to a
// request for a pod IP address in 10.42.0.0/16, because a real Service
// routes no requests to a pod that the endpoints controller has not
// added yet.
//
// The worker's fake extraction returns the hint it was given as its
// star list, so the solver's fake can read the hint back. The default
// reach of 6.8 degrees is how far the header of the CCD simulator's
// frame points from its stars.

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

// labIndexes is the index list of the solver in the lab fleet.
var labIndexes = solver.Indexes{
	{ID: 4207, Files: 12, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}},
	{ID: 4208, Files: 1, ScaleArcmin: astrophotography.ScaleRange{Low: 30, High: 42}},
}

// simulatorSolution is the solution of the CCD simulator's frame.
var simulatorSolution = solver.SolveResponse{
	Solved: true, Center: &sky.Position{RA: 29.6070, Dec: 59.8193}, Scale: 2.6815, Rotation: 180, Parity: solver.ParityNegative, Index: 4207,
}

type fakePeers struct {
	mu sync.Mutex
	// indexes is the index list that every solver returns. set closes
	// changed on each call, so each open watch request sends the list
	// again.
	indexes solver.Indexes
	changed chan struct{}
	// refuseIndexes is how many more index list requests get a 503.
	refuseIndexes int
	// endIndexes ends each watch request after one list, as a broken
	// connection does.
	endIndexes bool
	// reach is the distance in degrees from the hint to the stars. A
	// solve succeeds when the hint's radius is at least reach, or when
	// there is no hint. With a negative reach, no solve succeeds.
	reach float64
	// solutions are the solutions for the next successful solves, in
	// order, as when centering moves the mount and each round finds a
	// new center. When none are left, a successful solve returns
	// simulatorSolution.
	solutions []solver.SolveResponse
	// solved runs after each successful solve. A test uses it to delete
	// the Solver in the middle of a centering.
	solved func()
	// stars is the number of stars that each extraction finds.
	stars int32
	// failExtract makes every extraction return 500, and failSolve does
	// the same for every solve.
	failExtract bool
	failSolve   bool
	// requests records the method, host, and path of each request.
	requests []string
	extracts []worker.ExtractRequest
}

func newFakePeers() *fakePeers {
	return &fakePeers{indexes: labIndexes, changed: make(chan struct{}), reach: 6.8, stars: 1675}
}

func (f *fakePeers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.Host+r.URL.Path)
	if strings.HasPrefix(r.Host, "10.42.") && r.URL.Path == solver.PathIndexes && f.refuseIndexes == 0 {
		f.mu.Unlock()
		f.followIndexes(w, r)
		return
	}
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.Host, "10.42.") && r.URL.Path == solver.PathIndexes:
		f.refuseIndexes--
		http.Error(w, "starting", http.StatusServiceUnavailable)
	case strings.Contains(r.Host, "-solver.") && r.URL.Path == solver.PathSolve && f.failSolve:
		http.Error(w, "astrometry-engine: exit status 1: Failed to load index file", http.StatusInternalServerError)
	case strings.Contains(r.Host, "-solver.") && r.URL.Path == solver.PathSolve:
		var hint worker.Hint
		_ = json.NewDecoder(r.Body).Decode(&hint)
		response := solver.SolveResponse{}
		if f.reach >= 0 && (hint.Radius == 0 || hint.Radius >= f.reach) {
			response = simulatorSolution
			if len(f.solutions) > 0 {
				response, f.solutions = f.solutions[0], f.solutions[1:]
			}
			if f.solved != nil {
				f.solved()
			}
		}
		_ = json.NewEncoder(w).Encode(response)
	case strings.Contains(r.Host, "-astrophotography.") && r.URL.Path == worker.PathExtract:
		var request worker.ExtractRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		f.extracts = append(f.extracts, request)
		if f.failExtract {
			http.Error(w, "source-extractor: exit status 1: cannot open the frame", http.StatusInternalServerError)
			return
		}
		axy, _ := json.Marshal(request.Hint)
		response := worker.ExtractResponse{AXY: axy, Stars: f.stars, MedianFWHM: 4.93}
		if f.stars == 0 {
			response.AXY = nil
		}
		_ = json.NewEncoder(w).Encode(response)
	default:
		http.NotFound(w, r)
	}
}

// followIndexes sends the list, and again after each change, until the
// client disconnects, as a solver pod does.
func (f *fakePeers) followIndexes(w http.ResponseWriter, r *http.Request) {
	encoder := json.NewEncoder(w)
	for {
		f.mu.Lock()
		indexes, changed, end := f.indexes, f.changed, f.endIndexes
		f.mu.Unlock()
		_ = encoder.Encode(indexes)
		_ = http.NewResponseController(w).Flush()
		if end {
			return
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		}
	}
}

// count returns how many requests went to one host and path.
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
	close(f.changed)
	f.changed = make(chan struct{})
}
