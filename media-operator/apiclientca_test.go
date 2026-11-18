package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// clientAuthority signs client certificates the way the cluster's
// client certificate authority does.
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

// leaf returns a client certificate like one in a kubeconfig, with the
// user as the subject common name and the groups as its organizations.
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

// verified returns the connection state a TLS listener passes to the
// handler after it verified a leaf against this authority.
func (a *clientAuthority) verified(t *testing.T, user string, groups []string) *tls.ConnectionState {
	t.Helper()
	leaf := a.leaf(t, user, groups)
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf.Leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf.Leaf, a.certificate}},
	}
}

// callOver sends one request with the given TLS connection state and
// headers, without a real TLS listener.
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

// The SubjectAccessReview has the leaf's common name as the user and
// its organizations as the groups.
func TestAVerifiedClientCertificateIsTheCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		authority := newClientAuthority(t)

		recorder := fixture.callOver(playerPathFor(""),
			authority.verified(t, "admin", []string{"system:masters"}), nil)

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, len(fixture.plane.reviews), 1)
		mustMatch(t, fixture.plane.reviews[0].Spec.User, "admin")
		mustMatchAll(t, fixture.plane.reviews[0].Spec.Groups, []string{"system:masters"})
	})
}

func TestTheCapturedEventNamesTheCertificateUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))
		authority := newClientAuthority(t)

		recorder := fixture.callOver(playerPathFor("media.mp4"),
			authority.verified(t, "admin", []string{"system:masters"}), nil)
		synctest.Wait()

		mustMatch(t, recorder.Code, http.StatusOK)
		posted := fixture.events.About("Player", testAPIPlayer)
		mustMatch(t, len(posted), 1)
		mustMatch(t, posted[0].Message, "admin took the media of "+testAPIPlayer+" as video/mp4")
	})
}

func TestTheCertificateComesBeforeTheToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		authority := newClientAuthority(t)

		recorder := fixture.callOver(playerPathFor(""),
			authority.verified(t, "admin", []string{"system:masters"}),
			http.Header{"Authorization": {"Bearer " + testAPIToken}})

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, fixture.plane.reviews[0].Spec.User, "admin")
		mustMatch(t, fixture.plane.tokenReviews, 0)
	})
}

// A certificate that the listener did not verify identifies no caller,
// so the request is authenticated by its bearer token.
func TestAnUnverifiedCertificateNamesNobody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestACertificateWithNoCommonNameNamesNobody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		authority := newClientAuthority(t)

		recorder := fixture.callOver(playerPathFor(""),
			authority.verified(t, "", []string{"system:masters"}), http.Header{})

		mustMatch(t, recorder.Code, http.StatusUnauthorized)
		mustMatch(t, recorder.Header().Get("WWW-Authenticate"), `Bearer realm="`+apiRealm+`"`)
	})
}

// A request with neither credential gets the bare challenge of RFC 6750
// section 3.
func TestNoCertificateAndNoTokenIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)

		recorder := fixture.callOver(playerPathFor(""), &tls.ConnectionState{}, http.Header{})

		mustMatch(t, recorder.Code, http.StatusUnauthorized)
		mustMatch(t, recorder.Header().Get("WWW-Authenticate"), `Bearer realm="`+apiRealm+`"`)
	})
}

// A second certificate for the same user does not reuse the first
// certificate's cached grant.
func TestTheGrantCacheKeysOnTheCertificate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		authority := newClientAuthority(t)
		state := authority.verified(t, "admin", []string{"system:masters"})

		fixture.callOver(playerPathFor(""), state, nil)
		fixture.callOver(playerPathFor(""), state, nil)
		mustMatch(t, len(fixture.plane.reviews), 1)

		fixture.callOver(playerPathFor(""), authority.verified(t, "admin", []string{"system:masters"}), nil)
		mustMatch(t, len(fixture.plane.reviews), 2)
	})
}

