// Package identity mints, adopts, and derives credentials from a
// deployment's identity: the certificate authorities and join token
// that make machines built from the same image members of the same
// cluster.
//
// Kubernetes trust is built from several small PKIs, each covering
// one relationship. k3s checks for these files before generating its
// own, so placing them in /var/lib/rancher/k3s/server/tls ahead of
// first start reverses the usual flow. Normally the identity is an
// output that has to be extracted from a running machine, which a
// machine with no shell could never hand over anyway; here it is an
// input the image carries. Everything k3s signs from here on (the
// API server's serving cert, kubelet certs, all of it) chains up to
// keys we held before the machine ever booted, which is what lets an
// operator's kubeconfig be computed offline (see kubeconfig.go).
//
// An identity belongs to a deployment, not to the OS: this package
// knows how to produce one, and the caller decides which deployment
// it is for. The files are private keys and never belong in history;
// deployment directories gitignore them.
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// The identity, as paths relative to its directory. Everything mint
// produces and adopt copies, no more. The token lives beside the tls
// tree, not in it, matching where k3s keeps its own
// (/var/lib/rancher/k3s/server/token).
var bundle = []string{
	"token",
	"tls/server-ca.crt", "tls/server-ca.key",
	"tls/client-ca.crt", "tls/client-ca.key",
	"tls/request-header-ca.crt", "tls/request-header-ca.key",
	"tls/service.key",
	"tls/etcd/server-ca.crt", "tls/etcd/server-ca.key",
	"tls/etcd/peer-ca.crt", "tls/etcd/peer-ca.key",
}

// Mint creates the identity's artifacts in dir, keeping any that
// already exist. The authorities:
//
//	server-ca          signs the API server's serving certificates,
//	                   the thing kubectl verifies before trusting a
//	                   connection
//	client-ca          signs client certificates. The API server reads
//	                   identity out of the subject: CN is the
//	                   username, each O is a group membership
//	request-header-ca  the aggregation layer's trust root: extension
//	                   API servers accept proxied-authentication
//	                   headers only from a front proxy bearing a cert
//	                   from this CA
//	etcd/server-ca     etcd's two PKIs. liken's k3s keeps state in
//	etcd/peer-ca       sqlite via kine, not etcd, but k3s manages the
//	                   full CA family as a set
//	service.key        not a CA: the key that signs every
//	                   ServiceAccount token. Whoever holds this key
//	                   can mint valid identities for any pod
//
// Everything is ECDSA P-256, matching what k3s generates for itself.
// Ten-year lifetimes; rotating these roots is work a later milestone
// takes up, and until then a long life keeps the identity story
// simple.
//
// Each artifact is minted only if it doesn't already exist, so adding
// a new artifact (or re-running for any reason) never replaces an
// identity that machines already carry: replacing the CAs would
// orphan every kubeconfig computed from them, and replacing the token
// would strand any machine that hasn't joined yet. Replacing the
// identity is a deliberate act: delete the identity directory, and
// the next mint creates a new one.
func Mint(dir string, out io.Writer) error {
	tls := filepath.Join(dir, "tls")
	if err := os.MkdirAll(filepath.Join(tls, "etcd"), 0o755); err != nil {
		return err
	}

	authorities := []struct {
		path string
		cn   string
	}{
		{"server-ca", "liken server CA"},
		{"client-ca", "liken client CA"},
		{"request-header-ca", "liken request-header CA"},
		{"etcd/server-ca", "liken etcd server CA"},
		{"etcd/peer-ca", "liken etcd peer CA"},
	}
	for _, ca := range authorities {
		if _, err := os.Stat(filepath.Join(tls, ca.path+".crt")); err == nil {
			fmt.Fprintf(out, "keeping %s: %s\n", ca.path, ca.cn)
			continue
		}
		if err := newCA(tls, ca.path, ca.cn); err != nil {
			return fmt.Errorf("minting %s: %w", ca.path, err)
		}
		fmt.Fprintf(out, "minted %s: %s\n", ca.path, ca.cn)
	}

	// The ServiceAccount signing key is not like the CAs above:
	// tokens are JWTs, so there's no certificate, just a keypair the
	// API server signs with and verifies against. The encoding
	// matters: kube-apiserver reads this file with a parser that
	// understands the older SEC1 encoding ("EC PRIVATE KEY") but not
	// PKCS#8 ("PRIVATE KEY"); given PKCS#8 it fails on startup with
	// an error that the file contains no valid keys.
	serviceKey := filepath.Join(tls, "service.key")
	if _, err := os.Stat(serviceKey); err == nil {
		fmt.Fprintln(out, "keeping service.key: the ServiceAccount token signing key")
	} else {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		sec1, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return err
		}
		if err := writePEM(serviceKey, "EC PRIVATE KEY", sec1, 0o600); err != nil {
			return err
		}
		fmt.Fprintln(out, "minted service.key: the ServiceAccount token signing key")
	}

	// The cluster's join token, in k3s's "secure" format:
	//
	//	K10<CA-HASH>::<user>:<password>
	//
	// Normally this has to be copied off a running server
	// (/var/lib/rancher/k3s/server/node-token), because the CA it
	// hashes doesn't exist until k3s generates it at first boot.
	// liken reverses that: the server CA is minted above, before any
	// machine exists, so the whole token is computable right here.
	// The CA-HASH is the SHA256 of the cluster CA certificate file. A
	// joining machine fetches the server's CA bundle, hashes it, and
	// compares before it trusts the endpoint or presents the secret,
	// so the token authenticates in both directions: the machine
	// proves itself to the cluster, and the cluster proves itself to
	// the machine. The secret half is 32 hex characters of real
	// randomness, the same format k3s generates. "server" is the
	// credential's username: whoever bears this token may join
	// machines to the cluster.
	tokenPath := filepath.Join(dir, "token")
	if _, err := os.Stat(tokenPath); err == nil {
		fmt.Fprintln(out, "keeping token: the cluster join token")
	} else {
		crt, err := os.ReadFile(filepath.Join(tls, "server-ca.crt"))
		if err != nil {
			return err
		}
		secret := make([]byte, 16)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		token := fmt.Sprintf("K10%x::server:%s\n", sha256.Sum256(crt), hex.EncodeToString(secret))
		if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
			return err
		}
		fmt.Fprintln(out, "minted token: the cluster join token")
	}

	return nil
}

// newCA creates one self-signed root: a fresh P-256 key and a
// certificate whose extensions mark it as a CA that may sign other
// certificates.
func newCA(tls, path, cn string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	// Serial numbers must be unique per issuer; 128 random bits is
	// how every CA in practice satisfies that without keeping state.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             now,
		NotAfter:              now.AddDate(0, 0, 3650),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature |
			x509.KeyUsageCertSign |
			x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}

	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := writePEM(filepath.Join(tls, path+".key"), "EC PRIVATE KEY", sec1, 0o600); err != nil {
		return err
	}
	return writePEM(filepath.Join(tls, path+".crt"), "CERTIFICATE", der, 0o644)
}

// writePEM writes one PEM block to a file.
func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), mode)
}
