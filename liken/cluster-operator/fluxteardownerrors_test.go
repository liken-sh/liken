package main

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// capturedStdout returns what run writes to os.Stdout.
func capturedStdout(t *testing.T, run func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = write
	// The cleanup puts os.Stdout back even when run panics.
	t.Cleanup(func() {
		os.Stdout = orig
		read.Close()
	})
	collected := make(chan string, 1)
	go func() {
		raw, _ := io.ReadAll(read)
		collected <- string(raw)
	}()
	run()
	os.Stdout = orig
	write.Close()
	return <-collected
}

// A teardown that a refused request stops must say so, or a person
// sees a flux-system Namespace that stays and no reason for it.
func TestTheFluxTeardownReportsARefusedRequest(t *testing.T) {
	cases := []struct {
		name    string
		refuses func(r *http.Request) bool
	}{
		{"a refused list of the controller pods", func(r *http.Request) bool {
			return r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods")
		}},
		{"a refused patch of the finalizers", func(r *http.Request) bool {
			return r.Method == http.MethodPatch
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case c.refuses(r):
					w.WriteHeader(http.StatusForbidden)
				case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
					w.Write([]byte(annotatedFluxNamespace))
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
					http.NotFound(w, r)
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods"):
					w.Write([]byte(`{"items": []}`))
				default:
					w.Write([]byte(`{}`))
				}
			}))

			out := capturedStdout(t, func() { tearDownFlux(client, retractedCluster()) })

			if !strings.Contains(out, "403") {
				t.Errorf("the teardown printed %q, want a line that gives the status 403 of the refused request", out)
			}
		})
	}
}
