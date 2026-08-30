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

			err := client.RequestJSON(http.MethodGet, "/version", nil, &Version{})
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

	if err := client.RequestJSON(http.MethodGet, "/version", nil, nil); err != nil {
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

	if err := client.RequestJSON(http.MethodPost, "/api/v1/namespaces/house/pods", []byte(`{"kind":"Pod"}`), nil); err != nil {
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
	if err := client.RequestJSON(http.MethodGet, "/version", nil, nil); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer a-service-account-token" {
		t.Errorf("Authorization = %q, want the token from disk", authorization)
	}
}

func TestRequestJSONFailsWhenTheTokenIsMissing(t *testing.T) {
	client := NewClient("https://kubernetes.default.svc", http.DefaultClient, t.TempDir())

	err := client.RequestJSON(http.MethodGet, "/version", nil, nil)
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

	err := client.RequestJSON(http.MethodGet, "/version", nil, nil)

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
			Metadata: ObjectMeta{Name: "films", Namespace: "house"},
			Spec:     LibrarySpec{Kind: libraryKindFilms, Storage: LibraryStorage{Claim: "films", Root: "/"}},
		}},
	})

	list, err := ListLibraries(client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/apis/library.liken.sh/v1alpha1/libraries")
	if list.Metadata.ResourceVersion != "1200" {
		t.Errorf("resourceVersion = %q, want the collection's 1200", list.Metadata.ResourceVersion)
	}
	if len(list.Items) != 1 || list.Items[0].Spec.Storage.Claim != "films" {
		t.Errorf("items = %+v, want the one library the server answered", list.Items)
	}
}

// The status goes through its own subresource, so this request can
// never touch the spec a person declared.
func TestPutLibraryStatusWritesTheStatusSubresource(t *testing.T) {
	written := &Library{
		Metadata: ObjectMeta{Name: "films", Namespace: "house", ResourceVersion: "1200"},
		Status:   LibraryStatus{Titles: 412, Pod: "films-scanner"},
	}
	client, recorded := recordingAPI(t, written)

	back, err := PutLibraryStatus(client, written)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPut, "/apis/library.liken.sh/v1alpha1/namespaces/house/libraries/films/status")
	if !strings.Contains(recorded.body, `"resourceVersion":"1200"`) {
		t.Errorf("body = %s, want the resourceVersion that makes the write conditional", recorded.body)
	}
	if !strings.Contains(recorded.body, `"titles":412`) {
		t.Errorf("body = %s, want the counts the operator folded", recorded.body)
	}
	if back.Status.Pod != "films-scanner" {
		t.Errorf("pod = %q, want the value the server wrote back", back.Status.Pod)
	}
}

