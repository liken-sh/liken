package machine

// Private registries: how container images arrive on the fleet.
//
// Two declarations live here. The first is spec.registries on the
// Cluster (RegistriesSpec): mirror endpoints containerd pulls
// through, and k3s's embedded peer-to-peer registry. Those are
// cluster facts — any node may be asked to pull any image — and they
// converge inside the canonical cluster document like every other
// fleet-wide fact.
//
// The second is the credentials document (RegistryCredentials), and
// it is deliberately not part of the Cluster spec. A spec is public:
// anyone who can get the Cluster can read every field, and a
// password in a spec would also make every credential rotation a
// document edit for people to hand-author. Credentials instead enter
// through a Kubernetes Secret (kubernetes.io/dockerconfigjson, the
// shape `kubectl create secret docker-registry` produces), which the
// machine operator reads and renders into this document — canonical
// bytes with a hash, riding the same staged/proven lifecycle as
// every other document, in its own store. The operator is the
// document's only author: no image ever carries a seed, and a
// machine that has never had credentials staged simply has none,
// which is an ordinary state (anonymous pulls), not an error.

import (
	"cmp"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"slices"

	"sigs.k8s.io/yaml"
)

// RegistriesSpec is the Cluster's declaration of how images arrive.
// The zero value means nothing declared: no mirrors, no embedded
// registry, no registries.yaml written at all.
type RegistriesSpec struct {
	// Mirrors maps a registry host, as an image reference names it
	// (docker.io, registry.example:5000), to the endpoint URLs
	// containerd should try, in preference order, before falling
	// back to the registry itself.
	Mirrors map[string][]string `json:"mirrors,omitempty"`

	// Embedded turns on k3s's embedded registry mirror (Spegel):
	// every node serves the images it already holds to its peers,
	// which is what keeps a fleet on a slow uplink from pulling the
	// same bytes once per machine.
	Embedded bool `json:"embedded,omitempty"`
}

// validateRegistries holds spec.registries to its shape. The spec
// itself may be null or absent — both decode to the zero struct and
// genuinely mean "no registries configuration", a contrast with
// spec.features, where a null could be mistaken for an opt-in. But a
// mirror host with a null or empty endpoint list is refused loudly:
// a mirror with nowhere to point is neither a mirror nor nothing,
// and `docker.io:` in hand-written YAML is a mistake to name, not to
// guess about.
func validateRegistries(r RegistriesSpec) error {
	for _, host := range slices.Sorted(maps.Keys(r.Mirrors)) {
		if host == "" {
			return fmt.Errorf("spec.registries.mirrors: a mirror's key must be the registry host it stands in for (docker.io, registry.example:5000)")
		}
		endpoints := r.Mirrors[host]
		if len(endpoints) == 0 {
			return fmt.Errorf("spec.registries.mirrors: %s lists no endpoints; list at least one endpoint URL, or remove the host entirely", host)
		}
		for _, endpoint := range endpoints {
			u, err := url.Parse(endpoint)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("spec.registries.mirrors: %s endpoint %q must be an http:// or https:// URL", host, endpoint)
			}
		}
	}
	return nil
}

// RegistryCredential is one registry's login: the auth containerd
// presents when it pulls from this host (or from a mirror endpoint
// on it).
type RegistryCredential struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// RegistryCredentials is the credentials document: the operator's
// rendering of the registry-credentials Secret, in liken's own
// canonical shape. An empty document (no hosts) is the retraction
// rendering — a deleted Secret stages this, and it needs a real hash
// so the lifecycle can tell "credentials withdrawn" apart from
// "never had any".
type RegistryCredentials struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Hosts      []RegistryCredential `json:"hosts,omitempty"`
}

// RegistryCredentialsStore is the credentials document's lifecycle
// store under the given machineState root, beside the Machine's, the
// Cluster's, and the system release's. The files land owner-only
// (writeDurable's temp files are 0600, and rename preserves that),
// which matters here more than anywhere: these bytes are passwords.
func RegistryCredentialsStore(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "registries")}
}

// RenderRegistryCredentials produces the document's canonical bytes
// and their hash. Hosts are sorted so the same credentials always
// render the same bytes: the hash is the document's identity for
// staging idempotence, and an incidental ordering difference must
// never read as drift. The input is copied before sorting; a
// caller's slice is theirs.
func RenderRegistryCredentials(hosts []RegistryCredential) ([]byte, string, error) {
	sorted := slices.Clone(hosts)
	slices.SortFunc(sorted, func(a, b RegistryCredential) int {
		return cmp.Compare(a.Host, b.Host)
	})
	doc := RegistryCredentials{
		APIVersion: APIVersion,
		Kind:       "RegistryCredentials",
		Hosts:      sorted,
	}
	raw, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, "", err
	}
	return raw, ManifestHash(raw), nil
}

// ParseRegistryCredentials reads the document strictly, like every
// liken document: a malformed rendering should be rejected once,
// visibly, not retried forever. Every entry must name its host and a
// username; a password may be empty (some registries accept one),
// but an entry with no username could never authenticate anywhere
// and is a rendering bug to surface.
func ParseRegistryCredentials(raw []byte) (*RegistryCredentials, error) {
	c := &RegistryCredentials{}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, err
	}
	if c.Kind != "RegistryCredentials" {
		return nil, fmt.Errorf("expected kind RegistryCredentials, got %q", c.Kind)
	}
	for _, h := range c.Hosts {
		if h.Host == "" {
			return nil, fmt.Errorf("a credential entry names no host")
		}
		if h.Username == "" {
			return nil, fmt.Errorf("the credential for %s names no username", h.Host)
		}
	}
	return c, nil
}

// RegistriesStatus is what this boot rendered into registries.yaml:
// hosts and counts only, never credential material. It exists for
// console parity — the same facts init prints at boot, made
// queryable on the Machine.
type RegistriesStatus struct {
	// Mirrors lists the registry hosts registries.yaml carries
	// mirror entries for, "*" included when the embedded registry
	// adds it.
	Mirrors []string `json:"mirrors,omitempty"`

	// CredentialedHosts lists the hosts registries.yaml carries auth
	// for — the hosts, deliberately never the credentials.
	CredentialedHosts []string `json:"credentialedHosts,omitempty"`

	// Embedded reports whether this boot turned the embedded
	// registry on.
	Embedded bool `json:"embedded,omitempty"`
}
