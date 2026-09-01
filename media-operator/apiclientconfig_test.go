package main

// These tests cover how the client reads its own configuration: the
// bearer token it re-reads on every request, the failures that stop a request
// before it is sent, and the in-cluster config the pod's environment carries.

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A client built with a credentials directory reads the token from disk
// on every request and sends it as a bearer token, because the kubelet
// refreshes that file as each token nears expiry.
func TestTheClientSendsTheServiceAccountTokenOnEveryRequest(t *testing.T) {
	credentials := t.TempDir()
	mustSucceed(t, os.WriteFile(filepath.Join(credentials, "token"), []byte("first-token"), 0o600))

	sent := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent <- r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(PlayList{})
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.URL, server.Client(), credentials)

	_, err := ListPlays(client)
	mustSucceed(t, err)
	mustMatch(t, <-sent, "Bearer first-token")

	mustSucceed(t, os.WriteFile(filepath.Join(credentials, "token"), []byte("second-token"), 0o600))
	_, err = ListPlays(client)
	mustSucceed(t, err)
	mustMatch(t, <-sent, "Bearer second-token")
}

// A request the client cannot build and a token it cannot read both fail
// before anything reaches the network.
func TestTheClientFailsBeforeItSends(t *testing.T) {
	t.Run("the token is not there", func(t *testing.T) {
		client := NewClient("http://127.0.0.1:1", http.DefaultClient, filepath.Join(t.TempDir(), "absent"))
		_, err := client.Do(http.MethodGet, playsPath, nil)
		mustFail(t, err)
	})
	t.Run("the method is not a method", func(t *testing.T) {
		client := NewClient("http://127.0.0.1:1", http.DefaultClient, "")
		_, err := client.Do("GET PLAYS", playsPath, nil)
		mustFail(t, err)
	})
}

// A certificate in PEM form, taken from a test server's own
// certificate, so the pool the client builds holds a real one.
func testCAPEM(t *testing.T) []byte {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
}

// A service account directory the test controls, holding whatever CA
// file the case wants.
func useServiceAccountDir(t *testing.T, caPEM []byte) string {
	t.Helper()
	dir := t.TempDir()
	if caPEM != nil {
		mustSucceed(t, os.WriteFile(filepath.Join(dir, "ca.crt"), caPEM, 0o644))
	}
	dirWas := serviceAccountDir
	t.Cleanup(func() { serviceAccountDir = dirWas })
	serviceAccountDir = dir
	return dir
}

// The five values a pod holds are the whole of an in-cluster config: the
// two environment variables name the server, and the mounted CA and token are
// what the client trusts and sends.
func TestInClusterClientReadsThePodsOwnConfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	dir := useServiceAccountDir(t, testCAPEM(t))

	client, err := InClusterClient()
	mustSucceed(t, err)
	mustMatch(t, client.base, "https://10.43.0.1:443")
	mustMatch(t, client.credentials, dir)
}

// Every config a pod cannot supply fails at startup rather than at the
// first request, so a misconfigured deployment says what is missing.
func TestInClusterClientRefusesAConfigItCannotBuild(t *testing.T) {
	cases := []struct {
		name  string
		host  string
		caPEM []byte
	}{
		{name: "the environment names no server", caPEM: []byte("ignored")},
		{name: "the CA file is not there", host: "10.43.0.1"},
		{name: "the CA file holds no certificate", host: "10.43.0.1", caPEM: []byte("not a certificate")},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", each.host)
			t.Setenv("KUBERNETES_SERVICE_PORT", "443")
			useServiceAccountDir(t, each.caPEM)

			_, err := InClusterClient()
			mustFail(t, err)
		})
	}
}