// A zero-title report is an answer, so the count goes on the wire as 0
// rather than being dropped as an empty field.
func TestPutLibraryStatusWritesAZeroCount(t *testing.T) {
	client, recorded := recordingAPI(t, &Library{})

	if _, err := PutLibraryStatus(client, &Library{
		Metadata: ObjectMeta{Name: "films", Namespace: "house"},
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
		Metadata: ObjectMeta{Name: "films", Namespace: "house"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-films"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	})

	claim, err := GetPersistentVolumeClaim(client, "house", "films")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/namespaces/house/persistentvolumeclaims/films")
	if claim.Status.Phase != claimBound || claim.Spec.VolumeName != "pv-films" {
		t.Errorf("claim = %+v, want the bound claim and its volume", claim)
	}
}

// A PersistentVolume is cluster-scoped, so its path carries no
// namespace.
func TestGetPersistentVolumeReadsWhatServesTheStorage(t *testing.T) {
	client, recorded := recordingAPI(t, map[string]any{
		"metadata": map[string]any{"name": "pv-films"},
		"spec": map[string]any{
			"capacity": map[string]any{"storage": "8Ti"},
			"nfs":      map[string]any{"server": "films.example", "path": "/volume1/films"},
		},
	})

	volume, err := GetPersistentVolume(client, "pv-films")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/persistentvolumes/pv-films")
	if volume.Spec.Source != "nfs" || volume.Spec.NFS.Server != "films.example" {
		t.Errorf("spec = %+v, want the NFS export the server answered", volume.Spec)
	}
}

// One list answers every namespace, and the label selector is what
// keeps the answer to this operator's own pods.
func TestListScannerPodsSelectsTheOperatorsOwnPods(t *testing.T) {
	client, recorded := recordingAPI(t, PodList{
		Metadata: ListMeta{ResourceVersion: "88"},
		Items:    []Pod{{Metadata: ObjectMeta{Name: "films-scanner", Namespace: "house"}}},
	})

	list, err := ListScannerPods(client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/pods")
	if got := recorded.query.Get("labelSelector"); got != "app.kubernetes.io/name=library-scanner" {
		t.Errorf("labelSelector = %q, want the scanner selector", got)
	}
	if len(list.Items) != 1 || list.Items[0].Metadata.Name != "films-scanner" {
		t.Errorf("items = %+v, want the one scanner pod the server answered", list.Items)
	}
}

func TestGetPodReadsOnePodByName(t *testing.T) {
	client, recorded := recordingAPI(t, Pod{
		Metadata: ObjectMeta{Name: "films-scanner", Namespace: "house"},
		Status: PodStatus{
			Phase:             podRunning,
			ContainerStatuses: []ContainerStatus{{Name: "scanner", Ready: true}},
		},
	})

	pod, err := GetPod(client, "house", "films-scanner")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/namespaces/house/pods/films-scanner")
	if pod.Status.Phase != podRunning || !pod.Status.ContainerStatuses[0].Ready {
		t.Errorf("status = %+v, want the running pod with its ready container", pod.Status)
	}
}

func TestCreatePodPostsIntoTheLibrarysNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, Pod{Metadata: ObjectMeta{Name: "films-scanner", Namespace: "house"}})

	created, err := CreatePod(client, &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:      "films-scanner",
			Namespace: "house",
			Labels:    map[string]string{scannerLabelKey: scannerLabelValue},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPost, "/api/v1/namespaces/house/pods")
	if !strings.Contains(recorded.body, `"name":"films-scanner"`) {
		t.Errorf("body = %s, want the pod the operator built", recorded.body)
	}
	if created.Metadata.Name != "films-scanner" {
		t.Errorf("name = %q, want the pod the server wrote back", created.Metadata.Name)
	}
}

// A pod the operator already removed, or one Kubernetes removed first,
// leaves nothing to do, so an absent pod is success. Any other failure
// is reported.
func TestDeletePodTreatsAnAbsentPodAsDone(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "the pod was there", status: http.StatusOK},
		{name: "the pod was already gone", status: http.StatusNotFound},
		{name: "the server refused", status: http.StatusForbidden, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var path string
			client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.WriteHeader(testCase.status)
			}))

			err := DeletePod(client, "house", "films-scanner")

			if (err != nil) != testCase.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, testCase.wantErr)
			}
			if path != "/api/v1/namespaces/house/pods/films-scanner" {
				t.Errorf("path = %q, want the pod's own path", path)
			}
		})
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
		{name: "ListLibraries", call: func(c *Client) error { _, err := ListLibraries(c); return err }},
		{name: "PutLibraryStatus", call: func(c *Client) error { _, err := PutLibraryStatus(c, &Library{}); return err }},
		{name: "GetPersistentVolumeClaim", call: func(c *Client) error {
			_, err := GetPersistentVolumeClaim(c, "house", "films")
			return err
		}},
		{name: "GetPersistentVolume", call: func(c *Client) error { _, err := GetPersistentVolume(c, "pv-films"); return err }},
		{name: "ListScannerPods", call: func(c *Client) error { _, err := ListScannerPods(c); return err }},
		{name: "GetPod", call: func(c *Client) error { _, err := GetPod(c, "house", "films-scanner"); return err }},
		{name: "CreatePod", call: func(c *Client) error { _, err := CreatePod(c, &Pod{}); return err }},
		{name: "DeletePod", call: func(c *Client) error { return DeletePod(c, "house", "films-scanner") }},
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
