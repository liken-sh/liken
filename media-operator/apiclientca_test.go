package main

// These tests cover the client-certificate half of authentication:
// the anchors read from the ConfigMap, the caller read out of a
// verified leaf, and the whole policy on a real TLS listener.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// An authority in the shape the API server publishes in
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
	mustSucceed(t, err)
	serial, err := serialNumber()
	mustSucceed(t, err)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "a cluster client authority"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	mustSucceed(t, err)
	certificate, err := x509.ParseCertificate(der)
	mustSucceed(t, err)
	return &clientAuthority{
		certPEM:     encodePEM("CERTIFICATE", der),
		certificate: certificate,
		key:         key,
	}
}

// A leaf in the shape a kubeconfig carries: the user in the subject's
// common name and the groups in its organization.
func (a *clientAuthority) leaf(t *testing.T, user string, groups []string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mustSucceed(t, err)
	serial, err := serialNumber()
	mustSucceed(t, err)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: user, Organization: groups},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	mustSucceed(t, err)
	certificate, err := x509.ParseCertificate(der)
	mustSucceed(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}
}

// The state a TLS listener hands the handler once it verified the leaf
// against the cluster's authority.
func (a *clientAuthority) verified(t *testing.T, user string, groups []string) *tls.ConnectionState {
	t.Helper()
	leaf := a.leaf(t, user, groups)
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf.Leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf.Leaf, a.certificate}},
	}
}

// One request over a connection that carries the given TLS state, with
// no Authorization field unless the header states one.
func (f *apiFixture) callOver(target string, state *tls.ConnectionState, header http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	request.TLS = state
	recorder := httptest.NewRecorder()
	f.server.handler().ServeHTTP(recorder, request)
	return recorder
}

// The user is the leaf's subject common name and the groups are its
// subject organization values, which is what the SubjectAccessReview
// has to carry for a rule written for that person to answer.
func TestAVerifiedClientCertificateIsTheCaller(t *testing.T) {
	fixture := newAPIFixture(t)
	authority := newClientAuthority(t)

	recorder := fixture.callOver(playerPathFor(""),
		authority.verified(t, "admin", []string{"system:masters"}), nil)

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, len(fixture.plane.reviews), 1)
	mustMatch(t, fixture.plane.reviews[0].Spec.User, "admin")
	mustMatchAll(t, fixture.plane.reviews[0].Spec.Groups, []string{"system:masters"})
}

// kubectl describe player answers with the Captured Event, so the
// Event names the certificate's user.
func TestTheCapturedEventNamesTheCertificateUser(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))
	authority := newClientAuthority(t)

	recorder := fixture.callOver(playerPathFor("media.mp4"),
		authority.verified(t, "admin", []string{"system:masters"}), nil)

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, len(fixture.plane.events), 1)
	mustMatch(t, fixture.plane.events[0].Message,
		"admin took the media of "+testAPIPlayer+" as video/mp4")
}

// A certificate that named a caller is the caller, and the token beside
// it is never reviewed.
func TestTheCertificateComesBeforeTheToken(t *testing.T) {
	fixture := newAPIFixture(t)
	authority := newClientAuthority(t)

	recorder := fixture.callOver(playerPathFor(""),
		authority.verified(t, "admin", []string{"system:masters"}),
		http.Header{"Authorization": {"Bearer " + testAPIToken}})

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, fixture.plane.reviews[0].Spec.User, "admin")
	mustMatch(t, fixture.plane.tokenReviews, 0)
}

// Only a chain the listener verified names a caller, so an offered
// certificate falls to the token beside it.
func TestAnUnverifiedCertificateNamesNobody(t *testing.T) {
	fixture := newAPIFixture(t)
	authority := newClientAuthority(t)
	offered := &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{authority.leaf(t, "admin", []string{"system:masters"}).Leaf},
	}

	recorder := fixture.callOver(playerPathFor(""), offered,
		http.Header{"Authorization": {"Bearer " + testAPIToken}})

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, fixture.plane.reviews[0].Spec.User, testAPISubject)
	mustMatch(t, fixture.plane.tokenReviews, 1)
}