func authenticationConfigMap(caPEM string, version string) *ConfigMap {
	return &ConfigMap{
		Metadata: ObjectMeta{Name: clientCAConfigMap, Namespace: clientCANamespace, ResourceVersion: version},
		Data:     map[string]string{clientCAKey: caPEM},
	}
}

func publishAuthority(t *testing.T, api *coreAPI, caPEM string) {
	t.Helper()
	api.seed(t, "configmaps/"+clientCAConfigMap, authenticationConfigMap(caPEM, "7"))
}

// verifiesAgainst reports whether a leaf that authority signed
// verifies against the current pool.
func verifiesAgainst(t *testing.T, anchors *clientAnchors, authority *clientAuthority) bool {
	t.Helper()
	leaf := authority.leaf(t, "admin", []string{"system:masters"})
	_, err := leaf.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.current(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err == nil
}

type reportedLines struct {
	mu    sync.Mutex
	lines []string
}

func (r *reportedLines) report(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

func (r *reportedLines) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.lines)
}

// keepAnchors runs keepClientAnchors until the test ends. It returns
// after the watch has processed everything the server sent, because it
// waits for the synctest bubble to settle.
func keepAnchors(t *testing.T, handler http.Handler, anchors *clientAnchors, lines *reportedLines, synced chan struct{}) {
	t.Helper()
	watcher := testWatcher(t, handler)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		stop()
		<-done
	})
	go func() {
		defer close(done)
		keepClientAnchors(ctx, watcher, anchors, lines.report, synced)
	}()
	synctest.Wait()
}

// Every request that keepClientAnchors sends selects that ConfigMap by
// its name.
func TestTheClientAnchorsFollowTheConfigMap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := newClientAuthority(t), newClientAuthority(t)
		api := newCoreAPI()
		publishAuthority(t, api, string(first.certPEM))
		anchors := &clientAnchors{}

		synced := make(chan struct{})

		keepAnchors(t, api.handler(), anchors, &reportedLines{}, synced)
		mustMatch(t, received(synced), true)
		mustMatch(t, verifiesAgainst(t, anchors, first), true)
		mustMatch(t, verifiesAgainst(t, anchors, second), false)

		api.seed(t, "configmaps/"+clientCAConfigMap, authenticationConfigMap(string(second.certPEM), "8"))
		synctest.Wait()
		mustMatch(t, verifiesAgainst(t, anchors, second), true)
		for _, request := range api.requested() {
			mustMatch(t, request, "/api/v1/namespaces/kube-system/configmaps?fieldSelector=metadata.name="+clientCAConfigMap)
		}
	})
}

func TestTheClientAnchorsFollowTheConfigMapWithNoSyncedChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := newClientAuthority(t), newClientAuthority(t)
		api := newCoreAPI()
		publishAuthority(t, api, string(first.certPEM))
		anchors := &clientAnchors{}

		keepAnchors(t, api.handler(), anchors, &reportedLines{}, nil)
		mustMatch(t, verifiesAgainst(t, anchors, first), true)
		api.seed(t, "configmaps/"+clientCAConfigMap, authenticationConfigMap(string(second.certPEM), "8"))
		synctest.Wait()

		mustMatch(t, verifiesAgainst(t, anchors, second), true)
	})
}

// When the API server refuses the read, the pool stays empty and synced
// stays open.
func TestAnUnreadableConfigMapLeavesAnEmptyPool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		authority := newClientAuthority(t)
		api := newCoreAPI()
		publishAuthority(t, api, string(authority.certPEM))
		api.refusal = http.StatusForbidden
		anchors := &clientAnchors{}

		synced := make(chan struct{})

		keepAnchors(t, api.handler(), anchors, &reportedLines{}, synced)

		mustMatch(t, len(api.requested()) > 0, true)
		mustMatch(t, received(synced), false)
		mustMatch(t, verifiesAgainst(t, anchors, authority), false)
	})
}

