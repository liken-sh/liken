package main

// The dataset server the IMDb tests share: it serves the gzipped TSV files
// under testdata/imdb the way datasets.imdbws.com serves the real ones, with
// an ETag, a Last-Modified time, and a 304 for a GET whose If-None-Match
// names the current ETag. No test reaches IMDb.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type datasetServer struct {
	*httptest.Server
	mutex sync.Mutex
	// The Last-Modified time of every file, and the version of each file, which
	// the ETag carries, so a test publishes a new version by raising it.
	modified time.Time
	versions map[string]int
	// A status a test makes one file answer with.
	statuses map[string]int
	// Every request, as the method, the file, and the status it got.
	requests []string
}

func newDatasetServer(t *testing.T, modified time.Time) *datasetServer {
	t.Helper()
	server := &datasetServer{modified: modified, versions: map[string]int{}, statuses: map[string]int{}}
	server.Server = httptest.NewServer(http.HandlerFunc(server.serve))
	t.Cleanup(server.Close)
	return server
}

func (s *datasetServer) etag(name string) string {
	return `"` + name + "-" + strconv.Itoa(s.versions[name]) + `"`
}

func (s *datasetServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
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

// The size of one fixture file, which the check reads as Content-Length.
func fixtureSize(t *testing.T, name string) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join("testdata", "imdb", name+".tsv.gz"))
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
