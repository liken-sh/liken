package main

// The operator's one inbound endpoint. The playback pod holds no API
// credentials, so it says what it sees to this endpoint, and the
// operator alone decides what any report means for a Play's status.
// The desk below is that boundary in code: reports land here, and
// the reconcile pass reads them.

import (
	"encoding/json"
	"net/http"
	"sync"
)

// maxReportBody caps the body because the endpoint answers the least
// trusted process in this system, and a real report is a few hundred
// bytes.
const maxReportBody = 4 << 10

// reports is what the reconcile loop and the report handler share:
// the minted tokens and the newest report per run. One mutex covers
// both maps, because the handler runs on the HTTP server's
// goroutines and the loop runs on its own.
type reports struct {
	mutex  sync.Mutex
	tokens map[string]string
	latest map[string]playReport
	wake   chan<- struct{}
}

func newReports(wake chan<- struct{}) *reports {
	return &reports{
		tokens: map[string]string{},
		latest: map[string]playReport{},
		wake:   wake,
	}
}

// runKey is the one key shape for a run. Namespace and name identify
// a run everywhere in this operator, and one shape keeps the two
// maps and the pass in step.
func runKey(namespace, name string) string {
	return namespace + "/" + name
}

// remember records a token, both the one this operator minted and
// the one it adopted from a pod a previous operator created.
func (r *reports) remember(namespace, name, token string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.tokens[runKey(namespace, name)] = token
}

func (r *reports) token(namespace, name string) string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.tokens[runKey(namespace, name)]
}

// latestFor returns the newest report, the only one kept. A report
// is a whole observation, so an older one says nothing a newer one
// does not.
func (r *reports) latestFor(namespace, name string) *playReport {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	report, held := r.latest[runKey(namespace, name)]
	if !held {
		return nil
	}
	return &report
}

// retain drops what a deleted Play left behind. The pass hands over
// the set of runs that still exist, and the maps shrink to match, so
// the desk never grows past the collection it serves.
func (r *reports) retain(live map[string]bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for key := range r.tokens {
		if !live[key] {
			delete(r.tokens, key)
		}
	}
	for key := range r.latest {
		if !live[key] {
			delete(r.latest, key)
		}
	}
}

// accept takes a report only when the operator manages that Play and
// the token matches the one it minted into the pod. An unknown Play
// and a wrong token get the same refusal, so a probe of this
// endpoint learns nothing about which plays exist.
func (r *reports) accept(report playReport) bool {
	if report.Namespace == "" || report.Name == "" || report.Token == "" {
		return false
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	key := runKey(report.Namespace, report.Name)
	minted, held := r.tokens[key]
	if !held || minted != report.Token {
		return false
	}
	r.latest[key] = report
	return true
}

// ServeHTTP folds one report in. An accepted report is also a wake:
// the position moved, so the status the operator publishes is
// already behind.
func (r *reports) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	var report playReport
	if err := json.NewDecoder(http.MaxBytesReader(w, request.Body, maxReportBody)).Decode(&report); err != nil {
		http.Error(w, "the report does not decode", http.StatusBadRequest)
		return
	}
	if !r.accept(report) {
		http.Error(w, "the report names no play this operator runs, or its token is wrong", http.StatusForbidden)
		return
	}
	poke(r.wake)
	w.WriteHeader(http.StatusOK)
}

// reportHandler routes by method and path in the pattern, so a wrong
// method or a wrong path is answered by the mux and never reaches
// the handler.
func reportHandler(desk *reports) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /report", desk)
	return mux
}
