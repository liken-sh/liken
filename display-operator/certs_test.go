package main

import (
	"crypto/rand"
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

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// objectAPI is a fake API server that stores Secrets and ConfigMaps in
// memory and responds to get, create, and update like the real one,
// including 404 and 409. An entry in conflicts makes the next create at
// that path fail with 409 and stores the entry as the object that won
// the race.
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

func (a *objectAPI) store(t *testing.T, path string, object any) {
	t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encoding the object as JSON: %v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.objects[path] = raw
}

func (a *objectAPI) load(t *testing.T, path string, out any) {
	t.Helper()
	a.mu.Lock()
	raw, ok := a.objects[path]
	a.mu.Unlock()
	if !ok {
		t.Fatalf("the fake API server has no object at %s", path)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decoding the stored object: %v", err)
	}
}

// remove deletes an object, as kubectl delete does.
func (a *objectAPI) remove(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.objects, path)
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

func objectClient(t *testing.T, api *objectAPI) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return apiclient.New(server.URL, server.Client(), "")
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
		t.Fatalf("parsing the certificate: %v", err)
	}
	return certificate
}

func testAuthority(t *testing.T) *certificateAuthority {
	t.Helper()
	ca, err := mintAuthority(mintTime)
	if err != nil {
		t.Fatalf("minting the authority: %v", err)
	}
	return ca
}

func TestMintAuthorityCreatesACA(t *testing.T) {
	ca := testAuthority(t)

	if got := ca.certificate.NotAfter.Sub(ca.certificate.NotBefore); got != authorityLife {
		t.Errorf("the authority is valid for %v, want %v", got, authorityLife)
	}
	if !ca.certificate.IsCA {
		t.Error("the authority is not a CA")
	}
	if !ca.certificate.BasicConstraintsValid {
		t.Error("the authority has no valid basic constraints")
	}
	if got := ca.certificate.KeyUsage; got != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Errorf("key usage is %v, want CertSign|CRLSign", got)
	}
	if parseCertificate(t, ca.certPEM).SerialNumber.Sign() <= 0 {
		t.Error("the serial number is not a positive random number")
	}
}

func TestMintLeafCreatesAServingCertificate(t *testing.T) {
	ca := testAuthority(t)

	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	leaf := parseCertificate(t, certPEM)

	if got := leaf.NotAfter.Sub(leaf.NotBefore); got != leafLife {
		t.Errorf("the leaf is valid for %v, want %v", got, leafLife)
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

func TestLeafVerifiesOnlyForTheNamesOfTheService(t *testing.T) {
	ca := testAuthority(t)
	certPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.certPEM) {
		t.Fatal("the authority's PEM contains no certificate")
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

func TestAPILeafAndSidecarLeafHaveDifferentSANs(t *testing.T) {
	ca := testAuthority(t)
	publicPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	sidecarPEM, _, err := ca.mintLeaf(sidecarName, []string{sidecarName}, mintTime)
	if err != nil {
		t.Fatalf("minting the sidecar leaf: %v", err)
	}

	public, sidecar := parseCertificate(t, publicPEM), parseCertificate(t, sidecarPEM)
	if !reflect.DeepEqual(sidecar.DNSNames, []string{sidecarName}) {
		t.Errorf("the sidecar's SANs are %v, want [%s]", sidecar.DNSNames, sidecarName)
	}
	if reflect.DeepEqual(public.DNSNames, sidecar.DNSNames) {
		t.Error("the two leaves have the same SANs")
	}
	if public.SerialNumber.Cmp(sidecar.SerialNumber) == 0 {
		t.Error("the two leaves have the same serial number")
	}
}

func TestNeedsRenewalInTheLastThirdOfALeafsLife(t *testing.T) {
	ca := testAuthority(t)
	certPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
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
				t.Errorf("needsRenewal at %v is %v, want %v", test.when, got, test.renew)
			}
		})
	}
}

