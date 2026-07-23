package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/cluster"
	"golang.org/x/crypto/ssh"
)

func fluxCluster() *cluster.Cluster {
	c := &cluster.Cluster{}
	c.Metadata.Name = "lab"
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"flux": {"repository": "ssh://git@forge.example/fleet.git"},
	}
	return c
}

// The minted pair must round-trip through the same library Flux's
// git client uses to read it, and the two halves must agree.
func TestMintDeployKeyProducesAMatchedPair(t *testing.T) {
	pub, priv, err := mintDeployKey("lab")
	if err != nil {
		t.Fatal(err)
	}
	parsedPub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		t.Fatalf("the public half must parse as an authorized_keys line: %v", err)
	}
	if comment != "liken:lab" {
		t.Errorf("the comment should name the cluster, got %q", comment)
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatalf("the private half must parse as an SSH private key: %v", err)
	}
	if string(signer.PublicKey().Marshal()) != string(parsedPub.Marshal()) {
		t.Error("the two halves must be the same key")
	}
}

func TestEnsureFluxDeployKeyDoesNothingWithoutTheFeature(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no API call should happen: %s %s", r.Method, r.URL.Path)
	}))
	plain := &cluster.Cluster{}
	plain.Metadata.Name = "lab"
	if got := ensureFluxDeployKey(c, plain); got != "" {
		t.Errorf("no feature, no key: got %q", got)
	}
}

func TestEnsureFluxDeployKeyReadsAnExistingKey(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("an existing key must never be replaced: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"type": "Opaque", "data": {"identity.pub": "c3NoLWVkMjU1MTkgQUFBQSBsaWtlbjpsYWI="}}`))
	}))
	got := ensureFluxDeployKey(c, fluxCluster())
	if got != "ssh-ed25519 AAAA liken:lab" {
		t.Errorf("got %q", got)
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
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
		}
	}))
	got := ensureFluxDeployKey(c, fluxCluster())
	if created.Metadata.Name != "flux-system" || created.Metadata.Namespace != "flux-system" {
		t.Errorf("the Secret must use Flux's conventional name: %+v", created.Metadata)
	}
	if !strings.HasPrefix(created.StringData["identity"], "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Errorf("identity must carry the private half: %q", created.StringData["identity"])
	}
	if !strings.HasPrefix(created.StringData["identity.pub"], "ssh-ed25519 ") {
		t.Errorf("identity.pub must carry the public half: %q", created.StringData["identity.pub"])
	}
	if got != created.StringData["identity.pub"] {
		t.Errorf("the returned key must be the minted one: %q vs %q", got, created.StringData["identity.pub"])
	}
}

// A conflicting create means another leader's operator minted first.
// The sweep returns nothing this pass and reads the winner's key on
// the next one; both copies then publish the same value.
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
	if got := ensureFluxDeployKey(c, fluxCluster()); got != "" {
		t.Errorf("a lost race publishes nothing this pass: got %q", got)
	}
}
