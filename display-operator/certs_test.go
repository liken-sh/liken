package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// This fixture is an API server that holds Secrets and ConfigMaps in
// memory and answers get, create, and update the way the real one
// does, 404 and 409 included.
type objectAPI struct {
	mu        sync.Mutex
	objects   map[string]json.RawMessage
	conflicts map[string]json.RawMessage
	creates   map[string]int
	updates   map[string]int
}

func newObjectAPI() *objectAPI {
	return &objectAPI{
		objects:   map[string]json.RawMessage{},
		conflicts: map[string]json.RawMessage{},
		creates:   map[string]int{},
		updates:   map[string]int{},
	}
}

func (a *objectAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch req.Method {
	case http.MethodPost:
		a.creates[req.URL.Path]++
		winner, lost := a.conflicts[req.URL.Path]
		if lost {
			a.objects[req.URL.Path+"/"+objectName(winner)] = winner
			delete(a.conflicts, req.URL.Path)
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message": "secrets \"display-api-tls\" already exists"}`)
			return
		}
		a.objects[req.URL.Path+"/"+objectName(raw)] = raw
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(raw)
	case http.MethodPut:
		a.updates[req.URL.Path]++
		a.objects[req.URL.Path] = raw
		_, _ = w.Write(raw)
	default:
		stored, held := a.objects[req.URL.Path]
		if !held {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message": "not found"}`)
			return
		}
		_, _ = w.Write(stored)
	}
}

func (a *objectAPI) hold(t *testing.T, path string, object any) {
	t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.objects[path] = raw
}

func (a *objectAPI) held(t *testing.T, path string, out any) {
	t.Helper()
	a.mu.Lock()
	raw, ok := a.objects[path]
	a.mu.Unlock()
	if !ok {
		t.Fatalf("the API server holds nothing at %s", path)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

func (a *objectAPI) counts(path string) (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creates[path], a.updates[path]
}

func objectName(raw json.RawMessage) string {
	object := struct {
		Metadata objectMeta `json:"metadata"`
	}{}
	_ = json.Unmarshal(raw, &object)
	return object.Metadata.Name
}

func objectClient(t *testing.T, api *objectAPI) *Client {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client(), "")
}

const (
	testNamespace = "liken-system"
	testSecret    = "display-api-tls"
	testAnchor    = "display-api-ca"
	testSidecar   = "display-capture-server"
)

var mintTime = time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)

func parseCertificate(t *testing.T, block []byte) *x509.Certificate {
	t.Helper()
	decoded, _ := pem.Decode(block)
	if decoded == nil {
		t.Fatalf("no PEM block in %q", block)
	}
	certificate, err := x509.ParseCertificate(decoded.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func testAuthority(t *testing.T) *certificateAuthority {
	t.Helper()
	ca, err := mintAuthority(mintTime)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func TestCertificateAuthorityLives(t *testing.T) {
	ca := testAuthority(t)

	if got := ca.certificate.NotAfter.Sub(ca.certificate.NotBefore); got != authorityLife {
		t.Errorf("the authority lives %v, want %v", got, authorityLife)
	}
	if !ca.certificate.IsCA {
		t.Error("the authority is not a CA")
	}
	if !ca.certificate.BasicConstraintsValid {
		t.Error("the authority carries no basic constraints")
	}
	if got := ca.certificate.KeyUsage; got != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Errorf("key usage is %v, want CertSign|CRLSign", got)
	}
	if parseCertificate(t, ca.certPEM).SerialNumber.Sign() <= 0 {
		t.Error("the serial number is not a positive random number")
	}
}

func TestCertificateLeafLives(t *testing.T) {
	ca := testAuthority(t)

	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	leaf := parseCertificate(t, certPEM)

	if got := leaf.NotAfter.Sub(leaf.NotBefore); got != leafLife {
		t.Errorf("the leaf lives %v, want %v", got, leafLife)
	}
	if leaf.IsCA {
		t.Error("the leaf is a CA")
	}
	if !reflect.DeepEqual(leaf.DNSNames, serviceNames(testNamespace)) {
		t.Errorf("the leaf's SANs are %v, want %v", leaf.DNSNames, serviceNames(testNamespace))
	}
	if !reflect.DeepEqual(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Errorf("extended key usage is %v, want ServerAuth", leaf.ExtKeyUsage)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Errorf("the leaf is not a serving certificate: %v", err)
	}
}

func TestCertificateVerifiesAgainstTheAuthority(t *testing.T) {
	ca := testAuthority(t)
	certPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.certPEM) {
		t.Fatal("the authority's PEM holds no certificate")
	}
	leaf := parseCertificate(t, certPEM)

	cases := []struct {
		name    string
		dnsName string
		trusted bool
	}{
		{"the Service's short name", "display-api", true},
		{"the Service's cluster name", "display-api.liken-system.svc.cluster.local", true},
		{"a port forward", "localhost", true},
		{"the sidecar's name", "display-capture", false},
		{"another cluster's Service", "display-api.other.svc", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := leaf.Verify(x509.VerifyOptions{
				Roots:       roots,
				DNSName:     test.dnsName,
				CurrentTime: mintTime.Add(time.Hour),
				KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			})
			if (err == nil) != test.trusted {
				t.Errorf("verifying %q gave %v, want trusted=%v", test.dnsName, err, test.trusted)
			}
		})
	}
}

