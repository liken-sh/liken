package main

// audio-api stores its TLS material in these objects, and apitls.go
// mints it:
//
//   - The Secret audio-api-tls contains the CA certificate and key, and
//     the leaf certificate and key that the API serves.
//   - The ConfigMap audio-api-ca contains the CA certificate only. A
//     client that trusts the API needs get on this ConfigMap, because
//     get on the Secret would also give it the CA's private key.
//   - The Secret audio-capture-server contains the leaf certificate and
//     key of the capture containers.

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// secret is the part of a Secret that the API reads and writes.
type secret struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   EndpointMeta      `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
}

// configMap is the part of a ConfigMap that the API reads and writes.
// The PEM certificate goes in data and not binaryData, so a person can
// read it with kubectl get -o jsonpath.
type configMap struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   EndpointMeta      `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

func secretsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets"
}

func configMapsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/configmaps"
}

// certificates creates the objects above when they are missing, and
// renews a certificate when it is expiring.
type certificates struct {
	client    *apiclient.Client
	namespace string
	service   string

	// now lets a test set the time instead of waiting for a
	// certificate to age.
	now func() time.Time

	// mu guards authority and public, because keepCertificates
	// replaces them in its own goroutine while each forwarded request
	// reads the CA. A reader must not get a new leaf with the old CA.
	mu        sync.RWMutex
	authority authority
	public    keyPair

	// writes serializes ensure and keepCaptureLeaf. keepCertificates
	// and the watch on the capture Secret call them from separate
	// goroutines, and each reads a Secret and writes it back, so two
	// calls at once could both mint a leaf for the same Secret.
	writes sync.Mutex
}

func newCertificates(client *apiclient.Client, namespace, service string) *certificates {
	return &certificates{client: client, namespace: namespace, service: service, now: time.Now}
}

// ensure brings the objects above up to date and returns the leaf that
// the API serves.
func (c *certificates) ensure() (tls.Certificate, error) {
	c.writes.Lock()
	defer c.writes.Unlock()
	stored, err := c.readSecret(apiTLSSecret)
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		if err := c.mintAuthorityAndLeaf(); err != nil {
			return tls.Certificate{}, err
		}
	case err != nil:
		return tls.Certificate{}, err
	default:
		if err := c.adopt(stored); err != nil {
			return tls.Certificate{}, err
		}
	}
	if err := c.publishCAConfigMap(); err != nil {
		return tls.Certificate{}, err
	}
	if err := c.publishCaptureLeaf(); err != nil {
		return tls.Certificate{}, err
	}
	_, public := c.keys()
	return tls.X509KeyPair(public.Certificate, public.Key)
}

// mintAuthorityAndLeaf creates a CA and a leaf and writes them to the
// audio-api-tls Secret. When another API pod creates the Secret first,
// this pod adopts that pod's CA and leaf, so every pod serves a leaf
// that one CA signed.
func (c *certificates) mintAuthorityAndLeaf() error {
	minted, err := mintAuthority(c.now())
	if err != nil {
		return err
	}
	leaf, err := minted.mintLeaf(c.service, serviceNames(c.service, c.namespace), c.now())
	if err != nil {
		return err
	}
	existing, existed, err := c.createSecret(apiTLSSecret, map[string][]byte{
		tlsCertFile: leaf.Certificate,
		tlsKeyFile:  leaf.Key,
		tlsCABundle: minted.Pair.Certificate,
		caKeyFile:   minted.Pair.Key,
	})
	if err != nil {
		return err
	}
	if existed {
		return c.adopt(existing)
	}
	c.setKeys(minted, leaf)
	return nil
}

func (c *certificates) setKeys(minted authority, leaf keyPair) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authority, c.public = minted, leaf
}

// keys returns the CA and the leaf under one lock, so the two match.
func (c *certificates) keys() (authority, keyPair) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authority, c.public
}

// caKeyFile is the Secret key of the CA's private key, which the
// kubernetes.io/tls Secret type does not define. The CA key is in the
// same Secret as the leaf because the API signs the capture leaf with
// it, and a second Secret would need a second RBAC grant.
//
// When cert-manager writes the Secret from a Certificate of the same
// name, the Secret has no ca.key. The API then serves the leaf it finds
// and mints no capture leaf, so the owner of the Certificate must issue
// the capture leaf too.
const caKeyFile = "ca.key"