// A leaf with no common name names no user, so the request falls to the
// token and ends in the ordinary 401 when it carries none.
func TestACertificateWithNoCommonNameNamesNobody(t *testing.T) {
	fixture := newAPIFixture(t)
	authority := newClientAuthority(t)

	recorder := fixture.callOver(playerPathFor(""),
		authority.verified(t, "", []string{"system:masters"}), http.Header{})

	mustMatch(t, recorder.Code, http.StatusUnauthorized)
	mustMatch(t, recorder.Header().Get("WWW-Authenticate"), `Bearer realm="`+apiRealm+`"`)
}

// Neither credential is the bare challenge, RFC 6750 section 3.
func TestNoCertificateAndNoTokenIsRefused(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.callOver(playerPathFor(""), &tls.ConnectionState{}, http.Header{})

	mustMatch(t, recorder.Code, http.StatusUnauthorized)
	mustMatch(t, recorder.Header().Get("WWW-Authenticate"), `Bearer realm="`+apiRealm+`"`)
}

// The grant cache is keyed on the leaf, so one certificate costs one
// access review and a second certificate never reuses the first one's
// grant.
func TestTheGrantCacheKeysOnTheCertificate(t *testing.T) {
	fixture := newAPIFixture(t)
	authority := newClientAuthority(t)
	state := authority.verified(t, "admin", []string{"system:masters"})

	fixture.callOver(playerPathFor(""), state, nil)
	fixture.callOver(playerPathFor(""), state, nil)
	mustMatch(t, len(fixture.plane.reviews), 1)

	fixture.callOver(playerPathFor(""), authority.verified(t, "admin", []string{"system:masters"}), nil)
	mustMatch(t, len(fixture.plane.reviews), 2)
}

// The API server's own ConfigMap, served the way it answers a get.
type authenticationConfigMap struct {
	server *httptest.Server

	mutex  sync.Mutex
	caPEM  string
	absent bool
}

func newAuthenticationConfigMap(t *testing.T, caPEM string) *authenticationConfigMap {
	t.Helper()
	held := &authenticationConfigMap{caPEM: caPEM}
	held.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		held.mutex.Lock()
		defer held.mutex.Unlock()
		if held.absent {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(ConfigMap{
			Metadata: ObjectMeta{Name: clientCAConfigMap, Namespace: clientCANamespace},
			Data:     map[string]string{clientCAKey: held.caPEM},
		})
	}))
	t.Cleanup(held.server.Close)
	return held
}

func (m *authenticationConfigMap) holds(caPEM string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.caPEM = caPEM
}

func (m *authenticationConfigMap) client() *Client {
	return NewClient(m.server.URL, m.server.Client(), "")
}

// Whether a leaf of this authority verifies against the anchors the
// pool holds now.
func verifiesAgainst(t *testing.T, anchors *clientAnchors, authority *clientAuthority) bool {
	t.Helper()
	leaf := authority.leaf(t, "admin", []string{"system:masters"})
	_, err := leaf.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.held(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err == nil
}

// The pool comes from the ConfigMap, and reading it again after the
// cluster rotated its authority replaces the pool with no restart.
func TestTheClientAnchorsFollowTheConfigMap(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newAuthenticationConfigMap(t, string(first.certPEM))
	anchors := &clientAnchors{}

	mustSucceed(t, anchors.load(published.client()))
	mustMatch(t, verifiesAgainst(t, anchors, first), true)
	mustMatch(t, verifiesAgainst(t, anchors, second), false)

	published.holds(string(second.certPEM))
	mustSucceed(t, anchors.load(published.client()))
	mustMatch(t, verifiesAgainst(t, anchors, second), true)
}

// A ConfigMap the API cannot read answers an error and leaves an empty
// pool behind, because the API still answers every token caller.
func TestAConfigMapThisAPICannotReadLeavesAnEmptyPool(t *testing.T) {
	published := newAuthenticationConfigMap(t, "")
	published.absent = true
	anchors := &clientAnchors{}

	err := anchors.load(published.client())

	mustFail(t, err)
	if anchors.held() == nil {
		t.Fatal("the pool is nil, so a handshake would verify against the system roots")
	}
}

// A ConfigMap that carries no usable authority answers an error too.
func TestAConfigMapWithNoAuthorityAnswersAnError(t *testing.T) {
	rows := []struct {
		name  string
		caPEM string
	}{
		{name: "the key is absent", caPEM: ""},
		{name: "the key holds no certificate", caPEM: "not a certificate"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			published := newAuthenticationConfigMap(t, row.caPEM)
			anchors := &clientAnchors{}

			mustFail(t, anchors.load(published.client()))
		})
	}
}

