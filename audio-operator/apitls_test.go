package main

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// objectStore fakes the API server's Secrets and ConfigMaps. It
// returns 409 to a create of an object that it already stores, which
// is how the API server refuses the pod that loses the race to create
// one.
type objectStore struct {
	mu         sync.Mutex
	secrets    map[string]secret
	configMaps map[string]configMap
	server     *apiservertest.Server
}

func newObjectStore(t *testing.T) *objectStore {
	t.Helper()
	store := &objectStore{
		secrets:    map[string]secret{},
		configMaps: map[string]configMap{},
	}
	store.server = apiservertest.Start(t, http.HandlerFunc(store.serve))
	return store
}

func (s *objectStore) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	secrets := secretsPath("liken-system")
	maps := configMapsPath("liken-system")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == secrets:
		var written secret
		_ = json.NewDecoder(r.Body).Decode(&written)
		if _, taken := s.secrets[written.Metadata.Name]; taken {
			w.WriteHeader(http.StatusConflict)
			return
		}
		s.secrets[written.Metadata.Name] = written
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(&written)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, secrets+"/"):
		var written secret
		_ = json.NewDecoder(r.Body).Decode(&written)
		s.secrets[strings.TrimPrefix(r.URL.Path, secrets+"/")] = written
		_ = json.NewEncoder(w).Encode(&written)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, secrets+"/"):
		held, found := s.secrets[strings.TrimPrefix(r.URL.Path, secrets+"/")]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&held)
	case r.Method == http.MethodPost && r.URL.Path == maps:
		var written configMap
		_ = json.NewDecoder(r.Body).Decode(&written)
		if _, taken := s.configMaps[written.Metadata.Name]; taken {
			w.WriteHeader(http.StatusConflict)
			return
		}
		s.configMaps[written.Metadata.Name] = written
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(&written)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, maps+"/"):
		var written configMap
		_ = json.NewDecoder(r.Body).Decode(&written)
		s.configMaps[strings.TrimPrefix(r.URL.Path, maps+"/")] = written
		_ = json.NewEncoder(w).Encode(&written)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, maps+"/"):
		held, found := s.configMaps[strings.TrimPrefix(r.URL.Path, maps+"/")]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&held)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *objectStore) certificates() *certificates {
	return newCertificates(apiclient.New(apiservertest.Host, s.server.Client(), ""), "liken-system", apiService)
}

func (s *objectStore) holdsSecret(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.secrets[name]
	return found
}

func (s *objectStore) secretData(t *testing.T, name string) map[string][]byte {
	t.Helper()
	s.mu.Lock()
	held, found := s.secrets[name]
	s.mu.Unlock()
	if !found {
		t.Fatalf("the store has no Secret %s", name)
	}
	decoded, err := decodeSecretData(held.Data)
	if err != nil {
		t.Fatalf("decoding the data of the Secret: %v", err)
	}
	return decoded
}

func TestTheFirstStartMintsACAAndBothLeaves(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("the first start: %v", err)
	}

	held := store.secretData(t, apiTLSSecret)
	for _, key := range []string{tlsCertFile, tlsKeyFile, tlsCABundle, caKeyFile} {
		if len(held[key]) == 0 {
			t.Errorf("the Secret %s has no %s", apiTLSSecret, key)
		}
	}
	store.mu.Lock()
	kind := store.secrets[apiTLSSecret].Type
	store.mu.Unlock()
	if kind != "kubernetes.io/tls" {
		t.Errorf("the Secret's type is %q, want kubernetes.io/tls", kind)
	}

	// The capture container's leaf is signed by the same CA and lists
	// only the name that the API dials.
	leaf := store.secretData(t, captureTLSSecret)
	certificate, err := parseCertificate(leaf[tlsCertFile])
	if err != nil {
		t.Fatalf("parsing the capture leaf: %v", err)
	}
	if !slices.Equal(certificate.DNSNames, []string{captureAudience}) {
		t.Errorf("the capture leaf names %v, want [%s]", certificate.DNSNames, captureAudience)
	}
	authority, err := parseCertificate(held[tlsCABundle])
	if err != nil {
		t.Fatalf("parsing the CA certificate: %v", err)
	}
	if err := certificate.CheckSignatureFrom(authority); err != nil {
		t.Errorf("the capture leaf is not signed by the CA in audio-api-tls: %v", err)
	}

	store.mu.Lock()
	anchor := store.configMaps[apiCAConfigMap].Data[tlsCABundle]
	store.mu.Unlock()
	if anchor != string(held[tlsCABundle]) {
		t.Error("the ConfigMap does not contain the CA certificate")
	}
	if strings.Contains(anchor, "PRIVATE KEY") {
		t.Error("the ConfigMap contains a private key")
	}
}

