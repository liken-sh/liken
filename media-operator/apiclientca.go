package main

// This file holds the second way a caller names itself: a client
// certificate the cluster's own authority signed. The API server
// publishes that authority in a ConfigMap, this API verifies a
// caller's certificate against it, and the leaf's subject is the
// caller. A person with a kubeconfig then captures a Player with the
// credentials they already hold, and the ClusterRole an owner bound
// to them matches, because the user and the groups are spelled the
// way the API server spells them.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
)

// The ConfigMap the API server publishes the cluster's client
// certificate authority in, and the key that holds its PEM. The API
// server writes this object at start and rewrites it when the
// authority changes, so it is the one anchor every client certificate
// the cluster issues verifies against. The built-in Role
// extension-apiserver-authentication-reader grants get, list, and
// watch on this one object.
const (
	clientCANamespace = "kube-system"
	clientCAConfigMap = "extension-apiserver-authentication"
	clientCAKey       = "client-ca-file"
)

// The anchors a handshake verifies a client certificate against. The
// pool is replaced and never written to, because a handshake reads it
// while a load runs.
type clientAnchors struct {
	mutex sync.RWMutex
	pool  *x509.CertPool
}

func (a *clientAnchors) set(pool *x509.CertPool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.pool = pool
}

// held answers an empty pool before the first load, so a handshake
// that arrives early verifies no certificate. A nil ClientCAs would
// mean the machine's own root store, which holds no authority this
// cluster issued.
func (a *clientAnchors) held() *x509.CertPool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.pool == nil {
		return x509.NewCertPool()
	}
	return a.pool
}

// take replaces the pool with what the ConfigMap holds. The key
// carries one PEM block when no rotation is in progress and more than
// one during a rotation, and a pool takes all of them. A ConfigMap
// that carries no usable authority leaves the pool as it is, because
// the certificates that pool holds are still the ones the cluster
// issued.
func (a *clientAnchors) take(configMap *ConfigMap) error {
	anchorPEM := configMap.Data[clientCAKey]
	if anchorPEM == "" {
		return fmt.Errorf("the configmap carries no %s", clientCAKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchorPEM)) {
		return fmt.Errorf("the %s of the configmap holds no certificate", clientCAKey)
	}
	a.set(pool)
	return nil
}

// keepClientAnchors is the watch on the cluster's client authority.
// The API server rewrites the ConfigMap when the authority rotates,
// and the watch delivers that write, so the rotated authority reaches
// the next handshake with no restart. A deleted ConfigMap leaves the
// pool as it is, for the reason take gives, and reports one line. A
// copy that holds no authority is reported, and the pool stays as it
// is.
func keepClientAnchors(ctx context.Context, watcher dynamic.Interface, anchors *clientAnchors,
	report func(string), synced chan<- struct{}) {
	subject := "configmap " + clientCANamespace + "/" + clientCAConfigMap
	watchNamed(ctx, watcher, configMapResource, clientCANamespace, clientCAConfigMap, subject,
		func(configMap *ConfigMap) {
			if configMap == nil {
				report(subject + " is absent; media-api verifies client certificates against the authority it read last")
				return
			}
			if err := anchors.take(configMap); err != nil {
				report(fmt.Sprintf("reading %s: %v", subject, err))
			}
		}, synced)
}

// certificateCaller reads the caller out of a connection that carries
// a verified client certificate.
//
// The user is the leaf's subject common name and the groups are its
// subject organization values. That is how the API server reads a
// client certificate, so a ClusterRole an owner bound to a person
// matches here with no second spelling of who they are. A certificate
// carries no uid and no extra attributes, so the subject has none.
//
// Only a chain the listener verified names a caller, and a leaf with
// no common name names no user. Both fall to the Bearer token, which
// ends in the ordinary 401 when the request carries none.
func certificateCaller(state *tls.ConnectionState) (captureSubject, bool) {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return captureSubject{}, false
	}
	leaf := state.VerifiedChains[0][0]
	if leaf.Subject.CommonName == "" {
		return captureSubject{}, false
	}
	return captureSubject{
		User:   leaf.Subject.CommonName,
		Groups: leaf.Subject.Organization,
		key:    leafKey(leaf),
		lapses: verdictLapse(time.Now(), leaf.NotAfter),
	}, true
}

// leafKey is the SHA-256 of the leaf's own bytes, so a cached verdict
// belongs to one certificate. A second certificate for the same user
// asks the API server again.
func leafKey(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:])
}
