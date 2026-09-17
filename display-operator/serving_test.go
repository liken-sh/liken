package main

import (
	"context"
	"crypto/x509"
	"os"
	"testing"
	"time"
)

// The holder is what a handshake asks, so a certificate that changed
// is served with no restart.
func TestTheHolderServesWhatItWasGiven(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	holder := &certificateHolder{}
	if holder.held() {
		t.Error("an empty holder says it holds a certificate")
	}
	if _, err := holder.get(nil); err == nil {
		t.Error("an empty holder answered a handshake")
	}
	if err := holder.set(certPEM, keyPEM, false); err != nil {
		t.Fatal(err)
	}
	if !holder.held() {
		t.Error("the holder does not report the certificate it was given")
	}
	pair, err := holder.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	if pair.Leaf.Subject.CommonName != "display-api" {
		t.Errorf("the handshake answers %q", pair.Leaf.Subject.CommonName)
	}
	if !holder.expiry().Equal(pair.Leaf.NotAfter) {
		t.Errorf("the holder reports %s, want the leaf's own date", holder.expiry())
	}
}

// A certificate a process minted for itself leaves it not ready,
// because nothing else verifies it.
func TestACertificateAProcessMintedItselfIsNotReady(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf(sidecarName, []string{sidecarName}, mintTime)
	if err != nil {
		t.Fatal(err)
	}
	holder := &certificateHolder{}
	if err := holder.set(certPEM, keyPEM, true); err != nil {
		t.Fatal(err)
	}
	if holder.held() {
		t.Error("a process that minted its own certificate reports itself ready")
	}
	if _, err := holder.get(nil); err != nil {
		t.Errorf("the listener has no certificate to answer with: %v", err)
	}
}

// The sidecar answers on a certificate of its own making until the
// API mints one, and takes the minted one when it lands in the
// directory.
func TestTheSidecarTakesTheLeafWhenItLands(t *testing.T) {
	directory := t.TempDir()
	holder := &certificateHolder{}
	now := time.Now()

	if err := loadCaptureLeaf(directory, holder, now); err != nil {
		t.Fatal(err)
	}
	if holder.held() {
		t.Error("the sidecar reports itself ready with no leaf from the API")
	}
	if _, err := holder.get(nil); err != nil {
		t.Fatalf("the sidecar's listener cannot answer: %v", err)
	}

	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf(sidecarName, []string{sidecarName}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory+"/"+tlsCertFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory+"/"+tlsKeyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadCaptureLeaf(directory, holder, now); err != nil {
		t.Fatal(err)
	}
	if !holder.held() {
		t.Error("the sidecar did not take the leaf the API minted")
	}
	pair, err := holder.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pair.Leaf.DNSNames) != 1 || pair.Leaf.DNSNames[0] != sidecarName {
		t.Errorf("the sidecar serves %v, want the name the API verifies it under", pair.Leaf.DNSNames)
	}
}

// The leaf names the four names the Service answers on and
// localhost, the one a port-forward reaches it by.
func TestTheServiceNames(t *testing.T) {
	names := serviceNames("liken-system")
	want := []string{
		"display-api",
		"display-api.liken-system",
		"display-api.liken-system.svc",
		"display-api.liken-system.svc.cluster.local",
		"localhost",
	}
	if len(names) != len(want) {
		t.Fatalf("the leaf names %v", names)
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("name %d is %q, want %q", i, names[i], name)
		}
	}
}

// The API's first start leaves three objects behind, the two Secrets
// and the ConfigMap, and a client reads the trust anchor from the
// ConfigMap.
func TestTheAPIMintsItsAuthorityAtFirstStart(t *testing.T) {
	api := newObjectAPI()
	client := objectClient(t, api)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	readings := newAPIMetrics(apiComponent, version)
	holder := &certificateHolder{}
	anchor, err := startAuthority(ctx, client, testNamespace, holder, readings)
	if err != nil {
		t.Fatal(err)
	}
	if !holder.held() {
		t.Fatal("the API holds no serving certificate after its first start")
	}

	var sidecar Secret
	api.held(t, secretsPath(testNamespace)+"/"+sidecarTLSSecret, &sidecar)
	leaf := parseCertificate(t, sidecar.Data[tlsCertKey])
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: anchor, DNSName: sidecarName}); err != nil {
		t.Errorf("the sidecar's leaf does not verify under the anchor: %v", err)
	}

	var trust ConfigMap
	api.held(t, configMapsPath(testNamespace)+"/"+trustAnchorMap, &trust)
	if trust.Data[caCertKey] == "" {
		t.Error("the ConfigMap carries no trust anchor")
	}
	if trust.Data[caKeyKey] != "" {
		t.Error("the ConfigMap carries the authority's key, which no client may read")
	}
}

// An owner who brought their own certificate keeps it, and this API
// mints nothing beside it.
func TestTheAPIMintsNothingBesideAnOwnersCertificate(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}
	api := newObjectAPI()
	api.hold(t, secretsPath(testNamespace)+"/"+apiTLSSecret, Secret{
		APIVersion: "v1", Kind: "Secret",
		Metadata: objectMeta{Name: apiTLSSecret, Namespace: testNamespace},
		Type:     tlsSecretType,
		Data: map[string][]byte{
			tlsCertKey: certPEM,
			tlsKeyKey:  keyPEM,
			caCertKey:  ca.certPEM,
		},
	})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	holder := &certificateHolder{}
	if _, err := startAuthority(ctx, objectClient(t, api), testNamespace, holder,
		newAPIMetrics(apiComponent, version)); err != nil {
		t.Fatal(err)
	}
	if !holder.held() {
		t.Error("the API did not serve the certificate the owner supplied")
	}
	if creates, _ := api.counts(secretsPath(testNamespace)); creates != 0 {
		t.Errorf("the API created %d Secrets beside the owner's", creates)
	}
}

// A leaf is re-minted under a third of its life and left alone
// above it, so an ordinary pass writes nothing.
func TestTheLeafIsReMintedUnderAThirdOfItsLife(t *testing.T) {
	api := newObjectAPI()
	client := objectClient(t, api)
	material, ca, err := ensureServingMaterial(client, testNamespace, apiTLSSecret,
		serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatal(err)
	}

	standing, err := renewServingMaterial(client, testNamespace, apiTLSSecret, ca,
		serviceNames(testNamespace), mintTime.Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if standing != nil {
		t.Error("a leaf with most of its life left was re-minted")
	}

	renewed, err := renewServingMaterial(client, testNamespace, apiTLSSecret, ca,
		serviceNames(testNamespace), material.Expires.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if renewed == nil {
		t.Fatal("a leaf near the end of its life was not re-minted")
	}
	if !renewed.Expires.After(material.Expires) {
		t.Errorf("the new leaf expires %s, want later than %s", renewed.Expires, material.Expires)
	}
}
