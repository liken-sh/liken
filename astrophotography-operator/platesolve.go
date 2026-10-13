package main

// The operator's side of a plate solve, which the runner's centering
// calls with a frame in the outbox (plan 02). The operator asks the
// reservation's worker for the frame's star list with a hint, and
// carries the list to the target's solver, so the worker and the
// solver never talk to each other and only kilobytes cross the
// network.
//
// A frame that does not solve within the first radius is tried again
// with each wider radius of the Solver's spec.search, and then with no
// position at all when spec.search.blind is true. Plan 02's default
// radius of 5° missed a frame of the CCD simulator whose header pointed
// 6.8° from the stars it drew, and 10° solved it. Each attempt asks the
// worker for a new star list, because the hint is in the .axy, and an
// extraction took 0.26 s.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The bounds of each request of a solve. An extraction took 0.26 s in
// plan 02. A solve waits its turn behind other telescopes' solves, each
// bounded by the solver's wall time of two minutes.
const (
	extractTimeout = 2 * time.Minute
	solveTimeout   = 10 * time.Minute
)

// scaleTolerance is the width of the scale hint around the expected
// plate scale, ±10% (plan 02).
const scaleTolerance = 0.1

// solveRequest is one frame to solve.
type solveRequest struct {
	// Namespace holds the Reservation, the Target, and the Solver.
	Namespace string
	// Reservation names the reservation whose worker reads the frame.
	Reservation string
	// Solver names the Target's spec.centering.solver.
	Solver string
	// Frame is the frame's path relative to the outbox's root
	// (outbox.Frame.Path).
	Frame string
	// Center is where the operator expects the frame to point, J2000.
	Center sky.Position
	// Scale is the expected plate scale, in arcseconds per pixel: 206.265
	// × the pixel size in µm × the binning / the focal length in mm.
	Scale float64
}

// solveAttempt is one attempt of a solve, for the log and the status.
type solveAttempt struct {
	// Radius is the search radius in degrees, and 0 for a blind solve.
	Radius float64
	// Stars counts the stars the worker found.
	Stars int32
	// Solved is true for the attempt that solved.
	Solved bool
	// Took is the attempt's time, from the request to the worker to the
	// solver's answer.
	Took time.Duration
}

// solveResult is the outcome of a solve: the solution of the attempt
// that solved, or an answer with Solved false when none did.
type solveResult struct {
	Answer     solver.SolveAnswer
	Stars      int32
	MedianFWHM float64
	Attempts   []solveAttempt
}

// errSolverNotReady is the error of a solve whose Solver does not
// exist or is not Ready. The runner images the target without
// centering it (centering.go).
var errSolverNotReady = errors.New("the Solver is not Ready")

// plateSolve solves one frame. It answers an error when the Solver is
// not Ready or a request fails, and a result whose answer has Solved
// false when no attempt solved. A frame with no star ends the attempts
// at once, because every attempt reads the same stars.
func (o *operator) plateSolve(ctx context.Context, request solveRequest) (solveResult, error) {
	s, err := o.readySolver(request.Namespace, request.Solver)
	if err != nil {
		return solveResult{}, err
	}
	solverURL := fmt.Sprintf("http://%s:%d%s", s.Status.Endpoint.Host, s.Status.Endpoint.Port, solver.PathSolve)
	var result solveResult
	for _, radius := range searchRadii(s.Spec.Search) {
		hint := worker.Hint{
			Center: request.Center, Radius: radius,
			ScaleLow: request.Scale * (1 - scaleTolerance), ScaleHigh: request.Scale * (1 + scaleTolerance),
		}
		attempt, err := o.attempt(ctx, &result, workerURL(request.Namespace, request.Reservation, worker.PathExtract).String(), solverURL,
			worker.ExtractRequest{Frame: request.Frame, Hint: hint})
		if err != nil {
			o.metrics.observeSolve(solveFailed, attempt.Took)
			return result, err
		}
		result.Attempts = append(result.Attempts, attempt)
		fmt.Fprintf(o.logs, "%s: solving %s in %s with a radius of %g degrees: %d stars, solved %v, in %s\n",
			managedBy, request.Frame, request.Namespace, radius, attempt.Stars, attempt.Solved, attempt.Took.Round(time.Millisecond))
		if attempt.Solved || attempt.Stars == 0 {
			break
		}
	}
	return result, nil
}

// attempt runs one extraction and, when the frame has stars, one solve.
func (o *operator) attempt(ctx context.Context, result *solveResult, workerURL, solverURL string, request worker.ExtractRequest) (solveAttempt, error) {
	started := time.Now()
	attempt := solveAttempt{Radius: request.Hint.Radius}
	var extracted worker.ExtractAnswer
	extractCtx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	if err := o.peer(extractCtx, http.MethodPost, workerURL, jsonBody(request), &extracted); err != nil {
		attempt.Took = time.Since(started)
		return attempt, fmt.Errorf("extracting the stars of %s: %w", request.Frame, err)
	}
	attempt.Stars = extracted.Stars
	result.Stars, result.MedianFWHM = extracted.Stars, extracted.MedianFWHM
	if extracted.Stars == 0 {
		attempt.Took = time.Since(started)
		return attempt, nil
	}
	solveStarted := time.Now()
	solveCtx, cancelSolve := context.WithTimeout(ctx, solveTimeout)
	defer cancelSolve()
	var answer solver.SolveAnswer
	err := o.peer(solveCtx, http.MethodPost, solverURL, starList(extracted.AXY), &answer)
	attempt.Took = time.Since(started)
	if err != nil {
		return attempt, fmt.Errorf("solving the stars of %s: %w", request.Frame, err)
	}
	attempt.Solved = answer.Solved
	result.Answer = answer
	outcome := solveUnsolved
	if answer.Solved {
		outcome = solveSolved
	}
	o.metrics.observeSolve(outcome, time.Since(solveStarted))
	return attempt, nil
}

// searchRadii answers the radius of each attempt in order, and 0 for
// the blind one. The CRD fills in the Solver's search, so the stored
// Solver always names at least one radius.
func searchRadii(search astrophotography.Search) []float64 {
	radii := search.Radii
	if search.Blind {
		radii = append(radii[:len(radii):len(radii)], 0)
	}
	return radii
}

// readySolver answers a Solver whose Ready is True, from the store.
func (o *operator) readySolver(namespace, name string) (*astrophotography.Solver, error) {
	s, held := informer.Cached[astrophotography.Solver](o.stores.collections[astrophotography.SolverKind.Name].View(), namespace+"/"+name)
	if !held {
		return nil, fmt.Errorf("%w: no Solver %s in %s", errSolverNotReady, name, namespace)
	}
	ready, _ := conditions.Find(s.Status.Conditions, astrophotography.ConditionReady)
	if ready.Status != conditions.True || s.Status.Endpoint == nil {
		return nil, fmt.Errorf("%w: Solver %s/%s has no Ready True, and its reason is %q: %s", errSolverNotReady, namespace, name, ready.Reason, ready.Message)
	}
	return s, nil
}
