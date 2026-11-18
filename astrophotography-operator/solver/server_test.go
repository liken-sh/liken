package solver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

// serve sends one request to a server and returns the recorded response.
func serve(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(body)))
	return w
}

var lab = Indexes{{ID: 4207, Files: 12, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}}}

// servedSet is an index set with fixed series that follows no
// directory.
func servedSet(series Indexes) *indexSet {
	if series == nil {
		series = Indexes{}
	}
	return &indexSet{series: series, changed: make(chan struct{})}
}

// lineWriter sends each write of the response to lines, so a test reads
// a followed list one line at a time. When broken is set, every write
// fails.
type lineWriter struct {
	header http.Header
	lines  chan string
	broken bool
}

func (w *lineWriter) Header() http.Header { return w.header }
func (w *lineWriter) WriteHeader(int)     {}
func (w *lineWriter) Flush()              {}
func (w *lineWriter) Write(p []byte) (int, error) {
	if w.broken {
		return 0, errors.New("connection reset by peer")
	}
	w.lines <- string(p)
	return len(p), nil
}

// The test passes when ServeHTTP returns after the first failed write.
func TestAFollowerWhoseConnectionBreaksEnds(t *testing.T) {
	w := &lineWriter{header: http.Header{}, broken: true}
	newServer(nil, servedSet(lab), io.Discard).ServeHTTP(w, httptest.NewRequest(http.MethodGet, PathIndexes+"?watch=true", nil))
}

func TestAFollowerGetsEachNewList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		set := newIndexSet(filepath.Join(t.TempDir(), "engine.cfg"), 60, io.Discard)
		s := newServer(nil, set, io.Discard)
		w := &lineWriter{header: http.Header{}, lines: make(chan string, 4)}
		ctx, leave := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			s.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, PathIndexes+"?watch=true", nil))
			close(done)
		}()
		first := <-w.lines
		if err := set.put(IndexFile{Path: "/indexes/index-4208.fits", ID: 4208, Low: 30, High: 42}); err != nil {
			t.Fatalf("adding index 4208 to the set: %v", err)
		}
		second := <-w.lines
		leave()
		<-done
		want := []string{"[]\n", `[{"id":4208,"files":1,"scaleArcmin":{"low":30,"high":42}}]` + "\n"}
		if got := []string{first, second}; !slices.Equal(got, want) {
			t.Errorf("the follower read %q, want %q", got, want)
		}
	})
}

func TestTheServerReturnsItsIndexes(t *testing.T) {
	reply := serve(newServer(nil, servedSet(lab), io.Discard), http.MethodGet, PathIndexes, nil)
	var got Indexes
	if err := json.Unmarshal(reply.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].ID != 4207 {
		t.Errorf("GET %s returned %d %s, want the lab's series 4207", PathIndexes, reply.Code, reply.Body)
	}
}

// A solver with no index file returns an empty list and not null, so
// the operator reads the list and reports NoIndexFiles.
func TestTheServerReturnsAnEmptyListWithNoIndexFiles(t *testing.T) {
	reply := serve(newServer(nil, servedSet(nil), io.Discard), http.MethodGet, PathIndexes, nil)
	if body := strings.TrimSpace(reply.Body.String()); body != "[]" {
		t.Errorf("GET %s returned %s, want []", PathIndexes, body)
	}
}

func TestASolveThroughTheServerReturnsAndLogsTheSolution(t *testing.T) {
	var logs bytes.Buffer
	s := newServer(fakeEngineAt(t, engineSolves).Solve, servedSet(lab), &logs)
	reply := serve(s, http.MethodPost, PathSolve, readFixture(t, fixtureAXY))
	var got SolveResponse
	if err := json.Unmarshal(reply.Body.Bytes(), &got); err != nil || !got.Solved || got.Index != 4207 {
		t.Errorf("POST %s returned %d %s, want a solved solution from index 4207", PathSolve, reply.Code, reply.Body)
	}
	if !strings.Contains(logs.String(), "solved at 29.6070, 59.8193 with index 4207") {
		t.Errorf("the log is %q, want it to hold the solution at 29.6070, 59.8193 with index 4207", logs.String())
	}
}

func TestEachFailedRequestReturnsAStatusAndAReason(t *testing.T) {
	cases := []struct {
		name   string
		solve  solve
		method string
		path   string
		body   []byte
		status int
	}{
		{"a GET of the solve", nil, http.MethodGet, PathSolve, nil, http.StatusMethodNotAllowed},
		{"a POST of the indexes", nil, http.MethodPost, PathIndexes, nil, http.StatusMethodNotAllowed},
		{"another path", nil, http.MethodGet, "/", nil, http.StatusNotFound},
		{"a star list too large", nil, http.MethodPost, PathSolve, make([]byte, maxStarList+1), http.StatusRequestEntityTooLarge},
		{"a star list the solver refuses", respondWith(SolveResponse{}, ErrBadStarList), http.MethodPost, PathSolve, nil, http.StatusBadRequest},
		{"an engine that fails", respondWith(SolveResponse{}, errors.New("astrometry-engine: exit status 1")), http.MethodPost, PathSolve, nil, http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply := serve(newServer(c.solve, servedSet(lab), io.Discard), c.method, c.path, c.body)
			if reply.Code != c.status || strings.TrimSpace(reply.Body.String()) == "" {
				t.Errorf("returned %d %q, want %d with a reason", reply.Code, reply.Body, c.status)
			}
		})
	}
}

func respondWith(response SolveResponse, err error) solve {
	return func(context.Context, []byte) (SolveResponse, error) { return response, err }
}

func TestALogLineReportsEachOutcome(t *testing.T) {
	cases := []struct {
		name     string
		solution SolveResponse
		err      error
		want     string
	}{
		{"solved", SolveResponse{Solved: true, Center: &sky.Position{RA: 1, Dec: 2}, Index: 4208}, nil, "solved at 1.0000, 2.0000 with index 4208"},
		{"not solved", SolveResponse{}, nil, "no solution"},
		{"failed", SolveResponse{}, errors.New("broken"), "broken"},
	}
	for _, c := range cases {
		if got := outcome(c.solution, c.err); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The second request waits until the first ends, and a third whose
// client disconnects while it waits never runs.
func TestTheQueueRunsOneSolveAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		running, most, ran := 0, 0, 0
		slow := func(context.Context, []byte) (SolveResponse, error) {
			mu.Lock()
			running++
			ran++
			most = max(most, running)
			mu.Unlock()
			time.Sleep(time.Second)
			mu.Lock()
			running--
			mu.Unlock()
			return SolveResponse{}, nil
		}
		s := newServer(slow, servedSet(lab), io.Discard)
		for range 2 {
			go serve(s, http.MethodPost, PathSolve, nil)
		}
		synctest.Wait()
		ctx, leave := context.WithCancel(t.Context())
		go func() {
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, PathSolve, nil)
			s.ServeHTTP(httptest.NewRecorder(), request)
		}()
		synctest.Wait()
		leave()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if most != 1 || ran != 2 {
			t.Errorf("at most %d solves ran at once and %d ran, want 1 and 2", most, ran)
		}
	})
}
