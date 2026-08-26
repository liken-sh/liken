package main

// The injection cases are the point of this file. The supplicant
// treats an unknown line as a fatal error and a recognized one as an
// instruction, so a network name or a passphrase that could write a
// line of its own could turn the encryption off. Every value the
// generator writes must be unable to hold a quote, a space, a comment
// character, or a newline, and these tests hold that rule against the
// worst names they can spell.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// passphraseFiles points both passphrase homes at directories of their
// own and returns the machineState root. A test writes into either one
// to say where a passphrase lives.
func passphraseFiles(t *testing.T) (stateRoot string) {
	t.Helper()
	stateRoot = t.TempDir()
	image := t.TempDir()
	orig := imagePassphraseDir
	imagePassphraseDir = image
	t.Cleanup(func() { imagePassphraseDir = orig })
	return stateRoot
}

// writePassphrase puts one passphrase file in one of the two homes.
func writePassphrase(t *testing.T, dir, ssid, passphrase string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ssid), []byte(passphrase), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPassphraseComesFromTheImageWhenNothingIsStaged(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "stonypoint", "correcthorse\n")

	got, source, err := readPassphrase(stateRoot, "stonypoint")
	if err != nil {
		t.Fatal(err)
	}
	if got != "correcthorse" {
		t.Errorf("passphrase = %q", got)
	}
	if !strings.HasPrefix(source, imagePassphraseDir) {
		t.Errorf("source = %q, want the image's copy", source)
	}
}

func TestTheStagedPassphraseWinsOverTheImages(t *testing.T) {
	// This is the read order every other document already resolves in.
	// Nothing writes the staged copy today; rotation becomes a writer
	// of it, not a change to this order.
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "stonypoint", "theoldone")
	writePassphrase(t, stagedPassphraseDir(stateRoot), "stonypoint", "thenewone")

	got, source, err := readPassphrase(stateRoot, "stonypoint")
	if err != nil {
		t.Fatal(err)
	}
	if got != "thenewone" {
		t.Errorf("passphrase = %q", got)
	}
	if !strings.HasPrefix(source, stateRoot) {
		t.Errorf("source = %q, want the staged copy", source)
	}
}

func TestAMissingPassphraseNamesWhereOneBelongs(t *testing.T) {
	// The machine has no shell, so the message is the whole diagnosis.
	stateRoot := passphraseFiles(t)
	_, _, err := readPassphrase(stateRoot, "stonypoint")
	if err == nil {
		t.Fatal("a network with no passphrase file must refuse")
	}
	for _, want := range []string{"stonypoint", imagePassphraseDir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must carry %q: %v", want, err)
		}
	}
}

func TestTrimPassphraseFileRemovesOneLineEndingAndNothingElse(t *testing.T) {
	for raw, want := range map[string]string{
		"password\n":       "password",
		"password\r\n":     "password",
		"password":         "password",
		"trailing space  ": "trailing space  ",
		"password\n\n":     "password\n",
	} {
		if got := trimPassphraseFile(raw); got != want {
			t.Errorf("trimPassphraseFile(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestCheckPassphraseRefusesWhatNoAccessPointCouldHold(t *testing.T) {
	for name, passphrase := range map[string]string{
		"too short":     "short",
		"too long":      strings.Repeat("x", 64),
		"empty":         "",
		"a newline":     "pass\nword",
		"a carriage":    "pass\rword",
		"a NUL":         "pass\x00word",
		"a tab":         "pass\tword",
		"a byte over 7": "passw\xffrd",
	} {
		if err := checkPassphrase("stonypoint", passphrase); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
}

func TestCheckPassphraseAcceptsEveryPrintableCharacter(t *testing.T) {
	// A quote, a backslash, a comment character, and a space are all
	// legal in a WPA2 passphrase, and the generated file must be able
	// to carry every one of them.
	for _, passphrase := range []string{
		`say "hello"`, `back\slash`, "hash#mark", "with space", "8charact",
		strings.Repeat("x", 63),
	} {
		if err := checkPassphrase("stonypoint", passphrase); err != nil {
			t.Errorf("%q: %v", passphrase, err)
		}
	}
}

func TestDerivePMKMatchesTheStandardsOwnTestVector(t *testing.T) {
	// IEEE 802.11's reference vector for the passphrase "password" on
	// the network "IEEE". Getting this wrong produces a machine that
	// joins nothing and says only that the key was refused.
	const want = "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e"
	if got := derivePMK("IEEE", "password"); got != want {
		t.Errorf("derivePMK = %q, want %q", got, want)
	}
}

// wpaPSK is the spec a manifest writes for a home network.
func wpaPSK(ssid string) machine.WirelessSpec {
	return machine.WirelessSpec{SSID: ssid, Security: machine.WirelessWPAPSK}
}

func TestWirelessConfigJoinsWPA2AndWPA3AtOnce(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "stonypoint", "correcthorse\n")

	got, err := wirelessConfig(wpaPSK("stonypoint"), stateRoot, "/run/liken/wireless/wlan0/ctrl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ctrl_interface=/run/liken/wireless/wlan0/ctrl\n",
		"\tssid=73746f6e79706f696e74\n",
		"\tkey_mgmt=WPA-PSK WPA-PSK-SHA256 SAE\n",
		"\tieee80211w=1\n",
		"\tpsk=" + derivePMK("stonypoint", "correcthorse") + "\n",
		"\tsae_password=636f7272656374686f727365\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the configuration must carry %q:\n%s", want, got)
		}
	}
}

func TestWirelessConfigForAnOpenNetworkCarriesNoKey(t *testing.T) {
	stateRoot := passphraseFiles(t)
	got, err := wirelessConfig(machine.WirelessSpec{SSID: "guest", Security: machine.WirelessOpen},
		stateRoot, "/run/liken/wireless/wlan0/ctrl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\tkey_mgmt=NONE\n") {
		t.Errorf("an open network takes no key: %s", got)
	}
	for _, unwanted := range []string{"psk=", "sae_password="} {
		if strings.Contains(got, unwanted) {
			t.Errorf("an open network must carry no %s: %s", unwanted, got)
		}
	}
}

func TestWirelessConfigWritesEveryValueAsHex(t *testing.T) {
	// The injection rule, checked by its consequence rather than by
	// naming the characters: no value line in the generated file holds
	// a character that the parser reads as syntax.
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, `a "b" #c`, `say "hi" # now\n`)

	got, err := wirelessConfig(wpaPSK(`a "b" #c`), stateRoot, "/run/liken/wireless/wlan0/ctrl")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		_, value, found := strings.Cut(line, "=")
		if !found || strings.HasPrefix(line, "ctrl_interface") || strings.HasPrefix(line, "\tkey_mgmt") {
			continue
		}
		if strings.ContainsAny(value, `"# `) {
			t.Errorf("value line carries syntax: %q", line)
		}
	}
}