// A deleted ConfigMap reports one line and keeps the pool.
func TestADeletedConfigMapKeepsThePool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		authority := newClientAuthority(t)
		api := newCoreAPI()
		publishAuthority(t, api, string(authority.certPEM))
		anchors := &clientAnchors{}
		lines := &reportedLines{}
		synced := make(chan struct{})
		keepAnchors(t, api.handler(), anchors, lines, synced)
		mustMatch(t, received(synced), true)

		api.remove("configmaps/" + clientCAConfigMap)
		synctest.Wait()

		mustMatch(t, lines.count(), 1)
		mustMatch(t, verifiesAgainst(t, anchors, authority), true)
	})
}

func TestAConfigMapWithNoAuthorityIsReported(t *testing.T) {
	rows := []struct {
		name  string
		caPEM string
	}{
		{name: "the key is absent", caPEM: ""},
		{name: "the key holds no certificate", caPEM: "not a certificate"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				authority := newClientAuthority(t)
				api := newCoreAPI()
				publishAuthority(t, api, row.caPEM)
				anchors := &clientAnchors{}
				lines := &reportedLines{}
				synced := make(chan struct{})

				keepAnchors(t, api.handler(), anchors, lines, synced)
				mustMatch(t, received(synced), true)
				mustMatch(t, lines.count(), 1)

				publishAuthority(t, api, string(authority.certPEM))
				synctest.Wait()
				mustMatch(t, verifiesAgainst(t, anchors, authority), true)
			})
		})
	}
}

// listenOver serves the API with the server's real TLS configuration
// on a pipeListener, so the handshake runs inside the synctest bubble.
func listenOver(t *testing.T, fixture *apiFixture) (base string, serverAnchor *x509.CertPool, dial dialFunc) {
	t.Helper()
	authority, err := mintAuthority(time.Now())
	mustSucceed(t, err)
	certPEM, keyPEM, err := authority.mintLeaf(time.Now(), "localhost")
	mustSucceed(t, err)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	mustSucceed(t, err)
	fixture.server.setCertificate(&pair)

	socket := newPipeListener()
	// No lock guards the fixture's log slice, so disable logging while
	// the listener's goroutines serve requests.
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
	return "https://localhost", serverAnchor, socket.dial
}

type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

func callerWith(dial dialFunc, anchor *x509.CertPool, offered ...tls.Certificate) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:       dial,
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

// A certificate from another authority fails the TLS handshake, and a
// bearer token with no certificate still works.
func TestTheListenerAcceptsOnlyCertificatesFromTheClusterAuthority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		authority, foreign := newClientAuthority(t), newClientAuthority(t)
		pool := x509.NewCertPool()
		mustMatch(t, pool.AppendCertsFromPEM(authority.certPEM), true)
		fixture.server.anchors.set(pool)
		base, anchor, dial := listenOver(t, fixture)
		target := base + playerPathFor("")

		resp, err := callerWith(dial, anchor, authority.leaf(t, "admin", []string{"system:masters"})).Get(target)
		mustSucceed(t, err)
		mustSucceed(t, resp.Body.Close())
		mustMatch(t, resp.StatusCode, http.StatusOK)
		mustMatch(t, fixture.plane.reviews[0].Spec.User, "admin")
		// Without NextProtos in the configuration that GetConfigForClient
		// returns, every client uses HTTP/1.1.
		mustMatch(t, resp.Proto, "HTTP/2.0")

		_, err = callerWith(dial, anchor, foreign.leaf(t, "admin", nil)).Get(target)
		mustFail(t, err)

		request, err := http.NewRequest(http.MethodGet, target, nil)
		mustSucceed(t, err)
		request.Header.Set("Authorization", "Bearer "+testAPIToken)
		tokenResponse, err := callerWith(dial, anchor).Do(request)
		mustSucceed(t, err)
		mustSucceed(t, tokenResponse.Body.Close())
		mustMatch(t, tokenResponse.StatusCode, http.StatusOK)
	})
}
