package main

// These tests run the client against a small HTTP server that
// answers the way the API server answers, so the paths, the methods,
// and the named error are proved without a cluster.

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The credentials are empty, so the client sends no bearer token and
// reads nothing from disk.
func testAPIClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return apiclient.New(server.URL, server.Client(), "")
}

func TestServerVersionReadsTheVersionEndpoint(t *testing.T) {
	var asked string
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.Method + " " + r.URL.Path
		_ = json.NewEncoder(w).Encode(Version{GitVersion: "v1.34.1+k3s1"})
	}))

	version, err := ServerVersion(t.Context(), client)
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

// The PEM the client must trust: the certificate an httptest TLS
// server presents.
func testCertificatePEM(t *testing.T, server *httptest.Server) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	}))
}

// One request a verb made, in the terms these tests assert on: the
// method and the path name the verb, the query carries the selector,
// and the body is what the operator wrote.
type recordedRequest struct {
	method string
	path   string
	query  url.Values
	body   string
}

// An API server that records the request a verb makes and answers it
// with the object the test supplies.
func recordingAPI(t *testing.T, answer any) (*apiclient.Client, *recordedRequest) {
	t.Helper()
	recorded := &recordedRequest{}
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*recorded = recordedRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.Query(),
			body:   string(body),
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	return client, recorded
}

func expectRequest(t *testing.T, recorded *recordedRequest, method, path string) {
	t.Helper()
	if recorded.method != method {
		t.Errorf("method = %q, want %q", recorded.method, method)
	}
	if recorded.path != path {
		t.Errorf("path = %q, want %q", recorded.path, path)
	}
}

// The status goes through its own subresource, so this request can
// never touch the spec a person declared.
func TestReplaceStatusWritesTheStatusSubresource(t *testing.T) {
	written := &Library{
		Metadata: ObjectMeta{Name: "movies", Namespace: "house", ResourceVersion: "1200"},
		Status:   LibraryStatus{Titles: 412, Webhook: "http://library-operator.liken-system.svc/webhook/house/movies"},
	}
	client, recorded := recordingAPI(t, written)

	back := *written
	if err := apiclient.ReplaceStatus(client.WithContext(t.Context()), libraryPath("house", "movies"), &back); err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPut, "/apis/library.liken.sh/v1alpha1/namespaces/house/libraries/movies/status")
	if !strings.Contains(recorded.body, `"resourceVersion":"1200"`) {
		t.Errorf("body = %s, want the resourceVersion that makes the write conditional", recorded.body)
	}
	if !strings.Contains(recorded.body, `"titles":412`) {
		t.Errorf("body = %s, want the counts the operator folded", recorded.body)
	}
	if back.Status.Webhook == "" {
		t.Errorf("webhook = %q, want the value the server wrote back", back.Status.Webhook)
	}
}

// A merge patch states the one list it edits and carries the
// resourceVersion, and the Content-Type header names the patch dialect.
func TestPatchLibraryFinalizersSendsAConditionalMergePatch(t *testing.T) {
	var contentType, body, asked string
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ := io.ReadAll(r.Body)
		contentType, body, asked = r.Header.Get("Content-Type"), string(read), r.Method+" "+r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"resourceVersion": "1201"},
		})
	}))

	version, err := PatchLibraryFinalizers(t.Context(), client, "house", "movies", "1200",
		[]string{libraryFinalizer})
	if err != nil {
		t.Fatal(err)
	}

	if asked != "PATCH /apis/library.liken.sh/v1alpha1/namespaces/house/libraries/movies" {
		t.Errorf("request = %q, want the PATCH of the Library itself", asked)
	}
	if contentType != mergePatchType {
		t.Errorf("content type = %q, want %q", contentType, mergePatchType)
	}
	if !strings.Contains(body, `"resourceVersion":"1200"`) {
		t.Errorf("body = %s, want the resourceVersion that makes the write conditional", body)
	}
	if !strings.Contains(body, libraryFinalizer) {
		t.Errorf("body = %s, want the finalizer list", body)
	}
	if version != "1201" {
		t.Errorf("resourceVersion = %q, want the one the write produced", version)
	}
}

// A Library changed since the list answers the conflict the caller carries
// on from.
func TestPatchLibraryFinalizersReportsAConflict(t *testing.T) {
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))

	_, err := PatchLibraryFinalizers(t.Context(), client, "house", "movies", "1200", nil)

	if !errors.Is(err, apiclient.ErrConflict) {
		t.Errorf("err = %v, want apiclient.ErrConflict", err)
	}
}

