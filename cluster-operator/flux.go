package main

// The deploy key minter: the cluster operator's part of the flux
// feature.
//
// The flux feature syncs the fleet's declared state from a git
// repository, and that repository is expected to be private, so the
// sync engine needs a credential. liken mints that credential inside
// the cluster, instead of asking a person to generate a key and
// carry it in. The person's whole job is to copy the public half
// from the Cluster's status and register it at the forge as a deploy
// key. Private material never travels, never sits on a USB stick,
// and never depends on someone getting key formats right by hand.
//
// The key is one per cluster, not one per machine, because a
// narrower key would not narrow anything: the private half lives in
// a Secret, every Secret lives in the cluster datastore, and every
// leader carries that datastore. The datastore is the unit of
// exposure, so one key per datastore states the truth. Rotation is a
// person's decision, made by deleting the Secret; the next sweep
// mints a fresh pair and publishes the new half to register.
//
// This job belongs to the cluster operator because the credential is
// cluster-scoped: the sweep is the one writer of Cluster status, and
// the flux-system Secret is a cluster-level object that any leader's
// sweep may create. Init cannot do it, because init runs before k3s
// exists. The permission to touch the Secret arrives with the
// feature itself: the flux feature's manifests carry a Role in
// flux-system (flux/manifests/flux-system.yaml), seeded only while
// the feature is declared, so this operator holds no standing Secret
// access on fleets that never asked for GitOps.

import (
	"crypto/ed25519"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"golang.org/x/crypto/ssh"
	"sigs.k8s.io/yaml"
)

// The Secret's home, by Flux's conventions: the flux-system
// namespace, a Secret named flux-system, and the identity /
// identity.pub keys that Flux's git client reads. Following the
// conventions means the sync engine finds the credential with no
// configuration pointing at it.
const (
	fluxSecretPath  = "/api/v1/namespaces/flux-system/secrets/flux-system"
	fluxSecretsPath = "/api/v1/namespaces/flux-system/secrets"
)

// ensureFluxDeployKey makes sure the fleet's deploy key exists when
// the flux feature is declared, and returns the public half for the
// status to carry. It returns "" when the feature is off, and also
// on any API failure, because the sweep runs again and a missing
// status one pass long is cheaper than a wrong one. The first passes
// after the feature turns on can fail here legitimately: the Role
// that grants this operator its Secret access is itself seeded by
// the feature, and k3s may not have applied it yet. The sweep's
// level-triggered loop absorbs that window; nothing here needs to
// wait or retry.
func ensureFluxDeployKey(c *kubernetes.Client, clusterDoc *cluster.Cluster) string {
	if !clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		return ""
	}
	// The declared configuration rides along for known_hosts. A
	// malformed declaration does not block the mint: the key is
	// per-cluster and parameter-independent, and init's feature pass
	// already reports the configuration problem.
	cfg, _ := clusterDoc.FluxConfig()

	secret := &kubernetes.Secret{}
	err := c.RequestJSON(http.MethodGet, fluxSecretPath, nil, secret)
	if err == nil {
		// The key exists and never changes here, but the Secret's
		// known_hosts entry follows the declaration: a forge that
		// rotates its host key must not strand the sync on a stale
		// pin.
		if cfg != nil && string(secret.Data["known_hosts"]) != cfg.KnownHosts {
			patch, _ := json.Marshal(map[string]any{
				"stringData": map[string]string{"known_hosts": cfg.KnownHosts},
			})
			if err := c.PatchJSON(fluxSecretPath, patch); err != nil {
				fmt.Printf("refreshing the flux known_hosts: %v\n", err)
			}
		}
		return string(secret.Data["identity.pub"])
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		fmt.Printf("reading the flux deploy key: %v\n", err)
		return ""
	}

	pub, priv, err := mintDeployKey(clusterDoc.Metadata.Name)
	if err != nil {
		fmt.Printf("minting the flux deploy key: %v\n", err)
		return ""
	}
	knownHosts := ""
	if cfg != nil {
		knownHosts = cfg.KnownHosts
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      "flux-system",
			"namespace": "flux-system",
		},
		"type": "Opaque",
		// stringData is the write-side convenience: the API server
		// encodes it into data, so this code never handles base64.
		"stringData": map[string]string{
			"identity":     priv,
			"identity.pub": pub,
			"known_hosts":  knownHosts,
		},
	})
	if err != nil {
		fmt.Printf("encoding the flux deploy key secret: %v\n", err)
		return ""
	}
	if err := c.RequestJSON(http.MethodPost, fluxSecretsPath, body, nil); err != nil {
		// A conflict means another leader's sweep minted first. That
		// copy's key is the fleet's key; the next pass reads it.
		if !errors.Is(err, kubernetes.ErrConflict) {
			fmt.Printf("creating the flux deploy key secret: %v\n", err)
		}
		return ""
	}
	fmt.Printf("minted the flux deploy key; register the public half from the Cluster's status at the forge\n")
	return pub
}

// The engine seed: the pinned gotk-components.yaml that the flux
// domain renders at build time and the Makefile copies here for
// the embed below, which cannot reach outside the package. The
// directory always holds a README, so a plain `go build` works
// before the flux domain has fetched anything; a binary built that
// way carries no seed, and the planter reports it instead of
// planting nothing silently.
//
//go:embed seed
var seedFS embed.FS

func engineSeed() ([]byte, error) {
	return seedFS.ReadFile("seed/gotk-components.yaml")
}

