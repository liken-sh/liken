package main

// This file holds the second way a caller names itself: a client
// certificate the cluster's own authority signed. The API server
// publishes that authority in a ConfigMap, this API verifies a
// caller's certificate against it, and the leaf's subject is the
// caller. A person with a kubeconfig then reads a screen with the
// credentials they already hold, and the ClusterRole an owner bound
// to them matches, because the user and the groups are spelled the
// way kube-apiserver spells them.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

// How often the API reads the ConfigMap again. A minute is the same
// pass the sidecar's Secret gets, for the same reason: one get of one
// object costs the API server almost nothing, and the alternative is
// a cluster whose rotated authority opens no door until the next
// restart. It is a variable so a test can shorten it, the way this
// repository's other waits are.
var clientAnchorInterval = time.Minute

// The anchors a handshake verifies a client certificate against. The
// pool is replaced and never written to, because a handshake reads it
// while a load runs.
type clientAnchors struct {
	mu   sync.RWMutex
	pool *x509.CertPool
}

func (a *clientAnchors) set(pool *x509.CertPool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pool = pool
}

// held answers an empty pool before the first load, so a handshake
// that arrives early verifies no certificate. A nil ClientCAs would
// mean the machine's own root store, which holds no authority this
// cluster issued.
func (a *clientAnchors) held() *x509.CertPool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.pool == nil {
		return x509.NewCertPool()
	}
	return a.pool
}

// load reads the ConfigMap and replaces the pool with what it holds.
// The key carries one PEM block when no rotation is in progress and
// more than one during a rotation, and a pool takes all of them.
func (a *clientAnchors) load(c *Client) error {
	path := "/api/v1/namespaces/" + clientCANamespace + "/configmaps/" + clientCAConfigMap
	held, err := get[ConfigMap](c, path)
	if err != nil {
		return fmt.Errorf("reading the ConfigMap %s: %w", clientCAConfigMap, err)
	}
	anchorPEM := held.Data[clientCAKey]
	if anchorPEM == "" {
		return fmt.Errorf("the ConfigMap %s carries no %s", clientCAConfigMap, clientCAKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchorPEM)) {
		return fmt.Errorf("the %s of the ConfigMap %s holds no certificate", clientCAKey, clientCAConfigMap)
	}
	a.set(pool)
	return nil
}

// Every minute the API reads the authority again, so a rotated
// authority opens the door with no restart. A read that fails is
// reported, and the API keeps the pool it could not replace, because
// the certificates that pool holds are still the ones the cluster
// issued.
func keepClientAnchors(ctx context.Context, c *Client, anchors *clientAnchors) {
	tick := time.NewTicker(clientAnchorInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := anchors.load(c); err != nil {
				fmt.Fprintf(os.Stderr, "reading the cluster's client authority: %v\n", err)
			}
		}
	}
}

// The configuration the API listens with. The certificate and the
// anchors are both read on every handshake, through this function,
// so a re-minted leaf and a rotated client authority each reach the
// next connection with no restart. A whole configuration is returned
// rather than one field written in place, because a tls.Config a
// handshake is reading must not be written to.
//
// VerifyClientCertIfGiven is the policy: a caller that offers no
// certificate still reaches the Bearer token path, and a caller that
// offers one has it verified before any route runs. NextProtos names
// the two protocols net/http would have negotiated, because a
// configuration returned here replaces the one the server built.
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
				ClientCAs:      anchors.held(),
			}, nil
		},
	}
}

// certificateCaller reads the caller out of a connection that carries
// a verified client certificate.
//
// The user is the leaf's subject common name and the groups are its
// subject organization values. That is how kube-apiserver reads a
// client certificate, so a ClusterRole an owner bound to a person
// matches here with no second spelling of who they are. The
// certificate carries no uid and no extra attributes, so the caller
// has none.
//
// Only a chain the listener verified names a caller. A certificate a
// client merely offered is no identity, and a leaf with no common
// name names no user: both fall to the Bearer token, which ends in
// the ordinary 401 when the request carries none.
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
