package main

import (
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

func TestTheHolderServesTheCertificateItWasGiven(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the API leaf: %v", err)
	}
	holder := &certificateHolder{}
	if holder.verifiable() {
		t.Error("an empty holder reports a certificate, want none")
	}
	if _, err := holder.get(nil); err == nil {
		t.Error("an empty holder returned a certificate for a handshake, want an error")
	}
	if err := holder.set(certPEM, keyPEM, false); err != nil {
		t.Fatalf("storing the leaf in the holder: %v", err)
	}
	if !holder.verifiable() {
		t.Error("the holder reports no certificate after set, want one")
	}
	pair, err := holder.get(nil)
	if err != nil {
		t.Fatalf("getting the certificate from the holder: %v", err)
	}
	if pair.Leaf.Subject.CommonName != "display-api" {
		t.Errorf("the handshake certificate is for %q, want display-api", pair.Leaf.Subject.CommonName)
	}
	if !holder.expiry().Equal(pair.Leaf.NotAfter) {
		t.Errorf("the holder expires at %s, want the leaf's NotAfter %s", holder.expiry(), pair.Leaf.NotAfter)
	}
}

func TestASelfSignedCertificateIsServedButNotReady(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf(sidecarName, []string{sidecarName}, mintTime)
	if err != nil {
		t.Fatalf("minting the sidecar leaf: %v", err)
	}
	holder := &certificateHolder{}
	if err := holder.set(certPEM, keyPEM, true); err != nil {
		t.Fatalf("storing the self-signed leaf in the holder: %v", err)
	}
	if holder.verifiable() {
		t.Error("a self-signed certificate counts as held, want not held")
	}
	if _, err := holder.get(nil); err != nil {
		t.Errorf("getting a self-signed certificate for a handshake: %v, want it served", err)
	}
}

func TestTheSidecarLoadsTheLeafOnceItAppears(t *testing.T) {
	directory := t.TempDir()
	holder := &certificateHolder{}
	now := time.Now()

	if err := loadCaptureLeaf(directory, holder, now); err != nil {
		t.Fatalf("loading the capture leaf from an empty directory: %v", err)
	}
	if holder.verifiable() {
		t.Error("the sidecar reports a held certificate with no leaf from the API, want none")
	}
	if _, err := holder.get(nil); err != nil {
		t.Fatalf("getting a certificate for a handshake before the API leaf arrives: %v, want its own certificate", err)
	}

	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf(sidecarName, []string{sidecarName}, now)
	if err != nil {
		t.Fatalf("minting the sidecar leaf: %v", err)
	}
	if err := os.WriteFile(directory+"/"+tlsCertFile, certPEM, 0o600); err != nil {
		t.Fatalf("writing the certificate file: %v", err)
	}
	if err := os.WriteFile(directory+"/"+tlsKeyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("writing the key file: %v", err)
	}
	if err := loadCaptureLeaf(directory, holder, now); err != nil {
		t.Fatalf("loading the capture leaf from the files: %v", err)
	}
	if !holder.verifiable() {
		t.Error("the sidecar does not hold the leaf the API minted, want it held")
	}
	pair, err := holder.get(nil)
	if err != nil {
		t.Fatalf("getting the certificate from the holder: %v", err)
	}
	if len(pair.Leaf.DNSNames) != 1 || pair.Leaf.DNSNames[0] != sidecarName {
		t.Errorf("the sidecar leaf has DNS names %v, want [%s], the name the API verifies", pair.Leaf.DNSNames, sidecarName)
	}
}

