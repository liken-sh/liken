package main

// These tests cover approve-reboot: chooseApproval picks which
// staged change to grant, and renderPending reports what a machine
// waits for. disruptions_test.go drives the whole command against a
// fake API server.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func TestChooseApprovalPrefersTheReboot(t *testing.T) {
	pending := []machine.PendingDisruption{
		{Condition: "CredentialsConverged", Kind: machine.DisruptionRestart, Hash: "aaaa"},
		{Condition: "VersionConverged", Kind: machine.DisruptionReboot, Hash: "bbbb"},
	}
	got := chooseApproval(pending)
	if got == nil || got.Hash != "bbbb" {
		t.Fatalf("a reboot applies every staged document, so it wins: %+v", got)
	}
}

func TestChooseApprovalTakesTheOnlyEntry(t *testing.T) {
	pending := []machine.PendingDisruption{
		{Condition: "CredentialsConverged", Kind: machine.DisruptionRestart, Hash: "aaaa"},
	}
	got := chooseApproval(pending)
	if got == nil || got.Hash != "aaaa" {
		t.Fatalf("got %+v", got)
	}
}

func TestChooseApprovalReturnsNilWhenNothingIsPending(t *testing.T) {
	if got := chooseApproval(nil); got != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestRenderPendingShowsEachChangeWithItsReason(t *testing.T) {
	m := &machine.Machine{
		Metadata: api.ObjectMeta{Name: "node-5"},
		Status: machine.MachineStatus{
			Conditions: []api.Condition{
				{Type: "CredentialsConverged", Status: api.ConditionFalse, Reason: "RestartPending"},
			},
			Pending: []machine.PendingDisruption{
				{Condition: "CredentialsConverged", Kind: machine.DisruptionRestart,
					Hash:    "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123",
					Summary: "registry credentials for 2 hosts"},
			},
		},
	}
	got := renderPending(m)
	for _, want := range []string{
		"node-5 is waiting on one change:",
		"CredentialsConverged  RestartPending",
		"registry credentials for 2 hosts (3943abfa6adf)",
		"a k3s restart applies this; the machine does not reboot",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderPendingOmitsTheReasonWhenTheConditionIsMissing(t *testing.T) {
	m := &machine.Machine{
		Metadata: api.ObjectMeta{Name: "node-5"},
		Status: machine.MachineStatus{
			Pending: []machine.PendingDisruption{
				{Condition: "CredentialsConverged", Kind: machine.DisruptionRestart,
					Hash:    "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123",
					Summary: "registry credentials for 2 hosts"},
			},
		},
	}
	got := renderPending(m)
	if strings.Contains(got, "CredentialsConverged  \n") || strings.Contains(got, "CredentialsConverged \n") {
		t.Fatalf("a missing reason left trailing spaces on the line:\n%s", got)
	}
	if !strings.Contains(got, "CredentialsConverged\n") {
		t.Fatalf("missing a clean condition line in:\n%s", got)
	}
}

func TestRenderPendingSaysARebootAppliesEveryStagedChange(t *testing.T) {
	m := &machine.Machine{
		Metadata: api.ObjectMeta{Name: "node-5"},
		Status: machine.MachineStatus{
			Conditions: []api.Condition{
				{Type: "VersionConverged", Status: api.ConditionFalse, Reason: "RebootPending"},
			},
			Pending: []machine.PendingDisruption{
				{Condition: "VersionConverged", Kind: machine.DisruptionReboot,
					Hash:    "aaaabbbbcccc0123456789abcdef0123456789abcdef0123456789abcdef0123",
					Summary: "release 2026.07.31-001 on slot systemB"},
			},
		},
	}
	got := renderPending(m)
	if !strings.Contains(got, "a reboot applies this, and every other staged change with it") {
		t.Fatalf("missing the reboot-kind wording in:\n%s", got)
	}
}

func TestRenderPendingSaysConverged(t *testing.T) {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-5"}}
	if got := renderPending(m); !strings.Contains(got, "node-5 is converged; nothing is waiting") {
		t.Fatalf("got %q", got)
	}
}

// issueServerCertForTest signs a server certificate for 127.0.0.1
// with the deployment's own server-ca, and returns a pool holding
// its client-ca. identity.Kubeconfig trusts the server-ca and signs
// the admin client certificate with the client-ca, so an httptest
// server presenting this certificate and requiring a client
// certificate against this pool authenticates the same way a real
// API server would.
func issueServerCertForTest(t *testing.T, dir string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	tlsDir := filepath.Join(dir, "identity", "tls")
	caCert, caKey := readTestCA(t, tlsDir, "server-ca")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}

	clientCA, err := os.ReadFile(filepath.Join(tlsDir, "client-ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(clientCA)
	return cert, pool
}

// readTestCA loads one authority's certificate and private key from
// an identity's tls tree, the same files identity.Kubeconfig reads
// through its own unexported readCA.
func readTestCA(t *testing.T, tlsDir, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	certPEM, err := os.ReadFile(filepath.Join(tlsDir, name+".crt"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(tlsDir, name+".key"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ = pem.Decode(keyPEM)
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}
