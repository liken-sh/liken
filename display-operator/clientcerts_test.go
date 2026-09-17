package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A certificate authority in the shape the API server publishes in
// extension-apiserver-authentication, with the client leaves it
// signs. Every test below that offers a client certificate mints one
// of these.
type clientAuthority struct {
	certPEM     []byte
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
}

func newClientAuthority(t *testing.T) *clientAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          randomTestSerial(t),
		Subject:               pkix.Name{CommonName: "a cluster client CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &clientAuthority{
		certPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		certificate: certificate,
		key:         key,
	}
}

func randomTestSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := randomSerial()
	if err != nil {
		t.Fatal(err)
	}
	return serial
}

// A leaf in the shape a kubeconfig carries: the user in the subject's
// common name and the groups in its organization.
func (a *clientAuthority) leaf(t *testing.T, user string, groups []string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: randomTestSerial(t),
		Subject:      pkix.Name{CommonName: user, Organization: groups},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, key.Public(), a.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}
}

// The state a TLS listener hands the handler once it has verified the
// leaf against the cluster's client CA.
func (a *clientAuthority) verified(t *testing.T, user string, groups []string) *tls.ConnectionState {
	t.Helper()
	leaf := a.leaf(t, user, groups)
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf.Leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf.Leaf, a.certificate}},
	}
}

// One request over a connection that carries the given TLS state,
// with no Authorization field unless the header states one.
func callOver(t *testing.T, server *apiServer, target string,
	state *tls.ConnectionState, header http.Header) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if header != nil {
		request.Header = header
	}
	request.TLS = state
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Result()
}

// The user is the leaf's common name and the groups are its
// organization values, which is what the SubjectAccessReview has to
// carry for a rule written for that person to answer.
func TestAVerifiedClientCertificateIsTheCaller(t *testing.T) {
	authority := newClientAuthority(t)
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		authority.verified(t, "admin", []string{"system:masters"}), nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the certificate answered %d, want 200: %s", resp.StatusCode, body(t, resp))
	}
	reviewed := cluster.reviewed()
	if len(reviewed) != 1 {
		t.Fatalf("the request sent %d access reviews, want 1", len(reviewed))
	}
	if reviewed[0].Spec.User != "admin" {
		t.Errorf("the review named the user %q, want admin", reviewed[0].Spec.User)
	}
	if strings.Join(reviewed[0].Spec.Groups, ",") != "system:masters" {
		t.Errorf("the review named the groups %v, want [system:masters]", reviewed[0].Spec.Groups)
	}
}

// kubectl describe display answers with the Event, so the Event
// names the certificate's user.
func TestTheCapturedEventNamesTheCertificateUser(t *testing.T) {
	authority := newClientAuthority(t)
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		authority.verified(t, "admin", []string{"system:masters"}), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the certificate answered %d, want 200: %s", resp.StatusCode, body(t, resp))
	}

	recorded := cluster.recorded()
	if len(recorded) != 1 {
		t.Fatalf("the capture wrote %d events, want 1", len(recorded))
	}
	want := "admin took the screen of HDMI-A-1 as image/png"
	if recorded[0].Message != want {
		t.Errorf("the event reads %q, want %q", recorded[0].Message, want)
	}
}

// A certificate that named a caller is the caller, and the Bearer
// token beside it is never reviewed. This is the API server's own
// order.
func TestTheCertificateComesBeforeTheToken(t *testing.T) {
	authority := newClientAuthority(t)
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		authority.verified(t, "admin", []string{"system:masters"}),
		bearer("Bearer a-caller-token"))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the request answered %d, want 200: %s", resp.StatusCode, body(t, resp))
	}
	if user := cluster.reviewed()[0].Spec.User; user != "admin" {
		t.Errorf("the review named the user %q, want admin", user)
	}
}

