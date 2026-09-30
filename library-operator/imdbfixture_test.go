package main

// The dataset server the IMDb tests share: it serves the gzipped TSV files
// under testdata/imdb the way datasets.imdbws.com serves the real ones, with
// an ETag, a Last-Modified time, and a 304 for a GET whose If-None-Match
// names the current ETag. No test reaches IMDb. The server answers over
// in-memory pipes, so a test in a synctest bubble waits out the pace
// between two requests on the bubble's clock.

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

type datasetServer struct {
	*apiservertest.Server
	// The address every request goes to. The client the server answers
	// ignores it, because each request reaches the server through a pipe.
	URL   string
	mutex sync.Mutex
	// The Last-Modified time of every file, and the version of each file, which
	// the ETag carries, so a test publishes a new version by raising it.
	modified time.Time
	versions map[string]int
	// A status a test makes one file answer with.
	statuses map[string]int
	// Every request, as the method, the file, and the status it got, and
	// the time each one arrived.
	requests []string
	arrived  []time.Time
	// A handler that answers every request in place of the files.
	replaced http.Handler
}

func newDatasetServer(t *testing.T, modified time.Time) *datasetServer {
	t.Helper()
	server := &datasetServer{URL: apiservertest.Host, modified: modified,
		versions: map[string]int{}, statuses: map[string]int{}}
	server.Server = apiservertest.Start(t, http.HandlerFunc(server.serve))
	return server
}

func (s *datasetServer) etag(name string) string {
	return `"` + name + "-" + strconv.Itoa(s.versions[name]) + `"`
}

// replace answers every later request with the handler.
func (s *datasetServer) replace(handler http.Handler) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.replaced = handler
}

func (s *datasetServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.replaced != nil {
		s.replaced.ServeHTTP(w, r)
		return
	}
	s.arrived = append(s.arrived, time.Now())
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".tsv.gz")
	status := s.answer(w, r, name)
	s.requests = append(s.requests, r.Method+" "+name+" "+strconv.Itoa(status))
}

func (s *datasetServer) answer(w http.ResponseWriter, r *http.Request, name string) int {
	if status := s.statuses[name]; status != 0 {
		w.WriteHeader(status)
		return status
	}
	body, err := os.ReadFile(filepath.Join("testdata", "imdb", name+".tsv.gz"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return http.StatusNotFound
	}
	w.Header().Set("ETag", s.etag(name))
	w.Header().Set("Last-Modified", s.modified.Format(http.TimeFormat))
	if r.Header.Get("If-None-Match") == s.etag(name) {
		w.WriteHeader(http.StatusNotModified)
		return http.StatusNotModified
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	return http.StatusOK
}

// The requests so far, in order.
func (s *datasetServer) log() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.requests...)
}

// The gaps between the requests so far, in order.
func (s *datasetServer) gaps() []time.Duration {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	var gaps []time.Duration
	for index := 1; index < len(s.arrived); index++ {
		gaps = append(gaps, s.arrived[index].Sub(s.arrived[index-1]))
	}
	return gaps
}

// The size of one fixture file, which the check reads as Content-Length.
func fixtureSize(t *testing.T, name string) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join("testdata", "imdb", name+".tsv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