// engineProbePath names the one object whose absence means the
// engine is gone: the kustomize-controller Deployment. That
// controller is the applier that heals everything else from git, so
// while it exists, git owns the engine, whatever its state; when it
// does not, nothing can heal, and liken re-plants the seed.
const engineProbePath = "/apis/apps/v1/namespaces/flux-system/deployments/kustomize-controller"

// seedObject is one document of the seed: its addressing fields,
// and its whole body as JSON, ready to send.
type seedObject struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	body       []byte
}

// parseSeed splits the seed into its documents. The seed is
// flux-rendered YAML with clean document separators, so the split is
// by separator lines; each document converts to JSON once, here,
// and never again on the apply path.
func parseSeed(raw []byte) ([]seedObject, error) {
	var objects []seedObject
	for _, doc := range strings.Split("\n"+string(raw), "\n---\n") {
		if strings.TrimSpace(stripYAMLComments(doc)) == "" {
			continue
		}
		body, err := yaml.YAMLToJSON([]byte(doc))
		if err != nil {
			return nil, fmt.Errorf("parsing a seed document: %w", err)
		}
		var meta struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			return nil, err
		}
		objects = append(objects, seedObject{
			APIVersion: meta.APIVersion,
			Kind:       meta.Kind,
			Name:       meta.Metadata.Name,
			Namespace:  meta.Metadata.Namespace,
			body:       body,
		})
	}
	return objects, nil
}

// stripYAMLComments drops comment and blank lines, so a document
// that is only commentary (the seed's generated-file header) reads
// as empty.
func stripYAMLComments(doc string) string {
	var kept []string
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// seedKinds maps each kind the seed may contain to its REST
// resource. liken's API client has no discovery machinery, and it
// does not need any: the seed's vocabulary is small and known, and
// an unknown kind is a loud error naming what to add, which happens
// exactly when a new Flux version grows a new kind.
var seedKinds = map[string]struct {
	resource   string
	namespaced bool
}{
	"Namespace":                {"namespaces", false},
	"ResourceQuota":            {"resourcequotas", true},
	"NetworkPolicy":            {"networkpolicies", true},
	"CustomResourceDefinition": {"customresourcedefinitions", false},
	"ServiceAccount":           {"serviceaccounts", true},
	"ClusterRole":              {"clusterroles", false},
	"ClusterRoleBinding":       {"clusterrolebindings", false},
	"Role":                     {"roles", true},
	"RoleBinding":              {"rolebindings", true},
	"Service":                  {"services", true},
	"Deployment":               {"deployments", true},
}

// collectionPath builds the create URL for one seed object: the core
// group lives under /api/v1, every other group under /apis/<group>/
// <version>, with the namespace segment for namespaced kinds.
func collectionPath(o seedObject) (string, error) {
	kind, known := seedKinds[o.Kind]
	if !known {
		return "", fmt.Errorf("the seed contains a %s, a kind the planter's table does not map; add it to seedKinds", o.Kind)
	}
	base := "/apis/" + o.APIVersion
	if !strings.Contains(o.APIVersion, "/") {
		base = "/api/" + o.APIVersion
	}
	if kind.namespaced {
		return base + "/namespaces/" + o.Namespace + "/" + kind.resource, nil
	}
	return base + "/" + kind.resource, nil
}

// ensureFluxEngine plants the engine seed when the engine is gone.
// The probe runs on every sweep, so a deleted engine heals in
// seconds, not at the next boot. Each object is a plain create, and
// a conflict means the object already exists, which the planter
// leaves exactly as it found it: the seed only ever fills absence.
// Present but broken stays the repository's problem on purpose;
// liken answers only for gone.
func ensureFluxEngine(c *kubernetes.Client, clusterDoc *cluster.Cluster, seed []byte) {
	if !clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		return
	}
	err := c.RequestJSON(http.MethodGet, engineProbePath, nil, nil)
	if err == nil {
		return
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		fmt.Printf("probing for the flux engine: %v\n", err)
		return
	}
	objects, err := parseSeed(seed)
	if err != nil {
		fmt.Printf("reading the engine seed: %v\n", err)
		return
	}
	fmt.Printf("the flux engine is absent; planting the seed (%d objects)\n", len(objects))
	for _, o := range objects {
		path, err := collectionPath(o)
		if err != nil {
			fmt.Printf("planting the engine seed: %v\n", err)
			continue
		}
		err = c.RequestJSON(http.MethodPost, path, o.body, nil)
		if errors.Is(err, kubernetes.ErrConflict) {
			continue // it already exists; whatever is there stays
		}
		if err != nil {
			fmt.Printf("planting %s %s: %v\n", o.Kind, o.Name, err)
		}
	}
}

// mintDeployKey generates the pair: an ed25519 key, the modern SSH
// default, small enough to read aloud and accepted by every current
// forge. The public half is one authorized_keys line, commented with
// the cluster's name so a forge's key list stays legible. The
// private half is OpenSSH PEM, the format Flux's git client parses.
func mintDeployKey(clusterName string) (publicKey, privateKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}
	comment := "liken:" + clusterName
	pemBlock, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	// MarshalAuthorizedKey ends the line with a newline and no
	// comment; the status reads better with the comment and without
	// the newline.
	line := string(ssh.MarshalAuthorizedKey(sshPub))
	line = line[:len(line)-1] + " " + comment
	return line, string(pem.EncodeToMemory(pemBlock)), nil
}
