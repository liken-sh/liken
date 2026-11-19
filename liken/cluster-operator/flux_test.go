package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/cluster"
	"golang.org/x/crypto/ssh"
)

func fluxCluster() *cluster.Cluster {
	c := &cluster.Cluster{}
	c.Metadata.Name = "lab"
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"flux": {
			"repository": "ssh://git@forge.example/fleet.git",
			"knownHosts": "forge.example ssh-ed25519 HOSTKEY",
		},
	}
	return c
}

// Flux's git client parses keys with the same ssh library, so the
// generated keys must parse with it and be halves of one key.
func TestMintDeployKeyProducesAMatchedPair(t *testing.T) {
	pub, priv, err := mintDeployKey("lab")
	if err != nil {
		t.Fatalf("generating the deploy key: %v", err)
	}
	parsedPub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		t.Fatalf("parsing the public half as an authorized_keys line: %v", err)
	}
	if comment != "liken:lab" {
		t.Errorf("the key comment is %q, want liken:lab", comment)
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatalf("parsing the private half as an SSH private key: %v", err)
	}
	if string(signer.PublicKey().Marshal()) != string(parsedPub.Marshal()) {
		t.Error("the public half and the private half are different keys, want one key")
	}
}

func TestEnsureFluxDeployKeyDoesNothingWithoutTheFeature(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the request %s %s was sent, want no request", r.Method, r.URL.Path)
	}))
	plain := &cluster.Cluster{}
	plain.Metadata.Name = "lab"
	if got := ensureFluxDeployKey(c, nil, plain); got != "" {
		t.Errorf("the key is %q with the feature off, want empty", got)
	}
}

func TestEnsureFluxDeployKeyReadsAnExistingKey(t *testing.T) {
	// The API returns data values base64-encoded. known_hosts already
	// matches the declaration, so the function must write nothing.
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("the request %s %s was sent, want only reads when the key and known_hosts are current", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"type": "Opaque", "data": {
			"identity.pub": "c3NoLWVkMjU1MTkgQUFBQSBsaWtlbjpsYWI=",
			"known_hosts": "Zm9yZ2UuZXhhbXBsZSBzc2gtZWQyNTUxOSBIT1NUS0VZ"}}`))
	}))
	got := ensureFluxDeployKey(c, nil, fluxCluster())
	if got != "ssh-ed25519 AAAA liken:lab" {
		t.Errorf("the public key is %q, want the key in the Secret", got)
	}
}

// The Cluster's knownHosts can change after the key is created, for
// example when a forge rotates its host key. The key pair stays the
// same and the Secret's known_hosts entry is updated.
func TestEnsureFluxDeployKeyRefreshesStaleKnownHosts(t *testing.T) {
	var patched string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"type": "Opaque", "data": {
				"identity.pub": "c3NoLWVkMjU1MTkgQUFBQSBsaWtlbjpsYWI=",
				"known_hosts": "b2xk"}}`))
		case http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			patched = string(body)
		default:
			t.Errorf("the request %s %s was sent, want only GET and PATCH", r.Method, r.URL.Path)
		}
	}))
	if got := ensureFluxDeployKey(c, nil, fluxCluster()); got != "ssh-ed25519 AAAA liken:lab" {
		t.Errorf("the public key is %q, want the key in the Secret", got)
	}
	if !strings.Contains(patched, "forge.example ssh-ed25519 HOSTKEY") {
		t.Errorf("the patch body is %q, want the declared known_hosts", patched)
	}
}

func TestEnsureFluxDeployKeyMintsIntoTheSecret(t *testing.T) {
	var created struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		StringData map[string]string `json:"stringData"`
	}
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			http.NotFound(w, r)
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatalf("decoding the created Secret: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
		}
	}))
	got := ensureFluxDeployKey(c, nil, fluxCluster())
	if created.Metadata.Name != "flux-system" || created.Metadata.Namespace != "flux-system" {
		t.Errorf("the created Secret is %+v, want name and namespace flux-system", created.Metadata)
	}
	if !strings.HasPrefix(created.StringData["identity"], "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Errorf("identity is %q, want the private half", created.StringData["identity"])
	}
	if !strings.HasPrefix(created.StringData["identity.pub"], "ssh-ed25519 ") {
		t.Errorf("identity.pub is %q, want the public half", created.StringData["identity.pub"])
	}
	if created.StringData["known_hosts"] != "forge.example ssh-ed25519 HOSTKEY" {
		t.Errorf("known_hosts is %q, want the declared host keys", created.StringData["known_hosts"])
	}
	if got != created.StringData["identity.pub"] {
		t.Errorf("the returned key is %q, want the created identity.pub %q", got, created.StringData["identity.pub"])
	}
}

