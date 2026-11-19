package machine

// This file has the machine's half of private registry support: the
// credentials document and the status that a boot reports. The
// mirror and embedded-registry settings in spec.registries belong to
// the cluster package (cluster/registries.go).
//
// The credentials are not part of the Cluster spec, because anyone who
// can read the Cluster can read every field of its spec, and a
// password there would turn each rotation into a hand edit. Credentials
// come from a Kubernetes Secret of type kubernetes.io/dockerconfigjson,
// the kind that `kubectl create secret docker-registry` creates. The
// machine operator reads the Secret and renders it into the
// RegistryCredentials document, which has canonical bytes and a hash
// and goes through the same staged and proven lifecycle as the other
// documents, in its own store. The operator is the only author of the
// document and no image has a seed copy. A machine with no
// credentials staged pulls anonymously, which is not an error.

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/liken-sh/liken/liken/api"
	"sigs.k8s.io/yaml"
)

// RegistryCredential is the login for one registry host. containerd
// sends it when it pulls from that host or from a mirror endpoint on
// it.
type RegistryCredential struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// RegistryCredentials is the credentials document: the operator's
// rendering of the registry-credentials Secret. A document with no
// hosts is what the operator stages after the Secret is deleted. It
// has a real hash, so the lifecycle can tell withdrawn credentials
// from a machine that never had any.
type RegistryCredentials struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Hosts      []RegistryCredential `json:"hosts,omitempty"`
}

// RegistryCredentialsStore is the store of the credentials document,
// under the machineState root beside the stores of the other
// documents. The files are readable by their owner only, because
// WriteDurable creates its temp files with mode 0600 and rename keeps
// that mode. The files hold passwords, so the mode matters.
func RegistryCredentialsStore(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "registries")}
}

// RenderRegistryCredentials returns the document's bytes and hash. It
// sorts the hosts so the same credentials always produce the same
// bytes, because the hash decides whether the operator stages a new
// document and a different order must not look like a change. It
// sorts a copy, so the caller's slice keeps its order.
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

// ParseRegistryCredentials parses the document strictly, like every
// liken document, so a malformed rendering is rejected once instead of
// retried. Every entry needs a host and a username. A password can be
// empty because some registries accept an empty password. An entry
// with no username cannot authenticate anywhere, so it is reported as
// a rendering bug.
func ParseRegistryCredentials(raw []byte) (*RegistryCredentials, error) {
	c := &RegistryCredentials{}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, err
	}
	if c.Kind != "RegistryCredentials" {
		return nil, fmt.Errorf("the document has kind %q, and a credentials document has kind RegistryCredentials", c.Kind)
	}
	for _, h := range c.Hosts {
		if h.Host == "" {
			return nil, errors.New("a credential entry has no host")
		}
		if h.Username == "" {
			return nil, fmt.Errorf("the credential for host %q has no username", h.Host)
		}
	}
	return c, nil
}

// RegistriesStatus is what this boot wrote into registries.yaml, so
// the Machine shows the same facts that init prints on the console.
// It never holds credentials.
type RegistriesStatus struct {
	// Mirrors lists the hosts that registries.yaml has mirror entries
	// for. It includes "*" when the embedded registry adds one.
	Mirrors []string `json:"mirrors,omitempty"`

	// CredentialedHosts lists the hosts that registries.yaml has
	// credentials for. It lists the hosts only.
	CredentialedHosts []string `json:"credentialedHosts,omitempty"`

	// Embedded is true when this boot enabled the embedded registry.
	Embedded bool `json:"embedded,omitempty"`
}
