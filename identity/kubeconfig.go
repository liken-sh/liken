package identity

// This file computes the operator's kubeconfig offline from a
// deployment's identity. This code never asks the machine for a
// credential. Pre-seeding the CAs (mint.go) exists precisely so that
// this code can compute the credential without contacting the
// machine.
//
// A kubeconfig states three facts:
//
//  1. where the cluster is (a URL),
//  2. why to trust that this is really the cluster (the server CA
//     that signed its serving certificate),
//  3. who the client is (a client certificate that the cluster's
//     client CA signed).
//
// The identity in a client certificate lives in its subject. The API
// server reads CN as the username, and every O as a group. No user
// database exists behind this. Presenting a certificate with
// O=system:masters makes the bearer a cluster admin, because RBAC
// binds that group to cluster-admin. The certificates themselves are
// the only user records.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// server is where the cluster is. QEMU forwards this host port to
// the guest's API server. The dev-cluster Makefile's run target
// defines that mapping. The serving certificate that k3s mints
// covers 127.0.0.1 by default, so the forwarded connection verifies
// without any extra SANs.
const server = "https://127.0.0.1:16443"

// Kubeconfig computes an admin credential from the identity in dir,
// and writes dir/kubeconfig. Kubeconfig writes the result into the
// identity directory and nowhere else. liken never changes
// ~/.kube/config or any other kubeconfig that the operator already
// has. Point kubectl at the file explicitly:
//
//	kubectl --kubeconfig dev-cluster/identity/kubeconfig get nodes
//
// Each run mints a fresh keypair and certificate. The certificate
// lasts one year, which is generous for a development credential.
// Running Kubeconfig again replaces it in seconds.
func Kubeconfig(dir string, out io.Writer) error {
	tls := filepath.Join(dir, "tls")

	caCert, caKey, err := readCA(tls, "client-ca")
	if err != nil {
		return err
	}

	// This code creates the client identity: a fresh keypair, and a
	// certificate for it signed by the client CA. A client
	// certificate needs the clientAuth extended key usage. The API
	// server rejects certificates that do not declare their purpose.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "admin",
			Organization: []string{"system:masters"},
		},
		NotBefore:   now,
		NotAfter:    now.AddDate(0, 0, 365),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}

	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	adminCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	adminKey := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1})

	serverCA, err := os.ReadFile(filepath.Join(tls, "server-ca.crt"))
	if err != nil {
		return err
	}

	// This is the kubeconfig itself, with the certificates embedded
	// in base64, as every kubeconfig does. This makes the file
	// self-contained and portable.
	b64 := base64.StdEncoding.EncodeToString
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
  - name: liken
    cluster:
      server: %s
      certificate-authority-data: %s
contexts:
  - name: liken
    context:
      cluster: liken
      user: admin
current-context: liken
users:
  - name: admin
    user:
      client-certificate-data: %s
      client-key-data: %s
`, server, b64(serverCA), b64(adminCert), b64(adminKey))

	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s for admin (O=system:masters) at %s\n", path, server)
	return nil
}

// readCA loads one authority's certificate and private key from the
// tls tree.
func readCA(tls, name string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(tls, name+".crt"))
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("%s.crt is not PEM", name)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyPEM, err := os.ReadFile(filepath.Join(tls, name+".key"))
	if err != nil {
		return nil, nil, err
	}
	block, _ = pem.Decode(keyPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("%s.key is not PEM", name)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}