func TestCertificateLeavesAreToldApartByTheirSANs(t *testing.T) {
	ca := testAuthority(t)
	publicPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	sidecarPEM, _, err := ca.mintLeaf(sidecarName, []string{sidecarName}, mintTime)
	if err != nil {
		t.Fatal(err)
	}

	public, sidecar := parseCertificate(t, publicPEM), parseCertificate(t, sidecarPEM)
	if !reflect.DeepEqual(sidecar.DNSNames, []string{sidecarName}) {
		t.Errorf("the sidecar's SANs are %v, want [%s]", sidecar.DNSNames, sidecarName)
	}
	if reflect.DeepEqual(public.DNSNames, sidecar.DNSNames) {
		t.Error("the two leaves carry the same SANs")
	}
	if public.SerialNumber.Cmp(sidecar.SerialNumber) == 0 {
		t.Error("the two leaves carry the same serial number")
	}
}

func TestCertificateNeedsRenewal(t *testing.T) {
	ca := testAuthority(t)
	certPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	leaf := parseCertificate(t, certPEM)
	third := leafLife / 3

	cases := []struct {
		name  string
		when  time.Time
		renew bool
	}{
		{"fresh", mintTime, false},
		{"a second over a third left", leaf.NotAfter.Add(-third - time.Second), false},
		{"exactly a third left", leaf.NotAfter.Add(-third), false},
		{"a second under a third left", leaf.NotAfter.Add(-third + time.Second), true},
		{"expired", leaf.NotAfter.Add(time.Hour), true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := needsRenewal(leaf, test.when); got != test.renew {
				t.Errorf("needsRenewal is %v, want %v", got, test.renew)
			}
		})
	}
}

