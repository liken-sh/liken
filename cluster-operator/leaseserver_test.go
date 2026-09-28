package main

// The fake API server the leader tests run against. It holds one Lease
// the way the API server does: a create conflicts with a Lease that
// exists, and an update from a stale resourceVersion conflicts with a
// newer write.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

type leaseServer struct {
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

func newLeaseServer() *leaseServer {
	return &leaseServer{refuse: map[string]int{}, requests: map[string]int{}}
}

// config is a client configuration for the server, the way
// rest.InClusterConfig is one for the real API server. The typed client
// sends protobuf to the real API server, and JSON here, because the fake
// decodes JSON alone.
func (s *leaseServer) config(t *testing.T) *rest.Config {
	t.Helper()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return &rest.Config{Host: server.URL, ContentConfig: rest.ContentConfig{ContentType: "application/json"}}
}

// store writes a Lease at the next version. Callers hold mu.
func (s *leaseServer) store(object map[string]any) map[string]any {
	s.version++
	metadata, _ := object["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		object["metadata"] = metadata
	}
	metadata["resourceVersion"] = strconv.Itoa(s.version)
	s.current = object
	return object
}

// holdAs writes the Lease as another process would, with a renewal
// that is fresh now.
func (s *leaseServer) holdAs(holder string) {
	s.holdAsRenewedAt(holder, time.Now())
}

// holdAsRenewedAt writes the Lease as a process that last renewed it at
// renewed, such as a process that has since exited.
func (s *leaseServer) holdAsRenewedAt(holder string, renewed time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := renewed.UTC().Format("2006-01-02T15:04:05.000000Z07:00")
	s.store(map[string]any{
		"apiVersion": "coordination.k8s.io/v1",
		"kind":       "Lease",
		"metadata":   map[string]any{"name": leaseName, "namespace": leaseNamespace},
		"spec": map[string]any{
			"holderIdentity":       holder,
			"leaseDurationSeconds": int(testLeaseTiming.duration / time.Second),
			"acquireTime":          now,
			"renewTime":            now,
		},
	})
}

func (s *leaseServer) holder() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return ""
	}
	spec, _ := s.current["spec"].(map[string]any)
	holder, _ := spec["holderIdentity"].(string)
	return holder
}

func (s *leaseServer) landAWriteAfterTheNextRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.landAfterRead = true
}

func (s *leaseServer) setRefusal(method string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse[method] = status
}

func (s *leaseServer) clearRefusal(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refuse, method)
}

func (s *leaseServer) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[method]
}

func (s *leaseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/leases/"+leaseName):
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
