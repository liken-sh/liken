package kubernetes

// The one Secret liken reads: the fleet's registry credentials.
//
// Secrets are the API's envelope for confidential material, and the
// machine operator reads exactly one, by exact name, under RBAC that
// grants get on that name and nothing else (the operator's manifest
// carries the Role). The well-known name is what makes that narrow
// grant possible: no configuration points at an arbitrary Secret, so
// no wider permission is ever needed.

import (
	"errors"
	"net/http"
)

// RegistryCredentialsSecretPath is where the fleet's registry
// credentials live: a kubernetes.io/dockerconfigjson Secret named
// registry-credentials in liken-system, the shape `kubectl create
// secret docker-registry` produces. The URL carries a namespace
// segment because Secrets, unlike liken's own cluster-scoped CRDs,
// live inside one.
const RegistryCredentialsSecretPath = "/api/v1/namespaces/liken-system/secrets/registry-credentials"

// Secret is the sliver of a Kubernetes Secret liken reads: its type,
// which says what the data means, and the data itself. The API
// serves data values base64-encoded, and []byte is how encoding/json
// says "decode that for me".
type Secret struct {
	Type string            `json:"type"`
	Data map[string][]byte `json:"data"`
}

// GetRegistryCredentialsSecret reads the fleet's registry
// credentials. An absent Secret returns nil, nil: a fleet with
// anonymous mirrors, or no registries at all, is an ordinary state,
// not an error.
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