func TestEnsureServingMaterialCreatesTheSecretWhenItIsMissing(t *testing.T) {
	api := newObjectAPI()
	client := objectClient(t, api)

	material, ca, err := ensureServingMaterial(client, testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("ensuring the serving material: %v", err)
	}
	if ca == nil {
		t.Fatal("ensureServingMaterial returned no authority for a missing Secret, want a new one")
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
	api.load(t, secretsPath(testNamespace)+"/"+testSecret, held)
	if held.Type != tlsSecretType {
		t.Errorf("the Secret's type is %q, want %q", held.Type, tlsSecretType)
	}
	for _, key := range []string{tlsCertKey, tlsKeyKey, caCertKey, caKeyKey} {
		t.Run(key, func(t *testing.T) {
			if len(held.Data[key]) == 0 {
				t.Errorf("the Secret has no %s", key)
			}
		})
	}
}

func TestEnsureServingMaterialReadsAnExistingSecret(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	api := newObjectAPI()
	api.store(t, secretsPath(testNamespace)+"/"+testSecret, &Secret{
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
		t.Fatalf("ensuring the serving material: %v", err)
	}
	if !reflect.DeepEqual(material.CertPEM, certPEM) {
		t.Error("the material's certificate is not the one in the existing Secret")
	}
	if !reflect.DeepEqual(material.KeyPEM, keyPEM) {
		t.Error("the material's key is not the one in the existing Secret")
	}
	if held == nil || !held.certificate.Equal(ca.certificate) {
		t.Error("the returned authority is not the one in the existing Secret")
	}
	if created, _ := api.counts(secretsPath(testNamespace)); created != 0 {
		t.Errorf("created %d Secrets, want 0", created)
	}
}

func TestEnsureServingMaterialDoesNotWriteAnOwnersSecret(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	api := newObjectAPI()
	api.store(t, secretsPath(testNamespace)+"/"+testSecret, &Secret{
		Metadata: objectMeta{Name: testSecret, Namespace: testNamespace},
		Type:     tlsSecretType,
		Data:     map[string][]byte{tlsCertKey: certPEM, tlsKeyKey: keyPEM},
	})

	material, held, err := ensureServingMaterial(objectClient(t, api), testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("ensuring the serving material: %v", err)
	}
	if held != nil {
		t.Error("an authority was returned for a Secret that has no authority, want nil")
	}
	if !reflect.DeepEqual(material.CertPEM, certPEM) {
		t.Error("the material's certificate is not the one in the existing Secret")
	}
	if created, updated := api.counts(secretsPath(testNamespace)); created != 0 || updated != 0 {
		t.Errorf("created the Secret %d times and updated it %d times, want 0 and 0", created, updated)
	}
}

func TestEnsureServingMaterialUsesTheSecretThatWonARace(t *testing.T) {
	winner := testAuthority(t)
	certPEM, keyPEM, err := winner.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
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
		t.Fatalf("encoding the Secret as JSON: %v", err)
	}
	api := newObjectAPI()
	api.conflicts[secretsPath(testNamespace)] = raw

	material, held, err := ensureServingMaterial(objectClient(t, api), testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("ensuring the serving material: %v", err)
	}
	if !reflect.DeepEqual(material.CertPEM, certPEM) {
		t.Error("the material's certificate is not the one from the winning Secret")
	}
	if held == nil || !held.certificate.Equal(winner.certificate) {
		t.Error("the returned authority is not the one from the winning Secret")
	}
	if created, _ := api.counts(secretsPath(testNamespace)); created != 1 {
		t.Errorf("created %d Secrets, want 1", created)
	}
}

func TestEnsureSidecarLeafCreatesThenRenewsNearExpiry(t *testing.T) {
	ca := testAuthority(t)
	api := newObjectAPI()
	client := objectClient(t, api)
	path := secretsPath(testNamespace) + "/" + testSidecar

	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, mintTime); err != nil {
		t.Fatalf("ensuring the sidecar leaf: %v", err)
	}
	held := &Secret{}
	api.load(t, path, held)
	leaf := parseCertificate(t, held.Data[tlsCertKey])
	if !reflect.DeepEqual(leaf.DNSNames, []string{sidecarName}) {
		t.Errorf("the sidecar's SANs are %v, want [%s]", leaf.DNSNames, sidecarName)
	}

	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, mintTime.Add(time.Hour)); err != nil {
		t.Fatalf("ensuring the sidecar leaf: %v", err)
	}
	if _, updated := api.counts(path); updated != 0 {
		t.Errorf("updated the Secret %d times, want 0", updated)
	}

	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, leaf.NotAfter.Add(-time.Hour)); err != nil {
		t.Fatalf("ensuring the sidecar leaf: %v", err)
	}
	if _, updated := api.counts(path); updated != 1 {
		t.Errorf("updated the Secret %d times, want 1", updated)
	}
	renewed := &Secret{}
	api.load(t, path, renewed)
	if reflect.DeepEqual(renewed.Data[tlsCertKey], held.Data[tlsCertKey]) {
		t.Error("the sidecar's certificate did not change after renewal")
	}
}

func TestEnsureSidecarLeafReplacesAnUnusableLeaf(t *testing.T) {
	ca := testAuthority(t)
	wrongPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	// A CA that the API minted before its own Secret was deleted.
	replacedPEM, _, err := testAuthority(t).mintLeaf(sidecarName, []string{sidecarName}, mintTime)
	if err != nil {
		t.Fatalf("minting the leaf of the replaced CA: %v", err)
	}
	cases := []struct {
		name string
		data map[string][]byte
	}{
		{"a certificate that does not read", map[string][]byte{tlsCertKey: []byte("not a certificate")}},
		{"a leaf under another name", map[string][]byte{tlsCertKey: wrongPEM}},
		{"a leaf that another CA signed", map[string][]byte{tlsCertKey: replacedPEM}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := newObjectAPI()
			path := secretsPath(testNamespace) + "/" + testSidecar
			api.store(t, path, &Secret{
				Metadata: objectMeta{Name: testSidecar, Namespace: testNamespace},
				Type:     tlsSecretType,
				Data:     test.data,
			})

			if err := ensureSidecarLeaf(objectClient(t, api), testNamespace, testSidecar, ca, sidecarName, mintTime); err != nil {
				t.Fatalf("ensuring the sidecar leaf: %v", err)
			}
			if _, updated := api.counts(path); updated != 1 {
				t.Fatalf("updated the Secret %d times, want 1", updated)
			}
			held := &Secret{}
			api.load(t, path, held)
			if got := parseCertificate(t, held.Data[tlsCertKey]).DNSNames; !reflect.DeepEqual(got, []string{sidecarName}) {
				t.Errorf("the leaf's SANs are %v, want [%s]", got, sidecarName)
			}
		})
	}
}

