package apiclient

// Tests of the in-cluster client, which reads the ServiceAccount
// directory that the kubelet mounts and the API server address from the
// pod's environment.

import (
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tlsCluster starts an API server that serves TLS, and writes a
// ServiceAccount directory with its CA and a token, as the kubelet
// mounts one.
func tlsCluster(t *testing.T) (dir string, server *httptest.Server) {
	t.Helper()
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"` + r.Header.Get("Authorization") + `"}`))
	}))
	t.Cleanup(server.Close)
	dir = t.TempDir()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca, 0o600); err != nil {
		t.Fatalf("writing the CA file: %v", err)
	}
	writeToken(t, dir, "pod-token")
	return dir, server
}

// An in-cluster client connects to the API server from the
// environment, trusts the mounted CA, and sends the mounted token.
func TestAnInClusterClientReachesTheAPIServer(t *testing.T) {
	dir, server := tlsCluster(t)
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatalf("splitting the server address: %v", err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)

	client, err := InCluster(InClusterOptions{ServiceAccountDir: dir})
	if err != nil {
		t.Fatalf("creating the in-cluster client: %v", err)
	}
	got, err := Get[struct{ Name string }](client, "/api/v1/nodes/node-1")
	if err != nil || got.Name != "Bearer pod-token" {
		t.Errorf("Get = %+v, %v; want the name that the server read from the pod's token", got, err)
	}
}

// An in-cluster client fails to start when the environment has no API
// server address or the CA file cannot be read.
func TestAnInClusterClientNeedsItsEnvironment(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		ca      []byte
		wantErr string
	}{
		{"no address", "", []byte("unused"), "not running in a cluster"},
		{"no CA", "10.43.0.1", nil, "reading service account CA"},
		{"a CA with no certificate", "10.43.0.1", []byte("not a certificate"), "contains no certificates"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", c.host)
			t.Setenv("KUBERNETES_SERVICE_PORT", "443")
			dir := t.TempDir()
			if c.ca != nil {
				if err := os.WriteFile(filepath.Join(dir, "ca.crt"), c.ca, 0o600); err != nil {
					t.Fatalf("writing the CA file: %v", err)
				}
			}

			_, err := InCluster(InClusterOptions{ServiceAccountDir: dir})

			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want one that contains %q", err, c.wantErr)
			}
		})
	}
}

// An in-cluster client given a server address uses it, with the mounted
// CA and token, and needs no address from the environment.
func TestAnInClusterClientReachesTheServerItIsGiven(t *testing.T) {
	dir, server := tlsCluster(t)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	client, err := InCluster(InClusterOptions{ServiceAccountDir: dir, Server: server.URL})
	if err != nil {
		t.Fatalf("creating the in-cluster client: %v", err)
	}
	got, err := Get[struct{ Name string }](client, "/api/v1/nodes/node-1")
	if err != nil || got.Name != "Bearer pod-token" {
		t.Errorf("Get = %+v, %v; want the name that the server read from the pod's token", got, err)
	}
}

// An in-cluster client fails a request that takes longer than its
// timeout.
func TestAnInClusterClientAbandonsARequestAtItsTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	dir := t.TempDir()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca, 0o600); err != nil {
		t.Fatalf("writing the CA file: %v", err)
	}
	writeToken(t, dir, "pod-token")

	client, err := InCluster(InClusterOptions{ServiceAccountDir: dir, Server: server.URL, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("creating the in-cluster client: %v", err)
	}
	started := time.Now()
	err = client.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil)
	if err == nil || time.Since(started) > 5*time.Second {
		t.Errorf("err = %v after %s, want an error at the 50ms timeout", err, time.Since(started))
	}
}