const testFluxManifest = `---
# This manifest was generated by flux. DO NOT EDIT.
apiVersion: v1
kind: Namespace
metadata:
  name: flux-system
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gitrepositories.source.toolkit.fluxcd.io
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kustomize-controller
  namespace: flux-system
`

func TestParseFluxManifestReadsEveryDocument(t *testing.T) {
	objects, err := parseFluxManifest([]byte(testFluxManifest))
	if err != nil {
		t.Fatalf("parsing the test Flux manifest: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("the Flux manifest has %d objects, want 3", len(objects))
	}
	if objects[0].Kind != "Namespace" || objects[0].Name != "flux-system" {
		t.Errorf("the first object is %+v, want Namespace flux-system", objects[0])
	}
	if objects[2].Namespace != "flux-system" {
		t.Errorf("the Deployment is %+v, want namespace flux-system", objects[2])
	}
}

func TestCollectionPathOfEachManifestKind(t *testing.T) {
	for _, tc := range []struct {
		apiVersion, kind, namespace, want string
	}{
		{"v1", "Namespace", "", "/api/v1/namespaces"},
		{"v1", "ServiceAccount", "flux-system", "/api/v1/namespaces/flux-system/serviceaccounts"},
		{"apps/v1", "Deployment", "flux-system", "/apis/apps/v1/namespaces/flux-system/deployments"},
		{"apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "/apis/apiextensions.k8s.io/v1/customresourcedefinitions"},
		{"rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "", "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings"},
		{"networking.k8s.io/v1", "NetworkPolicy", "flux-system", "/apis/networking.k8s.io/v1/namespaces/flux-system/networkpolicies"},
	} {
		got, err := collectionPath(manifestObject{APIVersion: tc.apiVersion, Kind: tc.kind, Namespace: tc.namespace})
		if err != nil {
			t.Errorf("collectionPath for %s returned the error %v", tc.kind, err)
		}
		if got != tc.want {
			t.Errorf("collectionPath for %s is %q, want %q", tc.kind, got, tc.want)
		}
	}
	if _, err := collectionPath(manifestObject{APIVersion: "v1", Kind: "Pod"}); err == nil {
		t.Error("collectionPath for a Pod returned no error, want an error because manifestKinds has no Pod")
	}
}

// realFluxManifest returns the Flux manifest that the binary embeds. The flux
// component renders it, and `make test-go` copies it into seed/ before
// the tests run. A plain `go test` in a checkout without the rendered
// manifest skips the test, because the embed holds only the README.
// Under CI a missing manifest fails the test instead, because a skip
// would let a Flux upgrade that breaks the manifest pass.
func realFluxManifest(t *testing.T) []byte {
	t.Helper()
	manifest, err := fluxManifest()
	if err == nil {
		return manifest
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("the Flux manifest is missing under CI: %v. Run make test-go, which copies the Flux manifest from the flux component", err)
	}
	t.Skip("the Flux manifest is not copied yet. make test-go copies it")
	return nil
}

// Every document of the real Flux manifest must parse and have a kind in
// manifestKinds, and the Flux manifest must contain the kustomize-controller
// Deployment that the probe looks for. This test fails when a Flux
// upgrade adds a kind that manifestKinds does not map.
func TestTheRealSeedParsesAndMapsWhole(t *testing.T) {
	manifest := realFluxManifest(t)
	objects, err := parseFluxManifest(manifest)
	if err != nil {
		t.Fatalf("parsing the real Flux manifest: %v", err)
	}
	probeSeen := false
	for _, o := range objects {
		if _, err := collectionPath(o); err != nil {
			t.Errorf("the Flux manifest object %s %s has no collection path: %v", o.Kind, o.Name, err)
		}
		if o.Kind == "Deployment" && o.Name == "kustomize-controller" {
			probeSeen = true
		}
	}
	if !probeSeen {
		t.Error("the Flux manifest has no kustomize-controller Deployment, want one because the Flux probe reads it")
	}
}

func TestEnsureFluxControllersLeavesPresentControllersAlone(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("the request %s %s was sent, want only reads when the Flux controllers are present", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"metadata": {"name": "kustomize-controller"}}`))
	}))
	ensureFluxControllers(c, nil, fluxCluster(), []byte(testFluxManifest), &fluxProbe{}, passTime)
}

func TestEnsureFluxControllersCreatesThemWhenAbsent(t *testing.T) {
	var created []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			http.NotFound(w, r)
		case http.MethodPost:
			created = append(created, r.URL.Path)
			// The namespace already exists, so ensureFluxControllers must
			// accept the conflict and continue.
			if r.URL.Path == "/api/v1/namespaces" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}
	}))
	ensureFluxControllers(c, nil, fluxCluster(), []byte(testFluxManifest), &fluxProbe{}, passTime)
	want := []string{
		"/api/v1/namespaces",
		"/apis/apiextensions.k8s.io/v1/customresourcedefinitions",
		"/apis/apps/v1/namespaces/flux-system/deployments",
	}
	if !slices.Equal(created, want) {
		t.Errorf("the created paths are %v, want %v", created, want)
	}
}

// The first pass probes, a pass inside fluxProbeInterval does
// not, and a pass at the interval probes again.
func TestEnsureFluxControllersProbesOnceAnInterval(t *testing.T) {
	probes := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes++
		w.Write([]byte(`{"metadata": {"name": "kustomize-controller"}}`))
	}))
	probe := &fluxProbe{}

	ensureFluxControllers(c, nil, fluxCluster(), []byte(testFluxManifest), probe, passTime)
	if probes != 1 {
		t.Fatalf("after the first pass there were %d probes, want 1", probes)
	}
	ensureFluxControllers(c, nil, fluxCluster(), []byte(testFluxManifest), probe, passTime.Add(50*time.Second))
	if probes != 1 {
		t.Errorf("after a pass inside the interval there were %d probes, want 1", probes)
	}
	ensureFluxControllers(c, nil, fluxCluster(), []byte(testFluxManifest), probe, passTime.Add(fluxProbeInterval))
	if probes != 2 {
		t.Errorf("after a pass at the interval there were %d probes, want 2", probes)
	}
}

// Creating the Flux manifest counts as a probe, because the probe time is
// recorded before the check runs. The next pass, ten seconds later,
// does not probe again.
func TestEnsureFluxControllersCountsACreateAsAProbe(t *testing.T) {
	probes, creates := 0, 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			probes++
			http.NotFound(w, r)
		case http.MethodPost:
			creates++
			w.WriteHeader(http.StatusCreated)
		}
	}))
	probe := &fluxProbe{}

	ensureFluxControllers(c, nil, fluxCluster(), []byte(testFluxManifest), probe, passTime)
	ensureFluxControllers(c, nil, fluxCluster(), []byte(testFluxManifest), probe, passTime.Add(10*time.Second))
	if probes != 1 {
		t.Errorf("there were %d probes, want 1", probes)
	}
	if creates != 3 {
		t.Errorf("there were %d creates, want 3 for the whole Flux manifest", creates)
	}
}

// A cluster without the flux feature makes no request and does not
// record a probe time, so the first pass after the feature is
// declared probes at once.
func TestEnsureFluxControllersDoesNothingWithoutTheFeature(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the request %s %s was sent, want no request", r.Method, r.URL.Path)
	}))
	plain := &cluster.Cluster{}
	plain.Metadata.Name = "lab"
	probe := &fluxProbe{}

	ensureFluxControllers(c, nil, plain, []byte(testFluxManifest), probe, passTime)

	if !probe.lastProbe.IsZero() {
		t.Errorf("lastProbe is %v with the feature off, want the zero time", probe.lastProbe)
	}
}

// A conflict on create means another leader's operator created the
// Secret first. This pass returns no key, and the next pass reads the
// other operator's key, so both operators publish the same value.
func TestEnsureFluxDeployKeyYieldsToAConcurrentMint(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			http.NotFound(w, r)
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"kind": "Status", "reason": "AlreadyExists"}`))
		}
	}))
	if got := ensureFluxDeployKey(c, nil, fluxCluster()); got != "" {
		t.Errorf("the key is %q after a conflict on create, want empty", got)
	}
}

