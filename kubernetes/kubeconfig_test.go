package kubernetes

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testPKI is the smallest authority a kubeconfig test needs: one CA
// that signs both the server's certificate and the client's, the
// way a one-cluster identity does.
type testPKI struct {
	caPEM      []byte
	serverCert tls.Certificate
	clientPEM  []byte
	clientKey  []byte
	pool       *x509.CertPool
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	issue := func(cn string, ip net.IP, usage x509.ExtKeyUsage) ([]byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		if ip != nil {
			template.IPAddresses = []net.IP{ip}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, _ := x509.MarshalECPrivateKey(key)
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	}

	serverPEM, serverKey := issue("server", net.ParseIP("127.0.0.1"), x509.ExtKeyUsageServerAuth)
	serverCert, err := tls.X509KeyPair(serverPEM, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	clientPEM, clientKey := issue("admin", nil, x509.ExtKeyUsageClientAuth)

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	return testPKI{
		caPEM:      pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		serverCert: serverCert,
		clientPEM:  clientPEM,
		clientKey:  clientKey,
		pool:       pool,
	}
}

func writeTestKubeconfig(t *testing.T, pki testPKI, server string) string {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString
	doc := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
  - name: liken
    cluster:
      server: %s
      certificate-authority-data: %s
contexts:
  - name: liken
    context: {cluster: liken, user: admin}
current-context: liken
users:
  - name: admin
    user:
      client-certificate-data: %s
      client-key-data: %s
`, server, b64(pki.caPEM), b64(pki.clientPEM), b64(pki.clientKey))
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKubeconfigClientAuthenticatesWithItsCertificate(t *testing.T) {
	pki := newTestPKI(t)
	var sawAuthHeader bool
	var sawPeerCN string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization") != ""
		if len(r.TLS.PeerCertificates) > 0 {
			sawPeerCN = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kind":"Status"}`)
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pki.pool,
	}
	server.StartTLS()
	defer server.Close()

	path := writeTestKubeconfig(t, pki, server.URL)
	c, err := KubeconfigClient(path)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(http.MethodGet, "/healthz", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if sawPeerCN != "admin" {
		t.Fatalf("the server saw client CN %q, want admin", sawPeerCN)
	}
	if sawAuthHeader {
		t.Fatal("a certificate client must send no bearer token")
	}
}

func TestKubeconfigClientRefusesAFileWithNoUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	os.WriteFile(path, []byte("apiVersion: v1\nkind: Config\n"), 0o600)
	if _, err := KubeconfigClient(path); err == nil {
		t.Fatal("an empty kubeconfig must be an error")
	}
}
