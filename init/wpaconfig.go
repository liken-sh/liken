package main

// Where the passphrase comes from, and how a network's name and its
// passphrase reach the supplicant without either one being able to
// write a configuration directive of its own.
//
// The passphrase has two homes. The image carries one file for each
// network under /etc/liken/psk, beside the join token, because a
// passphrase is the same class of fact: a cluster credential the
// machine needs before the cluster can give it anything. machineState
// may hold a staged copy, and the read order is staged then image,
// the same order the cluster document resolves in. Nothing writes the
// staged copy today; rotation, when it arrives, becomes a writer of
// that copy and changes nothing here (plans/62-wifi.md).
//
// The generated file uses only value forms that carry no syntax. The
// supplicant's parser reads a value as hex whenever the value does
// not start with a quote, and an unknown line makes the whole file
// fail to parse rather than being skipped. A value that could write a
// line of its own could therefore disable the network block or turn
// the encryption off. Every value liken writes is hex, which holds no
// quote, no space, and no comment character.

import (
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// imagePassphraseDir is where the image carries one passphrase file for
// each network. It is a variable so tests can point it at a directory
// of their own.
var imagePassphraseDir = "/etc/liken/psk"

// stagedPassphraseDir is the passphrase's staged home on machineState.
func stagedPassphraseDir(stateRoot string) string {
	return filepath.Join(stateRoot, "psk")
}

// The passphrase length WPA2 itself states. liken enforces the range
// even though the hex form it writes has no length rule, because the
// access point derives its own key from the same bytes under the same
// rule. A passphrase outside this range is one no access point could
// hold, and refusing it here puts the reason on the console instead
// of into a failed handshake nobody can read.
const (
	minPassphraseBytes = 8
	maxPassphraseBytes = 63
)

// readPassphrase resolves one network's passphrase: the staged copy on
// machineState first, then the image's file. It reports where the
// passphrase came from, so the console can say which one this boot used.
//
// A missing file is a decision, not a wait. The manifest declared a
// network that needs a key, no file on this machine holds one, and no
// amount of retrying produces one, so this failure joins WRONG_KEY
// under the park rule.
func readPassphrase(stateRoot, ssid string) (passphrase, source string, err error) {
	staged := filepath.Join(stagedPassphraseDir(stateRoot), ssid)
	image := filepath.Join(imagePassphraseDir, ssid)
	for _, path := range []string{staged, image} {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("reading the passphrase at %s: %w", path, err)
		}
		return trimPassphraseFile(string(raw)), path, nil
	}
	return "", "", fmt.Errorf("no passphrase for %q; the image carries one file for each network at %s/<ssid>, and this machine has none",
		ssid, imagePassphraseDir)
}

// trimPassphraseFile removes the line ending that a text editor
// leaves on the last line of a file. Exactly one line ending goes and
// nothing else does: a trailing space is a legal character in a WPA2
// passphrase, so trimming all trailing whitespace would silently
// change the key, while the one final newline was never part of the
// passphrase at all.
func trimPassphraseFile(raw string) string {
	trimmed := strings.TrimSuffix(raw, "\n")
	return strings.TrimSuffix(trimmed, "\r")
}

// checkPassphrase refuses a passphrase the generator has no safe
// rendering for, and says which rule refused it. The range is WPA2's
// own. A byte outside printable ASCII in a passphrase file is nearly
// always a stray line ending or an encoding accident, not a key any
// access point holds.
func checkPassphrase(ssid, passphrase string) error {
	if n := len(passphrase); n < minPassphraseBytes || n > maxPassphraseBytes {
		return fmt.Errorf("the passphrase for %q is %d bytes; WPA2 passphrases are %d to %d printable characters",
			ssid, n, minPassphraseBytes, maxPassphraseBytes)
	}
	for i := 0; i < len(passphrase); i++ {
		if b := passphrase[i]; b < 0x20 || b > 0x7e {
			return fmt.Errorf("the passphrase for %q holds the byte %#02x at position %d; a WPA2 passphrase holds printable characters only",
				ssid, b, i)
		}
	}
	return nil
}

// derivePMK derives the key the WPA2 four-way handshake actually
// uses, so the supplicant never needs the passphrase for that half.
// The 802.11 derivation is PBKDF2 over HMAC-SHA1, with the network's
// name as the salt and 4096 rounds. The supplicant accepts the result
// as 64 hex digits, a form no passphrase can break out of. The salt
// binds the result to the network's name, which is why a change to
// either one re-derives it.
func derivePMK(ssid, passphrase string) string {
	key, err := pbkdf2.Key(sha1.New, passphrase, []byte(ssid), 4096, 32)
	if err != nil {
		// The parameters are constants, so the only error this call
		// can report is one this code itself would have to
		// introduce. An empty result fails the supplicant's parse,
		// which fails closed.
		return ""
	}
	return hex.EncodeToString(key)
}

// wirelessConfig renders the supplicant's whole configuration for one
// interface. It refuses rather than render a value it cannot write
// safely, because a machine with no shell cannot be asked afterwards.
func wirelessConfig(w machine.WirelessSpec, stateRoot, ctrlDir string) (string, error) {
	var b strings.Builder

	// The one global line. The control directory lives in the file
	// rather than on the command line because the -C flag overrides
	// this line, upstream's own help text describes that override
	// backwards, and one source of truth is worth more than a flag.
	fmt.Fprintf(&b, "ctrl_interface=%s\n", ctrlDir)
	b.WriteString("network={\n")

	// The name is written as hex because 802.11 lets a network's
	// name carry any octet. The parser reads an unquoted value as
	// hex, and hex cannot hold the quote, the space, or the comment
	// character that would otherwise end the line early.
	fmt.Fprintf(&b, "\tssid=%s\n", hex.EncodeToString([]byte(w.SSID)))

	if w.SecurityOrDefault() == machine.WirelessOpen {
		// NONE is the supplicant's word for a network with no
		// encryption at all.
		b.WriteString("\tkey_mgmt=NONE\n")
		b.WriteString("}\n")
		return b.String(), nil
	}

	passphrase, source, err := readPassphrase(stateRoot, w.SSID)
	if err != nil {
		return "", err
	}
	if err := checkPassphrase(w.SSID, passphrase); err != nil {
		return "", err
	}
	fmt.Printf("liken: wireless: the passphrase for %s comes from %s\n", w.SSID, source)

	// All three key managements are named at once because a home
	// access point today commonly offers WPA2 and WPA3 together. The
	// supplicant picks the strongest one the access point
	// advertises. Naming only one would refuse half the networks a
	// machine may meet.
	b.WriteString("\tkey_mgmt=WPA-PSK WPA-PSK-SHA256 SAE\n")

	// Protected management frames are optional (1) rather than off
	// or required. WPA3 requires them and WPA2 does not: required
	// (2) refuses every WPA2-only access point, off (0) breaks SAE
	// against an access point that requires them, and optional lets
	// the supplicant negotiate whichever each access point offers.
	b.WriteString("\tieee80211w=1\n")

	// Two credential lines carry one passphrase. WPA2 uses the
	// derived key, and WPA3's SAE uses the passphrase itself. The
	// derived form leaves the supplicant no passphrase to hand SAE,
	// so a configuration with only psk= would join a WPA2 access
	// point and fail against a WPA3 one.
	fmt.Fprintf(&b, "\tpsk=%s\n", derivePMK(w.SSID, passphrase))
	fmt.Fprintf(&b, "\tsae_password=%s\n", hex.EncodeToString([]byte(passphrase)))

	b.WriteString("}\n")
	return b.String(), nil
}