// authorityWithoutCAConstraints is a CA that an owner wrote into
// display-api-tls with a ca.key, from a certificate that is not marked
// as a CA and has no certSign key usage.
func authorityWithoutCAConstraints(t *testing.T) *certificateAuthority {
	t.Helper()
	ca := testAuthority(t)
	template := &x509.Certificate{
		SerialNumber: ca.certificate.SerialNumber,
		Subject:      ca.certificate.Subject,
		NotBefore:    mintTime,
		NotAfter:     mintTime.Add(authorityLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, ca.key.Public(), ca.key)
	if err != nil {
		t.Fatalf("signing the authority certificate without CA constraints: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	held, err := readAuthority(certPEM, ca.keyPEM)
	if err != nil {
		t.Fatalf("reading the authority without CA constraints: %v", err)
	}
	return held
}

// A leaf that the CA signed stays, so the watch of keepSidecarLeaf does
// not write the Secret again on each event of its own write.
func TestEnsureSidecarLeafKeepsALeafOfACAWithoutCAConstraints(t *testing.T) {
	ca := authorityWithoutCAConstraints(t)
	api := newObjectAPI()
	client := objectClient(t, api)
	path := secretsPath(testNamespace) + "/" + testSidecar
	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, mintTime); err != nil {
		t.Fatalf("ensuring the sidecar leaf: %v", err)
	}

	if err := ensureSidecarLeaf(client, testNamespace, testSidecar, ca, sidecarName, mintTime.Add(time.Minute)); err != nil {
		t.Fatalf("ensuring the sidecar leaf again: %v", err)
	}
	if _, updated := api.counts(path); updated != 0 {
		t.Errorf("updated the Secret %d times a minute after the first leaf, want 0", updated)
	}
}

func TestEnsureServingMaterialFailsOnAnUnreadableSecret(t *testing.T) {
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
			api.store(t, secretsPath(testNamespace)+"/"+testSecret, &Secret{
				Metadata: objectMeta{Name: testSecret, Namespace: testNamespace},
				Type:     tlsSecretType,
				Data:     test.data,
			})

			_, _, err := ensureServingMaterial(objectClient(t, api), testNamespace, testSecret, serviceNames(testNamespace), mintTime)
			if err == nil {
				t.Error("ensureServingMaterial returned no error for a Secret that cannot be parsed")
			}
		})
	}
}

func mustLeaf(t *testing.T, ca *certificateAuthority) []byte {
	t.Helper()
	certPEM, _, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the leaf: %v", err)
	}
	return certPEM
}

func TestEnsureTrustAnchorCreatesThenUpdates(t *testing.T) {
	ca, other := testAuthority(t), testAuthority(t)
	api := newObjectAPI()
	client := objectClient(t, api)
	path := configMapsPath(testNamespace) + "/" + testAnchor

	if err := ensureTrustAnchor(client, testNamespace, testAnchor, ca.certPEM); err != nil {
		t.Fatalf("ensuring the trust anchor: %v", err)
	}
	held := &ConfigMap{}
	api.load(t, path, held)
	if held.Data[caCertKey] != string(ca.certPEM) {
		t.Errorf("the ConfigMap contains %q, want the CA's PEM", held.Data[caCertKey])
	}
	if created, _ := api.counts(configMapsPath(testNamespace)); created != 1 {
		t.Errorf("created %d ConfigMaps, want 1", created)
	}

	if err := ensureTrustAnchor(client, testNamespace, testAnchor, ca.certPEM); err != nil {
		t.Fatalf("ensuring the trust anchor: %v", err)
	}
	if _, updated := api.counts(path); updated != 0 {
		t.Errorf("updated the ConfigMap %d times, want 0", updated)
	}

	if err := ensureTrustAnchor(client, testNamespace, testAnchor, other.certPEM); err != nil {
		t.Fatalf("ensuring the trust anchor: %v", err)
	}
	if _, updated := api.counts(path); updated != 1 {
		t.Errorf("updated the ConfigMap %d times, want 1", updated)
	}
	rotated := &ConfigMap{}
	api.load(t, path, rotated)
	if rotated.Data[caCertKey] != string(other.certPEM) {
		t.Error("the ConfigMap did not take the new CA")
	}
}
