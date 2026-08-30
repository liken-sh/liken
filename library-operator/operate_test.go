package main

// The operator's skeleton proves two outcomes: it reaches the API
// server and says so, and it runs until the kubelet asks it to stop.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An API server that answers /version and nothing else, which is
// every request the skeleton makes.
func testVersionAPI(t *testing.T, gitVersion string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Version{GitVersion: gitVersion})
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client(), "")
}

func TestRunReportsTheConnectionAndTheEmptyWorkload(t *testing.T) {
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	var reported bytes.Buffer

	if err := run(stopped, testVersionAPI(t, "v1.34.1+k3s1"), &reported); err != nil {
		t.Fatal(err)
	}

	line := reported.String()
	if !strings.Contains(line, "v1.34.1+k3s1") {
		t.Errorf("report = %q, want the server's version", line)
	}
	if !strings.Contains(line, "Library") {
		t.Errorf("report = %q, want it to name the Library resources it has none of", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("report = %q, want one line", line)
	}
}

func TestRunFailsWhenTheAPIServerDoesNotAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("the API server is unwell"))
	}))
	t.Cleanup(server.Close)

	err := run(context.Background(), NewClient(server.URL, server.Client(), ""), io.Discard)

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

func TestRunWaitsUntilTheContextEnds(t *testing.T) {
	running, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	returned := make(chan error, 1)
	go func() { returned <- run(running, testVersionAPI(t, "v1.34.1+k3s1"), io.Discard) }()

	select {
	case err := <-returned:
		t.Fatalf("run returned %v while the context was still open", err)
	case <-time.After(100 * time.Millisecond):
	}

	stop()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return when the context ended")
	}
}

func TestOperateRefusesOutsideACluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	err := operate()

	if err == nil || !strings.Contains(err.Error(), "not running in a cluster") {
		t.Fatalf("err = %v, want the in-cluster failure", err)
	}
}

// testClusterEnvironment builds the pod's whole environment: the two
// address variables, a mounted CA and token, and an API server that
// answers /version. The returned channel closes when that server is
// reached.
func testClusterEnvironment(t *testing.T, gitVersion string) chan struct{} {
	t.Helper()
	reached := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		_ = json.NewEncoder(w).Encode(Version{GitVersion: gitVersion})
	}))
	t.Cleanup(server.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)

	directory := testServiceAccountDir(t, testCertificatePEM(t, server))
	if err := os.WriteFile(filepath.Join(directory, "token"), []byte("a-service-account-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	serviceAccountDir = directory
	t.Cleanup(func() { serviceAccountDir = defaultServiceAccountDir })
	return reached
}

func TestOperateRunsUntilTheStopSignal(t *testing.T) {
	reached := testClusterEnvironment(t, "v1.34.1+k3s1")
	returned := make(chan error, 1)
	go func() { returned <- operate() }()

	select {
	case <-reached:
	case err := <-returned:
		t.Fatalf("operate returned %v before it reached the API server", err)
	case <-time.After(10 * time.Second):
		t.Fatal("operate did not reach the API server")
	}

	// The request above happens only after operate registers for the
	// signal, so this signal always reaches operate's handler and never
	// the default one, which would end the test binary.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("operate did not return after SIGTERM")
	}
}
