package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
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
		SerialNumber:          testSerial(t),
		Subject:               pkix.Name{CommonName: "a cluster client CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	body, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(body)
	if err != nil {
		t.Fatal(err)
	}
	return &clientAuthority{
		certPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: body}),
		certificate: certificate,
		key:         key,
	}
}

func testSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := newSerial()
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
		SerialNumber: testSerial(t),
		Subject:      pkix.Name{CommonName: user, Organization: groups},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	body, err := x509.CreateCertificate(rand.Reader, template, a.certificate, key.Public(), a.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(body)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{body}, PrivateKey: key, Leaf: certificate}
}

// The state a TLS listener hands the handler once it has verified the
// leaf against the cluster's client authority.
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
func (h *apiHarness) callOver(t *testing.T, target string,
	state *tls.ConnectionState, header http.Header) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header = header
	request.TLS = state
	recorder := httptest.NewRecorder()
	h.server.ServeHTTP(recorder, request)
	answer := recorder.Result()
	t.Cleanup(func() { _ = answer.Body.Close() })
	return answer
}

const tapRoute = "/v1/audio/sinks/kitchen/audio.wav"

func TestAVerifiedClientCertificateIsTheCaller(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	authority := newClientAuthority(t)

	answer := harness.callOver(t, tapRoute,
		authority.verified(t, "admin", []string{"system:masters"}), http.Header{})

	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the certificate answered %s: %s", answer.Status, body)
	}
	reviewed := harness.cluster.reviewed()
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

func TestTheCapturedEventNamesTheCertificatesUser(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	authority := newClientAuthority(t)

	answer := harness.callOver(t, tapRoute,
		authority.verified(t, "admin", []string{"system:masters"}), http.Header{})
	_, _ = io.Copy(io.Discard, answer.Body)

	written := harness.cluster.recorded()
	if len(written) != 1 {
		t.Fatalf("%d events were written", len(written))
	}
	want := "admin captured the audio of this Sink as wav"
	if written[0].Message != want {
		t.Errorf("the event reads %q, want %q", written[0].Message, want)
	}
}

func TestTheCertificateComesBeforeTheToken(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	authority := newClientAuthority(t)

	answer := harness.callOver(t, tapRoute,
		authority.verified(t, "admin", []string{"system:masters"}),
		http.Header{"Authorization": {"Bearer a.b.c"}})

	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the request answered %s: %s", answer.Status, body)
	}
	if user := harness.cluster.reviewed()[0].Spec.User; user != "admin" {
		t.Errorf("the review named the user %q, want admin", user)
	}
	if reviews := harness.server.review.reviews(); reviews != 0 {
		t.Errorf("the token was reviewed %d times, want 0", reviews)
	}
}

func TestAnUnverifiedCertificateNamesNobody(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	authority := newClientAuthority(t)
	offered := &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{
			authority.leaf(t, "admin", []string{"system:masters"}).Leaf,
		},
	}

	answer := harness.callOver(t, tapRoute, offered,
		http.Header{"Authorization": {"Bearer a.b.c"}})

	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the request answered %s: %s", answer.Status, body)
	}
	user := harness.cluster.reviewed()[0].Spec.User
	if user != "system:serviceaccount:liken-system:listener" {
		t.Errorf("the review named the user %q, want the token's subject", user)
	}
}

func TestACertificateWithNoCommonNameNamesNobody(t *testing.T) {
	harness := newAPIHarness(t)
	authority := newClientAuthority(t)

	answer := harness.callOver(t, tapRoute,
		authority.verified(t, "", []string{"system:masters"}), http.Header{})

	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the nameless certificate answered %s", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-api"` {
		t.Errorf("the challenge is %q", got)
	}
}

func TestNoCertificateAndNoTokenIsRefused(t *testing.T) {
	harness := newAPIHarness(t)

	answer := harness.callOver(t, tapRoute, &tls.ConnectionState{}, http.Header{})

	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no credential answered %s", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-api"` {
		t.Errorf("the challenge is %q", got)
	}
}

// The API server's own ConfigMap, served the way a get answers it.
type authenticationConfigMap struct {
	mu     sync.Mutex
	caPEM  string
	absent bool
	server *httptest.Server
}

func newAuthenticationConfigMap(t *testing.T, caPEM string) *authenticationConfigMap {
	t.Helper()
	held := &authenticationConfigMap{caPEM: caPEM}
	held.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		held.mu.Lock()
		defer held.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if held.absent {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&configMap{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Metadata:   EndpointMeta{Name: clientCAConfigMap},
			Data:       map[string]string{clientCAKey: held.caPEM},
		})
	}))
	t.Cleanup(held.server.Close)
	return held
}