// The minute loop reads the ConfigMap again, so a rotated authority
// reaches the next handshake with no restart.
func TestTheAnchorLoopTakesUpARotatedAuthority(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	published := newAuthenticationConfigMap(t, string(first.certPEM))
	anchors := &clientAnchors{}
	held := clientAnchorInterval
	clientAnchorInterval = 5 * time.Millisecond
	t.Cleanup(func() { clientAnchorInterval = held })

	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go keepClientAnchors(ctx, published.client(), anchors)
	published.holds(string(second.certPEM))

	deadline := time.Now().Add(10 * time.Second)
	for !verifiesAgainst(t, anchors, second) {
		if time.Now().After(deadline) {
			t.Fatal("the loop did not take up the rotated authority within ten seconds")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The API served over its own TLS configuration, with the anchors the
// test set, so a caller meets the policy a cluster caller meets.
func listenOver(t *testing.T, fixture *apiFixture) (base string, serverAnchor *x509.CertPool) {
	t.Helper()
	authority, err := mintAuthority(time.Now())
	mustSucceed(t, err)
	certPEM, keyPEM, err := authority.mintLeaf(time.Now(), "localhost")
	mustSucceed(t, err)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	mustSucceed(t, err)
	fixture.server.holdCertificate(&pair)

	socket, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)
	// The log slice is the fixture's own and no lock guards it, so a
	// listener that answers on goroutines of its own writes no lines.
	fixture.server.log = nil
	serving := &http.Server{
		Handler:           fixture.server.handler(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		TLSConfig:         fixture.server.tlsConfig(),
	}
	go func() { _ = serving.ServeTLS(socket, "", "") }()
	t.Cleanup(func() { _ = serving.Close() })

	serverAnchor = x509.NewCertPool()
	mustMatch(t, serverAnchor.AppendCertsFromPEM(authority.certPEM), true)
	return "https://" + socket.Addr().String(), serverAnchor
}

// The client a caller dials the listener with, offering the leaves it
// holds and naming the server leaf's own name.
func callerWith(anchor *x509.CertPool, offered ...tls.Certificate) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				RootCAs:      anchor,
				Certificates: offered,
				ServerName:   "localhost",
				MinVersion:   tls.VersionTLS12,
			},
		},
	}
}

// The whole policy on a real connection: a leaf of the cluster's
// authority is the caller, a leaf of any other authority never
// reaches a route, and a token answers on the same listener.
func TestTheListenerTakesTheClusterAuthorityAlone(t *testing.T) {
	fixture := newAPIFixture(t)
	authority, foreign := newClientAuthority(t), newClientAuthority(t)
	pool := x509.NewCertPool()
	mustMatch(t, pool.AppendCertsFromPEM(authority.certPEM), true)
	fixture.server.anchors.set(pool)
	base, anchor := listenOver(t, fixture)
	target := base + playerPathFor("")

	resp, err := callerWith(anchor, authority.leaf(t, "admin", []string{"system:masters"})).Get(target)
	mustSucceed(t, err)
	mustSucceed(t, resp.Body.Close())
	mustMatch(t, resp.StatusCode, http.StatusOK)
	mustMatch(t, fixture.plane.reviews[0].Spec.User, "admin")
	// The listener answers its own TLS configuration on every
	// handshake, and that configuration names the protocols. A
	// configuration that named none would drop every caller to
	// HTTP/1.1 and nothing else would say so.
	mustMatch(t, resp.Proto, "HTTP/2.0")

	_, err = callerWith(anchor, foreign.leaf(t, "admin", nil)).Get(target)
	mustFail(t, err)

	request, err := http.NewRequest(http.MethodGet, target, nil)
	mustSucceed(t, err)
	request.Header.Set("Authorization", "Bearer "+testAPIToken)
	answered, err := callerWith(anchor).Do(request)
	mustSucceed(t, err)
	mustSucceed(t, answered.Body.Close())
	mustMatch(t, answered.StatusCode, http.StatusOK)
}