// adopt loads the CA and the leaf from the data of an existing
// audio-api-tls Secret. When the leaf is expiring, adopt mints a new one
// and updates the Secret.
//
// adopt replaces the pair with one setKeys call at the end, so a
// forwarded request reads the old pair or the new pair, and a Secret
// that adopt refuses leaves the pair that the API already serves.
func (c *certificates) adopt(data map[string][]byte) error {
	public := keyPair{Certificate: data[tlsCertFile], Key: data[tlsKeyFile]}
	if len(public.Certificate) == 0 || len(public.Key) == 0 {
		return fmt.Errorf("the Secret %s has no %s and %s", apiTLSSecret, tlsCertFile, tlsKeyFile)
	}
	if len(data[caKeyFile]) == 0 {
		// Another issuer, such as cert-manager, writes this Secret.
		// The API serves its leaf and signs nothing.
		c.setKeys(authority{Pair: keyPair{Certificate: data[tlsCABundle]}}, public)
		return nil
	}
	found, err := readAuthority(keyPair{Certificate: data[tlsCABundle], Key: data[caKeyFile]})
	if err != nil {
		return err
	}
	if !expiring(public, c.now()) {
		c.setKeys(found, public)
		return nil
	}
	leaf, err := found.mintLeaf(c.service, serviceNames(c.service, c.namespace), c.now())
	if err != nil {
		return err
	}
	if err := c.updateSecret(apiTLSSecret, map[string][]byte{
		tlsCertFile: leaf.Certificate,
		tlsKeyFile:  leaf.Key,
		tlsCABundle: found.Pair.Certificate,
		caKeyFile:   found.Pair.Key,
	}); err != nil {
		return err
	}
	c.setKeys(found, leaf)
	return nil
}

// publishCAConfigMap writes the CA certificate to the audio-api-ca
// ConfigMap. It does nothing when the ConfigMap already contains the
// certificate.
func (c *certificates) publishCAConfigMap() error {
	signer, _ := c.keys()
	anchor := string(signer.Pair.Certificate)
	if anchor == "" {
		return nil
	}
	published, err := c.readConfigMap(apiCAConfigMap)
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		_, _, err := c.createConfigMap(apiCAConfigMap, map[string]string{tlsCABundle: anchor})
		return err
	case err != nil:
		return err
	}
	if strings.Contains(published[tlsCABundle], anchor) {
		return nil
	}
	return c.updateConfigMap(apiCAConfigMap, map[string]string{tlsCABundle: anchor})
}

// keepCaptureLeaf runs on each change that the watch on the
// audio-capture-server Secret delivers. When the Secret's leaf is
// valid, a call makes one GET and no write.
func (c *certificates) keepCaptureLeaf() error {
	c.writes.Lock()
	defer c.writes.Unlock()
	return c.publishCaptureLeaf()
}

// publishCaptureLeaf writes a new leaf to the audio-capture-server
// Secret, which the capture DaemonSet mounts, when the Secret has no
// leaf, the leaf is expiring, or another CA signed it. It does nothing
// when the API has no CA key to sign with.
func (c *certificates) publishCaptureLeaf() error {
	signer, _ := c.keys()
	if signer.key == nil {
		return nil
	}
	stored, err := c.readSecret(captureTLSSecret)
	if err == nil {
		current := keyPair{Certificate: stored[tlsCertFile], Key: stored[tlsKeyFile]}
		if len(current.Certificate) > 0 && !expiring(current, c.now()) &&
			signedBy(current, signer) {
			return nil
		}
	} else if !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}

	leaf, mintErr := signer.mintLeaf(captureAudience, []string{captureAudience}, c.now())
	if mintErr != nil {
		return mintErr
	}
	data := map[string][]byte{
		tlsCertFile: leaf.Certificate,
		tlsKeyFile:  leaf.Key,
		tlsCABundle: signer.Pair.Certificate,
	}
	if errors.Is(err, apiclient.ErrNotFound) {
		// When another API pod creates the Secret first, its leaf
		// stays, because the same CA signed it.
		_, _, createErr := c.createSecret(captureTLSSecret, data)
		return createErr
	}
	return c.updateSecret(captureTLSSecret, data)
}

