package main

// The operator's side of a plate solve of a centering frame in the
// outbox. The reservation's worker extracts the frame's stars into an
// .axy file, and the operator sends that file to the target's solver.
// So only the star list crosses the network, not the frame.
//
// A frame that does not solve at the first radius of the Solver's
// spec.search.radii is tried at each next radius, and then with no
// position hint when spec.search.blind is true. Each attempt extracts
// the stars again, because the worker writes the hint into the .axy
// file.

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

// A solve request has a longer timeout than an extraction because the
// solver runs one solve at a time. A request can wait behind the solves
// of other telescopes, and each of those can run for the solver's
// --timeout, two minutes by default.
const (
	extractTimeout = 2 * time.Minute
	solveTimeout   = 10 * time.Minute
)

// scaleTolerance is the width of the scale hint on each side of the
// expected plate scale, as a fraction of it.
const scaleTolerance = 0.1

type solveRequest struct {
	// Namespace is the namespace of the Reservation, the Target, and the
	// Solver.
	Namespace string
	// Reservation is the Reservation whose worker extracts the stars.
	Reservation string
	// Solver is the Target's spec.centering.solver.
	Solver string
	// Frame is outbox.Frame.Path.
	Frame string
	// Center is where the frame should point, in J2000.
	Center sky.Position
	// Scale is the expected plate scale in arcseconds per pixel:
	// 206.265 × pixel size in µm × binning / focal length in mm.
	Scale float64
}

// solveAttempt is one try at one search radius.
type solveAttempt struct {
	// Radius is the search radius in degrees, and 0 for a blind solve.
	Radius float64
	// Stars is the number of stars the worker found.
	Stars  int32
	Solved bool
	// Took runs from the request to the worker until the last response,
	// which is the worker's when it found no stars.
	Took time.Duration
}

// solveResult is the outcome of a solve. Response is the solver's
// response to the last attempt that reached the solver.
type solveResult struct {
	Response   solver.SolveResponse
	Stars      int32
	MedianFWHM float64
	Attempts   []solveAttempt
}

// errSolverNotReady is returned when the Solver does not exist or is
// not Ready. centering.go then images the target without centering it.
var errSolverNotReady = errors.New("the Solver is not Ready")

// plateSolve solves one frame. It returns an error when the Solver is
// not Ready or a request fails, and a result with Response.Solved false
// when no attempt solved. A frame with no stars ends the attempts at
// once, because a wider radius cannot find stars that are not there.
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

func (o *operator) attempt(ctx context.Context, result *solveResult, workerURL, solverURL string, request worker.ExtractRequest) (solveAttempt, error) {
	started := time.Now()
	attempt := solveAttempt{Radius: request.Hint.Radius}
	var extracted worker.ExtractResponse
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
	var response solver.SolveResponse
	err := o.peer(solveCtx, http.MethodPost, solverURL, starList(extracted.AXY), &response)
	attempt.Took = time.Since(started)
	if err != nil {
		return attempt, fmt.Errorf("solving the stars of %s: %w", request.Frame, err)
	}
	attempt.Solved = response.Solved
	result.Response = response
	outcome := solveUnsolved
	if response.Solved {
		outcome = solveSolved
	}
	o.metrics.observeSolve(outcome, time.Since(solveStarted))
	return attempt, nil
}

// searchRadii returns the radius of each attempt in order, with 0 for
// the blind attempt. The CRD requires at least one radius.
func searchRadii(search astrophotography.Search) []float64 {
	radii := search.Radii
	if search.Blind {
		radii = append(radii[:len(radii):len(radii)], 0)
	}
	return radii
}

func (o *operator) readySolver(namespace, name string) (*astrophotography.Solver, error) {
	s, found := informer.Cached[astrophotography.Solver](o.stores.collections[astrophotography.SolverKind.Name].View(), namespace+"/"+name)
	if !found {
		return nil, fmt.Errorf("%w: no Solver %s in namespace %s", errSolverNotReady, name, namespace)
	}
	ready, _ := conditions.Find(s.Status.Conditions, astrophotography.ConditionReady)
	if ready.Status != conditions.True || s.Status.Endpoint == nil {
		return nil, fmt.Errorf("%w: Solver %s/%s is not Ready True or has no status.endpoint, reason %q: %s", errSolverNotReady, namespace, name, ready.Reason, ready.Message)
	}
	return s, nil
}
