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
	"testing"

	"sigs.k8s.io/yaml"
)

// kubeconfigDocument is enough of kubectl's config schema to check
// what this code writes into it.
type kubeconfigDocument struct {
	Clusters []struct {
		Cluster struct {
			Server                   string `json:"server"`
			CertificateAuthorityData []byte `json:"certificate-authority-data"`
		} `json:"cluster"`
	} `json:"clusters"`
	Users []struct {
		User struct {
			ClientCertificateData []byte `json:"client-certificate-data"`
			ClientKeyData         []byte `json:"client-key-data"`
		} `json:"user"`
	} `json:"users"`
}

// computedKubeconfig mints an identity, computes its kubeconfig, and
// parses the result.
func computedKubeconfig(t *testing.T) (string, kubeconfigDocument) {
	t.Helper()
	dir := mintedIdentity(t)
	if err := Kubeconfig(dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "kubeconfig"))
	if err != nil {
		t.Fatal(err)
	}
	var doc kubeconfigDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Clusters) != 1 || len(doc.Users) != 1 {
		t.Fatalf("kubeconfig shape: %d clusters, %d users", len(doc.Clusters), len(doc.Users))
	}
	return dir, doc
}

func TestKubeconfigPointsAtTheForwardedPort(t *testing.T) {
	dir, doc := computedKubeconfig(t)
	if got := doc.Clusters[0].Cluster.Server; got != "https://127.0.0.1:16443" {
		t.Errorf("server: got %q", got)
	}
	serverCA, err := os.ReadFile(filepath.Join(dir, "tls", "server-ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc.Clusters[0].Cluster.CertificateAuthorityData, serverCA) {
		t.Error("embedded CA is not the server CA")
	}
}

func TestAdminCertificateChainsToTheClientCA(t *testing.T) {
	dir, doc := computedKubeconfig(t)

	block, _ := pem.Decode(doc.Users[0].User.ClientCertificateData)
	if block == nil {
		t.Fatal("client-certificate-data is not PEM")
	}
	admin, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	if admin.Subject.CommonName != "admin" {
		t.Errorf("CN: got %q", admin.Subject.CommonName)
	}
	if len(admin.Subject.Organization) != 1 || admin.Subject.Organization[0] != "system:masters" {
		t.Errorf("O: got %v", admin.Subject.Organization)
	}

	roots := x509.NewCertPool()
	roots.AddCert(parseCertificate(t, dir, "client-ca.crt"))
	if _, err := admin.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("admin certificate does not verify as a client cert: %v", err)
	}
}

func TestKubeconfigEmbedsAWorkingKeypair(t *testing.T) {
	_, doc := computedKubeconfig(t)
	block, _ := pem.Decode(doc.Users[0].User.ClientKeyData)
	if block == nil {
		t.Fatal("client-key-data is not PEM")
	}
	if _, err := x509.ParseECPrivateKey(block.Bytes); err != nil {
		t.Errorf("admin key does not parse: %v", err)
	}
}

func TestKubeconfigMintsAFreshCredentialEachRun(t *testing.T) {
	dir, doc := computedKubeconfig(t)
	if err := Kubeconfig(dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "kubeconfig"))
	if err != nil {
		t.Fatal(err)
	}
	var again kubeconfigDocument
	if err := yaml.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(doc.Users[0].User.ClientCertificateData, again.Users[0].User.ClientCertificateData) {
		t.Error("a second run reused the admin certificate")
	}
}
