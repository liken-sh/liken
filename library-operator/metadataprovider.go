package main

// metadataprovider.go holds the MetadataProvider wire type, one account with
// one metadata provider that a Library's sources name, and the reads the
// operator makes for it: the provider collection, its status write, and the
// Secret that holds the key.

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"
)

// A MetadataProvider shares the Library's group and version, because it is
// this operator's own resource.
const metadataProviderAPIVersion = libraryAPIVersion

// A MetadataProvider is one account with one provider: the Secret that holds
// its key, and the concerns it may serve.
type MetadataProvider struct {
	APIVersion string                 `json:"apiVersion,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	Metadata   ObjectMeta             `json:"metadata"`
	Spec       MetadataProviderSpec   `json:"spec"`
	Status     MetadataProviderStatus `json:"status"`
}

// The collection ListMetadataProviders answers, read once per pass.
type MetadataProviderList struct {
	Metadata ListMeta           `json:"metadata"`
	Items    []MetadataProvider `json:"items"`
}

// One block per provider, the way a Library carries one block per kind, and
// the concerns this account serves.
type MetadataProviderSpec struct {
	TMDb     *ProviderTMDb `json:"tmdb,omitempty"`
	Concerns []string      `json:"concerns,omitempty"`
}

// The TMDb block names the Secret alone. The endpoint is TMDb's own, and the
// account is the key.
type ProviderTMDb struct {
	SecretRef SecretKeyRef `json:"secretRef"`
}

// One key in one Secret of the provider's own namespace.
type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}

// The key a provider reads when it names none of its own.
const defaultProviderSecretKey = "token"

// The key the operator reads out of the Secret: the provider's own, or the
// default the CRD writes.
func (r SecretKeyRef) secretKey() string {
	if r.Key != "" {
		return r.Key
	}
	return defaultProviderSecretKey
}

// What the operator reports on a provider: the Ready condition its one check
// per pass produced, and when the provider last refused the key.
type MetadataProviderStatus struct {
	Conditions  []Condition `json:"conditions,omitempty"`
	LastRefusal time.Time   `json:"lastRefusal,omitzero"`
}

// The three reasons the Ready condition takes, one per answer the check can
// get.
const (
	reasonReachable = "Reachable"
	reasonNoSecret  = "NoSecret"
	reasonRefused   = "Refused"
)

// A provider serves a concern only when it lists that concern.
func (p *MetadataProvider) serves(concern string) bool {
	return slices.Contains(p.Spec.Concerns, concern)
}

// A provider is ready when its last check reached it. A provider no check has
// reported on yet is not ready.
func (p *MetadataProvider) ready() bool {
	for _, condition := range p.Status.Conditions {
		if condition.Type == conditionReady {
			return condition.Status == ConditionTrue
		}
	}
	return false
}

// The reason of the Ready condition, which the Library's Sources condition
// repeats, so a person reads one answer on the Library and not two objects. A
// provider no check has reported on yet has no reason.
func (p *MetadataProvider) readyReason() string {
	for _, condition := range p.Status.Conditions {
		if condition.Type == conditionReady {
			return condition.Reason
		}
	}
	return ""
}

// A Secret as this operator reads it. The Data values arrive base64-encoded,
// and a []byte field decodes them on the way in.
type Secret struct {
	Metadata ObjectMeta        `json:"metadata"`
	Data     map[string][]byte `json:"data,omitempty"`
}

// The providers of every namespace, read with one request, and the two paths
// one provider is written on and its Secret is read on.
const metadataProvidersPath = "/apis/" + metadataProviderAPIVersion + "/metadataproviders"

func metadataProviderPath(namespace, name string) string {
	return libraryPrefix + namespace + "/metadataproviders/" + name
}

func secretPath(namespace, name string) string {
	return corePrefix + namespace + "/secrets/" + name
}

// A cluster that has not applied this CRD serves no such collection. The
// caller reports that and carries on.
func ListMetadataProviders(ctx context.Context, c *Client) (*MetadataProviderList, error) {
	list := &MetadataProviderList{}
	if err := c.RequestJSON(ctx, http.MethodGet, metadataProvidersPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// The status subresource is its own write path, so this request never touches
// the spec a person declared.
func PutMetadataProviderStatus(ctx context.Context, c *Client, provider *MetadataProvider) (*MetadataProvider, error) {
	body, err := json.Marshal(provider)
	if err != nil {
		return nil, err
	}
	written := &MetadataProvider{}
	path := metadataProviderPath(provider.Metadata.Namespace, provider.Metadata.Name) + "/status"
	if err := c.RequestJSON(ctx, http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// The operator reads the Secret for the reachability check alone. The key
// reaches a container through a secretKeyRef on the pod, so no worker holds
// an API credential.
func GetSecret(ctx context.Context, c *Client, namespace, name string) (*Secret, error) {
	secret := &Secret{}
	if err := c.RequestJSON(ctx, http.MethodGet, secretPath(namespace, name), nil, secret); err != nil {
		return nil, err
	}
	return secret, nil
}
