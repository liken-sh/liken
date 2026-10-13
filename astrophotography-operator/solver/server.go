package solver

// The HTTP server of the solver's pod. It runs one engine at a time:
// each solve takes about a second, and each reservation centers a few
// times an hour, so one solver serves many telescopes, and two engines
// at once would only share the same CPU. A request waits its turn in
// the queue, and leaves the queue when its client goes away.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxStarList bounds the body of a solve. An .axy of the CCD
// simulator's 1,675 stars is 48,960 bytes.
const maxStarList = 4 << 20

// solve is what the server needs of an engine. A test serves a server
// whose solve it controls.
type solve func(ctx context.Context, axy []byte) (SolveAnswer, error)

// Server answers PathSolve and PathIndexes.
type Server struct {
	solve   solve
	indexes Indexes
	// turn holds one token while the engine runs. A channel and not a
	// mutex, so a request whose client goes away leaves the queue.
	turn chan struct{}
	// logs receives one line for each solve.
	logs io.Writer
}

// NewServer serves an engine and the index series it reads, and logs
// each solve to logs.
func NewServer(engine Engine, indexes Indexes, logs io.Writer) *Server {
	return newServer(engine.Solve, indexes, logs)
}

func newServer(s solve, indexes Indexes, logs io.Writer) *Server {
	if indexes == nil {
		indexes = Indexes{}
	}
	return &Server{solve: s, indexes: indexes, turn: make(chan struct{}, 1), logs: logs}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == PathIndexes && r.Method == http.MethodGet:
		answer(w, s.indexes)
	case r.URL.Path == PathSolve && r.Method == http.MethodPost:
		s.serveSolve(w, r)
	case r.URL.Path == PathIndexes || r.URL.Path == PathSolve:
		http.Error(w, r.Method+" is not allowed on "+r.URL.Path, http.StatusMethodNotAllowed)
	default:
		http.NotFound(w, r)
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
		answer(w, solution)
	}
}

// outcome is the end of a solve's log line.
func outcome(solution SolveAnswer, err error) string {
	switch {
	case err != nil:
		return err.Error()
	case !solution.Solved:
		return "no solution"
	}
	return fmt.Sprintf("solved at %.4f, %.4f with index %d", solution.Center.RA, solution.Center.Dec, solution.Index)
}

// answer writes a JSON answer. A client that went away gets nothing,
// and the error of the write tells the server nothing it can act on.
func answer(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