func (m *authenticationConfigMap) holds(caPEM string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caPEM = caPEM
}

func (m *authenticationConfigMap) client() *Client {
	return NewClient(m.server.URL, m.server.Client(), "")
}

func TestTheClientAnchorsFollowTheConfigMap(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newAuthenticationConfigMap(t, string(first.certPEM))
	anchors := &clientAnchors{}

	if err := anchors.load(published.client()); err != nil {
		t.Fatal(err)
	}
	if !verifiesAgainst(t, anchors, first) {
		t.Error("the first authority's leaf does not verify against the pool")
	}
	if verifiesAgainst(t, anchors, second) {
		t.Error("an authority the ConfigMap never carried verifies against the pool")
	}

	published.holds(string(second.certPEM))
	if err := anchors.load(published.client()); err != nil {
		t.Fatal(err)
	}
	if !verifiesAgainst(t, anchors, second) {
		t.Error("the rotated authority's leaf does not verify against the pool")
	}
}

func verifiesAgainst(t *testing.T, anchors *clientAnchors, authority *clientAuthority) bool {
	t.Helper()
	leaf := authority.leaf(t, "admin", []string{"system:masters"})
	_, err := leaf.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.held(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err == nil
}

func TestAConfigMapTheAPICannotReadLeavesAnEmptyPool(t *testing.T) {
	published := newAuthenticationConfigMap(t, "")
	published.absent = true
	anchors := &clientAnchors{}

	err := anchors.load(published.client())

	if err == nil {
		t.Fatal("an absent ConfigMap answered no error")
	}
	if anchors.held() == nil {
		t.Error("the pool is nil, so a handshake would verify against the system's own roots")
	}
}

// The listener the API serves with, so a test meets the same TLS
// policy a caller does.
func (h *apiHarness) listenOver(t *testing.T, anchors *clientAnchors) (string, *x509.CertPool) {
	t.Helper()
	signer, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := signer.mintLeaf("localhost", []string{"localhost"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := tls.X509KeyPair(pair.Certificate, pair.Key)
	if err != nil {
		t.Fatal(err)
	}
	held := &servedLeaf{}
	held.set(leaf)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serving := &http.Server{
		Handler:           h.server,
		ReadHeaderTimeout: time.Second,
		TLSConfig:         apiTLSConfig(held, anchors),
	}
	go func() { _ = serving.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = serving.Close() })

	anchor := x509.NewCertPool()
	if !anchor.AppendCertsFromPEM(signer.Pair.Certificate) {
		t.Fatal("the test authority holds no certificate")
	}
	return "https://" + listener.Addr().String(), anchor
}

func TestTheListenerTakesTheClusterAuthorityAlone(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("kitchen", "node-1", drillPipeWireNode)
	authority, foreign := newClientAuthority(t), newClientAuthority(t)
	anchors := &clientAnchors{}
	anchors.set(poolOf(t, authority.certPEM))
	base, anchor := harness.listenOver(t, anchors)
	target := base + tapRoute

	answer, err := callerWith(anchor, authority.leaf(t, "admin", []string{"system:masters"})).Get(target)
	if err != nil {
		t.Fatalf("the cluster's own certificate: %v", err)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	_ = answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		t.Errorf("the cluster's own certificate answered %s, want 200", answer.Status)
	}
	// The listener answers its own TLS configuration on every
	// handshake, and that configuration names the protocols. A
	// configuration that named none would drop every caller to
	// HTTP/1.1 and nothing else would say so.
	if answer.Proto != "HTTP/2.0" {
		t.Errorf("the connection negotiated %s, want HTTP/2.0", answer.Proto)
	}
	if user := harness.cluster.reviewed()[0].Spec.User; user != "admin" {
		t.Errorf("the review named the user %q, want admin", user)
	}

	if _, err := callerWith(anchor, foreign.leaf(t, "admin", nil)).Get(target); err == nil {
		t.Error("a certificate from another authority reached a route")
	}

	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	tokenAnswer, err := callerWith(anchor).Do(request)
	if err != nil {
		t.Fatalf("a Bearer token on the same listener: %v", err)
	}
	_, _ = io.Copy(io.Discard, tokenAnswer.Body)
	_ = tokenAnswer.Body.Close()
	if tokenAnswer.StatusCode != http.StatusOK {
		t.Errorf("the Bearer token answered %s, want 200", tokenAnswer.Status)
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
				RootCAs:      anchor,
				Certificates: offered,
				// The listener is on a loopback address and its leaf
				// carries the Service's names, so the client states
				// the name it verifies.
				ServerName: "localhost",
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}