// Every cluster-scoped object that the Flux manifest creates must be in
// fluxTeardownPaths, and manifests/cluster-operator.yaml must name it
// in the operator's RBAC rules. A Flux upgrade that adds a CRD fails
// here until both are updated. Namespaced objects need no entry,
// because deleting the namespace deletes them.
func TestTheTeardownAndItsRightsCoverTheFluxManifest(t *testing.T) {
	manifest := realFluxManifest(t)
	objects, err := parseFluxManifest(manifest)
	if err != nil {
		t.Fatalf("parsing the real Flux manifest: %v", err)
	}
	rights, err := os.ReadFile("manifests/cluster-operator.yaml")
	if err != nil {
		t.Fatalf("reading the operator manifest: %v", err)
	}
	for _, o := range objects {
		var path string
		switch o.Kind {
		case "CustomResourceDefinition":
			path = "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/" + o.Name
		case "ClusterRole":
			path = "/apis/rbac.authorization.k8s.io/v1/clusterroles/" + o.Name
		case "ClusterRoleBinding":
			path = "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/" + o.Name
		default:
			continue
		}
		if !slices.Contains(fluxTeardownPaths, path) {
			t.Errorf("fluxTeardownPaths has no path for %s %s, want one", o.Kind, o.Name)
		}
		if !strings.Contains(string(rights), o.Name) {
			t.Errorf("the RBAC rules in manifests/cluster-operator.yaml do not name %s %s, want it named", o.Kind, o.Name)
		}
	}
}
