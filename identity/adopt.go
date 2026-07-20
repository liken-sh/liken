package identity

// This file implements adoption: taking on an existing cluster's
// identity. Adoption is the reverse of minting.
//
// The image carries the cluster's certificate authorities and join
// token. This is why machines built from the same image belong to
// the same cluster. Minting creates that identity before any machine
// exists, which works for a cluster that liken founds. For a cluster
// that liken did not create, such as any existing k3s cluster
// however it was set up, the identity already exists on that
// cluster's servers. It must be copied off one of them instead.
// Adoption takes that copy and places it into the deployment's
// identity directory exactly as minting would have. Because of this,
// everything downstream (the kubeconfig, the image build, and
// init's seeding of /var/lib/rancher/k3s/server/tls) is identical
// whether the identity was minted or adopted. An image built from an
// adopted identity joins the existing cluster. Its machines present
// the real token, and every certificate they see chains to the real
// CAs.
//
// To harvest the identity, run this as root on any server of the
// existing cluster:
//
//	cd /var/lib/rancher/k3s/server
//	tar czf /tmp/identity.tgz token \
//	    tls/server-ca.{crt,key} \
//	    tls/client-ca.{crt,key} \
//	    tls/request-header-ca.{crt,key} \
//	    tls/service.key \
//	    tls/etcd/server-ca.{crt,key} \
//	    tls/etcd/peer-ca.{crt,key}
//
// Then unpack that archive somewhere private, and point adoption at
// the directory. Only the certificate authorities and the token come
// over. The tls directory on a live server also holds the leaf
// certificates that k3s signed from them, such as the API server's
// serving certificate and the kubelet certificates. Those stay
// behind, because every server signs its own leaf certificates from
// the shared roots. The service.key file is included for the same
// reason it exists in minting. It signs every ServiceAccount token,
// and a control plane that verified tokens against a different key
// would reject every pod's identity.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Adopt copies a harvested identity from harvest into dir. Adopt
// refuses anything that is not a complete, self-consistent bundle
// placed over an empty deployment.
func Adopt(harvest, dir string, out io.Writer) error {
	// This code refuses a partial harvest before it changes anything.
	// A bundle that is missing one CA would produce an image that
	// boots, and then fails later in a way that is hard to trace,
	// such as a control plane that cannot sign one kind of
	// certificate, or pods whose tokens do not verify.
	for _, f := range Bundle {
		if _, err := os.Stat(filepath.Join(harvest, f)); err != nil {
			return fmt.Errorf("harvest is missing %s; re-run the tar on the existing server", f)
		}
	}

	// This code cross-checks the token: the token's embedded CA hash
	// must match the harvested server CA. The token file on a running
	// server is in k3s's "secure" format, K10<CA-HASH>::<user>:
	// <password>, where CA-HASH is the SHA256 hash of the cluster CA
	// certificate. This code checks that hash here. This check
	// catches a token harvested from one cluster mixed with CAs from
	// another, a mixup that would otherwise surface only later, as
	// every machine refuses to join. This is the same verification
	// that a joining machine performs before it trusts an endpoint,
	// done early, where the fix (re-harvest, from one server this
	// time) is cheap.
	token, err := os.ReadFile(filepath.Join(harvest, "token"))
	if err != nil {
		return err
	}
	if strings.HasPrefix(string(token), "K10") {
		crt, err := os.ReadFile(filepath.Join(harvest, "tls", "server-ca.crt"))
		if err != nil {
			return err
		}
		want := fmt.Sprintf("K10%x::", sha256.Sum256(crt))
		if !strings.HasPrefix(string(token), want) {
			return fmt.Errorf("the harvested token does not hash the harvested server CA; these came from different clusters")
		}
	}

	// Replacing an identity is a deliberate act, the same as minting.
	// An image built from a mix of two identities could not join
	// either cluster. If the identity directory holds any file from
	// the bundle, this code stops and makes the operator choose.
	for _, f := range Bundle {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return fmt.Errorf("%s already exists; this deployment already holds an identity — delete the identity directory first if replacing it is really the intent", filepath.Join(dir, f))
		}
	}

	for _, f := range Bundle {
		if err := copyPrivately(filepath.Join(harvest, f), filepath.Join(dir, f)); err != nil {
			return err
		}
		fmt.Fprintf(out, "adopted %s\n", f)
	}

	// Private keys and the token are secrets, but the certificates
	// are not. Restricting the whole tree, including the identity
	// directory, is simpler than listing which paths need the
	// restriction.
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()&^0o077)
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "the identity is adopted: images built from it join the existing cluster")
	return nil
}

// copyPrivately copies one file, creating parents as needed.
func copyPrivately(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}
