package main

// The API accepts a client certificate that the cluster's client CA
// signed, so a person can tap a Sink with the certificate in their
// kubeconfig. The API reads the user and groups from the certificate
// the same way the API server does, so the RBAC bindings that already
// name that person apply here unchanged. A caller with no certificate
// is authenticated by the TokenReview in tokenreview.go.

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The API server publishes the cluster's client CA as PEM in this
// ConfigMap, and rewrites it when the CA changes.
const (
	clientCANamespace = "kube-system"
	clientCAConfigMap = "extension-apiserver-authentication"
	clientCAKey       = "client-ca-file"
)

// clientAnchors is the pool that a handshake verifies a client
// certificate against. A load replaces the pool and never adds to it, so
// a handshake can keep using the pool it got while a load runs.
type clientAnchors struct {
	mu   sync.RWMutex
	pool *x509.CertPool
}

func (a *clientAnchors) set(pool *x509.CertPool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pool = pool
}

// current returns an empty pool before the first load, so an early
// handshake accepts no client certificate. A nil ClientCAs would make Go
// verify against the machine's root store.
func (a *clientAnchors) current() *x509.CertPool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.pool == nil {
		return x509.NewCertPool()
	}
	return a.pool
}

// load reads the ConfigMap once, at start, so the first handshake can
// verify a client certificate before the watch has delivered the
// ConfigMap. After that, the watch passes each change to adopt.
func (a *clientAnchors) load(client *apiclient.Client) error {
	published, err := apiclient.Get[configMap](client, configMapsPath(clientCANamespace)+"/"+clientCAConfigMap)
	if err != nil {
		return fmt.Errorf("reading the ConfigMap %s: %w", clientCAConfigMap, err)
	}
	return a.adopt(published)
}

// adopt replaces the pool with every PEM block in the ConfigMap, because
// the key can hold more than one CA while the CA rotates. A nil
// published means the ConfigMap does not exist. On any error, the pool
// keeps the certificates it had.
func (a *clientAnchors) adopt(published *configMap) error {
	if published == nil {
		return fmt.Errorf("the ConfigMap %s does not exist", clientCAConfigMap)
	}
	anchors := published.Data[clientCAKey]
	if anchors == "" {
		return fmt.Errorf("the ConfigMap %s has no %s", clientCAConfigMap, clientCAKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchors)) {
		return fmt.Errorf("the %s of the ConfigMap %s contains no PEM certificate",
			clientCAKey, clientCAConfigMap)
	}
	a.set(pool)
	return nil
}

// apiTLSConfig builds a new configuration on every handshake, with the
// current leaf and the current pool, so a new leaf or a rotated CA
// applies to the next connection without a restart. It builds a new one
// because a tls.Config that a handshake is reading must not be modified.
//
// VerifyClientCertIfGiven makes the certificate optional, so a caller
// can use a bearer token instead. A certificate that fails verification
// ends the handshake before any route runs.
//
// net/http adds NextProtos only to the server's own configuration, and
// this one replaces it, so without NextProtos every caller would
// negotiate HTTP/1.1.
func apiTLSConfig(leaf *servedLeaf, anchors *clientAnchors) *tls.Config {
	return &tls.Config{
		GetCertificate: leaf.certificate,
		MinVersion:     tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{
				GetCertificate: leaf.certificate,
				MinVersion:     tls.VersionTLS12,
				NextProtos:     []string{"h2", "http/1.1"},
				ClientAuth:     tls.VerifyClientCertIfGiven,
				ClientCAs:      anchors.current(),
			}, nil
		},
	}
}

// certificateCaller reads the user from the common name of the leaf's
// subject and the groups from its organization values, as the API server
// does. It returns false for a chain that the listener did not verify and
// for a leaf with no common name. The request then falls back to the
// bearer token, and gets a 401 when it has none.
func certificateCaller(state *tls.ConnectionState) (caller, bool) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return caller{}, false
	}
	leaf := state.VerifiedChains[0][0]
	if leaf.Subject.CommonName == "" {
		return caller{}, false
	}
	return caller{Username: leaf.Subject.CommonName, Groups: leaf.Subject.Organization}, true
}
