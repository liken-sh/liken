package main

// This file provides a shared test fixture: a client wired to an
// httptest server. The kubernetes package's own tests use the same
// arrangement.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
	"github.com/liken-sh/liken/metrics"
)

// fleetMetrics builds one operator registry with the fleet's layer 3
// on it. It returns the layer that a sweep publishes into, and a
// function that scrapes the registry the way Prometheus does: one
// HTTP GET, and the text document that comes back.
func fleetMetrics(t *testing.T) (*clusterMetrics, func() string) {
	t.Helper()
	o := metrics.NewOperator(component, machine.Version, []string{clusterKind}, []string{machineKind})
	layer := newClusterMetrics(o)

	return layer, func() string { return scrapeHandler(t, o.Handler()) }
}

// scrapeHandler reads a registry the way Prometheus reads it: one
// HTTP GET against the real handler, and the text document that
// comes back.
func scrapeHandler(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// testClient wires a client to a test server. The client has a
// credentials directory holding a token, the same way kubelet would
// mount one.
func testClient(t *testing.T, handler http.Handler) *kubernetes.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return kubernetes.NewClient(server.URL, server.Client(), credentials)
}