func TestCertificateMaterialMintedWhenTheSecretIsAbsent(t *testing.T) {
	api := newObjectAPI()
	client := objectClient(t, api)

	material, ca, err := ensureServingMaterial(client, testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	if ca == nil {
		t.Fatal("the API minted no authority")
	}
	if _, err := tls.X509KeyPair(material.CertPEM, material.KeyPEM); err != nil {
		t.Errorf("the material is not a serving certificate: %v", err)
	}
	if !material.Expires.Equal(mintTime.Add(leafLife)) {
		t.Errorf("the material expires %v, want %v", material.Expires, mintTime.Add(leafLife))
	}
	if !parseCertificate(t, material.CAPEM).IsCA {
		t.Error("the material's CA PEM is not a CA")
	}

	created, _ := api.counts(secretsPath(testNamespace))
	if created != 1 {
		t.Errorf("created %d Secrets, want 1", created)
	}
	held := &Secret{}
	api.held(t, secretsPath(testNamespace)+"/"+testSecret, held)
	if held.Type != tlsSecretType {
		t.Errorf("the Secret's type is %q, want %q", held.Type, tlsSecretType)
	}
	for _, key := range []string{tlsCertKey, tlsKeyKey, caCertKey, caKeyKey} {
		t.Run(key, func(t *testing.T) {
			if len(held.Data[key]) == 0 {
				t.Errorf("the Secret carries no %s", key)
			}
		})
	}
}

func TestCertificateMaterialReadFromAnExistingSecret(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	api := newObjectAPI()
	api.hold(t, secretsPath(testNamespace)+"/"+testSecret, &Secret{
		Metadata: objectMeta{Name: testSecret, Namespace: testNamespace},
		Type:     tlsSecretType,
		Data: map[string][]byte{
			tlsCertKey: certPEM,
			tlsKeyKey:  keyPEM,
			caCertKey:  ca.certPEM,
			caKeyKey:   ca.keyPEM,
		},
	})

	material, held, err := ensureServingMaterial(objectClient(t, api), testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(material.CertPEM, certPEM) {
		t.Error("the API did not serve the Secret's own certificate")
	}
	if !reflect.DeepEqual(material.KeyPEM, keyPEM) {
		t.Error("the API did not serve the Secret's own key")
	}
	if held == nil || !held.certificate.Equal(ca.certificate) {
		t.Error("the API did not read the Secret's own authority")
	}
	if created, _ := api.counts(secretsPath(testNamespace)); created != 0 {
		t.Errorf("created %d Secrets, want 0", created)
	}
}

func TestCertificateMaterialLeavesAnOwnersSecretAlone(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	api := newObjectAPI()
	api.hold(t, secretsPath(testNamespace)+"/"+testSecret, &Secret{
		Metadata: objectMeta{Name: testSecret, Namespace: testNamespace},
		Type:     tlsSecretType,
		Data:     map[string][]byte{tlsCertKey: certPEM, tlsKeyKey: keyPEM},
	})

	material, held, err := ensureServingMaterial(objectClient(t, api), testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	if held != nil {
		t.Error("the API took an owner's Secret for its own authority")
	}
	if !reflect.DeepEqual(material.CertPEM, certPEM) {
		t.Error("the API did not serve the Secret's own certificate")
	}
	if created, updated := api.counts(secretsPath(testNamespace)); created != 0 || updated != 0 {
		t.Errorf("wrote the Secret %d times and updated it %d times, want 0 and 0", created, updated)
	}
}

func TestCertificateMaterialReadsTheWinnerOfARace(t *testing.T) {
	winner := testAuthority(t)
	certPEM, keyPEM, err := winner.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(&Secret{
		Metadata: objectMeta{Name: testSecret, Namespace: testNamespace},
		Type:     tlsSecretType,
		Data: map[string][]byte{
			tlsCertKey: certPEM,
			tlsKeyKey:  keyPEM,
			caCertKey:  winner.certPEM,
			caKeyKey:   winner.keyPEM,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	api := newObjectAPI()
	api.conflicts[secretsPath(testNamespace)] = raw

	material, held, err := ensureServingMaterial(objectClient(t, api), testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(material.CertPEM, certPEM) {
		t.Error("the loser of the race served its own certificate")
	}
	if held == nil || !held.certificate.Equal(winner.certificate) {
		t.Error("the loser of the race kept its own authority")
	}
	if created, _ := api.counts(secretsPath(testNamespace)); created != 1 {
		t.Errorf("created %d Secrets, want 1", created)
	}
}

func TestCertificateSidecarLeafIsMintedOnceAndRenewed(t *testing.T) {
	ca := testAuthority(t)
	api := newObjectAPI()
	client := objectClient(t, api)
	path := secretsPath(testNamespace) + "/" + testSidecar

	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, mintTime); err != nil {
		t.Fatal(err)
	}
	held := &Secret{}
	api.held(t, path, held)
	leaf := parseCertificate(t, held.Data[tlsCertKey])
	if !reflect.DeepEqual(leaf.DNSNames, []string{sidecarName}) {
		t.Errorf("the sidecar's SANs are %v, want [%s]", leaf.DNSNames, sidecarName)
	}

	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, mintTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, updated := api.counts(path); updated != 0 {
		t.Errorf("updated the Secret %d times, want 0", updated)
	}

	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, leaf.NotAfter.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, updated := api.counts(path); updated != 1 {
		t.Errorf("updated the Secret %d times, want 1", updated)
	}
	renewed := &Secret{}
	api.held(t, path, renewed)
	if reflect.DeepEqual(renewed.Data[tlsCertKey], held.Data[tlsCertKey]) {
		t.Error("the sidecar's leaf was not renewed")
	}
}

func TestCertificateSidecarLeafReplacesOneThatDoesNotFit(t *testing.T) {
	ca := testAuthority(t)
	wrongPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data map[string][]byte
	}{
		{"a certificate that does not read", map[string][]byte{tlsCertKey: []byte("not a certificate")}},
		{"a leaf under another name", map[string][]byte{tlsCertKey: wrongPEM}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := newObjectAPI()
			path := secretsPath(testNamespace) + "/" + testSidecar
			api.hold(t, path, &Secret{
				Metadata: objectMeta{Name: testSidecar, Namespace: testNamespace},
				Type:     tlsSecretType,
				Data:     test.data,
			})

			if err := ensureSidecarLeaf(objectClient(t, api), testNamespace, testSidecar, ca, sidecarName, mintTime); err != nil {
				t.Fatal(err)
			}
			if _, updated := api.counts(path); updated != 1 {
				t.Fatalf("updated the Secret %d times, want 1", updated)
			}
			held := &Secret{}
			api.held(t, path, held)
			if got := parseCertificate(t, held.Data[tlsCertKey]).DNSNames; !reflect.DeepEqual(got, []string{sidecarName}) {
				t.Errorf("the leaf's SANs are %v, want [%s]", got, sidecarName)
			}
		})
	}
}

func TestCertificateMaterialRefusesASecretItCannotRead(t *testing.T) {
	ca := testAuthority(t)
	cases := []struct {
		name string
		data map[string][]byte
	}{
		{"a certificate that does not read", map[string][]byte{tlsCertKey: []byte("not a certificate")}},
		{"an authority key that does not read", map[string][]byte{
			tlsCertKey: mustLeaf(t, ca), caCertKey: ca.certPEM, caKeyKey: []byte("not a key"),
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := newObjectAPI()
			api.hold(t, secretsPath(testNamespace)+"/"+testSecret, &Secret{
				Metadata: objectMeta{Name: testSecret, Namespace: testNamespace},
				Type:     tlsSecretType,
				Data:     test.data,
			})

			_, _, err := ensureServingMaterial(objectClient(t, api), testNamespace, testSecret, serviceNames(testNamespace), mintTime)
			if err == nil {
				t.Error("the API served a Secret it cannot read")
			}
		})
	}
}

func mustLeaf(t *testing.T, ca *certificateAuthority) []byte {
	t.Helper()
	certPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	return certPEM
}

func TestCertificateTrustAnchorIsCreatedThenUpdated(t *testing.T) {
	ca, other := testAuthority(t), testAuthority(t)
	api := newObjectAPI()
	client := objectClient(t, api)
	path := configMapsPath(testNamespace) + "/" + testAnchor

	if err := ensureTrustAnchor(client, testNamespace, testAnchor, ca.certPEM); err != nil {
		t.Fatal(err)
	}
	held := &ConfigMap{}
	api.held(t, path, held)
	if held.Data[caCertKey] != string(ca.certPEM) {
		t.Errorf("the ConfigMap carries %q, want the CA's PEM", held.Data[caCertKey])
	}
	if created, _ := api.counts(configMapsPath(testNamespace)); created != 1 {
		t.Errorf("created %d ConfigMaps, want 1", created)
	}

	if err := ensureTrustAnchor(client, testNamespace, testAnchor, ca.certPEM); err != nil {
		t.Fatal(err)
	}
	if _, updated := api.counts(path); updated != 0 {
		t.Errorf("updated the ConfigMap %d times, want 0", updated)
	}

	if err := ensureTrustAnchor(client, testNamespace, testAnchor, other.certPEM); err != nil {
		t.Fatal(err)
	}
	if _, updated := api.counts(path); updated != 1 {
		t.Errorf("updated the ConfigMap %d times, want 1", updated)
	}
	rotated := &ConfigMap{}
	api.held(t, path, rotated)
	if rotated.Data[caCertKey] != string(other.certPEM) {
		t.Error("the ConfigMap did not take the new CA")
	}
}
