package kubernetes

// This file reads liken's one Secret: the fleet's registry
// credentials.
//
// Secrets are the API's structure for confidential material. The
// machine operator reads exactly one Secret, by its exact name,
// under RBAC that grants get on that name and nothing else (the
// operator's manifest carries the Role). The well-known name is what
// makes this narrow grant possible: no configuration points at an
// arbitrary Secret, so the operator never needs a wider permission.

import (
	"errors"
	"net/http"
)

// RegistryCredentialsSecretPath names the URL where the fleet's
// registry credentials live: a kubernetes.io/dockerconfigjson Secret
// named registry-credentials in liken-system, in the shape that
// `kubectl create secret docker-registry` produces. The URL carries
// a namespace segment because Secrets live inside a namespace,
// unlike liken's own cluster-scoped CRDs.
const RegistryCredentialsSecretPath = "/api/v1/namespaces/liken-system/secrets/registry-credentials"

// Secret holds the part of a Kubernetes Secret that liken reads: its
// type, which says what the data means, and the data itself. The API
// serves data values base64-encoded. Using []byte tells encoding/json
// to decode that value automatically.
type Secret struct {
	Type string            `json:"type"`
	Data map[string][]byte `json:"data"`
}

// GetRegistryCredentialsSecret reads the fleet's registry
// credentials. An absent Secret returns nil, nil. A fleet with
// anonymous mirrors, or with no registries at all, is a normal
// state, not an error.
func GetRegistryCredentialsSecret(c *Client) (*Secret, error) {
	secret := &Secret{}
	err := c.RequestJSON(http.MethodGet, RegistryCredentialsSecretPath, nil, secret)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return secret, nil
}
