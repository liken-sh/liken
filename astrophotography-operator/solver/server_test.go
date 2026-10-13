package solver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

// serve sends one request to a server and answers the recorded reply.
func serve(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(body)))
	return w
}

var lab = Indexes{{ID: 4207, Files: 12, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}}}

func TestTheServerAnswersItsIndexes(t *testing.T) {
	reply := serve(newServer(nil, lab, io.Discard), http.MethodGet, PathIndexes, nil)
	var got Indexes
	if err := json.Unmarshal(reply.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].ID != 4207 {
		t.Errorf("answered %d %s, want the lab's series", reply.Code, reply.Body)
	}
}

// A solver with no index file answers an empty list, not null, so the
// operator reads the list and reports NoIndexFiles.
func TestTheServerAnswersAnEmptyListWithNoIndexFiles(t *testing.T) {
	reply := serve(newServer(nil, nil, io.Discard), http.MethodGet, PathIndexes, nil)
	if body := strings.TrimSpace(reply.Body.String()); body != "[]" {
		t.Errorf("answered %s, want []", body)
	}
}

// Each solve logs one line with the wait, the run, and the outcome, so
// the pod's log holds every solve.
func TestASolveThroughTheServerAnswersAndLogsTheSolution(t *testing.T) {
	var logs bytes.Buffer
	s := NewServer(fakeEngineAt(t, engineSolves), lab, &logs)
	reply := serve(s, http.MethodPost, PathSolve, readFixture(t, fixtureAXY))
	var got SolveAnswer
	if err := json.Unmarshal(reply.Body.Bytes(), &got); err != nil || !got.Solved || got.Index != 4207 {
		t.Errorf("answered %d %s, want the solution", reply.Code, reply.Body)
	}
	if !strings.Contains(logs.String(), "solved at 29.6070, 59.8193 with index 4207") {
		t.Errorf("logged %q", logs.String())
	}
}

func TestEachFailedRequestAnswersAStatusAndAReason(t *testing.T) {
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
		{"a star list the solver refuses", answerWith(SolveAnswer{}, ErrBadStarList), http.MethodPost, PathSolve, nil, http.StatusBadRequest},
		{"an engine that fails", answerWith(SolveAnswer{}, errors.New("astrometry-engine: exit status 1")), http.MethodPost, PathSolve, nil, http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply := serve(newServer(c.solve, lab, io.Discard), c.method, c.path, c.body)
			if reply.Code != c.status || strings.TrimSpace(reply.Body.String()) == "" {
				t.Errorf("answered %d %q, want %d with a reason", reply.Code, reply.Body, c.status)
			}
		})
	}
}

func answerWith(answer SolveAnswer, err error) solve {
	return func(context.Context, []byte) (SolveAnswer, error) { return answer, err }
}

func TestALogLineSaysEachOutcome(t *testing.T) {
	cases := []struct {
		name     string
		solution SolveAnswer
		err      error
		want     string
	}{
		{"solved", SolveAnswer{Solved: true, Center: &sky.Position{RA: 1, Dec: 2}, Index: 4208}, nil, "solved at 1.0000, 2.0000 with index 4208"},
		{"not solved", SolveAnswer{}, nil, "no solution"},
		{"failed", SolveAnswer{}, errors.New("broken"), "broken"},
	}
	for _, c := range cases {
		if got := outcome(c.solution, c.err); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The engine runs one job at a time: the second request waits until
// the first ends, and a third whose client goes away leaves the queue
// without running.
func TestTheQueueRunsOneSolveAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		running, most, ran := 0, 0, 0
		slow := func(context.Context, []byte) (SolveAnswer, error) {
			mu.Lock()
			running++
			ran++
			most = max(most, running)
			mu.Unlock()
			time.Sleep(time.Second)
			mu.Lock()
			running--
			mu.Unlock()
			return SolveAnswer{}, nil
		}
		s := newServer(slow, lab, io.Discard)
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
