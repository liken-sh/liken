// Package identity mints, adopts, and derives credentials from a
// deployment's identity. An identity is the certificate authorities
// and join token that make machines built from the same image
// members of the same cluster.
//
// Kubernetes trust is built from several small PKIs, each one
// covering a single relationship. k3s checks for these files before
// it generates its own. Placing them in
// /var/lib/rancher/k3s/server/tls before first start reverses the
// usual flow. Normally, the identity is an output that must be
// extracted from a running machine, something a machine with no
// shell could never provide. Here, the identity is an input that the
// image carries. Everything that k3s signs from this point on,
// including the API server's serving certificate and the kubelet
// certificates, chains up to keys that existed before the machine
// ever booted. This is what lets an operator's kubeconfig be
// computed offline (see kubeconfig.go).
//
// An identity belongs to a deployment, not to the OS. This package
// knows how to produce an identity, and the caller decides which
// deployment it belongs to. The files are private keys and must
// never enter version-control history. Deployment directories list
// them in .gitignore.
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

// Bundle lists the identity as paths relative to its directory. It
// lists everything that mint produces and adopt copies, and nothing
// more. The token lives beside the tls tree, not inside it, matching
// where k3s keeps its own token (/var/lib/rancher/k3s/server/token).
// The image package reads this list to place an identity into a
// deployment layer, so a new artifact added here reaches the
// machines with no other change. The kubeconfig is deliberately not
// part of the bundle. It is the operator's credential, and it must
// never be included in an image.
var Bundle = []string{
	"token",
	"tls/server-ca.crt", "tls/server-ca.key",
	"tls/client-ca.crt", "tls/client-ca.key",
	"tls/request-header-ca.crt", "tls/request-header-ca.key",
	"tls/service.key",
	"tls/etcd/server-ca.crt", "tls/etcd/server-ca.key",
	"tls/etcd/peer-ca.crt", "tls/etcd/peer-ca.key",
}

// Mint creates the identity's artifacts in dir, and keeps any that
// already exist. The authorities:
//
//	server-ca          signs the API server's serving certificates.
//	                   kubectl checks this signature before it trusts
//	                   a connection.
//	client-ca          signs client certificates. The API server reads
//	                   the identity from the certificate subject. CN
//	                   is the username, and each O is a group.
//	request-header-ca  is the aggregation layer's trust root.
//	                   Extension API servers accept
//	                   proxied-authentication headers only from a
//	                   front proxy that presents a certificate from
//	                   this CA.
//	etcd/server-ca     are etcd's two PKIs. liken's k3s keeps its
//	etcd/peer-ca       state in sqlite through kine, not etcd, but k3s
//	                   still manages the full CA family as a set.
//	service.key        is not a CA. It is the key that signs every
//	                   ServiceAccount token. Whoever holds this key
//	                   can mint valid identities for any pod.
//
// Every key is ECDSA P-256, matching what k3s generates for itself.
// Every certificate has a ten-year lifetime. Rotating these roots is
// work that a later milestone will take up. Until then, a long
// lifetime keeps the identity simple to manage.
//
// Mint creates each artifact only if it does not already exist.
// Because of this, adding a new artifact, or running Mint again for
// any reason, never replaces an identity that machines already
// carry. Replacing the CAs would break every kubeconfig computed
// from them, and replacing the token would prevent any machine that
// has not joined yet from joining. Replacing the identity is a
// deliberate act. Delete the identity directory, and the next call
// to Mint creates a new one.
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

	// The ServiceAccount signing key differs from the CAs above.
	// Tokens are JWTs, so there is no certificate, only a keypair
	// that the API server signs with and verifies against. The
	// encoding matters. kube-apiserver reads this file with a parser
	// that understands the older SEC1 encoding ("EC PRIVATE KEY") but
	// not PKCS#8 ("PRIVATE KEY"). Given a PKCS#8 file, kube-apiserver
	// fails at startup with an error that says the file contains no
	// valid keys.
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

	// This is the cluster's join token, in k3s's "secure" format:
	//
	//	K10<CA-HASH>::<user>:<password>
	//
	// Normally, an operator must copy this token off a running
	// server (/var/lib/rancher/k3s/server/node-token), because the CA
	// that it hashes does not exist until k3s generates it at first
	// boot. liken reverses that order. The server CA is minted above,
	// before any machine exists, so this code can compute the whole
	// token right here. CA-HASH is the SHA256 hash of the cluster CA
	// certificate file. A joining machine fetches the server's CA
	// bundle, hashes it, and compares the hash before it trusts the
	// endpoint or presents the secret. Because of this, the token
	// authenticates in both directions: the machine proves its
	// identity to the cluster, and the cluster proves its identity to
	// the machine. The secret half is 32 hex characters of real
	// randomness, the same format that k3s generates. "server" is the
	// credential's username. Whoever holds this token may join
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

// newCA creates one self-signed root. It creates a fresh P-256 key
// and a certificate whose extensions mark it as a CA that can sign
// other certificates.
func newCA(tls, path, cn string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	// Serial numbers must be unique for each issuer. In practice,
	// every CA satisfies this requirement with 128 random bits,
	// without keeping state.
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
