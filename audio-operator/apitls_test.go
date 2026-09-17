package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// objectStore stands in for the API server's Secrets and ConfigMaps.
// It answers 409 on a create of something it already holds, which is
// the race the API has to lose cleanly.
type objectStore struct {
	mu         sync.Mutex
	secrets    map[string]secret
	configMaps map[string]configMap
	server     *httptest.Server
}

func newObjectStore(t *testing.T) *objectStore {
	t.Helper()
	store := &objectStore{
		secrets:    map[string]secret{},
		configMaps: map[string]configMap{},
	}
	store.server = httptest.NewServer(http.HandlerFunc(store.serve))
	t.Cleanup(store.server.Close)
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
	return newCertificates(NewClient(s.server.URL, s.server.Client(), ""), "liken-system", apiService)
}

func (s *objectStore) secretData(t *testing.T, name string) map[string][]byte {
	t.Helper()
	s.mu.Lock()
	held, found := s.secrets[name]
	s.mu.Unlock()
	if !found {
		t.Fatalf("the store holds no Secret %s", name)
	}
	decoded, err := decodeSecretData(held.Data)
	if err != nil {
		t.Fatal(err)
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
			t.Errorf("the Secret %s holds no %s", apiTLSSecret, key)
		}
	}
	store.mu.Lock()
	kind := store.secrets[apiTLSSecret].Type
	store.mu.Unlock()
	if kind != "kubernetes.io/tls" {
		t.Errorf("the Secret is typed %q", kind)
	}

	// The container's leaf is signed by the same CA, with the one name
	// the API dials it by.
	leaf := store.secretData(t, captureTLSSecret)
	certificate, err := parseCertificate(leaf[tlsCertFile])
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(certificate.DNSNames, []string{captureAudience}) {
		t.Errorf("the capture leaf names %v, want [%s]", certificate.DNSNames, captureAudience)
	}
	authority, err := parseCertificate(held[tlsCABundle])
	if err != nil {
		t.Fatal(err)
	}
	if err := certificate.CheckSignatureFrom(authority); err != nil {
		t.Errorf("the capture leaf is not signed by the domain's CA: %v", err)
	}

	// The trust anchor is a ConfigMap, so a client reads it with an
	// ordinary get and never touches the Secret.
	store.mu.Lock()
	anchor := store.configMaps[apiCAConfigMap].Data[tlsCABundle]
	store.mu.Unlock()
	if anchor != string(held[tlsCABundle]) {
		t.Error("the ConfigMap does not hold the CA certificate")
	}
	if strings.Contains(anchor, "PRIVATE KEY") {
		t.Error("the ConfigMap holds a private key")
	}
}

func TestThePublicLeafNamesEverySpellingOfTheService(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatal(err)
	}
	certificate, err := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	held := store.secretData(t, apiTLSSecret)

	authority, _ := parseCertificate(held[tlsCABundle])
	if years := authority.NotAfter.Sub(at).Hours() / 24 / 365; years < 9.9 || years > 10.1 {
		t.Errorf("the CA lives %.1f years, want 10", years)
	}
	leaf, _ := parseCertificate(held[tlsCertFile])
	if years := leaf.NotAfter.Sub(at).Hours() / 24 / 365; years < 0.99 || years > 1.01 {
		t.Errorf("the leaf lives %.2f years, want 1", years)
	}
}

func TestALeafIsMintedAgainUnderAThirdOfItsLife(t *testing.T) {
	store := newObjectStore(t)
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	first := store.certificates()
	first.now = func() time.Time { return at }
	if _, err := first.ensure(); err != nil {
		t.Fatal(err)
	}
	minted, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])

	// Eight months in, more than a third of a year remains.
	kept := store.certificates()
	kept.now = func() time.Time { return at.Add(8 * 30 * 24 * time.Hour) }
	if _, err := kept.ensure(); err != nil {
		t.Fatal(err)
	}
	same, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])
	if minted.SerialNumber.Cmp(same.SerialNumber) != 0 {
		t.Error("a leaf with two thirds of its life left was minted again")
	}

	// Eleven months in, under a third remains.
	renewed := store.certificates()
	renewed.now = func() time.Time { return at.Add(11 * 30 * 24 * time.Hour) }
	if _, err := renewed.ensure(); err != nil {
		t.Fatal(err)
	}
	next, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCertFile])
	if minted.SerialNumber.Cmp(next.SerialNumber) == 0 {
		t.Error("a leaf under a third of its life was not minted again")
	}
	// The CA is the same one, so nothing a client already trusts moves.
	authority, _ := parseCertificate(store.secretData(t, apiTLSSecret)[tlsCABundle])
	if err := next.CheckSignatureFrom(authority); err != nil {
		t.Errorf("the new leaf is signed by another CA: %v", err)
	}
}

func TestACreateThatLosesTheRaceReadsTheWinner(t *testing.T) {
	store := newObjectStore(t)
	winner := store.certificates()
	if _, err := winner.ensure(); err != nil {
		t.Fatal(err)
	}
	winnersCA := store.secretData(t, apiTLSSecret)[tlsCABundle]

	// The second pod sees no Secret at the moment it looks, mints its
	// own, and loses the create. It has to take the winner's CA, or the
	// two pods would serve leaves no one anchor covers.
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
	// A cert-manager owner replaces audio-api-tls with a Certificate of
	// the same name, which holds no CA key.
	issued, err := mintAuthority(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := issued.mintLeaf(apiService, serviceNames(apiService, "liken-system"), time.Now())
	if err != nil {
		t.Fatal(err)
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
		t.Error("the API signed a capture leaf with a CA key it does not hold")
	}
}

func TestTheNearestExpiryIsWhatTheGaugeReports(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	at := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	certs.now = func() time.Time { return at }
	if _, err := certs.ensure(); err != nil {
		t.Fatal(err)
	}
	// The leaf runs out first, so the leaf's expiry is the gauge.
	nearest := certs.nearestExpiry()
	if years := nearest.Sub(at).Hours() / 24 / 365; years > 1.01 {
		t.Errorf("the nearest expiry is %.2f years out, want the leaf's one year", years)
	}
}

func TestATrustAnchorTheContainerAcceptsIsWhatTheAPIPublishes(t *testing.T) {
	store := newObjectStore(t)
	certs := store.certificates()
	if _, err := certs.ensure(); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certs.anchor()) {
		t.Fatal("the anchor holds no certificates")
	}
	leaf, err := parseCertificate(store.secretData(t, captureTLSSecret)[tlsCertFile])
	if err != nil {
		t.Fatal(err)
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
	if _, err := expiryOf(keyPair{Certificate: []byte("x")}); !errors.Is(err, err) || err == nil {
		t.Error("a certificate that cannot be read reported an expiry")
	}
}
