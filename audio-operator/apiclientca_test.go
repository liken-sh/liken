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
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// clientAuthority is a test CA in place of the client CA that the API
// server publishes in extension-apiserver-authentication.
type clientAuthority struct {
	certPEM     []byte
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
}

func newClientAuthority(t *testing.T) *clientAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the authority's key: %v", err)
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
		t.Fatalf("creating the authority's certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(body)
	if err != nil {
		t.Fatalf("parsing the authority's certificate: %v", err)
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
		t.Fatalf("making a serial number: %v", err)
	}
	return serial
}

// leaf puts user in the subject's common name and groups in its
// organization values, as a kubeconfig's client certificate does.
func (a *clientAuthority) leaf(t *testing.T, user string, groups []string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the leaf's key: %v", err)
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
		t.Fatalf("creating the leaf certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(body)
	if err != nil {
		t.Fatalf("parsing the leaf certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{body}, PrivateKey: key, Leaf: certificate}
}

// verified returns the connection state that a TLS listener passes to
// the handler after it verified a leaf against the authority.
func (a *clientAuthority) verified(t *testing.T, user string, groups []string) *tls.ConnectionState {
	t.Helper()
	leaf := a.leaf(t, user, groups)
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf.Leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf.Leaf, a.certificate}},
	}
}

// callOver sends a GET to the API's handler as if it arrived over a
// connection with the given TLS state.
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
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	authority := newClientAuthority(t)

	answer := harness.callOver(t, tapRoute,
		authority.verified(t, "admin", []string{"system:masters"}), http.Header{})

	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the request with a verified certificate returned %s, want 200 OK: %s", answer.Status, body)
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
	synctest.Test(t, func(t *testing.T) {
		harness := newAPIHarness(t)
		harness.addSink("kitchen", "node-1", drillPipeWireNode)
		authority := newClientAuthority(t)

		answer := harness.callOver(t, tapRoute,
			authority.verified(t, "admin", []string{"system:masters"}), http.Header{})
		_, _ = io.Copy(io.Discard, answer.Body)

		written := harness.cluster.recorded()
		if len(written) != 1 {
			t.Fatalf("the request wrote %d Events, want 1", len(written))
		}
		want := "admin captured the audio of this Sink as wav"
		if written[0].Message != want {
			t.Errorf("the Event reads %q, want %q", written[0].Message, want)
		}
	})
}

func TestTheCertificateComesBeforeTheToken(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	authority := newClientAuthority(t)

	answer := harness.callOver(t, tapRoute,
		authority.verified(t, "admin", []string{"system:masters"}),
		http.Header{"Authorization": {"Bearer a.b.c"}})

	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the request with a certificate and a token returned %s, want 200 OK: %s", answer.Status, body)
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
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
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
		t.Fatalf("the request with an unverified certificate and a token returned %s, want 200 OK: %s", answer.Status, body)
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
		t.Fatalf("the request with a certificate that has no common name returned %s, want 401 Unauthorized", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-api"` {
		t.Errorf("the challenge is %q, want Bearer realm=\"audio-api\"", got)
	}
}