// Only a chain the listener verified names a caller. A certificate
// the client merely offered is no identity, and the request falls to
// the Bearer token beside it.
func TestAnUnverifiedCertificateNamesNobody(t *testing.T) {
	authority := newClientAuthority(t)
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))
	offered := &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{authority.leaf(t, "admin", []string{"system:masters"}).Leaf},
	}

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		offered, bearer("Bearer a-caller-token"))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the request answered %d, want 200: %s", resp.StatusCode, body(t, resp))
	}
	if user := cluster.reviewed()[0].Spec.User; user != "system:serviceaccount:liken-system:viewer" {
		t.Errorf("the review named the user %q, want the token's subject", user)
	}
}

// A leaf with no common name names no user, so the request falls to
// the Bearer token and ends in the ordinary 401 when it carries none.
func TestACertificateWithNoCommonNameNamesNobody(t *testing.T) {
	authority := newClientAuthority(t)
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		authority.verified(t, "", []string{"system:masters"}), http.Header{})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the nameless certificate answered %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != authenticateRealm {
		t.Errorf("the refusal challenged with %q, want %q", got, authenticateRealm)
	}
}

// Neither credential is the bare challenge, RFC 6750 section 3.
func TestNoCertificateAndNoTokenIsRefused(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		&tls.ConnectionState{}, http.Header{})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no credential answered %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != authenticateRealm {
		t.Errorf("the refusal challenged with %q, want %q", got, authenticateRealm)
	}
}

// The API server's own ConfigMap, served the way it answers a get.
type authenticationConfigMap struct {
	*httptest.Server

	mu     sync.Mutex
	caPEM  string
	absent bool
}

func newAuthenticationConfigMap(t *testing.T, caPEM string) *authenticationConfigMap {
	t.Helper()
	held := &authenticationConfigMap{caPEM: caPEM}
	held.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		held.mu.Lock()
		defer held.mu.Unlock()
		w.Header().Set("Content-Type", jsonMediaType)
		if held.absent {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","code":404}`)
			return
		}
		_ = json.NewEncoder(w).Encode(ConfigMap{
			Metadata: objectMeta{Name: clientCAConfigMap, Namespace: clientCANamespace},
			Data:     map[string]string{clientCAKey: held.caPEM},
		})
	}))
	t.Cleanup(held.Close)
	return held
}

func (m *authenticationConfigMap) holds(caPEM string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caPEM = caPEM
}

// The pool comes from the ConfigMap, and a read of the ConfigMap
// after the cluster's CA changed replaces it, so a rotated CA takes
// effect with no restart.
func TestTheClientAnchorsFollowTheConfigMap(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newAuthenticationConfigMap(t, string(first.certPEM))
	client := NewClient(published.URL, published.Client(), "")
	anchors := &clientAnchors{}

	if err := anchors.load(client); err != nil {
		t.Fatal(err)
	}
	if !verifies(t, anchors, first) {
		t.Error("the first authority's leaf does not verify against the pool")
	}
	if verifies(t, anchors, second) {
		t.Error("an authority the ConfigMap never carried verifies against the pool")
	}

	published.holds(string(second.certPEM))
	if err := anchors.load(client); err != nil {
		t.Fatal(err)
	}
	if !verifies(t, anchors, second) {
		t.Error("the rotated authority's leaf does not verify against the pool")
	}
}