func TestWirelessConfigCountsTheLinesItMeansTo(t *testing.T) {
	// A passphrase that could write a line of its own would show up
	// here as an extra line, whatever it said.
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "stonypoint", "aaaaaaaa")
	plain, err := wirelessConfig(wpaPSK("stonypoint"), stateRoot, "/ctrl")
	if err != nil {
		t.Fatal(err)
	}
	writePassphrase(t, imagePassphraseDir, "stonypoint", `a" b`+"\n\tkey_mgmt=NONE")
	injected, cerr := wirelessConfig(wpaPSK("stonypoint"), stateRoot, "/ctrl")
	if cerr == nil {
		t.Fatalf("a passphrase holding a newline must be refused:\n%s", injected)
	}
	if strings.Count(plain, "\n") != 8 {
		t.Errorf("the configuration is %d lines:\n%s", strings.Count(plain, "\n"), plain)
	}
}

func TestWirelessConfigRefusesAPassphraseItCannotRender(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "stonypoint", "short\n")
	if _, err := wirelessConfig(wpaPSK("stonypoint"), stateRoot, "/ctrl"); err == nil {
		t.Fatal("a passphrase WPA2 cannot hold must be refused")
	}
}

func TestWirelessConfigRefusesWhenNoPassphraseFileExists(t *testing.T) {
	stateRoot := passphraseFiles(t)
	if _, err := wirelessConfig(wpaPSK("stonypoint"), stateRoot, "/ctrl"); err == nil {
		t.Fatal("a wpa-psk network with no passphrase must be refused")
	}
}

func TestWirelessConfigDefaultsToWPAPSKWhenTheSpecSaysNothing(t *testing.T) {
	// An unset security field means wpa-psk, the common case, so an
	// unset field must reach the passphrase and not the open path.
	stateRoot := passphraseFiles(t)
	if _, err := wirelessConfig(machine.WirelessSpec{SSID: "stonypoint"}, stateRoot, "/ctrl"); err == nil {
		t.Fatal("an unset security field means wpa-psk, which needs a passphrase")
	}
}

// TestWirelessConfigDumpsForTheVendoredSupplicant writes the generated
// configuration where the LIKEN_WPA_DUMP environment variable points, so
// a person can run the vendored supplicant's own parser over it. The
// supplicant validates a file and exits when it is given no interface,
// which is the only exact check of this grammar that exists.
func TestWirelessConfigDumpsForTheVendoredSupplicant(t *testing.T) {
	dir := os.Getenv("LIKEN_WPA_DUMP")
	if dir == "" {
		t.Skip("set LIKEN_WPA_DUMP to write the generated configurations out")
	}
	stateRoot := passphraseFiles(t)
	for name, spec := range map[string]machine.WirelessSpec{
		"psk":     wpaPSK("stonypoint"),
		"open":    {SSID: "guest", Security: machine.WirelessOpen},
		"awkward": wpaPSK(`a "b" #c`),
		"unicode": wpaPSK("café ☕"),
	} {
		writePassphrase(t, imagePassphraseDir, spec.SSID, `say "hi" # now\`+"\n")
		config, err := wirelessConfig(spec, stateRoot, "/run/liken/wireless/wlan0/ctrl")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