func TestNoCertificateAndNoTokenIsRefused(t *testing.T) {
	harness := newAPIHarness(t)

	answer := harness.callOver(t, tapRoute, &tls.ConnectionState{}, http.Header{})

	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no credential returned %s, want 401 Unauthorized", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-api"` {
		t.Errorf("the challenge is %q, want Bearer realm=\"audio-api\"", got)
	}
}

// authenticationConfigMap serves the extension-apiserver-authentication
// ConfigMap, or a 404 while absent is true.
type authenticationConfigMap struct {
	mu     sync.Mutex
	caPEM  string
	absent bool
	server *httptest.Server
}

func newAuthenticationConfigMap(t *testing.T, caPEM string) *authenticationConfigMap {
	t.Helper()
	served := &authenticationConfigMap{caPEM: caPEM}
	served.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.mu.Lock()
		defer served.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if served.absent {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&configMap{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Metadata:   EndpointMeta{Name: clientCAConfigMap},
			Data:       map[string]string{clientCAKey: served.caPEM},
		})
	}))
	t.Cleanup(served.server.Close)
	return served
}

func (m *authenticationConfigMap) publish(caPEM string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caPEM = caPEM
}

func (m *authenticationConfigMap) client() *apiclient.Client {
	return apiclient.New(m.server.URL, m.server.Client(), "")
}

func TestTheClientAnchorsFollowTheConfigMap(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newAuthenticationConfigMap(t, string(first.certPEM))
	anchors := &clientAnchors{}

	if err := anchors.load(published.client()); err != nil {
		t.Fatalf("loading the first authority from the ConfigMap: %v", err)
	}
	if !verifiesAgainst(t, anchors, first) {
		t.Error("the first authority's leaf does not verify against the pool")
	}
	if verifiesAgainst(t, anchors, second) {
		t.Error("an authority the ConfigMap never carried verifies against the pool")
	}

	published.publish(string(second.certPEM))
	if err := anchors.load(published.client()); err != nil {
		t.Fatalf("loading the rotated authority from the ConfigMap: %v", err)
	}
	if !verifiesAgainst(t, anchors, second) {
		t.Error("the rotated authority's leaf does not verify against the pool")
	}
}

func verifiesAgainst(t *testing.T, anchors *clientAnchors, authority *clientAuthority) bool {
	t.Helper()
	leaf := authority.leaf(t, "admin", []string{"system:masters"})
	_, err := leaf.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.current(),
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
		t.Fatal("loading an absent ConfigMap returned no error, want one")
	}
	if anchors.current() == nil {
		t.Error("the pool is nil, so a handshake would verify against the system's own roots")
	}
}

// listenOver starts a TLS listener with apiTLSConfig, so a test goes
// through the same client certificate checks as a real caller. It
// returns the listener's base URL and a pool that trusts its leaf.
func (h *apiHarness) listenOver(t *testing.T, anchors *clientAnchors) (string, *x509.CertPool) {
	t.Helper()
	signer, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatalf("minting the listener's authority: %v", err)
	}
	pair, err := signer.mintLeaf("localhost", []string{"localhost"}, time.Now())
	if err != nil {
		t.Fatalf("minting the listener's leaf: %v", err)
	}
	leaf, err := tls.X509KeyPair(pair.Certificate, pair.Key)
	if err != nil {
		t.Fatalf("loading the listener's key pair: %v", err)
	}
	served := &servedLeaf{}
	served.set(leaf)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}
	serving := &http.Server{
		Handler:           h.server,
		ReadHeaderTimeout: time.Second,
		TLSConfig:         apiTLSConfig(served, anchors),
	}
	go func() { _ = serving.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = serving.Close() })

	anchor := x509.NewCertPool()
	if !anchor.AppendCertsFromPEM(signer.Pair.Certificate) {
		t.Fatal("the test authority's PEM contains no certificate")
	}
	return "https://" + listener.Addr().String(), anchor
}

func TestTheListenerTakesTheClusterAuthorityAlone(t *testing.T) {
	harness := newAPIHarness(t)
	harness.addSink("kitchen", "node-1", drillPipeWireNode)
	authority, foreign := newClientAuthority(t), newClientAuthority(t)
	anchors := &clientAnchors{}
	anchors.set(poolOf(t, authority.certPEM))
	base, anchor := harness.listenOver(t, anchors)
	target := base + tapRoute

	answer, err := callerWith(anchor, authority.leaf(t, "admin", []string{"system:masters"})).Get(target)
	if err != nil {
		t.Fatalf("calling with the cluster's own certificate: %v", err)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	_ = answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		t.Errorf("the request with the cluster's own certificate returned %s, want 200", answer.Status)
	}
	// Without NextProtos in the configuration of each handshake, every
	// caller would negotiate HTTP/1.1, and no other check would fail.
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
		t.Fatalf("building the Bearer token request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	tokenAnswer, err := callerWith(anchor).Do(request)
	if err != nil {
		t.Fatalf("calling with a Bearer token on the same listener: %v", err)
	}
	_, _ = io.Copy(io.Discard, tokenAnswer.Body)
	_ = tokenAnswer.Body.Close()
	if tokenAnswer.StatusCode != http.StatusOK {
		t.Errorf("the request with a Bearer token returned %s, want 200", tokenAnswer.Status)
	}
}

func poolOf(t *testing.T, certPEM []byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("the authority's PEM contains no certificate")
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
				// The client dials 127.0.0.1 and the listener's leaf
				// names only localhost.
				ServerName: "localhost",
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}
