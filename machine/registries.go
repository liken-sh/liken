package machine

// Private registries, the machine's half: the credentials document
// and the status this boot reports. The cluster's half — the mirror
// and embedded-registry declarations in spec.registries — lives in
// the cluster package (cluster/registries.go).
//
// The credentials document (RegistryCredentials) is deliberately not
// part of the Cluster spec. A spec is public:
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
	"path/filepath"
	"slices"

	"sigs.k8s.io/yaml"
)

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
// (WriteDurable's temp files are 0600, and rename preserves that),
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
	return renderDocument(RegistryCredentials{
		APIVersion: APIVersion,
		Kind:       "RegistryCredentials",
		Hosts:      sorted,
	})
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
