package solver

// The HTTP server of the solver pod. It runs one astrometry engine at a
// time, because two engines at once would only compete for the same
// CPU. A solve request waits in a queue for its turn, and leaves the
// queue when its client disconnects.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxStarList is the largest request body that a solve accepts. An .axy
// file takes about 30 bytes for each star, so the limit allows more than
// 100,000 stars.
const maxStarList = 4 << 20

// solve runs the engine on one star list. Tests pass a fake.
type solve func(ctx context.Context, axy []byte) (SolveResponse, error)

type Server struct {
	solve solve
	set   *indexSet
	// turn is a semaphore with one slot, full while the engine runs. It
	// is a channel and not a mutex, so a request can stop waiting when
	// its client disconnects.
	turn   chan struct{}
	logs   io.Writer
	failed chan error
	done   chan struct{}
}

func newServer(s solve, set *indexSet, logs io.Writer) *Server {
	return &Server{solve: s, set: set, turn: make(chan struct{}, 1), logs: logs, failed: make(chan error, 1), done: make(chan struct{})}
}

// Failed receives the error when the kernel refuses the watch of the
// index directory, or when the engine configuration cannot be written.
// The index set no longer follows the directory after that, so the
// solver exits and the kubelet restarts the container.
func (s *Server) Failed() <-chan error { return s.failed }

// Done closes when the watch of the index directory has stopped, after
// the context of Start ends or the watch fails. The watch can still
// apply one event after the context ends, and that event writes the
// engine configuration into the work directory, so a caller that
// removes the work directory waits on Done first.
func (s *Server) Done() <-chan struct{} { return s.done }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == PathIndexes && r.Method == http.MethodGet && r.URL.Query().Get("watch") == "true":
		s.followIndexes(w, r)
	case r.URL.Path == PathIndexes && r.Method == http.MethodGet:
		series, _ := s.set.current()
		writeJSON(w, series)
	case r.URL.Path == PathSolve && r.Method == http.MethodPost:
		s.serveSolve(w, r)
	case r.URL.Path == PathIndexes || r.URL.Path == PathSolve:
		http.Error(w, r.Method+" is not allowed on "+r.URL.Path, http.StatusMethodNotAllowed)
	default:
		http.NotFound(w, r)
	}
}

// followIndexes writes the series as one line of JSON, and one more
// line after each change, until the client disconnects. The operator
// keeps one such request open to each solver pod, so a file added to
// the claim reaches the Solver's status without a restart and without
// polling.
func (s *Server) followIndexes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	flush := http.NewResponseController(w)
	encoder := json.NewEncoder(w)
	for {
		series, changed := s.set.current()
		if err := encoder.Encode(series); err != nil {
			return
		}
		_ = flush.Flush()
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) serveSolve(w http.ResponseWriter, r *http.Request) {
	axy, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStarList))
	if err != nil {
		http.Error(w, "reading the star list: "+err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	queued := time.Now()
	select {
	case s.turn <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	started := time.Now()
	solution, err := s.solve(r.Context(), axy)
	<-s.turn
	fmt.Fprintf(s.logs, "a solve of %d bytes waited %s and ran %s: %s\n",
		len(axy), started.Sub(queued).Round(time.Millisecond), time.Since(started).Round(time.Millisecond), outcome(solution, err))
	switch {
	case errors.Is(err, ErrBadStarList):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		writeJSON(w, solution)
	}
}

// outcome returns the result of a solve as its log line writes it.
func outcome(solution SolveResponse, err error) string {
	switch {
	case err != nil:
		return err.Error()
	case !solution.Solved:
		return "no solution"
	}
	return fmt.Sprintf("solved at %.4f, %.4f with index %d", solution.Center.RA, solution.Center.Dec, solution.Index)
}

// writeJSON writes body as the JSON response. It ignores the error,
// because a write that fails means the client disconnected, and the
// server can do nothing about that.
func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
