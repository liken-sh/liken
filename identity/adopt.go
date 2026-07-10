package identity

// Adopting an existing cluster's identity: the inverse of minting.
//
// The image carries the cluster's certificate authorities and join
// token, which is why machines built from the same image belong to
// the same cluster. Minting creates that identity before any machine
// exists, which works for a cluster liken founds. For a cluster liken
// did not create, the identity already exists on that cluster's
// servers and has to be copied off one of them instead. Adoption
// takes that copy and lays it into the deployment's identity
// directory exactly as minting would have, so everything downstream
// (the kubeconfig, the image build, init's seeding of
// /var/lib/rancher/k3s/server/tls) is identical whether the identity
// was minted or adopted. An image built from an adopted identity
// joins the existing cluster: its machines present the real token,
// and every certificate they see chains to the real CAs.
//
// Harvesting, run as root on any server of the existing cluster:
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
// then unpack that archive somewhere private and point adoption at
// the directory. Only the certificate *authorities* and the token
// come over: the tls directory on a live server also holds the leaf
// certificates k3s signed from them (the API server's serving cert,
// kubelet certs), and those stay behind, because every server signs
// its own leaves from the shared roots. The service.key rides along
// for the same reason it exists in minting: it signs every
// ServiceAccount token, and a control plane that verified tokens
// against a different key would reject every pod's identity.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Adopt copies a harvested identity from harvest into dir, refusing
// anything that isn't a complete, self-consistent bundle laid over an
// empty deployment.
func Adopt(harvest, dir string, out io.Writer) error {
	// Refuse a partial harvest before touching anything: a bundle
	// missing one CA would produce an image that boots and then fails
	// in some distant, confusing way (a control plane that can't sign
	// one kind of certificate, pods whose tokens don't verify).
	for _, f := range bundle {
		if _, err := os.Stat(filepath.Join(harvest, f)); err != nil {
			return fmt.Errorf("harvest is missing %s; re-run the tar on the existing server", f)
		}
	}

	// The cross-check: the token's embedded CA hash must match the
	// harvested server CA. The token file on a running server is in
	// k3s's "secure" format, K10<CA-HASH>::<user>:<password>, where
	// CA-HASH is the SHA256 of the cluster CA certificate. That hash
	// is checkable right here, and checking it catches a token
	// harvested from one cluster mixed with CAs from another — the
	// mixup that would otherwise surface as every machine refusing to
	// join. This is the same verification a joining machine performs
	// before trusting an endpoint, done early, where the fix
	// (re-harvest, from one server this time) is cheap.
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

	// Replacing an identity is a deliberate act, same as minting: an
	// image built from a mix of two identities could not join either
	// cluster. If the identity directory holds any of the bundle,
	// stop and make the operator choose.
	for _, f := range bundle {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return fmt.Errorf("%s already exists; this deployment already holds an identity — delete the identity directory first if replacing it is really the intent", filepath.Join(dir, f))
		}
	}

	for _, f := range bundle {
		if err := copyPrivately(filepath.Join(harvest, f), filepath.Join(dir, f)); err != nil {
			return err
		}
		fmt.Fprintf(out, "adopted %s\n", f)
	}

	// Private keys and the token are secrets; the certificates are
	// not, but locking down the whole tree, the identity directory
	// included, is simpler than itemizing which paths need it.
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