func TestThePublicLeafNamesEverySpellingOfTheService(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	certificate, err := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatalf("parsing the public leaf: %v", err)
	}
	want := []string{
		"audio-api",
		"audio-api.liken-system",
		"audio-api.liken-system.svc",
		"audio-api.liken-system.svc.cluster.local",
	}
	if !slices.Equal(certificate.DNSNames, want) {
		t.Errorf("the public leaf names %v, want %v", certificate.DNSNames, want)
	}
}

func TestTheLifetimesAreTenYearsAndOne(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	certs.now = func() time.Time { return at }
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	held := store.secretData(t, apiTLSSecret)

	authority, _ := parseCertificate(held[tlsCABundle])
	if years := authority.NotAfter.Sub(at).Hours() / 24 / 365; years < 9.9 || years > 10.1 {
		t.Errorf("the CA is valid for %.1f years, want 10", years)
	}
	leaf, _ := parseCertificate(held[tlsCertFile])
	if years := leaf.NotAfter.Sub(at).Hours() / 24 / 365; years < 0.99 || years > 1.01 {
		t.Errorf("the leaf is valid for %.2f years, want 1", years)
	}
}

func TestALeafIsMintedAgainUnderAThirdOfItsLife(t *testing.T) {
	store := newObjectStore(t)
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	first := store.certificates()
	first.now = func() time.Time { return at }
	if _, err := first.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	minted, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])

	// Eight months in, more than a third of the leaf's life remains.
	kept := store.certificates()
	kept.now = func() time.Time { return at.Add(8 * 30 * 24 * time.Hour) }
	if _, err := kept.ensure(); err != nil {
		t.Fatalf("checking the certificates eight months later: %v", err)
	}
	same, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])
	if minted.SerialNumber.Cmp(same.SerialNumber) != 0 {
		t.Error("a leaf with two thirds of its life left was minted again")
	}

	// Eleven months in, less than a third of the leaf's life remains.
	renewed := store.certificates()
	renewed.now = func() time.Time { return at.Add(11 * 30 * 24 * time.Hour) }
	if _, err := renewed.ensure(); err != nil {
		t.Fatalf("checking the certificates eleven months later: %v", err)
	}
	next, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])
	if minted.SerialNumber.Cmp(next.SerialNumber) == 0 {
		t.Error("a leaf under a third of its life was not minted again")
	}
	// The new leaf is signed by the same CA, so clients that trust that
	// CA need no new trust anchor.
	authority, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCABundle])
	if err := next.CheckSignatureFrom(authority); err != nil {
		t.Errorf("the new leaf is signed by another CA: %v", err)
	}
}

func TestACreateThatLosesTheRaceReadsTheWinner(t *testing.T) {
	store := newObjectStore(t)
	winner := store.certificates()
	if _, err := winner.ensure(); err != nil {
		t.Fatalf("minting the certificates in the winning pod: %v", err)
	}
	winnersCA := store.secretData(t, apiTLSSecret)[tlsCABundle]

	// The second pod finds no Secret when it reads, mints its own CA,
	// and loses the create. If it kept its own CA, no single CA
	// certificate would verify the leaves of both pods.
	loser := store.certificates()
	if _, err := loser.ensure(); err != nil {
		t.Fatalf("the losing pod: %v", err)
	}
	if string(loser.anchor()) != string(winnersCA) {
		t.Error("the losing pod kept its own CA")
	}
	if string(store.secretData(t, apiTLSSecret)[tlsCABundle]) != string(winnersCA) {
		t.Error("the losing pod overwrote the winner's Secret")
	}
}

