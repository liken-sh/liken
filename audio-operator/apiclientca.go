package main

// This file holds the second way a caller names itself: a client
// certificate the cluster's own authority signed. The API server
// publishes that authority in a ConfigMap, this API verifies a
// caller's certificate against it, and the leaf's subject is the
// caller. A person with a kubeconfig then taps a sink with the
// credentials they already hold, and the ClusterRole an owner bound
// to them matches, because the user and the groups are spelled the
// way the API server spells them.
//
// A caller that offers no certificate falls to the TokenReview in
// tokenreview.go.

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
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
func (a *clientAnchors) load(client *Client) error {
	held, err := get[configMap](client, configMapsPath(clientCANamespace)+"/"+clientCAConfigMap)
	if err != nil {
		return fmt.Errorf("reading the ConfigMap %s: %w", clientCAConfigMap, err)
	}
	anchors := held.Data[clientCAKey]
	if anchors == "" {
		return fmt.Errorf("the ConfigMap %s holds no %s", clientCAConfigMap, clientCAKey)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(anchors)) {
		return fmt.Errorf("the %s of the ConfigMap %s holds no certificate",
			clientCAKey, clientCAConfigMap)
	}
	a.set(pool)
	return nil
}

// The configuration the public listener serves with. The leaf and the
// anchors are both read on every handshake, so a re-minted leaf and a
// rotated authority each reach the next connection with no restart.
//
// GetConfigForClient answers a whole configuration rather than writing
// one field in place, because a tls.Config a handshake is reading must
// not be written to.
//
// VerifyClientCertIfGiven is the policy. A caller that offers no
// certificate still reaches the Bearer token, and a caller that offers
// one has it verified before any route runs.
//
// NextProtos names the two protocols net/http would have negotiated,
// because the configuration returned here replaces the one the server
// built.
func apiTLSConfig(held *servedLeaf, anchors *clientAnchors) *tls.Config {
	return &tls.Config{
		GetCertificate: held.certificate,
		MinVersion:     tls.VersionTLS12,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{
				GetCertificate: held.certificate,
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
// subject organization values. That is how the API server reads a
// client certificate, so a ClusterRole an owner bound to a person
// matches here with no second spelling of who they are. A certificate
// carries no uid and no extra attributes, so the caller has none.
//
// Only a chain the listener verified names a caller, and a leaf with
// no common name names no user. Both fall to the Bearer token, which
// ends in the ordinary 401 when the request carries none.
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
