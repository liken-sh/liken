package main

// Init writes the wpa_supplicant configuration for each wireless
// interface from the machine's WirelessSpec and its passphrase file.
//
// The machine needs the passphrase before it can reach the cluster, so
// the image includes it, as it includes the join token: one file per
// network under /etc/liken/psk. A copy of the file staged on
// machineState takes precedence. No component writes a staged copy. It
// is where a passphrase rotation would write the new passphrase.
//
// A network name or passphrase that holds a quote or a newline could
// add a line of its own to the file, such as key_mgmt=NONE. So every
// value from the spec or the passphrase file is written as hex, which
// wpa_supplicant reads when a value does not start with a quote.

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

	"github.com/liken-sh/liken/liken/machine"
)

// imagePassphraseDir holds one passphrase file per network, named for
// the SSID. It is a variable so tests can point it elsewhere.
var imagePassphraseDir = "/etc/liken/psk"

// stagedPassphraseDir is the passphrase directory on machineState.
func stagedPassphraseDir(stateRoot string) string {
	return filepath.Join(stateRoot, "psk")
}

// WPA2 limits a passphrase to 8 to 63 characters. The hex form that
// this file writes has no limit, but no access point accepts a
// passphrase outside the range. Checking here puts the reason on the
// console, in place of a failed handshake with no explanation.
const (
	minPassphraseBytes = 8
	maxPassphraseBytes = 63
)

// readPassphrase returns one network's passphrase and the path it read,
// so the console can show which copy this boot used. It reads the
// staged copy on machineState first and the image's file second.
func readPassphrase(stateRoot, ssid string) (passphrase, source string, err error) {
	staged := filepath.Join(stagedPassphraseDir(stateRoot), ssid)
	image := filepath.Join(imagePassphraseDir, ssid)
	for _, path := range []string{staged, image} {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("reading the passphrase: %w", err)
		}
		return trimPassphraseFile(string(raw)), path, nil
	}
	return "", "", fmt.Errorf("no passphrase for %q: neither %s nor %s exists",
		ssid, staged, image)
}

// trimPassphraseFile removes one trailing line ending, which a text
// editor adds and which is not part of the passphrase. It trims nothing
// else because a trailing space is a valid passphrase character, and
// trimming it would change the key.
func trimPassphraseFile(raw string) string {
	trimmed := strings.TrimSuffix(raw, "\n")
	return strings.TrimSuffix(trimmed, "\r")
}

// checkPassphrase returns an error naming the rule that a passphrase
// breaks: the WPA2 length range, or printable ASCII. A byte outside
// printable ASCII in a passphrase file is nearly always a stray line
// ending or an encoding error.
func checkPassphrase(ssid, passphrase string) error {
	if n := len(passphrase); n < minPassphraseBytes || n > maxPassphraseBytes {
		return fmt.Errorf("the passphrase for %q is %d bytes, and a WPA2 passphrase is %d to %d printable characters",
			ssid, n, minPassphraseBytes, maxPassphraseBytes)
	}
	for i := 0; i < len(passphrase); i++ {
		if b := passphrase[i]; b < 0x20 || b > 0x7e {
			return fmt.Errorf("the passphrase for %q has the byte %#02x at position %d, and a WPA2 passphrase has printable characters only",
				ssid, b, i)
		}
	}
	return nil
}

// derivePMK returns the pairwise master key that the WPA2 four-way
// handshake uses, as 64 hex digits, which wpa_supplicant accepts in
// place of the passphrase. The 802.11 derivation is PBKDF2 with
// HMAC-SHA1, 4096 rounds, and the SSID as the salt, so a change to
// either the SSID or the passphrase changes the key.
func derivePMK(ssid, passphrase string) string {
	key, err := pbkdf2.Key(sha1.New, passphrase, []byte(ssid), 4096, 32)
	if err != nil {
		// Key fails only in Go's FIPS 140-only mode, which refuses
		// SHA-1, and init does not enable that mode. An empty psk
		// makes the supplicant refuse the file.
		return ""
	}
	return hex.EncodeToString(key)
}

// wirelessBackgroundScan is the bgscan value for every network:
// module:short interval in seconds:signal threshold in dBm:long
// interval in seconds.
const wirelessBackgroundScan = "simple:30:-70:3600"

// wirelessConfig returns the wpa_supplicant configuration for one
// interface. It returns an error when the passphrase is missing or
// invalid. joinWireless records that error as WrongKey, the same state
// as a passphrase the access point refused, because a retry fixes
// neither.
func wirelessConfig(w machine.WirelessSpec, stateRoot, ctrlDir string) (string, error) {
	var b strings.Builder

	// The control socket directory is set here and not with the -C
	// flag. The flag overrides this line, and wpa_supplicant's help
	// text describes that override backwards.
	fmt.Fprintf(&b, "ctrl_interface=%s\n", ctrlDir)
	b.WriteString("network={\n")

	fmt.Fprintf(&b, "\tssid=%s\n", hex.EncodeToString([]byte(w.SSID)))

	// Without bgscan the radio leaves an access point only when the
	// link drops, so a radio that joined a distant access point stays
	// on it after a nearer one comes back. The simple module scans
	// every 30 seconds while the signal is below -70 dBm and once an
	// hour above it, and moves to a stronger access point of the same
	// network when a scan finds one.
	fmt.Fprintf(&b, "\tbgscan=%q\n", wirelessBackgroundScan)

	if w.SecurityOrDefault() == machine.WirelessOpen {
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

	// Access points offer WPA2, WPA3, or both, and the supplicant picks
	// the strongest type that the access point advertises, so every
	// type is listed. FT-PSK and FT-SAE are the 802.11r fast transition
	// forms, which let the radio move between access points of one
	// network without a full handshake. An access point without 802.11r
	// does not advertise them.
	b.WriteString("\tkey_mgmt=WPA-PSK WPA-PSK-SHA256 SAE FT-PSK FT-SAE\n")

	// ieee80211w=1 makes protected management frames optional. WPA3
	// requires them, and some WPA2 access points do not support them,
	// so neither 0 nor 2 joins both.
	b.WriteString("\tieee80211w=1\n")

	// WPA2 uses the derived key in psk. SAE needs the passphrase
	// itself, in sae_password.
	fmt.Fprintf(&b, "\tpsk=%s\n", derivePMK(w.SSID, passphrase))
	fmt.Fprintf(&b, "\tsae_password=%s\n", hex.EncodeToString([]byte(passphrase)))

	b.WriteString("}\n")
	return b.String(), nil
}
