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
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// clientAuthority is a test client CA, like the one the API server
// publishes in extension-apiserver-authentication.
type clientAuthority struct {
	certPEM     []byte
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
}

func newClientAuthority(t *testing.T) *clientAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the authority key: %v", err)
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
		t.Fatalf("creating the authority certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the authority certificate: %v", err)
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
		t.Fatalf("drawing a certificate serial: %v", err)
	}
	return serial
}

// leaf signs a client certificate like a kubeconfig's: the user in the
// subject common name and the groups in the organization.
func (a *clientAuthority) leaf(t *testing.T, user string, groups []string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the leaf key: %v", err)
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
		t.Fatalf("creating the leaf certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the leaf certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}
}

// verified returns the connection state that a TLS listener passes to
// the handler after it verifies a leaf signed by this CA.
func (a *clientAuthority) verified(t *testing.T, user string, groups []string) *tls.ConnectionState {
	t.Helper()
	leaf := a.leaf(t, user, groups)
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf.Leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf.Leaf, a.certificate}},
	}
}

// callOver sends one request as if it arrived on a connection with the
// given TLS state. It has no Authorization header unless header sets
// one.
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

// The SubjectAccessReview sends the leaf's common name as the user and
// its organization values as the groups, so RBAC rules bound to that
// user or group apply.
func TestAVerifiedClientCertificateIsTheCaller(t *testing.T) {
	authority := newClientAuthority(t)
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		authority.verified(t, "admin", []string{"system:masters"}), nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the certificate responded with %d, want 200: %s", resp.StatusCode, body(t, resp))
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

func TestTheCapturedEventNamesTheCertificateUser(t *testing.T) {
	authority := newClientAuthority(t)
	synctest.Test(t, func(t *testing.T) {
		cluster := newTestCluster(t)
		server := newTestAPI(t, cluster, newPipedSidecar(t))

		resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
			authority.verified(t, "admin", []string{"system:masters"}), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the certificate responded with %d, want 200: %s", resp.StatusCode, body(t, resp))
		}
		synctest.Wait()

		want := []string{"Normal Captured admin captured the screen of HDMI-A-1 as image/png"}
		if got := eventLines(cluster.events.About("Display", "HDMI-A-1")); !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}
	})
}

// When a certificate identifies a caller, the Bearer token sent with
// it is ignored and never reviewed, as in kube-apiserver.
func TestAVerifiedCertificateTakesPrecedenceOverABearerToken(t *testing.T) {
	authority := newClientAuthority(t)
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		authority.verified(t, "admin", []string{"system:masters"}),
		bearer("Bearer a-caller-token"))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the request responded with %d, want 200: %s", resp.StatusCode, body(t, resp))
	}
	if user := cluster.reviewed()[0].Spec.User; user != "admin" {
		t.Errorf("the review named the user %q, want admin", user)
	}
}

// A certificate that the client sent but the listener did not verify
// identifies no one, and the request falls through to the Bearer token.
func TestAnUnverifiedCertificateIsNotACaller(t *testing.T) {
	authority := newClientAuthority(t)
	cluster := newTestCluster(t)
	server := newTestAPI(t, cluster, newSidecarFixture(t))
	offered := &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{authority.leaf(t, "admin", []string{"system:masters"}).Leaf},
	}

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		offered, bearer("Bearer a-caller-token"))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the request responded with %d, want 200: %s", resp.StatusCode, body(t, resp))
	}
	if user := cluster.reviewed()[0].Spec.User; user != "system:serviceaccount:liken-system:viewer" {
		t.Errorf("the review named the user %q, want the token's subject", user)
	}
}

// A leaf with an empty common name identifies no user, so the request
// falls through to the Bearer token and gets a 401 when it has none.
func TestACertificateWithNoCommonNameIsNotACaller(t *testing.T) {
	authority := newClientAuthority(t)
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		authority.verified(t, "", []string{"system:masters"}), http.Header{})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the nameless certificate responded with %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != authenticateRealm {
		t.Errorf("the refusal challenged with %q, want %q", got, authenticateRealm)
	}
}

// A request with no credential gets a bare challenge with no error
// code, as RFC 6750 section 3 specifies.
func TestNoCertificateAndNoTokenIsRefused(t *testing.T) {
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))

	resp := callOver(t, server, apiRoot+"/displays/HDMI-A-1/screen.png",
		&tls.ConnectionState{}, http.Header{})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no credential responded with %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != authenticateRealm {
		t.Errorf("the refusal challenged with %q, want %q", got, authenticateRealm)
	}
}

// authenticationConfigMap serves the extension-apiserver-authentication
// ConfigMap over HTTP, the way the API server responds to a get.
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

func (m *authenticationConfigMap) setCA(caPEM string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caPEM = caPEM
}