func TestASecretAnotherIssuerOwnsIsServedAndNotSigned(t *testing.T) {
	store := newObjectStore(t)
	// cert-manager can replace audio-api-tls with the Secret of a
	// Certificate of the same name. That Secret has no CA key.
	issued, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatalf("minting the other issuer's CA: %v", err)
	}
	leaf, err := issued.mintLeaf(apiService, serviceNames(apiService, "liken-system"), time.Now())
	if err != nil {
		t.Fatalf("minting the other issuer's leaf: %v", err)
	}
	store.mu.Lock()
	store.secrets[apiTLSSecret] = secret{
		Metadata: EndpointMeta{Name: apiTLSSecret},
		Type:     "kubernetes.io/tls",
		Data: map[string]string{
			tlsCertFile: base64.StdEncoding.EncodeToString(leaf.Certificate),
			tlsKeyFile:  base64.StdEncoding.EncodeToString(leaf.Key),
			tlsCABundle: base64.StdEncoding.EncodeToString(issued.Pair.Certificate),
		},
	}
	store.mu.Unlock()

	certs := store.certificates()
	served, err := certs.ensure()
	if err != nil {
		t.Fatalf("adopting another issuer's Secret: %v", err)
	}
	if len(served.Certificate) == 0 {
		t.Error("the API served no certificate")
	}
	store.mu.Lock()
	_, minted := store.secrets[captureTLSSecret]
	store.mu.Unlock()
	if minted {
		t.Error("the API signed a capture leaf with a CA key that the Secret does not contain")
	}
}

func TestTheNearestExpiryIsWhatTheGaugeReports(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	certs.now = func() time.Time { return at }
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	// The leaf expires in a year and the CA in ten, so the gauge reports
	// the leaf's expiry.
	nearest := certs.nearestExpiry()
	if years := nearest.Sub(at).Hours() / 24 / 365; years > 1.01 {
		t.Errorf("the nearest expiry is %.2f years out, want the leaf's one year", years)
	}
}

func TestATrustAnchorTheContainerAcceptsIsWhatTheAPIPublishes(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certs.anchor()) {
		t.Fatal("the anchor contains no certificates")
	}
	leaf, err := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatalf("parsing the capture leaf: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   captureAudience,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("the API's own anchor does not verify the leaf it minted: %v", err)
	}
}

func TestASecretWithNoLeafIsARefusal(t *testing.T) {
	store := newObjectStore(t)
	store.mu.Lock()
	store.secrets[apiTLSSecret] = secret{Metadata: EndpointMeta{Name: apiTLSSecret}}
	store.mu.Unlock()
	if _, err := store.certificates().ensure(); err == nil {
		t.Fatal("a Secret with no leaf was adopted")
	}
}

// storeAdoptableSecret replaces audio-api-tls with a leaf and a CA that
// another API pod minted, with caKey as the CA's private key.
func storeAdoptableSecret(t *testing.T, store *objectStore, caKey []byte) keyPair {
	t.Helper()
	issued, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatalf("minting the other pod's CA: %v", err)
	}
	leaf, err := issued.mintLeaf(apiService, serviceNames(apiService, "liken-system"), time.Now())
	if err != nil {
		t.Fatalf("minting the other pod's leaf: %v", err)
	}
	if caKey == nil {
		caKey = issued.Pair.Key
	}
	store.mu.Lock()
	store.secrets[apiTLSSecret] = secret{
		Metadata: EndpointMeta{Name: apiTLSSecret},
		Data: map[string]string{
			tlsCertFile: base64.StdEncoding.EncodeToString(leaf.Certificate),
			tlsKeyFile:  base64.StdEncoding.EncodeToString(leaf.Key),
			tlsCABundle: base64.StdEncoding.EncodeToString(issued.Pair.Certificate),
			caKeyFile:   base64.StdEncoding.EncodeToString(caKey),
		},
	}
	store.mu.Unlock()
	return leaf
}

func TestARefusedSecretLeavesTheServedPairInPlace(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	served, public := certs.keys()
	storeAdoptableSecret(t, store, []byte("not pem"))

	if _, err := certs.ensure(); err == nil {
		t.Fatal("a Secret whose CA key is not PEM was adopted")
	}
	authority, leaf := certs.keys()
	if !bytes.Equal(leaf.Certificate, public.Certificate) || !bytes.Equal(authority.Pair.Certificate, served.Pair.Certificate) {
		t.Error("after the refusal, the API holds a pair from the refused Secret, want the pair it served before")
	}
}

