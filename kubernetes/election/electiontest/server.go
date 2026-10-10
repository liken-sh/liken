// Package electiontest is a fake API server that holds one Lease, for
// the tests of an election. It answers the way the API server does: a
// create conflicts with a Lease that exists, and an update from a stale
// resourceVersion conflicts with a newer write. A test serves it through
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

// Server holds the Lease named Name in Namespace.
type Server struct {
	Name      string
	Namespace string

	mu      sync.Mutex
	current map[string]any
	version int
	// landAfterRead writes the Lease again, at a new version, right after
	// the next read is answered. It is the state a renewal leaves when
	// its client gave up on it and the API server applied it anyway.
	landAfterRead bool
	refuse        map[string]int
	requests      map[string]int
}

// New is a server that holds no Lease yet.
func New(name, namespace string) *Server {
	return &Server{Name: name, Namespace: namespace, refuse: map[string]int{}, requests: map[string]int{}}
}

// Config is a client configuration for the server, the way
// rest.InClusterConfig is one for the real API server. The typed client
// sends protobuf to the real API server, and JSON here, because the fake
// decodes JSON alone.
func (s *Server) Config(t *testing.T) *rest.Config {
	t.Helper()
	config := apiservertest.Start(t, s).Config()
	config.ContentType = "application/json"
	return config
}

// store writes a Lease at the next version. Callers hold mu.
func (s *Server) store(object map[string]any) map[string]any {
	s.version++
	metadata, _ := object["metadata"].(map[string]any)
	metadata["resourceVersion"] = strconv.Itoa(s.version)
	s.current = object
	return object
}

// HoldAs writes the Lease as another process would, with a renewal that
// is fresh now.
func (s *Server) HoldAs(holder string) {
	s.HoldAsRenewedAt(holder, time.Now())
}

// HoldAsRenewedAt writes the Lease as a process that last renewed it at
// renewed, such as a process that has since exited.
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

// Holder is the identity the Lease names, and empty when no Lease
// exists or it names no holder.
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

// LandAWriteAfterTheNextRead makes the next read of the Lease followed
// by a write that the reader did not see.
func (s *Server) LandAWriteAfterTheNextRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.landAfterRead = true
}

// Refuse answers every request of the method with the status code,
// until Allow.
func (s *Server) Refuse(method string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse[method] = status
}

// Allow ends the refusal of the method.
func (s *Server) Allow(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refuse, method)
}

// Count is how many requests of the method the server received.
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
		// The API server names the reason and the code in the Status
		// it answers, and client-go reads both.
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
		if s.landAfterRead {
			s.landAfterRead = false
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

// status answers the Status object the API server sends with an error,
// which is what client-go reads the reason from.
func status(w http.ResponseWriter, code int, reason string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": reason, "code": code,
	})
}
