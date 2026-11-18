package main

// media-api accepts a client certificate that the cluster's client
// certificate authority signed, as well as a bearer token. The API
// server publishes that authority in a ConfigMap, and the listener
// verifies client certificates against it. The user and groups come
// from the certificate the same way the API server derives them, so a
// person can capture a Player with the credentials in their kubeconfig,
// and the ClusterRoles bound to them apply.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/dynamic"
)

// The API server writes this ConfigMap at start and again when the
// client certificate authority changes. The built-in Role
// extension-apiserver-authentication-reader grants get, list, and watch
// on it.
const (
	clientCANamespace = "kube-system"
	clientCAConfigMap = "extension-apiserver-authentication"
	clientCAKey       = "client-ca-file"
)

// clientAnchors is the pool of authorities that a TLS handshake
// verifies client certificates against. The watch replaces the pool
// and never modifies it, because a handshake can read it at the same
// time.
type clientAnchors struct {
	mutex sync.RWMutex
	pool  *x509.CertPool
}

func (a *clientAnchors) set(pool *x509.CertPool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.pool = pool
}

// current returns the current pool. Before the first load it returns an
// empty pool, so the handshake refuses every client certificate. A nil
// tls.Config.ClientCAs would make Go verify against the system roots.
func (a *clientAnchors) current() *x509.CertPool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.pool == nil {
		return x509.NewCertPool()
	}
	return a.pool
}

// setFromConfigMap replaces the pool with every certificate in the
// ConfigMap's PEM data. While the authority rotates, the data has more
// than one. When the data is missing or has no certificate,
// setFromConfigMap returns an error and keeps the previous pool,
// because those certificates are still the cluster's authority.
func (a *clientAnchors) setFromConfigMap(configMap *ConfigMap) error {
	anchorPEM := configMap.Data[clientCAKey]
	if anchorPEM == "" {
		return fmt.Errorf("the data key %s is missing or empty", clientCAKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchorPEM)) {
		return fmt.Errorf("the data key %s has no PEM certificate", clientCAKey)
	}
	a.set(pool)
	return nil
}

// keepClientAnchors watches the ConfigMap and loads it into anchors on
// every change, so a rotated authority applies to the next handshake
// without a restart. When the ConfigMap is deleted or unusable, it
// reports that and keeps the previous pool. It closes synced, when
// synced is not nil, after the first read, and returns when ctx ends.
func keepClientAnchors(ctx context.Context, watcher dynamic.Interface, anchors *clientAnchors,
	report func(string), synced chan<- struct{}) {
	subject := "configmap " + clientCANamespace + "/" + clientCAConfigMap
	authority := informer.One{Resource: configMapResource, Namespace: clientCANamespace, Name: clientCAConfigMap, What: subject}
	var firstRead func()
	if synced != nil {
		firstRead = func() { close(synced) }
	}
	<-informer.WatchOne(ctx, watcher, authority, func(configMap *ConfigMap) {
		if configMap == nil {
			report(subject + " is absent; media-api verifies client certificates against the authority it read last")
			return
		}
		if err := anchors.setFromConfigMap(configMap); err != nil {
			report(fmt.Sprintf("reading %s: %v", subject, err))
		}
	}, firstRead).Done()
}

// certificateCaller returns the caller of a verified client
// certificate. The user is the leaf's subject common name and the
// groups are its subject organizations, the same mapping the API server
// uses. It returns false when the listener verified no chain or the
// leaf has no common name, and authenticate then reads the bearer
// token.
func certificateCaller(state *tls.ConnectionState) (captureSubject, bool) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return captureSubject{}, false
	}
	leaf := state.VerifiedChains[0][0]
	if leaf.Subject.CommonName == "" {
		return captureSubject{}, false
	}
	return captureSubject{
		User:    leaf.Subject.CommonName,
		Groups:  leaf.Subject.Organization,
		key:     leafKey(leaf),
		expires: verdictExpiry(time.Now(), leaf.NotAfter),
	}, true
}

// leafKey is the cache key for authorization results. It hashes the
// whole certificate, so a different certificate for the same user is
// checked with the API server again.
func leafKey(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:])
}
