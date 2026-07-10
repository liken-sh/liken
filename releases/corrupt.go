package releases

// Deliberately damage a published release, for drilling the trust
// chain: flip one byte a megabyte into liken.cpio, leaving
// release.yaml declaring a digest the bytes no longer match. A
// machine asked to run this release must refuse to stage it: the
// download completes, the verification fails, and the
// VersionConverged condition holds at DigestMismatch with nothing
// written to a slot.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// corruptionOffset is far enough into the archive that the damage
// lands in real content, not in a header a parser might reject for
// its own reasons.
const corruptionOffset = 1024 * 1024

// Corrupt flips the byte at the corruption offset of a published
// release's liken.cpio, in place. The byte is complemented, not
// overwritten with a constant, so the damage is guaranteed whatever
// value was there.
func Corrupt(channelDir, version string, out io.Writer) error {
	target := filepath.Join(channelDir, version, "liken.cpio")
	f, err := os.OpenFile(target, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	b := make([]byte, 1)
	if _, err := f.ReadAt(b, corruptionOffset); err != nil {
		return fmt.Errorf("reading the byte to flip: %w", err)
	}
	flipped := 255 - b[0]
	if _, err := f.WriteAt([]byte{flipped}, corruptionOffset); err != nil {
		return err
	}

	fmt.Fprintf(out, "flipped the byte at offset %d of %s (%d -> %d)\n",
		corruptionOffset, target, b[0], flipped)
	fmt.Fprintln(out, "release.yaml still promises the original digest; a machine must now refuse this release")
	return nil
}