// A zero-title report is an answer, so the count goes on the wire as 0
// rather than being dropped as an empty field.
func TestReplaceStatusWritesAZeroCount(t *testing.T) {
	client, recorded := recordingAPI(t, &Library{})

	if err := apiclient.ReplaceStatus(client.WithContext(t.Context()), libraryPath("house", "movies"), &Library{
		Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
		Status:   LibraryStatus{Titles: 0, Unidentified: 0},
	}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(recorded.body, `"titles":0`) {
		t.Errorf("body = %s, want a titles count of 0", recorded.body)
	}
	if strings.Contains(recorded.body, "lastWalk") {
		t.Errorf("body = %s, want no walk time until a scanner reports one", recorded.body)
	}
}

func TestGetPersistentVolumeClaimReadsTheClaimTheLibraryNames(t *testing.T) {
	client, recorded := recordingAPI(t, PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-movies"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	})

	claim, err := GetPersistentVolumeClaim(t.Context(), client, "house", "movies")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/namespaces/house/persistentvolumeclaims/movies")
	if claim.Status.Phase != claimBound || claim.Spec.VolumeName != "pv-movies" {
		t.Errorf("claim = %+v, want the bound claim and its volume", claim)
	}
}

// A PersistentVolume is cluster-scoped, so its path carries no
// namespace.
func TestGetPersistentVolumeReadsWhatServesTheStorage(t *testing.T) {
	client, recorded := recordingAPI(t, map[string]any{
		"metadata": map[string]any{"name": "pv-movies"},
		"spec": map[string]any{
			"capacity": map[string]any{"storage": "8Ti"},
			"nfs":      map[string]any{"server": "movies.example", "path": "/srv/media/movies"},
		},
	})

	volume, err := GetPersistentVolume(t.Context(), client, "pv-movies")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/persistentvolumes/pv-movies")
	if volume.Spec.Source != "nfs" || volume.Spec.NFS.Server != "movies.example" {
		t.Errorf("spec = %+v, want the NFS export the server answered", volume.Spec)
	}
}

// A verb that cannot read reports the server's failure rather than an
// empty object, so a pass stops instead of writing a status built on
// nothing.
func TestEveryVerbReportsAServerFailure(t *testing.T) {
	cases := []struct {
		name string
		call func(*apiclient.Client) error
	}{
		{name: "apiclient.ReplaceStatus", call: func(c *apiclient.Client) error {
			return apiclient.ReplaceStatus(c.WithContext(t.Context()), libraryPath("house", "movies"), &Library{})
		}},
		{name: "PatchLibraryFinalizers", call: func(c *apiclient.Client) error {
			_, err := PatchLibraryFinalizers(t.Context(), c, "house", "movies", "1", nil)
			return err
		}},
		{name: "GetPersistentVolumeClaim", call: func(c *apiclient.Client) error {
			_, err := GetPersistentVolumeClaim(t.Context(), c, "house", "movies")
			return err
		}},
		{name: "DeletePersistentVolumeClaim", call: func(c *apiclient.Client) error {
			return DeletePersistentVolumeClaim(t.Context(), c, "house", "den-media-browser-catalog")
		}},
		{name: "GetPersistentVolume", call: func(c *apiclient.Client) error {
			_, err := GetPersistentVolume(t.Context(), c, "pv-movies")
			return err
		}},
		{name: "PatchPlayMetadata", call: func(c *apiclient.Client) error {
			_, err := PatchPlayMetadata(t.Context(), c, "house", "den-b2k9x", "1", ObjectMeta{})
			return err
		}},
		{name: "PatchPersonFinalizers", call: func(c *apiclient.Client) error {
			_, err := PatchPersonFinalizers(t.Context(), c, "person-a", "1", nil)
			return err
		}},
		{name: "CreateJob", call: func(c *apiclient.Client) error { _, err := CreateJob(t.Context(), c, &Job{}); return err }},
		{name: "DeleteJob", call: func(c *apiclient.Client) error { return DeleteJob(t.Context(), c, "house", "movies-cleanup") }},
		{name: "DeleteCronJob", call: func(c *apiclient.Client) error { return DeleteCronJob(t.Context(), c, "house", "movies-scan") }},
		{name: "GetPod", call: func(c *apiclient.Client) error {
			_, err := GetPod(t.Context(), c, "house", "movies-scanner")
			return err
		}},
		{name: "CreatePod", call: func(c *apiclient.Client) error { _, err := CreatePod(t.Context(), c, &Pod{}); return err }},
		{name: "DeletePod", call: func(c *apiclient.Client) error { return DeletePod(t.Context(), c, "house", "movies-scanner") }},
		{name: "GetEndpointSlice", call: func(c *apiclient.Client) error {
			_, err := GetEndpointSlice(t.Context(), c, "house", "catalog")
			return err
		}},
		{name: "CreateEndpointSlice", call: func(c *apiclient.Client) error {
			_, err := CreateEndpointSlice(t.Context(), c, &EndpointSlice{})
			return err
		}},
		{name: "UpdateEndpointSlice", call: func(c *apiclient.Client) error {
			_, err := UpdateEndpointSlice(t.Context(), c, &EndpointSlice{})
			return err
		}},
		{name: "GetService", call: func(c *apiclient.Client) error {
			_, err := GetService(t.Context(), c, "house", "catalog")
			return err
		}},
		{name: "CreateService", call: func(c *apiclient.Client) error {
			_, err := CreateService(t.Context(), c, &Service{})
			return err
		}},
		{name: "UpdateService", call: func(c *apiclient.Client) error {
			_, err := UpdateService(t.Context(), c, &Service{})
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("the server failed"))
			}))

			err := testCase.call(client)

			if err == nil || !strings.Contains(err.Error(), "the server failed") {
				t.Fatalf("err = %v, want the server's own message", err)
			}
		})
	}
}
