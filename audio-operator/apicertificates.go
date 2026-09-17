package main

// The API's own certificates and the objects that hold them: the Secret
// audio-api-tls, which carries the CA and the public leaf, the
// ConfigMap audio-api-ca, which carries the trust anchor alone, and
// the Secret audio-capture-server, which carries the capture
// container's leaf. apitls.go mints what this file stores.
//
// The anchor is a ConfigMap and not the Secret because a client needs
// an ordinary get on a public certificate, and a grant on the Secret
// would hand it the keys as well.
//
// A create that loses the race reads the winner, so two API pods
// during a rollout settle on one CA rather than each serving leaves
// the other's anchor does not cover.

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
)

// secret is the part of a Kubernetes Secret this API reads and writes.
type secret struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   EndpointMeta      `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
}

// configMap is the same for a ConfigMap. The CA certificate is text,
// so it goes in data rather than binaryData and a person reads it with
// kubectl get -o jsonpath.
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

// certificates holds the API's own TLS material and keeps it current.
type certificates struct {
	client    *Client
	namespace string
	service   string

	// now is a field so a test mints a certificate that has already
	// half expired without waiting six months.
	now func() time.Time

	// keepCertificates writes these two on its own goroutine every
	// hour, and every forwarded request reads the anchor, so the lock
	// is what keeps a re-mint from tearing a read.
	mu        sync.RWMutex
	authority authority
	public    keyPair
}

func newCertificates(client *Client, namespace, service string) *certificates {
	return &certificates{client: client, namespace: namespace, service: service, now: time.Now}
}

// ensure reads the Secret, mints what is absent, and writes back what
// changed. It returns the public leaf the API serves.
func (c *certificates) ensure() (tls.Certificate, error) {
	held, err := c.readSecret(apiTLSSecret)
	switch {
	case errors.Is(err, ErrNotFound):
		if err := c.mintEverything(); err != nil {
			return tls.Certificate{}, err
		}
	case err != nil:
		return tls.Certificate{}, err
	default:
		if err := c.adopt(held); err != nil {
			return tls.Certificate{}, err
		}
	}
	if err := c.publishAnchor(); err != nil {
		return tls.Certificate{}, err
	}
	if err := c.publishCaptureLeaf(); err != nil {
		return tls.Certificate{}, err
	}
	_, public := c.held()
	return tls.X509KeyPair(public.Certificate, public.Key)
}

// mintEverything makes the CA and the public leaf, and writes them. A
// create that loses the race reads the winner, which is the other API
// pod's CA, so both serve leaves one trust anchor covers.
func (c *certificates) mintEverything() error {
	minted, err := mintAuthority(c.now())
	if err != nil {
		return err
	}
	leaf, err := minted.mintLeaf(c.service, serviceNames(c.service, c.namespace), c.now())
	if err != nil {
		return err
	}
	written, lost, err := c.createSecret(apiTLSSecret, map[string][]byte{
		tlsCertFile: leaf.Certificate,
		tlsKeyFile:  leaf.Key,
		tlsCABundle: minted.Pair.Certificate,
		caKeyFile:   minted.Pair.Key,
	})
	if err != nil {
		return err
	}
	if lost {
		return c.adopt(written)
	}
	c.hold(minted, leaf)
	return nil
}

// hold replaces the two certificates this API serves and signs with.
func (c *certificates) hold(minted authority, leaf keyPair) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authority, c.public = minted, leaf
}

// held answers the two, so a caller reads a matched pair rather than
// one from before a re-mint and one from after.
func (c *certificates) held() (authority, keyPair) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authority, c.public
}

// caKeyFile is the extra key in the Secret that the kubernetes.io/tls
// names do not cover. The CA's own key is in the same Secret because
// the API signs the capture leaf from it, and a second Secret would be
// a second grant for one thing. When a cert-manager owner replaces
// the Secret with a Certificate of the same name there is no ca.key,
// so the API serves the leaf it finds and mints no capture leaf, and
// the owner issues that one too.
const caKeyFile = "ca.key"

// adopt takes the material an existing Secret holds, and mints a leaf
// again when under a third of its life remains.
func (c *certificates) adopt(held map[string][]byte) error {
	c.public = keyPair{Certificate: held[tlsCertFile], Key: held[tlsKeyFile]}
	if len(c.public.Certificate) == 0 || len(c.public.Key) == 0 {
		return fmt.Errorf("the Secret %s holds no %s and %s", apiTLSSecret, tlsCertFile, tlsKeyFile)
	}
	if len(held[caKeyFile]) == 0 {
		// A Secret another issuer owns. The API serves what it finds
		// and signs nothing.
		c.authority = authority{Pair: keyPair{Certificate: held[tlsCABundle]}}
		return nil
	}
	found, err := readAuthority(keyPair{Certificate: held[tlsCABundle], Key: held[caKeyFile]})
	if err != nil {
		return err
	}
	c.authority = found
	if !expiring(c.public, c.now()) {
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
	c.hold(found, leaf)
	return nil
}

// publishAnchor writes the CA certificate alone into the ConfigMap, so
// a client reads the trust anchor with an ordinary get.
func (c *certificates) publishAnchor() error {
	signer, _ := c.held()
	anchor := string(signer.Pair.Certificate)
	if anchor == "" {
		return nil
	}
	published, err := c.readConfigMap(apiCAConfigMap)
	switch {
	case errors.Is(err, ErrNotFound):
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

// publishCaptureLeaf mints the container's leaf when none is held or
// the one held is expiring, and writes it into the Secret the
// DaemonSet mounts.
func (c *certificates) publishCaptureLeaf() error {
	signer, _ := c.held()
	if signer.key == nil {
		return nil
	}
	held, err := c.readSecret(captureTLSSecret)
	if err == nil {
		current := keyPair{Certificate: held[tlsCertFile], Key: held[tlsKeyFile]}
		if len(current.Certificate) > 0 && !expiring(current, c.now()) &&
			signedBy(current, signer) {
			return nil
		}
	} else if !errors.Is(err, ErrNotFound) {
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
	if errors.Is(err, ErrNotFound) {
		// A create that loses the race leaves the winner's leaf in
		// place, which this API's own CA signed too.
		_, _, createErr := c.createSecret(captureTLSSecret, data)
		return createErr
	}
	return c.updateSecret(captureTLSSecret, data)
}

// signedBy says whether a leaf was signed by the CA the API holds now.
// A leaf from a CA that is gone is one the container's clients would
// refuse, so it is minted again.
func signedBy(leaf keyPair, ca authority) bool {
	certificate, err := parseCertificate(leaf.Certificate)
	if err != nil || ca.certificate == nil {
		return false
	}
	return certificate.CheckSignatureFrom(ca.certificate) == nil
}

// anchor is the CA certificate the API trusts when it dials a capture
// container.
func (c *certificates) anchor() []byte {
	held, _ := c.held()
	return held.Pair.Certificate
}

// nearestExpiry is what audio_api_certificate_expiry_seconds carries:
// the first of the certificates this API owns to run out.
func (c *certificates) nearestExpiry() time.Time {
	held, public := c.held()
	nearest := time.Time{}
	for _, pair := range []keyPair{public, held.Pair} {
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
	held, err := get[secret](c.client, secretsPath(c.namespace)+"/"+name)
	if err != nil {
		return nil, err
	}
	return decodeSecretData(held.Data)
}

func decodeSecretData(data map[string]string) (map[string][]byte, error) {
	decoded := map[string][]byte{}
	for key, value := range data {
		body, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("reading the Secret's %s: %w", key, err)
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

// createSecret makes one Secret. A create that loses the race answers
// with the winner's data and lost set, so the caller adopts it.
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
	if errors.Is(err, ErrConflict) {
		winner, readErr := c.readSecret(name)
		return winner, true, readErr
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
	held, err := get[configMap](c.client, configMapsPath(c.namespace)+"/"+name)
	if err != nil {
		return nil, err
	}
	return held.Data, nil
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
	if errors.Is(err, ErrConflict) {
		winner, readErr := c.readConfigMap(name)
		return winner, true, readErr
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