// signedBy reports whether ca signed leaf. The API refuses a capture
// container whose leaf another CA signed, so a false result makes the
// caller mint a new leaf.
func signedBy(leaf keyPair, ca authority) bool {
	certificate, err := parseCertificate(leaf.Certificate)
	if err != nil || ca.certificate == nil {
		return false
	}
	return certificate.CheckSignatureFrom(ca.certificate) == nil
}

// anchor returns the CA certificate that the API trusts when it
// connects to a capture container.
func (c *certificates) anchor() []byte {
	signer, _ := c.keys()
	return signer.Pair.Certificate
}

// nearestExpiry returns the earlier expiry of the CA and the leaf, for
// the gauge audio_api_certificate_expiry_seconds.
func (c *certificates) nearestExpiry() time.Time {
	signer, public := c.keys()
	nearest := time.Time{}
	for _, pair := range []keyPair{public, signer.Pair} {
		at, err := expiryOf(pair)
		if err != nil {
			continue
		}
		if nearest.IsZero() || at.Before(nearest) {
			nearest = at
		}
	}
	return nearest
}

func (c *certificates) readSecret(name string) (map[string][]byte, error) {
	object, err := apiclient.Get[secret](c.client, secretsPath(c.namespace)+"/"+name)
	if err != nil {
		return nil, err
	}
	return decodeSecretData(object.Data)
}

func decodeSecretData(data map[string]string) (map[string][]byte, error) {
	decoded := map[string][]byte{}
	for key, value := range data {
		body, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("decoding the Secret's %s: %w", key, err)
		}
		decoded[key] = body
	}
	return decoded, nil
}

func encodeSecretData(data map[string][]byte) map[string]string {
	encoded := map[string]string{}
	for key, value := range data {
		encoded[key] = base64.StdEncoding.EncodeToString(value)
	}
	return encoded
}

// createSecret creates a Secret. When the Secret already exists, it
// returns that Secret's data and true, so the caller can adopt it.
func (c *certificates) createSecret(name string, data map[string][]byte) (map[string][]byte, bool, error) {
	body, err := json.Marshal(&secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   EndpointMeta{Name: name},
		Type:       "kubernetes.io/tls",
		Data:       encodeSecretData(data),
	})
	if err != nil {
		return nil, false, err
	}
	err = c.client.RequestJSON(http.MethodPost, secretsPath(c.namespace), body, nil)
	if errors.Is(err, apiclient.ErrConflict) {
		existing, readErr := c.readSecret(name)
		return existing, true, readErr
	}
	return nil, false, err
}

func (c *certificates) updateSecret(name string, data map[string][]byte) error {
	body, err := json.Marshal(&secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   EndpointMeta{Name: name},
		Type:       "kubernetes.io/tls",
		Data:       encodeSecretData(data),
	})
	if err != nil {
		return err
	}
	return c.client.RequestJSON(http.MethodPut, secretsPath(c.namespace)+"/"+name, body, nil)
}

func (c *certificates) readConfigMap(name string) (map[string]string, error) {
	object, err := apiclient.Get[configMap](c.client, configMapsPath(c.namespace)+"/"+name)
	if err != nil {
		return nil, err
	}
	return object.Data, nil
}

func (c *certificates) createConfigMap(name string, data map[string]string) (map[string]string, bool, error) {
	body, err := json.Marshal(&configMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   EndpointMeta{Name: name},
		Data:       data,
	})
	if err != nil {
		return nil, false, err
	}
	err = c.client.RequestJSON(http.MethodPost, configMapsPath(c.namespace), body, nil)
	if errors.Is(err, apiclient.ErrConflict) {
		existing, readErr := c.readConfigMap(name)
		return existing, true, readErr
	}
	return nil, false, err
}

func (c *certificates) updateConfigMap(name string, data map[string]string) error {
	body, err := json.Marshal(&configMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   EndpointMeta{Name: name},
		Data:       data,
	})
	if err != nil {
		return err
	}
	return c.client.RequestJSON(http.MethodPut, configMapsPath(c.namespace)+"/"+name, body, nil)
}
