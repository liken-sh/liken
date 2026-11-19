package main

// This file is the cluster operator's part of the flux feature. It
// creates the SSH deploy key that Flux uses to read the fleet's git
// repository, and it creates the Flux controllers again when someone
// deletes them.
//
// The operator generates the key pair inside the cluster, so the
// private key never leaves it. A person copies the public key from
// the Cluster's status.flux.publicKey and registers it at the git
// forge as a deploy key. To rotate the key, delete the Secret, and the
// next pass creates a new pair.
//
// The cluster has one key, not one for each machine. Every leader has
// the datastore that stores the Secret, so a key for each machine would
// not limit who can read it. Init cannot create the Secret, because
// init runs before k3s starts.
//
// The operator gets, patches, and creates the Secret through the Role
// liken-deploy-key-minter in flux/manifests/flux-system.yaml. The Role
// is in the flux-system namespace, and tearDownFlux deletes that
// namespace when the Cluster stops declaring the flux feature, so the
// operator has no access to Secrets on a fleet that does not use Flux.

import (
	"crypto/ed25519"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"golang.org/x/crypto/ssh"
	"sigs.k8s.io/yaml"
)

// The key's Secret follows Flux's conventions: it is named
// flux-system, it is in the flux-system namespace, and its identity
// and identity.pub keys hold the key pair. Flux's git client finds it
// with no further configuration.
const (
	fluxSecretPath  = "/api/v1/namespaces/flux-system/secrets/flux-system"
	fluxSecretsPath = "/api/v1/namespaces/flux-system/secrets"
)

// ensureFluxDeployKey creates the fleet's deploy key Secret when the
// flux feature is declared, and returns the public key for the
// Cluster's status. It returns "" when the feature is off. It also
// returns "" on an API failure, so the status omits the key for that
// pass and the next pass tries again. Failures are expected for a
// short time after the feature turns on, because init writes the
// Role's manifest for k3s at boot, and k3s may not have applied it yet.
//
// When it creates the key, it posts a FluxDeployKeyMinted Event on the
// Cluster, because the person must register the public key at the
// forge before the first sync.
func ensureFluxDeployKey(c *apiclient.Client, recorder *events.Recorder, clusterDoc *cluster.Cluster) string {
	if !clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		return ""
	}
	// A malformed configuration does not stop the key from being
	// created, because the key does not depend on it. Init reports the
	// configuration error.
	cfg, _ := clusterDoc.FluxConfig()

	secret := &kubernetes.Secret{}
	err := c.RequestJSON(http.MethodGet, fluxSecretPath, nil, secret)
	if err == nil {
		// The key pair is never replaced here, but known_hosts follows
		// the Cluster's declaration. Without the update, a forge that
		// rotates its host key would fail every sync against the old
		// pinned key.
		if cfg != nil && string(secret.Data["known_hosts"]) != cfg.KnownHosts {
			patch, _ := json.Marshal(map[string]any{
				"stringData": map[string]string{"known_hosts": cfg.KnownHosts},
			})
			if err := kubernetes.PatchJSON(c, fluxSecretPath, patch); err != nil {
				fmt.Printf("updating known_hosts in the flux-system Secret: %v\n", err)
			}
		}
		return string(secret.Data["identity.pub"])
	}
	if !errors.Is(err, apiclient.ErrNotFound) {
		fmt.Printf("reading the flux-system Secret: %v\n", err)
		return ""
	}

	pub, priv, err := mintDeployKey(clusterDoc.Metadata.Name)
	if err != nil {
		fmt.Printf("generating the flux deploy key: %v\n", err)
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
		// The API server base64-encodes stringData into data.
		"stringData": map[string]string{
			"identity":     priv,
			"identity.pub": pub,
			"known_hosts":  knownHosts,
		},
	})
	if err != nil {
		fmt.Printf("encoding the flux-system Secret: %v\n", err)
		return ""
	}
	if err := c.RequestJSON(http.MethodPost, fluxSecretsPath, body, nil); err != nil {
		// A conflict means another leader's pass created the Secret
		// first. Its key is the fleet's key, and the next pass reads it.
		if !errors.Is(err, apiclient.ErrConflict) {
			fmt.Printf("creating the flux-system Secret: %v\n", err)
		}
		return ""
	}
	fmt.Printf("created the flux-system Secret with a new deploy key\n")
	recorder.Normal(clusterReference(clusterDoc), reasonFluxDeployKeyMinted,
		"Created the flux-system Secret with a new deploy key. Register the public key from status.flux.publicKey at the git forge")
	return pub
}

// The Flux manifest is the pinned gotk-components.yaml, which holds the
// Flux controllers. The flux component renders it at build time, and
// the Makefile copies it into seed/ because go:embed cannot read
// outside the package. The directory also holds a README, so a plain
// `go build` works before the manifest is there. A binary built that
// way has no Flux manifest, and reconcileFleet logs that while the flux
// feature is on.
//
//go:embed seed
var fluxManifestFS embed.FS

func fluxManifest() ([]byte, error) {
	return fluxManifestFS.ReadFile("seed/gotk-components.yaml")
}

// kustomizeControllerPath is the URL of the kustomize-controller Deployment.
// kustomize-controller applies everything in git, including the Flux
// controllers' own manifests, so while it exists Flux can repair
// itself from git. When it is missing nothing can, so the operator
// creates the objects of the Flux manifest again.
const kustomizeControllerPath = "/apis/apps/v1/namespaces/flux-system/deployments/kustomize-controller"

