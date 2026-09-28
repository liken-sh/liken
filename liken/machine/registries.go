package machine

// This file holds the machine's half of private registries: the
// credentials document and the status this boot reports. The
// cluster's half lives in the cluster package (cluster/registries.go).
// It holds the mirror and embedded-registry declarations in
// spec.registries.
//
// The credentials document (RegistryCredentials) is deliberately not
// part of the Cluster spec. A spec is public: anyone who can get the
// Cluster can read every field. A password in a spec would also make
// every credential rotation a document edit that a person must write
// by hand. Instead, credentials enter through a Kubernetes Secret
// (kubernetes.io/dockerconfigjson, the shape that
// `kubectl create secret docker-registry` produces). The machine
// operator reads this Secret and renders it into this document. The
// document has canonical bytes with a hash, and it uses the same
// staged/proven lifecycle as every other document, in its own store.
// The operator is the document's only author: no image ever carries a
// seed. A machine that has never had credentials staged simply has
// none. This is an ordinary state (anonymous pulls), not an error.

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/liken-sh/liken/api"
	"sigs.k8s.io/yaml"
)

// RegistryCredential is one registry's login. It holds the auth that
// containerd presents when it pulls from this host, or from a mirror
// endpoint on it.
type RegistryCredential struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// RegistryCredentials is the credentials document. It is the
// operator's rendering of the registry-credentials Secret, in
// liken's own canonical shape. An empty document (no hosts) is the
// retraction rendering: a deleted Secret stages this document. The
// document needs a real hash so the lifecycle can tell "credentials
// withdrawn" apart from "never had any".
type RegistryCredentials struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Hosts      []RegistryCredential `json:"hosts,omitempty"`
}

// RegistryCredentialsStore is the credentials document's lifecycle
// store. It sits under the given machineState root, beside the
// Machine's, the Cluster's, and the system release's stores. The
// files land owner-only: WriteDurable's temp files are 0600, and
// rename preserves that mode. This matters here more than anywhere
// else, because these bytes are passwords.
func RegistryCredentialsStore(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "registries")}
}

// RenderRegistryCredentials produces the document's canonical bytes
// and their hash. It sorts the hosts so the same credentials always
// render the same bytes. The hash is the document's identity for
// staging idempotence, so an incidental ordering difference must
// never look like drift. The function copies the input before it
// sorts the hosts; a caller's slice stays the caller's own.
func RenderRegistryCredentials(hosts []RegistryCredential) ([]byte, string, error) {
	sorted := slices.Clone(hosts)
	slices.SortFunc(sorted, func(a, b RegistryCredential) int {
		return cmp.Compare(a.Host, b.Host)
	})
	return renderDocument(RegistryCredentials{
		APIVersion: api.APIVersion,
		Kind:       "RegistryCredentials",
		Hosts:      sorted,
	})
}

// ParseRegistryCredentials reads the document strictly, like every
// liken document. It rejects a malformed rendering once, visibly,
// instead of retrying it forever. Every entry must name its host and
// a username. A password may be empty, because some registries
// accept an empty password. But an entry with no username could
// never authenticate anywhere, so the function reports it as a
// rendering bug.
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

// RegistriesStatus is what this boot rendered into registries.yaml.
// It holds hosts and counts only, never credential material. It
// exists for console parity: it makes the same facts init prints at
// boot queryable on the Machine.
type RegistriesStatus struct {
	// Mirrors lists the registry hosts that registries.yaml carries
	// mirror entries for. The list includes "*" when the embedded
	// registry adds it.
	Mirrors []string `json:"mirrors,omitempty"`

	// CredentialedHosts lists the hosts that registries.yaml carries
	// auth for. It lists only the hosts, and it never lists the
	// credentials.
	CredentialedHosts []string `json:"credentialedHosts,omitempty"`

	// Embedded reports whether this boot turned the embedded
	// registry on.
	Embedded bool `json:"embedded,omitempty"`
}
