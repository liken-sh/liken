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

// maxRequest bounds the body of an extraction request, which names a
// frame and four numbers.
const maxRequest = 64 << 10

// Server answers the worker's paths for one Extractor.
type Server struct {
	extractor Extractor
	// logs receives one line for each extraction.
	logs io.Writer
}

// NewServer serves an Extractor, and logs each extraction to logs.
func NewServer(e Extractor, logs io.Writer) *Server {
	return &Server{extractor: e, logs: logs}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == PathExtract && r.Method == http.MethodPost:
		s.serveExtract(w, r)
	case r.URL.Path == PathOutbox && r.Method == http.MethodGet:
		answer, err := s.extractor.State()
		reply(w, answer, err)
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
	answer, err := s.extractor.Extract(r.Context(), request)
	took := time.Since(started).Round(time.Millisecond)
	if err != nil {
		fmt.Fprintf(s.logs, "extracting %s failed after %s: %v\n", request.Frame, took, err)
	} else {
		fmt.Fprintf(s.logs, "extracted %d stars of median FWHM %.2f pixels from %s in %s, with a radius of %g degrees\n",
			answer.Stars, answer.MedianFWHM, request.Frame, took, request.Hint.Radius)
	}
	reply(w, answer, err)
}

// reply writes an answer, or the status of an error with its text.
func reply(w http.ResponseWriter, answer any, err error) {
	switch {
	case errors.Is(err, ErrBadRequest):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrNoSuchFrame), errors.Is(err, fs.ErrNotExist):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		// A client that went away gets nothing, and the error of the
		// write tells the worker nothing it can act on.
		_ = json.NewEncoder(w).Encode(answer)
	}
}