// manifestObject is one YAML document of the Flux manifest, with the fields that
// identify it and its body already converted to JSON for the API.
type manifestObject struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	body       []byte
}

// parseFluxManifest splits the manifest into documents and converts each to JSON.
// It splits on lines that hold only "---", which is how Flux renders
// the file.
func parseFluxManifest(raw []byte) ([]manifestObject, error) {
	var objects []manifestObject
	for _, doc := range strings.Split("\n"+string(raw), "\n---\n") {
		if strings.TrimSpace(stripYAMLComments(doc)) == "" {
			continue
		}
		body, err := yaml.YAMLToJSON([]byte(doc))
		if err != nil {
			return nil, fmt.Errorf("parsing a document of the Flux manifest: %w", err)
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
		objects = append(objects, manifestObject{
			APIVersion: meta.APIVersion,
			Kind:       meta.Kind,
			Name:       meta.Metadata.Name,
			Namespace:  meta.Metadata.Namespace,
			body:       body,
		})
	}
	return objects, nil
}

// stripYAMLComments drops comment and blank lines, so a document that
// has only comments, such as the header of the generated manifest, reads
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

// manifestKinds maps each kind in the Flux manifest to its REST resource name and
// whether it is namespaced. The API client does no discovery, so a new
// Flux release that adds a kind makes collectionPath fail, and the
// error says to add the kind here.
var manifestKinds = map[string]struct {
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

// collectionPath returns the URL that creates one manifest object. Core
// group objects are under /api/v1, and other groups are under
// /apis/<group>/<version>. Namespaced kinds add the namespace.
func collectionPath(o manifestObject) (string, error) {
	kind, known := manifestKinds[o.Kind]
	if !known {
		return "", fmt.Errorf("the Flux manifest has a %s, and manifestKinds has no entry for that kind", o.Kind)
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

// fluxProbeInterval is the minimum time between checks for the
// kustomize-controller Deployment. No watch covers that Deployment, so
// this check is how the operator finds that a person deleted it while
// the flux feature is on. The pass runs at least every ten seconds,
// and a GET on every pass would be the operator's most frequent
// request, for a result that almost never changes.
const fluxProbeInterval = 60 * time.Second

// fluxProbe records when the pass last checked for the
// kustomize-controller Deployment. The caller keeps one across passes.
// It needs no mutex, because passes run one at a time on one goroutine.
type fluxProbe struct {
	lastProbe time.Time
}

// due reports whether the pass should check now, and records the time
// when it should. The zero value is due, so the first pass checks.
// Because due records the time before the check runs, a pass that
// creates the Flux objects also starts a new interval, and the objects
// that were not created are retried at the next check.
func (p *fluxProbe) due(now time.Time) bool {
	if now.Sub(p.lastProbe) < fluxProbeInterval {
		return false
	}
	p.lastProbe = now
	return true
}

// ensureFluxControllers creates the objects of the Flux manifest when
// the kustomize-controller Deployment is missing, checking at most once
// per fluxProbeInterval. A create that returns a conflict means the
// object exists, and it is left as it is, so only the missing objects
// are created. A Flux installation that exists but is broken is
// repaired from git by kustomize-controller, not here. While the
// flux feature is off it returns before it calls due, so the first
// pass after the feature is declared checks at once. It posts a
// FluxEngineSeeded Event on the Cluster with the count of objects
// created, present, and failed.
func ensureFluxControllers(c *apiclient.Client, recorder *events.Recorder, clusterDoc *cluster.Cluster, manifest []byte, probe *fluxProbe, now time.Time) {
	if !clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		return
	}
	if !probe.due(now) {
		return
	}
	err := c.RequestJSON(http.MethodGet, kustomizeControllerPath, nil, nil)
	if err == nil {
		return
	}
	if !errors.Is(err, apiclient.ErrNotFound) {
		fmt.Printf("reading the Deployment kustomize-controller: %v\n", err)
		return
	}
	objects, err := parseFluxManifest(manifest)
	if err != nil {
		fmt.Printf("parsing the embedded Flux manifest: %v\n", err)
		return
	}
	fmt.Printf("the Deployment kustomize-controller is missing, so the pass creates the %d objects of the embedded Flux manifest\n", len(objects))
	created, present, failed := 0, 0, 0
	for _, o := range objects {
		path, err := collectionPath(o)
		if err != nil {
			fmt.Printf("finding the API path of an object of the Flux manifest: %v\n", err)
			failed++
			continue
		}
		err = c.RequestJSON(http.MethodPost, path, o.body, nil)
		switch {
		case errors.Is(err, apiclient.ErrConflict):
			present++
		case err != nil:
			fmt.Printf("creating %s %s from the Flux manifest: %v\n", o.Kind, o.Name, err)
			failed++
		default:
			created++
		}
	}
	recorder.Normal(clusterReference(clusterDoc), reasonFluxEngineSeeded, fmt.Sprintf(
		"Applied the embedded Flux manifest, because the kustomize-controller Deployment was missing: %d objects created, %d already present, %d not created",
		created, present, failed))
}

// mintDeployKey generates an ed25519 key pair. The public key is one
// authorized_keys line that ends with the comment "liken:<cluster>",
// so the cluster is identifiable in a forge's list of keys. The
// private key is in OpenSSH PEM format, which Flux's git client
// parses.
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
	// MarshalAuthorizedKey adds a trailing newline and no comment.
	// This replaces the newline with the comment.
	line := string(ssh.MarshalAuthorizedKey(sshPub))
	line = line[:len(line)-1] + " " + comment
	return line, string(pem.EncodeToMemory(pemBlock)), nil
}
