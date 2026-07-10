package releases

// Publishing a deployment's release: lay out <channel>/<version>/ the
// way the release server serves it, and report the catalog entry that
// names it.
//
// The artifacts come straight from the install image's payload
// (<image>/install-root/usr/share/liken/release), and that is
// deliberate: install.sh already generated a release.yaml from the
// exact bytes it packed, so publishing the same files under the same
// document means the installer's copy and the server's copy are one
// document, byte for byte. A machine installed from the "USB stick"
// and a machine that downloaded the release verified the same digests
// against the same document.
//
// The last thing reported is the catalog entry: the release
// document's own sha256, which is what goes into the Cluster's
// spec.releases.catalog. That digest is the root of the trust chain:
// the API names the document, and the document names the artifacts.
// For a deployment's channel it exists only here, at publish time,
// because the composed artifacts embed the deployment's identity and
// so no digest is stable until the deployment's own bytes are.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Publish copies a built image's release payload into the channel and
// reports the catalog entry.
func Publish(imageDir, channelDir, version string, out io.Writer) error {
	payload := filepath.Join(imageDir, "install-root", "usr", "share", "liken", "release")
	dest := filepath.Join(channelDir, version)

	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	// install.cpio is published for people making fresh machines, but
	// the release document doesn't name it: an upgrading machine
	// never fetches it, and the stick carries its own embedded copy
	// of the document.
	files := []string{
		filepath.Join(payload, "vmlinuz"),
		filepath.Join(payload, "liken.cpio"),
		filepath.Join(payload, "release.yaml"),
		filepath.Join(imageDir, "install.cpio"),
	}
	for _, src := range files {
		if err := copyFile(src, filepath.Join(dest, filepath.Base(src))); err != nil {
			return err
		}
	}

	if err := report(dest, version, out); err != nil {
		return err
	}

	// The catalog entry is the deployment side of the trust chain:
	// this digest goes into the Cluster's spec.releases.catalog, and
	// the fleet trusts nothing the chain from it doesn't name.
	digest, err := fileSHA256(filepath.Join(dest, "release.yaml"))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\ncatalog entry for the Cluster's spec.releases.catalog:\n")
	fmt.Fprintf(out, "  - version: %s\n", version)
	fmt.Fprintf(out, "    digest: sha256:%s\n", digest)
	return nil
}

// report prints what landed in the channel.
func report(dest, version string, out io.Writer) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\npublished liken %s to %s:\n", version, dest)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  %8s  %s\n", humanSize(info.Size()), e.Name())
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// humanSize renders a byte count the way a person scans a listing,
// in the unit that keeps the number small.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}
