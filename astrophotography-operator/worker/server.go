package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// maxRequest limits the size of an extraction request body, which is a
// frame path and a search hint of a few numbers.
const maxRequest = 64 << 10

// Server serves the worker's HTTP paths.
type Server struct {
	extractor Extractor
	logs      io.Writer
}

// NewServer returns a Server for e that logs each extraction to logs.
func NewServer(e Extractor, logs io.Writer) *Server {
	return &Server{extractor: e, logs: logs}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == PathExtract && r.Method == http.MethodPost:
		s.serveExtract(w, r)
	case r.URL.Path == PathOutbox && r.Method == http.MethodGet:
		state, err := s.extractor.State()
		reply(w, state, err)
	case strings.HasPrefix(r.URL.Path, PathCentering) && r.Method == http.MethodDelete:
		err := s.extractor.RemoveCentering(strings.TrimPrefix(r.URL.Path, PathCentering))
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reply(w, nil, err)
	case r.URL.Path == PathExtract || r.URL.Path == PathOutbox || strings.HasPrefix(r.URL.Path, PathCentering):
		http.Error(w, r.Method+" is not allowed on "+r.URL.Path, http.StatusMethodNotAllowed)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveExtract(w http.ResponseWriter, r *http.Request) {
	var request ExtractRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequest)).Decode(&request); err != nil {
		http.Error(w, "reading the request: "+err.Error(), http.StatusBadRequest)
		return
	}
	started := time.Now()
	result, err := s.extractor.Extract(r.Context(), request)
	took := time.Since(started).Round(time.Millisecond)
	if err != nil {
		fmt.Fprintf(s.logs, "extracting the stars of %s stopped after %s: %v\n", request.Frame, took, err)
	} else {
		fmt.Fprintf(s.logs, "extracted %d stars of median FWHM %.2f pixels from %s in %s, with a radius of %g degrees\n",
			result.Stars, result.MedianFWHM, request.Frame, took, request.Hint.Radius)
	}
	reply(w, result, err)
}

// reply writes body as JSON, or, when err is not nil, an HTTP error with
// the status that err maps to.
func reply(w http.ResponseWriter, body any, err error) {
	switch {
	case errors.Is(err, ErrBadRequest):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrNoSuchFrame), errors.Is(err, fs.ErrNotExist):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		// The handler has no way to report a failed write to the client.
		_ = json.NewEncoder(w).Encode(body)
	}
}
