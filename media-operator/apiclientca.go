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
	"os"
	"sync"
	"time"
)

// The ConfigMap the API server publishes the cluster's client
// certificate authority in, and the key that holds its PEM. The API
// server writes this object at start and rewrites it when the
// authority changes, so it is the one anchor every client certificate
// the cluster issues verifies against.
const (
	clientCANamespace = "kube-system"
	clientCAConfigMap = "extension-apiserver-authentication"
	clientCAKey       = "client-ca-file"
)

// How often the API reads the ConfigMap again. A minute is short
// enough that a rotated authority opens the door without a restart,
// and one get of one object costs the API server almost nothing. It
// is a variable so a test can shorten it.
var clientAnchorInterval = time.Minute

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

// load reads the ConfigMap and replaces the pool with what it holds.
// The key carries one PEM block when no rotation is in progress and
// more than one during a rotation, and a pool takes all of them.
func (a *clientAnchors) load(client *Client) error {
	configMap, err := GetConfigMap(client, clientCANamespace, clientCAConfigMap)
	if err != nil {
		return fmt.Errorf("reading configmap %s/%s: %w", clientCANamespace, clientCAConfigMap, err)
	}
	anchorPEM := configMap.Data[clientCAKey]
	if anchorPEM == "" {
		return fmt.Errorf("configmap %s/%s carries no %s", clientCANamespace, clientCAConfigMap, clientCAKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchorPEM)) {
		return fmt.Errorf("the %s of configmap %s/%s holds no certificate",
			clientCAKey, clientCANamespace, clientCAConfigMap)
	}
	a.set(pool)
	return nil
}

// keepClientAnchors reads the authority again on its own clock, so a
// rotated authority opens the door with no restart. A read that fails
// is reported, and the API keeps the pool it could not replace,
// because the certificates that pool holds are still the ones the
// cluster issued.
func keepClientAnchors(ctx context.Context, client *Client, anchors *clientAnchors) {
	ticker := time.NewTicker(clientAnchorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := anchors.load(client); err != nil {
				fmt.Fprintf(os.Stderr, "reading the cluster's client certificate authority: %v\n", err)
			}
		}
	}
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
