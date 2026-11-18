package main

// A caller can authenticate with a client certificate that the
// cluster's client CA signed, in place of a Bearer token. This API
// verifies the certificate against the CA that the API server publishes
// in a ConfigMap, and reads the user and groups from the certificate's
// subject, as kube-apiserver does. So a person can read a screen with
// the credentials in their kubeconfig, under the RBAC rules bound to
// them.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The API server writes the client CA's PEM bundle to this ConfigMap
// key when it starts and again whenever the CA changes.
const (
	clientCANamespace = "kube-system"
	clientCAConfigMap = "extension-apiserver-authentication"
	clientCAKey       = "client-ca-file"
)

// clientAnchors is the CA pool that TLS handshakes verify client
// certificates against. An update replaces the pool and never changes
// it in place, because a handshake can be reading the old pool.
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
// handshake verifies no certificate. A nil ClientCAs would make Go
// fall back to the system root store, which holds none of this
// cluster's CAs.
func (a *clientAnchors) current() *x509.CertPool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.pool == nil {
		return x509.NewCertPool()
	}
	return a.pool
}

// load reads the ConfigMap and replaces the pool with its CAs. During a
// CA rotation the key has more than one certificate, and the pool
// trusts all of them.
func (a *clientAnchors) load(c *apiclient.Client) error {
	configMap, err := apiclient.Get[ConfigMap](c, configMapsPath(clientCANamespace)+"/"+clientCAConfigMap)
	if err != nil {
		return fmt.Errorf("reading the ConfigMap %s: %w", clientCAConfigMap, err)
	}
	return a.setFromConfigMap(configMap)
}

// setFromConfigMap replaces the pool with the CAs in the ConfigMap. It
// returns an error and leaves the pool unchanged when the ConfigMap has
// no certificate.
func (a *clientAnchors) setFromConfigMap(configMap *ConfigMap) error {
	anchorPEM := configMap.Data[clientCAKey]
	if anchorPEM == "" {
		return fmt.Errorf("the ConfigMap %s has no %s", clientCAConfigMap, clientCAKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchorPEM)) {
		return fmt.Errorf("the %s of the ConfigMap %s holds no certificate", clientCAKey, clientCAConfigMap)
	}
	a.set(pool)
	return nil
}

// keepClientAnchors watches the ConfigMap and updates anchors on every
// change, so a rotated CA takes effect without a restart. When the
// ConfigMap is deleted or has no certificate, it logs the problem and
// keeps the current pool, because that pool still verifies the
// certificates the cluster has issued.
func keepClientAnchors(ctx context.Context, watcher dynamic.Interface, anchors *clientAnchors) {
	authority := informer.One{Resource: configMapResource, Namespace: clientCANamespace, Name: clientCAConfigMap,
		What: "the ConfigMap " + clientCAConfigMap}
	<-informer.WatchOne(ctx, watcher, authority, func(configMap *ConfigMap) {
		if configMap == nil {
			fmt.Fprintf(os.Stderr, "the ConfigMap %s was deleted, so the API server keeps the client authority it has\n", clientCAConfigMap)
			return
		}
		if err := anchors.setFromConfigMap(configMap); err != nil {
			fmt.Fprintf(os.Stderr, "keeping the client authority the API server has: %v\n", err)
		}
	}, nil).Done()
}

// apiTLSConfig returns the TLS configuration the API listens with.
// GetConfigForClient builds a new tls.Config for each handshake with
// the current client CA pool, so a rotated CA applies to the next
// connection without a restart.
//
// VerifyClientCertIfGiven lets a caller with no certificate go on to
// Bearer token authentication, and fails the handshake for a
// certificate that does not verify. The returned configuration replaces
// the one that net/http built, so it must list in NextProtos the
// protocols that net/http would have offered.
func apiTLSConfig(holder *certificateHolder, anchors *clientAnchors) *tls.Config {
	return &tls.Config{
		GetCertificate: holder.get,
		MinVersion:     tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{
				GetCertificate: holder.get,
				MinVersion:     tls.VersionTLS12,
				NextProtos:     []string{"h2", "http/1.1"},
				ClientAuth:     tls.VerifyClientCertIfGiven,
				ClientCAs:      anchors.current(),
			}, nil
		},
	}
}

// certificateCaller returns the caller that a verified client
// certificate identifies, and false when there is none. As in
// kube-apiserver, the user is the certificate's subject common name and
// the groups are its subject organization values, and there is no uid
// or extra.
//
// It reads only VerifiedChains, because a certificate that the listener
// did not verify identifies no one. A certificate with an empty common
// name identifies no one either. In both cases authenticate tries the
// Bearer token next.
func certificateCaller(state *tls.ConnectionState) (*caller, bool) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return nil, false
	}
	leaf := state.VerifiedChains[0][0]
	if leaf.Subject.CommonName == "" {
		return nil, false
	}
	return &caller{Username: leaf.Subject.CommonName, Groups: leaf.Subject.Organization}, true
}