// A forwarded request reads the CA while keepCertificates adopts the
// Secret, so go test -race reports a write that does not take mu.
func TestAnAnchorReadDuringAnAdoptIsNotARace(t *testing.T) {
	store := newObjectStore(t)
	storeAdoptableSecret(t, store, nil)
	certs := store.certificates()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			_ = certs.anchor()
		}
	}()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("adopting the Secret: %v", err)
	}
	<-done
}

func TestPEMThatIsNotACertificateIsARefusal(t *testing.T) {
	if _, err := parseCertificate([]byte("not pem")); err == nil {
		t.Error("text that is not PEM parsed as a certificate")
	}
	if _, err := parseKey([]byte("not pem")); err == nil {
		t.Error("text that is not PEM parsed as a key")
	}
	if _, err := readAuthority(keyPair{Certificate: []byte("x"), Key: []byte("y")}); err == nil {
		t.Error("a CA that is not PEM was read")
	}
	if !expiring(keyPair{Certificate: []byte("x")}, time.Now()) {
		t.Error("a certificate that cannot be read was not treated as expiring")
	}
	if at, err := expiryOf(keyPair{Certificate: []byte("x")}); err == nil || !at.IsZero() {
		t.Errorf("expiryOf of a certificate that cannot be read = %v, %v, want no time and an error", at, err)
	}
}

// The watch on the audio-capture-server Secret calls keepCaptureLeaf
// when the Secret is deleted, so the Secret is written again at once
// and not at the next hourly certificate check. Until then the capture
// container serves a certificate that no client trusts, and every tap
// on its node fails with a 503.
func TestADeletedCaptureSecretIsMintedAgainByTheLeafCheck(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	first, err := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatalf("parsing the capture leaf: %v", err)
	}

	store.mu.Lock()
	delete(store.secrets, captureTLSSecret)
	store.mu.Unlock()

	if err := certs.keepCaptureLeaf(); err != nil {
		t.Fatalf("the capture leaf check: %v", err)
	}
	next, err := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatal("the Secret was not minted again")
	}
	if first.SerialNumber.Cmp(next.SerialNumber) == 0 {
		t.Error("the Secret carries the deleted leaf")
	}
	// The new leaf is signed by the same CA, so clients that trust that
	// CA need no new trust anchor.
	authority, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCABundle])
	if err := next.CheckSignatureFrom(authority); err != nil {
		t.Errorf("the new leaf is signed by another CA: %v", err)
	}
}

func TestKeepCaptureLeafWritesNothingWhenTheSecretIsInOrder(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	held, err := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatalf("parsing the capture leaf: %v", err)
	}
	for range 5 {
		if err := certs.keepCaptureLeaf(); err != nil {
			t.Fatalf("checking the capture leaf: %v", err)
		}
	}
	same, _ := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if held.SerialNumber.Cmp(same.SerialNumber) != 0 {
		t.Error("a pass over a Secret in order minted a new leaf")
	}
}

func TestALeafAnotherCASignedIsMintedAgain(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatalf("minting the certificates: %v", err)
	}
	// The Secret holds a leaf from a CA other than the API's. A
	// container that served it would present a certificate that the API
	// refuses.
	stranger, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatalf("minting the other CA: %v", err)
	}
	leaf, err := stranger.mintLeaf(captureAudience, []string{captureAudience}, time.Now())
	if err != nil {
		t.Fatalf("minting a leaf from the other CA: %v", err)
	}
	store.mu.Lock()
	store.secrets[captureTLSSecret] = secret{
		Metadata: EndpointMeta{Name: captureTLSSecret},
		Type:     "kubernetes.io/tls",
		Data: map[string]string{
			tlsCertFile: base64.StdEncoding.EncodeToString(leaf.Certificate),
			tlsKeyFile:  base64.StdEncoding.EncodeToString(leaf.Key),
		},
	}
	store.mu.Unlock()

	if err := certs.keepCaptureLeaf(); err != nil {
		t.Fatalf("checking the capture leaf: %v", err)
	}
	next, err := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatalf("parsing the capture leaf: %v", err)
	}
	authority, _ := parseCertificate(certs.anchor())
	if err := next.CheckSignatureFrom(authority); err != nil {
		t.Errorf("a leaf another CA signed was left in place: %v", err)
	}
}
