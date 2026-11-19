package identity

// This file tests minting for the properties that k3s and kubectl
// actually check. The certificates must be real CAs, because the API
// server refuses to chain to anything else. The ServiceAccount key
// must use the one encoding that kube-apiserver parses. The token
// must hash the server CA that it ships beside, because a joining
// machine verifies exactly that before it trusts the endpoint.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// mintedIdentity mints a fresh identity into a temp directory.
func mintedIdentity(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := Mint(dir, io.Discard); err != nil {
		t.Fatalf("minting the identity: %v", err)
	}
	return dir
}

// parseCertificate reads and parses one PEM certificate from the
// identity's tls tree.
func parseCertificate(t *testing.T, dir, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "tls", path))
	if err != nil {
		t.Fatalf("reading tls/%s: %v", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("%s holds no PEM certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing the certificate: %v", err)
	}
	return cert
}

func TestMintProducesTheWholeBundle(t *testing.T) {
	dir := mintedIdentity(t)
	for _, f := range Bundle {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s is missing, want the file", f)
		}
	}
}

func TestMintedAuthoritiesAreRealCAs(t *testing.T) {
	dir := mintedIdentity(t)
	cases := []struct {
		path string
		cn   string
	}{
		{"server-ca.crt", "liken server CA"},
		{"client-ca.crt", "liken client CA"},
		{"request-header-ca.crt", "liken request-header CA"},
		{"etcd/server-ca.crt", "liken etcd server CA"},
		{"etcd/peer-ca.crt", "liken etcd peer CA"},
	}
	for _, c := range cases {
		t.Run(c.cn, func(t *testing.T) {
			cert := parseCertificate(t, dir, c.path)
			if cert.Subject.CommonName != c.cn {
				t.Errorf("common name is %q, want %q", cert.Subject.CommonName, c.cn)
			}
			if !cert.IsCA || !cert.BasicConstraintsValid {
				t.Error("certificate is not a CA, want a CA")
			}
			want := x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign
			if cert.KeyUsage != want {
				t.Errorf("key usage is %v, want %v", cert.KeyUsage, want)
			}
			key, ok := cert.PublicKey.(*ecdsa.PublicKey)
			if !ok || key.Curve != elliptic.P256() {
				t.Error("public key is not ECDSA P-256, want ECDSA P-256")
			}
			lifetime := cert.NotAfter.Sub(cert.NotBefore)
			if lifetime < 3649*24*time.Hour || lifetime > 3651*24*time.Hour {
				t.Errorf("lifetime is %v, want ten years", lifetime)
			}
		})
	}
}

func TestServiceKeyUsesTheSEC1Encoding(t *testing.T) {
	dir := mintedIdentity(t)
	raw, err := os.ReadFile(filepath.Join(dir, "tls", "service.key"))
	if err != nil {
		t.Fatalf("reading service.key: %v", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("PEM type is %q, want EC PRIVATE KEY", block.Type)
	}
	if _, err := x509.ParseECPrivateKey(block.Bytes); err != nil {
		t.Errorf("parsing service.key as a SEC1 EC key: %v", err)
	}
}

func TestTokenHashesTheServerCA(t *testing.T) {
	dir := mintedIdentity(t)
	token, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatalf("reading the token: %v", err)
	}
	crt, err := os.ReadFile(filepath.Join(dir, "tls", "server-ca.crt"))
	if err != nil {
		t.Fatalf("reading server-ca.crt: %v", err)
	}
	want := regexp.MustCompile(
		fmt.Sprintf(`^K10%x::server:[0-9a-f]{32}\n$`, sha256.Sum256(crt)))
	if !want.Match(token) {
		t.Errorf("token %q does not contain the digest of the server CA", token)
	}
}

func TestTokenIsPrivate(t *testing.T) {
	dir := mintedIdentity(t)
	info, err := os.Stat(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatalf("reading the token: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token mode is %v, want 0600", info.Mode().Perm())
	}
}

func TestMintKeepsAnExistingIdentity(t *testing.T) {
	dir := mintedIdentity(t)
	before, err := os.ReadFile(filepath.Join(dir, "tls", "server-ca.crt"))
	if err != nil {
		t.Fatalf("reading server-ca.crt: %v", err)
	}

	var out bytes.Buffer
	if err := Mint(dir, &out); err != nil {
		t.Fatalf("minting a second time: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(dir, "tls", "server-ca.crt"))
	if err != nil {
		t.Fatalf("reading server-ca.crt: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a second mint changed server-ca.crt, want it kept")
	}
	if !bytes.Contains(out.Bytes(), []byte("keeping server-ca")) {
		t.Errorf("output is %q, want a line that starts with keeping", out.String())
	}
}