func verifies(t *testing.T, anchors *clientAnchors, authority *clientAuthority) bool {
	t.Helper()
	leaf := authority.leaf(t, "admin", []string{"system:masters"})
	_, err := leaf.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.held(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err == nil
}

// A ConfigMap the API cannot read answers an error and leaves the
// pool empty, because the API still answers every token caller.
func TestAnAbsentConfigMapLeavesNoAnchors(t *testing.T) {
	published := newAuthenticationConfigMap(t, "")
	published.absent = true
	anchors := &clientAnchors{}

	err := anchors.load(NewClient(published.URL, published.Client(), ""))

	if err == nil {
		t.Fatal("an absent ConfigMap answered no error")
	}
	if anchors.held() == nil {
		t.Error("the pool is nil, so a handshake would refuse every certificate with no pool to check")
	}
}

// The refresh loop reads the ConfigMap on its own clock, so a CA that
// changed reaches the listener with no restart.
func TestTheRefreshLoopTakesUpANewAuthority(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newAuthenticationConfigMap(t, string(first.certPEM))
	anchors := &clientAnchors{}
	clientAnchorInterval = 5 * time.Millisecond
	t.Cleanup(func() { clientAnchorInterval = time.Minute })

	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go keepClientAnchors(ctx, NewClient(published.URL, published.Client(), ""), anchors)
	published.holds(string(second.certPEM))

	deadline := time.Now().Add(5 * time.Second)
	for !verifies(t, anchors, second) {
		if time.Now().After(deadline) {
			t.Fatal("the loop did not take up the second authority within five seconds")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The listener the API serves with, so a test meets the same TLS
// policy a caller does.
func listenOver(t *testing.T, server *apiServer, anchors *clientAnchors) (base string, anchor *x509.CertPool) {
	t.Helper()
	ca, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.mintLeaf("localhost", []string{"localhost"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	holder := &certificateHolder{}
	if err := holder.set(certPEM, keyPEM, false); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serving := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: time.Second,
		TLSConfig:         apiTLSConfig(holder, anchors),
	}
	go func() { _ = serving.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = serving.Close() })

	anchor = x509.NewCertPool()
	if !anchor.AppendCertsFromPEM(ca.certPEM) {
		t.Fatal("the test authority holds no certificate")
	}
	return "https://" + listener.Addr().String(), anchor
}

// The whole policy on a real connection: a leaf the cluster's CA
// signed is the caller, a leaf from any other CA never reaches a
// route, and a Bearer token answers from the same pod either way.
func TestTheListenerTakesTheClusterAuthorityAlone(t *testing.T) {
	authority, foreign := newClientAuthority(t), newClientAuthority(t)
	cluster := newTestCluster(t)
	anchors := &clientAnchors{}
	anchors.set(poolOf(t, authority.certPEM))
	base, anchor := listenOver(t, newTestAPI(t, cluster, newSidecarFixture(t)), anchors)
	target := base + apiRoot + "/displays/HDMI-A-1/screen.png"

	resp, err := callerWith(anchor, authority.leaf(t, "admin", []string{"system:masters"})).Get(target)
	if err != nil {
		t.Fatalf("the cluster's own certificate: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the cluster's own certificate answered %d, want 200", resp.StatusCode)
	}
	// The listener answers its own TLS configuration on every
	// handshake, and that configuration names the protocols. A
	// configuration that named none would drop every caller to
	// HTTP/1.1 and nothing else would say so.
	if resp.Proto != "HTTP/2.0" {
		t.Errorf("the connection negotiated %s, want HTTP/2.0", resp.Proto)
	}
	if user := cluster.reviewed()[0].Spec.User; user != "admin" {
		t.Errorf("the review named the user %q, want admin", user)
	}

	if _, err := callerWith(anchor, foreign.leaf(t, "admin", nil)).Get(target); err == nil {
		t.Error("a certificate from another authority reached a route")
	}

	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer a-caller-token")
	tokenResp, err := callerWith(anchor).Do(request)
	if err != nil {
		t.Fatalf("a Bearer token on the same listener: %v", err)
	}
	_ = tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		t.Errorf("the Bearer token answered %d, want 200", tokenResp.StatusCode)
	}
}

func poolOf(t *testing.T, certPEM []byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("the authority holds no certificate")
	}
	return pool
}

func callerWith(anchor *x509.CertPool, offered ...tls.Certificate) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				RootCAs: anchor, Certificates: offered,
				// The listener is on a loopback address and its leaf
				// carries the Service's names, so the client states
				// the name it is verifying.
				ServerName: "localhost",
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}
