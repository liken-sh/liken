package releases

// Bundling a public release of liken itself: the generic OS with no
// deployment inside, laid out as a channel the same way a
// deployment's releases are.
//
// The bundle is three artifacts. vmlinuz and the generic liken.cpio
// are the operating system; the liken binary is the toolkit that
// turns them into a deployment's bootable media without this repo or
// a build. Because the generic archive embeds no identity and no
// manifests, every digest here is stable for a given source tree:
// publishable, committable, and the same for everyone.
//
// The release.yaml is the same document format a deployment's channel
// serves and the installer embeds (machine.ParseRelease reads all of
// them), so anything that can verify a deployment release can verify
// a public one.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Bundle lays out a public release: the named artifacts copied into
// <channel>/<version>/ beside a release.yaml naming each by sha256
// digest and size.
func Bundle(vmlinuz, image, cli, channelDir, version string, out io.Writer) error {
	dest := filepath.Join(channelDir, version)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	// The artifacts keep canonical names in the channel whatever
	// their build-tree paths were, so the document and the URLs are
	// the same for every release.
	sources := []struct {
		src, name string
	}{
		{vmlinuz, "vmlinuz"},
		{image, "liken.cpio"},
		{cli, "liken"},
	}
	document := fmt.Sprintf("apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: %s\nartifacts:\n", version)
	for _, s := range sources {
		dst := filepath.Join(dest, s.name)
		if err := copyFile(s.src, dst); err != nil {
			return err
		}
		digest, err := fileSHA256(dst)
		if err != nil {
			return err
		}
		info, err := os.Stat(dst)
		if err != nil {
			return err
		}
		document += fmt.Sprintf("  - name: %s\n    sha256: %s\n    size: %d\n", s.name, digest, info.Size())
	}

	// The document is generated from the copies in the channel, the
	// bytes that will actually be served, so the two can never
	// disagree.
	if err := os.WriteFile(filepath.Join(dest, "release.yaml"), []byte(document), 0o644); err != nil {
		return err
	}

	if err := report(dest, version, out); err != nil {
		return err
	}

	// A public release has no cluster catalog; its trust root is the
	// document's own digest, published beside the download so a
	// person (or their toolkit) can check what they fetched.
	digest, err := fileSHA256(filepath.Join(dest, "release.yaml"))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nrelease document digest (verify a download against this):\n")
	fmt.Fprintf(out, "  sha256:%s\n", digest)
	return nil
}
