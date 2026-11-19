package identity

// This file tests the computed kubeconfig. It must be a
// self-contained credential that kubectl can use against the
// cluster. This means the embedded admin certificate must chain to
// the client CA, carry the clientAuth usage that the API server
// requires, and claim the identity (CN=admin, O=system:masters) that
// RBAC binds to cluster-admin.

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

// kubeconfigDocument is enough of kubectl's config schema to check
// what this code writes into it.
type kubeconfigDocument struct {
	Clusters []struct {
		Name    string `json:"name"`
		Cluster struct {
			Server                   string `json:"server"`
			CertificateAuthorityData []byte `json:"certificate-authority-data"`
		} `json:"cluster"`
	} `json:"clusters"`
	Contexts []struct {
		Name    string `json:"name"`
		Context struct {
			Cluster string `json:"cluster"`
			User    string `json:"user"`
		} `json:"context"`
	} `json:"contexts"`
	CurrentContext string `json:"current-context"`
	Users          []struct {
		User struct {
			ClientCertificateData []byte `json:"client-certificate-data"`
			ClientKeyData         []byte `json:"client-key-data"`
		} `json:"user"`
	} `json:"users"`
}

// computedKubeconfig mints an identity, computes its kubeconfig for
// a cluster named mycluster against the forwarded dev-lab address,
// and parses the result.
func computedKubeconfig(t *testing.T) (string, kubeconfigDocument) {
	t.Helper()
	dir := mintedIdentity(t)
	if err := Kubeconfig(dir, "mycluster", "https://127.0.0.1:16443", io.Discard); err != nil {
		t.Fatalf("computing the kubeconfig: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "kubeconfig"))
	if err != nil {
		t.Fatalf("reading the kubeconfig: %v", err)
	}
	var doc kubeconfigDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing the kubeconfig: %v", err)
	}
	if len(doc.Clusters) != 1 || len(doc.Users) != 1 {
		t.Fatalf("the kubeconfig has %d clusters and %d users, want 1 of each", len(doc.Clusters), len(doc.Users))
	}
	return dir, doc
}

func TestKubeconfigNamesTheClusterAndContext(t *testing.T) {
	_, doc := computedKubeconfig(t)
	if got := doc.Clusters[0].Name; got != "mycluster" {
		t.Errorf("the cluster name is %q, want mycluster", got)
	}
	if len(doc.Contexts) != 1 {
		t.Fatalf("the kubeconfig has %d contexts, want 1", len(doc.Contexts))
	}
	if got := doc.Contexts[0].Name; got != "mycluster" {
		t.Errorf("the context name is %q, want mycluster", got)
	}
	if got := doc.Contexts[0].Context.Cluster; got != "mycluster" {
		t.Errorf("the context cluster is %q, want mycluster", got)
	}
	if got := doc.Contexts[0].Context.User; got != "admin" {
		t.Errorf("the context user is %q, want admin", got)
	}
	if got := doc.CurrentContext; got != "mycluster" {
		t.Errorf("current-context is %q, want mycluster", got)
	}
}

func TestKubeconfigPointsAtTheForwardedPort(t *testing.T) {
	dir, doc := computedKubeconfig(t)
	if got := doc.Clusters[0].Cluster.Server; got != "https://127.0.0.1:16443" {
		t.Errorf("the server is %q, want https://127.0.0.1:16443", got)
	}
	serverCA, err := os.ReadFile(filepath.Join(dir, "tls", "server-ca.crt"))
	if err != nil {
		t.Fatalf("reading tls/server-ca.crt: %v", err)
	}
	if !bytes.Equal(doc.Clusters[0].Cluster.CertificateAuthorityData, serverCA) {
		t.Error("the embedded CA differs from tls/server-ca.crt, want the same bytes")
	}
}

func TestAdminCertificateChainsToTheClientCA(t *testing.T) {
	dir, doc := computedKubeconfig(t)

	block, _ := pem.Decode(doc.Users[0].User.ClientCertificateData)
	if block == nil {
		t.Fatal("client-certificate-data holds no PEM block")
	}
	admin, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing the admin certificate: %v", err)
	}

	if admin.Subject.CommonName != "admin" {
		t.Errorf("the certificate CN is %q, want admin", admin.Subject.CommonName)
	}
	if len(admin.Subject.Organization) != 1 || admin.Subject.Organization[0] != "system:masters" {
		t.Errorf("the certificate O is %v, want [system:masters]", admin.Subject.Organization)
	}

	roots := x509.NewCertPool()
	roots.AddCert(parseCertificate(t, dir, "client-ca.crt"))
	if _, err := admin.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("verifying the admin certificate as a client certificate against client-ca.crt: %v", err)
	}
}

// A command sends its first request in the same second that it mints the
// credential. An API server whose clock is behind the workstation's refuses
// a certificate that is not valid yet, so the certificate is valid from a
// few minutes before it was minted.
func TestAdminCertificateIsValidOnAnAPIServerWhoseClockIsBehind(t *testing.T) {
	_, doc := computedKubeconfig(t)
	block, _ := pem.Decode(doc.Users[0].User.ClientCertificateData)
	admin, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing the admin certificate: %v", err)
	}

	behind := time.Now().Add(-time.Minute)

	if admin.NotBefore.After(behind) {
		t.Errorf("the certificate is valid from %s, want a time before %s, the time on a clock one minute behind",
			admin.NotBefore.UTC().Format(time.RFC3339), behind.UTC().Format(time.RFC3339))
	}
}

func TestKubeconfigEmbedsAWorkingKeypair(t *testing.T) {
	_, doc := computedKubeconfig(t)
	block, _ := pem.Decode(doc.Users[0].User.ClientKeyData)
	if block == nil {
		t.Fatal("client-key-data holds no PEM block")
	}
	if _, err := x509.ParseECPrivateKey(block.Bytes); err != nil {
		t.Errorf("parsing client-key-data: %v", err)
	}
}

func TestKubeconfigMintsAFreshCredentialEachRun(t *testing.T) {
	dir, doc := computedKubeconfig(t)
	if err := Kubeconfig(dir, "mycluster", "https://127.0.0.1:16443", io.Discard); err != nil {
		t.Fatalf("computing the kubeconfig: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "kubeconfig"))
	if err != nil {
		t.Fatalf("reading the kubeconfig: %v", err)
	}
	var again kubeconfigDocument
	if err := yaml.Unmarshal(raw, &again); err != nil {
		t.Fatalf("parsing the kubeconfig: %v", err)
	}
	if bytes.Equal(doc.Users[0].User.ClientCertificateData, again.Users[0].User.ClientCertificateData) {
		t.Error("the second run wrote the same admin certificate as the first, want a new one")
	}
}

func TestKubeconfigCarriesTheServerItWasGiven(t *testing.T) {
	dir := mintedIdentity(t)
	if err := Kubeconfig(dir, "mycluster", "https://198.51.100.7:6443", io.Discard); err != nil {
		t.Fatalf("computing the kubeconfig: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "kubeconfig"))
	if err != nil {
		t.Fatalf("reading the kubeconfig: %v", err)
	}
	if !strings.Contains(string(raw), "server: https://198.51.100.7:6443") {
		t.Fatalf("the kubeconfig is %q, want it to contain server: https://198.51.100.7:6443", raw)
	}
}
