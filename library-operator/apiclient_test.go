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

func TestRequestJSONTurnsStatusesIntoAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "absent", status: http.StatusNotFound, body: "", want: ErrNotFound},
		{name: "already written", status: http.StatusConflict, body: "", want: ErrConflict},
		{name: "forbidden", status: http.StatusForbidden, body: "no access", want: nil},
		{name: "broken", status: http.StatusInternalServerError, body: "the server failed", want: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))

			err := client.RequestJSON(t.Context(), http.MethodGet, "/version", nil, &Version{})
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

func TestRequestJSONAsksForJSONAndSendsNoTokenWithoutCredentials(t *testing.T) {
	var accept, authorization, contentType string
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		authorization = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte("{}"))
	}))

	if err := client.RequestJSON(t.Context(), http.MethodGet, "/version", nil, nil); err != nil {
		t.Fatal(err)
	}
	if accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", accept)
	}
	if authorization != "" {
		t.Errorf("Authorization = %q, want no header", authorization)
	}
	if contentType != "" {
		t.Errorf("Content-Type = %q, want no header on a request with no body", contentType)
	}
}

// A request that carries a body declares it as JSON, which is what the
// API server reads a status write and a pod creation as.
func TestRequestJSONSendsItsBodyAsJSON(t *testing.T) {
	var contentType, sent string
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		_, _ = w.Write([]byte("{}"))
	}))

	if err := client.RequestJSON(t.Context(), http.MethodPost, "/api/v1/namespaces/house/pods", []byte(`{"kind":"Pod"}`), nil); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if sent != `{"kind":"Pod"}` {
		t.Errorf("body = %q, want the bytes the caller passed", sent)
	}
}

func TestRequestJSONSendsTheTokenItReadsFromDisk(t *testing.T) {
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
	if err := client.RequestJSON(t.Context(), http.MethodGet, "/version", nil, nil); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer a-service-account-token" {
		t.Errorf("Authorization = %q, want the token from disk", authorization)
	}
}

func TestRequestJSONFailsWhenTheTokenIsMissing(t *testing.T) {
	client := NewClient("https://kubernetes.default.svc", http.DefaultClient, t.TempDir())

	err := client.RequestJSON(t.Context(), http.MethodGet, "/version", nil, nil)
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

func TestRequestJSONFailsWhenTheAddressIsUnusable(t *testing.T) {
	client := NewClient("https://kubernetes.default.svc\x7f", http.DefaultClient, "")

	err := client.RequestJSON(t.Context(), http.MethodGet, "/version", nil, nil)

	if err == nil || !strings.Contains(err.Error(), "/version") {
		t.Fatalf("err = %v, want the request it could not make", err)
	}
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
func recordingAPI(t *testing.T, answer any) (*Client, *recordedRequest) {
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

// A pass reads every Library in the cluster with one request, because
// a house keeps its media in whatever namespace it likes.
func TestListLibrariesReadsEveryNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, LibraryList{
		Metadata: ListMeta{ResourceVersion: "1200"},
		Items: []Library{{
			Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
			Spec:     LibrarySpec{Kind: libraryKindMovies, Storage: LibraryStorage{Claim: "movies", Root: "/"}},
		}},
	})

	list, err := ListLibraries(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/apis/library.liken.sh/v1alpha1/libraries")
	if list.Metadata.ResourceVersion != "1200" {
		t.Errorf("resourceVersion = %q, want the collection's 1200", list.Metadata.ResourceVersion)
	}
	if len(list.Items) != 1 || list.Items[0].Spec.Storage.Claim != "movies" {
		t.Errorf("items = %+v, want the one library the server answered", list.Items)
	}
}

// The status goes through its own subresource, so this request can
// never touch the spec a person declared.
func TestPutLibraryStatusWritesTheStatusSubresource(t *testing.T) {
	written := &Library{
		Metadata: ObjectMeta{Name: "movies", Namespace: "house", ResourceVersion: "1200"},
		Status:   LibraryStatus{Titles: 412, Pod: "movies-scanner"},
	}
	client, recorded := recordingAPI(t, written)

	back, err := PutLibraryStatus(t.Context(), client, written)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPut, "/apis/library.liken.sh/v1alpha1/namespaces/house/libraries/movies/status")
	if !strings.Contains(recorded.body, `"resourceVersion":"1200"`) {
		t.Errorf("body = %s, want the resourceVersion that makes the write conditional", recorded.body)
	}
	if !strings.Contains(recorded.body, `"titles":412`) {
		t.Errorf("body = %s, want the counts the operator folded", recorded.body)
	}
	if back.Status.Pod != "movies-scanner" {
		t.Errorf("pod = %q, want the value the server wrote back", back.Status.Pod)
	}
}

// a merge patch states the one list it edits and carries the
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

// a Library changed since the list answers the conflict the caller carries
// on from.
func TestPatchLibraryFinalizersReportsAConflict(t *testing.T) {
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))

	_, err := PatchLibraryFinalizers(t.Context(), client, "house", "movies", "1200", nil)

	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

// A zero-title report is an answer, so the count goes on the wire as 0
// rather than being dropped as an empty field.
func TestPutLibraryStatusWritesAZeroCount(t *testing.T) {
	client, recorded := recordingAPI(t, &Library{})

	if _, err := PutLibraryStatus(t.Context(), client, &Library{
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
		call func(*Client) error
	}{
		{name: "ListLibraries", call: func(c *Client) error { _, err := ListLibraries(t.Context(), c); return err }},
		{name: "PutLibraryStatus", call: func(c *Client) error { _, err := PutLibraryStatus(t.Context(), c, &Library{}); return err }},
		{name: "PatchLibraryFinalizers", call: func(c *Client) error {
			_, err := PatchLibraryFinalizers(t.Context(), c, "house", "movies", "1", nil)
			return err
		}},
		{name: "GetPersistentVolumeClaim", call: func(c *Client) error {
			_, err := GetPersistentVolumeClaim(t.Context(), c, "house", "movies")
			return err
		}},
		{name: "GetPersistentVolume", call: func(c *Client) error { _, err := GetPersistentVolume(t.Context(), c, "pv-movies"); return err }},
		{name: "ListScannerPods", call: func(c *Client) error { _, err := ListScannerPods(t.Context(), c); return err }},
		{name: "GetPod", call: func(c *Client) error { _, err := GetPod(t.Context(), c, "house", "movies-scanner"); return err }},
		{name: "CreatePod", call: func(c *Client) error { _, err := CreatePod(t.Context(), c, &Pod{}); return err }},
		{name: "DeletePod", call: func(c *Client) error { return DeletePod(t.Context(), c, "house", "movies-scanner") }},
		{name: "GetEndpointSlice", call: func(c *Client) error {
			_, err := GetEndpointSlice(t.Context(), c, "house", "catalog")
			return err
		}},
		{name: "CreateEndpointSlice", call: func(c *Client) error {
			_, err := CreateEndpointSlice(t.Context(), c, &EndpointSlice{})
			return err
		}},
		{name: "UpdateEndpointSlice", call: func(c *Client) error {
			_, err := UpdateEndpointSlice(t.Context(), c, &EndpointSlice{})
			return err
		}},
		{name: "GetService", call: func(c *Client) error {
			_, err := GetService(t.Context(), c, "house", "catalog")
			return err
		}},
		{name: "CreateService", call: func(c *Client) error {
			_, err := CreateService(t.Context(), c, &Service{})
			return err
		}},
		{name: "UpdateService", call: func(c *Client) error {
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
