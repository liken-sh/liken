// Package electiontest is a fake API server that stores one Lease, for
// the tests of an election. A create of a Lease that exists fails with
// AlreadyExists, and an update from a stale resourceVersion fails with
// a conflict, as on the real API server. It is served through
// apiservertest, so the election runs in a synctest bubble on the fake
// clock.
package electiontest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election"
)

// Server stores the Lease called Name in Namespace.
type Server struct {
	Name      string
	Namespace string

	mu               sync.Mutex
	current          map[string]any
	version          int
	rewriteAfterRead bool
	refuse           map[string]int
	requests         map[string]int
}

// New returns a server that has no Lease.
func New(name, namespace string) *Server {
	return &Server{Name: name, Namespace: namespace, refuse: map[string]int{}, requests: map[string]int{}}
}

// Config returns a client configuration for the server, as
// rest.InClusterConfig does for the real API server. The typed client
// sends protobuf to the real API server. The configuration here uses
// JSON, because the fake decodes only JSON.
func (s *Server) Config(t *testing.T) *rest.Config {
	t.Helper()
	config := apiservertest.Start(t, s).Config()
	config.ContentType = "application/json"
	return config
}

// store saves the Lease at the next resourceVersion. The caller holds
// mu.
func (s *Server) store(object map[string]any) map[string]any {
	s.version++
	metadata, _ := object["metadata"].(map[string]any)
	metadata["resourceVersion"] = strconv.Itoa(s.version)
	s.current = object
	return object
}

// HoldAs stores the Lease as held by holder, renewed now.
func (s *Server) HoldAs(holder string) {
	s.HoldAsRenewedAt(holder, time.Now())
}

// HoldAsRenewedAt stores the Lease as held by holder, last renewed at
// renewed.
func (s *Server) HoldAsRenewedAt(holder string, renewed time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := renewed.UTC().Format("2006-01-02T15:04:05.000000Z07:00")
	s.store(map[string]any{
		"apiVersion": "coordination.k8s.io/v1",
		"kind":       "Lease",
		"metadata":   map[string]any{"name": s.Name, "namespace": s.Namespace},
		"spec": map[string]any{
			"holderIdentity":       holder,
			"leaseDurationSeconds": int(election.Duration / time.Second),
			"acquireTime":          now,
			"renewTime":            now,
		},
	})
}

// Holder returns the identity in the Lease's holderIdentity, or an empty
// string when there is no Lease or it has no holder.
func (s *Server) Holder() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return ""
	}
	spec, _ := s.current["spec"].(map[string]any)
	holder, _ := spec["holderIdentity"].(string)
	return holder
}

// RewriteAfterNextRead makes the server store the Lease again, at a new
// resourceVersion, right after it serves the next read, so the reader's
// copy is stale. It reproduces a renewal that its client gave up on and
// the API server committed anyway.
func (s *Server) RewriteAfterNextRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewriteAfterRead = true
}

// Refuse makes every request that uses the HTTP method fail with the
// status code, until Allow.
func (s *Server) Refuse(method string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse[method] = status
}

// Allow stops Refuse for the HTTP method.
func (s *Server) Allow(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refuse, method)
}

// Count returns how many requests with the HTTP method the server has
// received.
func (s *Server) Count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[method]
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[r.Method]++
	w.Header().Set("Content-Type", "application/json")
	if code, refused := s.refuse[r.Method]; refused {
		reasons := map[int]string{http.StatusForbidden: "Forbidden", http.StatusNotFound: "NotFound"}
		reason, known := reasons[code]
		if !known {
			reason = "InternalError"
		}
		status(w, code, reason)
		return
	}
	body := map[string]any{}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/leases/"+s.Name):
		if s.current == nil {
			status(w, http.StatusNotFound, "NotFound")
			return
		}
		_ = json.NewEncoder(w).Encode(s.current)
		if s.rewriteAfterRead {
			s.rewriteAfterRead = false
			s.store(s.current)
		}
	case r.Method == http.MethodPost:
		if s.current != nil {
			status(w, http.StatusConflict, "AlreadyExists")
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(s.store(body))
	case r.Method == http.MethodPut:
		if s.current == nil {
			status(w, http.StatusNotFound, "NotFound")
			return
		}
		sent, _ := body["metadata"].(map[string]any)
		stored, _ := s.current["metadata"].(map[string]any)
		if sent["resourceVersion"] != stored["resourceVersion"] {
			status(w, http.StatusConflict, "Conflict")
			return
		}
		_ = json.NewEncoder(w).Encode(s.store(body))
	default:
		status(w, http.StatusNotFound, "NotFound")
	}
}

// status writes the Status object that the API server sends with an
// error. client-go reads the error's reason and code from it.
func status(w http.ResponseWriter, code int, reason string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": reason, "code": code,
	})
}