// The pool is built from the ConfigMap, and loading it again after the
// CA changes replaces the pool.
func TestTheClientAnchorsFollowTheConfigMap(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newAuthenticationConfigMap(t, string(first.certPEM))
	client := apiclient.New(published.URL, published.Client(), "")
	anchors := &clientAnchors{}

	if err := anchors.load(client); err != nil {
		t.Fatalf("loading the first authority from the ConfigMap: %v", err)
	}
	if !verifies(t, anchors, first) {
		t.Error("the first authority's leaf does not verify against the pool")
	}
	if verifies(t, anchors, second) {
		t.Error("an authority the ConfigMap never contained verifies against the pool")
	}

	published.setCA(string(second.certPEM))
	if err := anchors.load(client); err != nil {
		t.Fatalf("loading the rotated authority from the ConfigMap: %v", err)
	}
	if !verifies(t, anchors, second) {
		t.Error("the rotated authority's leaf does not verify against the pool")
	}
}

func verifies(t *testing.T, anchors *clientAnchors, authority *clientAuthority) bool {
	t.Helper()
	leaf := authority.leaf(t, "admin", []string{"system:masters"})
	_, err := leaf.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.current(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err == nil
}

// When the API cannot read the ConfigMap, load returns an error and
// the pool stays empty.
func TestAnAbsentConfigMapLeavesNoAnchors(t *testing.T) {
	published := newAuthenticationConfigMap(t, "")
	published.absent = true
	anchors := &clientAnchors{}

	err := anchors.load(apiclient.New(published.URL, published.Client(), ""))

	if err == nil {
		t.Fatal("loading an absent ConfigMap returned no error")
	}
	if anchors.current() == nil {
		t.Error("the pool is nil, want an empty pool, because a nil ClientCAs makes Go verify against the system root store")
	}
}

func TestTheWatchReplacesTheAnchorsWhenTheAuthorityChanges(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newConfigMapStore(t, clientCANamespace)
	published.put(authenticationConfigMapHolding(string(first.certPEM)))
	anchors := &clientAnchors{}

	go keepClientAnchors(t.Context(), published.watcher(), anchors)
	eventually(t, "the first authority", func() bool { return verifies(t, anchors, first) })
	published.put(authenticationConfigMapHolding(string(second.certPEM)))

	eventually(t, "the second authority", func() bool { return verifies(t, anchors, second) })
}

// When the ConfigMap is deleted, the pool keeps the CA it had, because
// that CA still verifies the certificates the cluster has issued.
func TestTheWatchKeepsTheAnchorsWhenTheConfigMapGoes(t *testing.T) {
	first := newClientAuthority(t)
	published := newConfigMapStore(t, clientCANamespace)
	published.put(authenticationConfigMapHolding(string(first.certPEM)))
	anchors := &clientAnchors{}
	go keepClientAnchors(t.Context(), published.watcher(), anchors)
	eventually(t, "the first authority", func() bool { return verifies(t, anchors, first) })

	published.remove(clientCAConfigMap)
	time.Sleep(50 * time.Millisecond)

	if !verifies(t, anchors, first) {
		t.Error("the pool dropped the authority when the ConfigMap went")
	}
}

func authenticationConfigMapHolding(caPEM string) ConfigMap {
	return ConfigMap{
		Metadata: objectMeta{Name: clientCAConfigMap, Namespace: clientCANamespace},
		Data:     map[string]string{clientCAKey: caPEM},
	}
}

// listenOver starts a TLS listener with apiTLSConfig. It returns the
// listener's base URL and a pool that trusts its server certificate.
func listenOver(t *testing.T, server *apiServer, anchors *clientAnchors) (base string, anchor *x509.CertPool) {
	t.Helper()
	ca, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatalf("minting the test authority: %v", err)
	}
	certPEM, keyPEM, err := ca.mintLeaf("localhost", []string{"localhost"}, time.Now())
	if err != nil {
		t.Fatalf("minting the server leaf: %v", err)
	}
	holder := &certificateHolder{}
	if err := holder.set(certPEM, keyPEM, false); err != nil {
		t.Fatalf("setting the server certificate: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on a local port: %v", err)
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

// The whole policy on a real connection: a leaf signed by the
// cluster's CA identifies the caller, a leaf from any other CA is
// rejected before any route runs, and a Bearer token works on the same
// listener.
func TestTheListenerTrustsOnlyTheClusterAuthority(t *testing.T) {
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
		t.Errorf("the cluster's own certificate responded with %d, want 200", resp.StatusCode)
	}
	// Without NextProtos in the configuration that GetConfigForClient
	// returns, every connection would fall back to HTTP/1.1.
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
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a-caller-token")
	tokenResp, err := callerWith(anchor).Do(request)
	if err != nil {
		t.Fatalf("a Bearer token on the same listener: %v", err)
	}
	_ = tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		t.Errorf("the Bearer token responded with %d, want 200", tokenResp.StatusCode)
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
				// The client dials 127.0.0.1 and the server
				// certificate names localhost.
				ServerName: "localhost",
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}