// writeSecretVolume writes files the way the kubelet updates a Secret
// volume. It writes them into a new timestamped directory, points a
// ..data_tmp symlink at that directory, and renames the symlink over
// ..data. Each file name is a symlink through ..data.
func writeSecretVolume(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	stamp := fmt.Sprintf("..%d", time.Now().UnixNano())
	if err := os.Mkdir(filepath.Join(dir, stamp), 0o755); err != nil {
		t.Fatalf("creating the volume version directory: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, stamp, name), content, 0o600); err != nil {
			t.Fatalf("writing a volume file: %v", err)
		}
	}
	if err := os.Symlink(stamp, filepath.Join(dir, "..data_tmp")); err != nil {
		t.Fatalf("linking ..data_tmp: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, secretVolumeData)); err != nil {
		t.Fatalf("renaming ..data_tmp over ..data: %v", err)
	}
	for name := range files {
		link := filepath.Join(dir, name)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		if err := os.Symlink(filepath.Join(secretVolumeData, name), link); err != nil {
			t.Fatalf("linking a volume file: %v", err)
		}
	}
}

// The fallback interval is longer than the test's deadline, so only
// the rename of ..data can wake the sidecar in time.
func TestTheSidecarLoadsTheLeafWhenTheVolumeIsRenamed(t *testing.T) {
	directory := t.TempDir()
	holder := &certificateHolder{}
	go watchCaptureLeaf(t.Context(), directory, holder, nil, time.Hour)
	time.Sleep(50 * time.Millisecond)
	if holder.verifiable() {
		t.Fatal("the sidecar reports a held certificate with no leaf from the API, want none")
	}

	certPEM, keyPEM, err := testAuthority(t).mintLeaf(sidecarName, []string{sidecarName}, time.Now())
	if err != nil {
		t.Fatalf("minting the sidecar leaf: %v", err)
	}
	writeSecretVolume(t, directory, map[string][]byte{tlsCertFile: certPEM, tlsKeyFile: keyPEM})

	deadline := time.Now().Add(5 * time.Second)
	for !holder.verifiable() {
		if time.Now().After(deadline) {
			t.Fatal("the sidecar did not load the leaf within 5 seconds of the ..data rename")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTheLeafCoversTheServiceNamesAndLocalhost(t *testing.T) {
	names := serviceNames("liken-system")
	want := []string{
		"display-api",
		"display-api.liken-system",
		"display-api.liken-system.svc",
		"display-api.liken-system.svc.cluster.local",
		"localhost",
	}
	if len(names) != len(want) {
		t.Fatalf("serviceNames returned %d names %v, want %d", len(names), names, len(want))
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("name %d is %q, want %q", i, names[i], name)
		}
	}
}

func TestTheFirstStartMintsTheAuthorityAndPublishesItsCertificate(t *testing.T) {
	api := newObjectAPI()
	client := objectClient(t, api)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	metrics := newAPIMetrics(apiComponent, version)
	holder := &certificateHolder{}
	anchor, err := startAuthority(ctx, client, idleWatcher(t), testNamespace, holder, metrics)
	if err != nil {
		t.Fatalf("starting the authority: %v", err)
	}
	if !holder.verifiable() {
		t.Fatal("the holder has no certificate after the first start, want one")
	}

	var sidecar Secret
	api.load(t, secretsPath(testNamespace)+"/"+sidecarTLSSecret, &sidecar)
	leaf := parseCertificate(t, sidecar.Data[tlsCertKey])
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: anchor, DNSName: sidecarName}); err != nil {
		t.Errorf("verifying the sidecar leaf under the trust anchor: %v, want it to verify", err)
	}

	var trust ConfigMap
	api.load(t, configMapsPath(testNamespace)+"/"+trustAnchorMap, &trust)
	if trust.Data[caCertKey] == "" {
		t.Error("the ConfigMap has no trust anchor, want one")
	}
	if trust.Data[caKeyKey] != "" {
		t.Error("the ConfigMap has the authority's key, want none because clients can read the ConfigMap")
	}
}

func TestTheAPIServesAnOwnersCertificateAndCreatesNoSecrets(t *testing.T) {
	ca := testAuthority(t)
	certPEM, keyPEM, err := ca.mintLeaf("display-api", serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the API leaf: %v", err)
	}
	api := newObjectAPI()
	api.store(t, secretsPath(testNamespace)+"/"+apiTLSSecret, Secret{
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
	if _, err := startAuthority(ctx, objectClient(t, api), idleWatcher(t), testNamespace, holder,
		newAPIMetrics(apiComponent, version)); err != nil {
		t.Fatalf("starting the authority: %v", err)
	}
	if !holder.verifiable() {
		t.Error("the holder has no certificate, want the one the owner supplied")
	}
	if creates, _ := api.counts(secretsPath(testNamespace)); creates != 0 {
		t.Errorf("the API created %d Secrets, want none beside the owner's", creates)
	}
}

func TestTheLeafIsMintedAgainWhenAThirdOfItsLifeRemains(t *testing.T) {
	api := newObjectAPI()
	client := objectClient(t, api)
	material, ca, err := ensureServingMaterial(client, testNamespace, apiTLSSecret,
		serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the serving material: %v", err)
	}

	unchanged, err := renewServingMaterial(client, testNamespace, apiTLSSecret, ca,
		serviceNames(testNamespace), mintTime.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("renewing the serving material after 30 days: %v", err)
	}
	if unchanged != nil {
		t.Error("a leaf with most of its life left was minted again, want it kept")
	}

	renewed, err := renewServingMaterial(client, testNamespace, apiTLSSecret, ca,
		serviceNames(testNamespace), material.Expires.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("renewing the serving material near expiry: %v", err)
	}
	if renewed == nil {
		t.Fatal("a leaf near the end of its life was kept, want it minted again")
	}
	if !renewed.Expires.After(material.Expires) {
		t.Errorf("the new leaf expires %s, want after %s", renewed.Expires, material.Expires)
	}
}

func TestTheSidecarSecretIsCreatedAgainAfterItIsDeleted(t *testing.T) {
	api := newObjectAPI()
	client := objectClient(t, api)
	_, ca, err := ensureServingMaterial(client, testNamespace, testSecret, serviceNames(testNamespace), mintTime)
	if err != nil {
		t.Fatalf("minting the serving material: %v", err)
	}
	if err := ensureSidecarLeaf(client, testNamespace, sidecarTLSSecret, ca, sidecarName, mintTime); err != nil {
		t.Fatalf("creating the sidecar leaf: %v", err)
	}
	created, _ := api.counts(secretsPath(testNamespace))

	if err := ensureSidecarLeaf(client, testNamespace, sidecarTLSSecret, ca, sidecarName, mintTime.Add(time.Minute)); err != nil {
		t.Fatalf("creating the sidecar leaf again: %v", err)
	}
	if again, _ := api.counts(secretsPath(testNamespace)); again != created {
		t.Errorf("a Secret with a good leaf was created again: %d creates, want %d", again, created)
	}

	api.remove(secretsPath(testNamespace) + "/" + sidecarTLSSecret)
	if err := ensureSidecarLeaf(client, testNamespace, sidecarTLSSecret, ca, sidecarName, mintTime.Add(2*time.Minute)); err != nil {
		t.Fatalf("creating the sidecar leaf after the Secret was deleted: %v", err)
	}

	var minted Secret
	api.load(t, secretsPath(testNamespace)+"/"+sidecarTLSSecret, &minted)
	leaf := parseCertificate(t, minted.Data[tlsCertKey])
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != sidecarName {
		t.Errorf("the leaf has DNS names %v, want [%s]", leaf.DNSNames, sidecarName)
	}
}

// ownersSecret returns a Secret with a leaf and the CA certificate and
// no CA key, as cert-manager writes it.
func ownersSecret(t *testing.T, ca *certificateAuthority) Secret {
	t.Helper()
	certPEM, keyPEM, err := ca.mintLeaf(apiAudience, serviceNames(testNamespace), time.Now())
	if err != nil {
		t.Fatalf("minting the owner's leaf: %v", err)
	}
	return Secret{
		Metadata: objectMeta{Name: apiTLSSecret, Namespace: testNamespace},
		Type:     tlsSecretType,
		Data:     map[string][]byte{tlsCertKey: certPEM, tlsKeyKey: keyPEM, caCertKey: ca.certPEM},
	}
}

// servedSerial returns "" when the holder serves no leaf.
func servedSerial(t *testing.T, holder *certificateHolder) string {
	t.Helper()
	pair, err := holder.get(nil)
	if err != nil {
		return ""
	}
	return pair.Leaf.SerialNumber.String()
}

func serialOf(t *testing.T, secret Secret) string {
	t.Helper()
	leaf, err := leafOf(&secret)
	if err != nil {
		t.Fatalf("reading the leaf of the Secret: %v", err)
	}
	return leaf.SerialNumber.String()
}

func TestTheAPIServesALeafTheOwnerRotated(t *testing.T) {
	ca := testAuthority(t)
	api := newSecretStore(t, testNamespace)
	first, second := ownersSecret(t, ca), ownersSecret(t, ca)
	api.put(first)
	holder := &certificateHolder{}

	go keepCertificates(t.Context(), api.client(), api.watcher(), testNamespace, nil, holder, nil)
	eventually(t, "the first leaf", func() bool { return servedSerial(t, holder) == serialOf(t, first) })
	api.put(second)

	eventually(t, "the rotated leaf", func() bool { return servedSerial(t, holder) == serialOf(t, second) })
}

// keepSidecarLeaf has no timer, so only the watch's report of the
// deletion can trigger the second mint.
func TestTheAPICreatesTheSidecarSecretAgainWhenItIsDeleted(t *testing.T) {
	ca := testAuthority(t)
	api := newSecretStore(t, testNamespace)
	go keepSidecarLeaf(t.Context(), api.client(), api.watcher(), testNamespace, ca)
	minted := func() bool {
		held, err := apiclient.Get[Secret](api.client(), secretsPath(testNamespace)+"/"+sidecarTLSSecret)
		return err == nil && sidecarLeafUsable(held, sidecarName, time.Now())
	}
	eventually(t, "the first mint", minted)

	api.remove(sidecarTLSSecret)

	eventually(t, "the second mint", minted)
}
