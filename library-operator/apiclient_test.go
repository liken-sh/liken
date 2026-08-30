package main

// These tests run the client against a small HTTP server that
// answers the way the API server answers, so the paths, the methods,
// and the named error are proved without a cluster.

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The credentials are empty, so the client sends no bearer token and
// reads nothing from disk.
func testAPIClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client(), "")
}

func TestServerVersionReadsTheVersionEndpoint(t *testing.T) {
	var asked string
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.Method + " " + r.URL.Path
		_ = json.NewEncoder(w).Encode(Version{GitVersion: "v1.34.1+k3s1"})
	}))

	version, err := ServerVersion(client)
	if err != nil {
		t.Fatal(err)
	}
	if version.GitVersion != "v1.34.1+k3s1" {
		t.Errorf("gitVersion = %q, want v1.34.1+k3s1", version.GitVersion)
	}
	if asked != "GET /version" {
		t.Errorf("request = %q, want GET /version", asked)
	}
}

func TestGetTurnsStatusesIntoAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "absent", status: http.StatusNotFound, body: "", want: ErrNotFound},
		{name: "forbidden", status: http.StatusForbidden, body: "no access", want: nil},
		{name: "broken", status: http.StatusInternalServerError, body: "the server failed", want: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))

			err := client.Get("/version", &Version{})
			if testCase.want != nil {
				if !errors.Is(err, testCase.want) {
					t.Fatalf("err = %v, want %v", err, testCase.want)
				}
				return
			}
			if err == nil {
				t.Fatal("err = nil, want the server's own message")
			}
			if !strings.Contains(err.Error(), testCase.body) {
				t.Errorf("err = %v, want it to carry %q", err, testCase.body)
			}
		})
	}
}

func TestGetAsksForJSONAndSendsNoTokenWithoutCredentials(t *testing.T) {
	var accept, authorization string
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("{}"))
	}))

	if err := client.Get("/version", nil); err != nil {
		t.Fatal(err)
	}
	if accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", accept)
	}
	if authorization != "" {
		t.Errorf("Authorization = %q, want no header", authorization)
	}
}

func TestGetSendsTheTokenItReadsFromDisk(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "token"), []byte("a-service-account-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, server.Client(), directory)
	if err := client.Get("/version", nil); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer a-service-account-token" {
		t.Errorf("Authorization = %q, want the token from disk", authorization)
	}
}

func TestGetFailsWhenTheTokenIsMissing(t *testing.T) {
	client := NewClient("https://kubernetes.default.svc", http.DefaultClient, t.TempDir())

	err := client.Get("/version", nil)
	if err == nil || !strings.Contains(err.Error(), "service account token") {
		t.Fatalf("err = %v, want a missing token", err)
	}
}

func TestInClusterClientRefusesWithoutTheCluster(t *testing.T) {
	cases := []struct {
		name string
		host string
		port string
		want string
	}{
		{name: "no host", host: "", port: "443", want: "not running in a cluster"},
		{name: "no port", host: "10.43.0.1", port: "", want: "not running in a cluster"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", testCase.host)
			t.Setenv("KUBERNETES_SERVICE_PORT", testCase.port)

			_, err := InClusterClient()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err = %v, want %q", err, testCase.want)
			}
		})
	}
}

// A directory in the shape the kubelet mounts, holding whatever CA
// bytes the test wants the client to read.
func testServiceAccountDir(t *testing.T, ca string) string {
	t.Helper()
	directory := t.TempDir()
	if ca != "" {
		if err := os.WriteFile(filepath.Join(directory, "ca.crt"), []byte(ca), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func TestInClusterClientRefusesAnUnusableCA(t *testing.T) {
	cases := []struct {
		name string
		ca   string
		want string
	}{
		{name: "absent", ca: "", want: "reading service account CA"},
		{name: "not certificates", ca: "this is not a certificate", want: "no certificates"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
			t.Setenv("KUBERNETES_SERVICE_PORT", "443")
			serviceAccountDir = testServiceAccountDir(t, testCase.ca)
			t.Cleanup(func() { serviceAccountDir = defaultServiceAccountDir })

			_, err := InClusterClient()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err = %v, want %q", err, testCase.want)
			}
		})
	}
}

// The PEM the client must trust: the certificate an httptest TLS
// server presents.
func testCertificatePEM(t *testing.T, server *httptest.Server) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	}))
}

func TestInClusterClientAddressesTheServiceFromTheEnvironment(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	certificate := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(certificate.Close)
	serviceAccountDir = testServiceAccountDir(t, testCertificatePEM(t, certificate))
	t.Cleanup(func() { serviceAccountDir = defaultServiceAccountDir })

	client, err := InClusterClient()
	if err != nil {
		t.Fatal(err)
	}
	if client.base != "https://10.43.0.1:443" {
		t.Errorf("base = %q, want https://10.43.0.1:443", client.base)
	}
	if client.credentials != serviceAccountDir {
		t.Errorf("credentials = %q, want the mounted directory", client.credentials)
	}
}

func TestGetFailsWhenTheAddressIsUnusable(t *testing.T) {
	client := NewClient("https://kubernetes.default.svc\x7f", http.DefaultClient, "")

	err := client.Get("/version", nil)

	if err == nil || !strings.Contains(err.Error(), "/version") {
		t.Fatalf("err = %v, want the request it could not make", err)
	}
}
