package main

// Several tests use network names and passphrases that hold quotes,
// spaces, comment characters, and newlines, and check that none of
// them adds a line to the generated configuration.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// passphraseFiles points the image passphrase directory at a temporary
// directory and returns an empty temporary machineState root. A test
// writes a passphrase file into either one.
func passphraseFiles(t *testing.T) (stateRoot string) {
	t.Helper()
	stateRoot = t.TempDir()
	image := t.TempDir()
	orig := imagePassphraseDir
	imagePassphraseDir = image
	t.Cleanup(func() { imagePassphraseDir = orig })
	return stateRoot
}

func writePassphrase(t *testing.T, dir, ssid, passphrase string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the passphrase directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ssid), []byte(passphrase), 0o600); err != nil {
		t.Fatalf("writing the passphrase file: %v", err)
	}
}

func TestPassphraseComesFromTheImageWhenNothingIsStaged(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "homenet", "correcthorse\n")

	got, source, err := readPassphrase(stateRoot, "homenet")
	if err != nil {
		t.Fatalf("reading the passphrase: %v", err)
	}
	if got != "correcthorse" {
		t.Errorf("the passphrase is %q, want \"correcthorse\"", got)
	}
	if !strings.HasPrefix(source, imagePassphraseDir) {
		t.Errorf("the source is %q, want a path under %s", source, imagePassphraseDir)
	}
}

func TestTheStagedPassphraseWinsOverTheImages(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "homenet", "theoldone")
	writePassphrase(t, stagedPassphraseDir(stateRoot), "homenet", "thenewone")

	got, source, err := readPassphrase(stateRoot, "homenet")
	if err != nil {
		t.Fatalf("reading the passphrase: %v", err)
	}
	if got != "thenewone" {
		t.Errorf("the passphrase is %q, want \"thenewone\"", got)
	}
	if !strings.HasPrefix(source, stateRoot) {
		t.Errorf("the source is %q, want a path under %s", source, stateRoot)
	}
}

func TestAMissingPassphraseNamesWhereOneBelongs(t *testing.T) {
	// The machine has no shell, so the message is the whole diagnosis.
	stateRoot := passphraseFiles(t)
	_, _, err := readPassphrase(stateRoot, "homenet")
	if err == nil {
		t.Fatal("readPassphrase returned no error, want one for a network with no passphrase file")
	}
	for _, want := range []string{"homenet", imagePassphraseDir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to contain %q", err, want)
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
		if err := checkPassphrase("homenet", passphrase); err == nil {
			t.Errorf("checkPassphrase accepted a passphrase with %s, want an error", name)
		}
	}
}

func TestCheckPassphraseAcceptsEveryPrintableCharacter(t *testing.T) {
	// A WPA2 passphrase can contain a quote, a backslash, a comment
	// character, and a space.
	for _, passphrase := range []string{
		`say "hello"`, `back\slash`, "hash#mark", "with space", "8charact",
		strings.Repeat("x", 63),
	} {
		if err := checkPassphrase("homenet", passphrase); err != nil {
			t.Errorf("checkPassphrase refused %q: %v, want it accepted", passphrase, err)
		}
	}
}

func TestDerivePMKMatchesTheStandardsOwnTestVector(t *testing.T) {
	// The IEEE 802.11 test vector for the passphrase "password" on the
	// network "IEEE". A wrong derivation shows on a machine only as an
	// access point that refuses the key.
	const want = "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e"
	if got := derivePMK("IEEE", "password"); got != want {
		t.Errorf("derivePMK is %q, want %q", got, want)
	}
}

// wpaPSK is the spec a manifest writes for a home network.
func wpaPSK(ssid string) machine.WirelessSpec {
	return machine.WirelessSpec{SSID: ssid, Security: machine.WirelessWPAPSK}
}

func TestWirelessConfigJoinsWPA2AndWPA3AtOnce(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "homenet", "correcthorse\n")

	got, err := wirelessConfig(wpaPSK("homenet"), stateRoot, "/run/liken/wireless/wlan0/ctrl")
	if err != nil {
		t.Fatalf("generating the configuration: %v", err)
	}
	for _, want := range []string{
		"ctrl_interface=/run/liken/wireless/wlan0/ctrl\n",
		"\tssid=686f6d656e6574\n",
		"\tbgscan=\"simple:30:-70:3600\"\n",
		"\tkey_mgmt=WPA-PSK WPA-PSK-SHA256 SAE FT-PSK FT-SAE\n",
		"\tieee80211w=1\n",
		"\tpsk=" + derivePMK("homenet", "correcthorse") + "\n",
		"\tsae_password=636f7272656374686f727365\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the configuration lacks %q:\n%s", want, got)
		}
	}
}

func TestWirelessConfigForAnOpenNetworkHasNoKey(t *testing.T) {
	stateRoot := passphraseFiles(t)
	got, err := wirelessConfig(machine.WirelessSpec{SSID: "guest", Security: machine.WirelessOpen},
		stateRoot, "/run/liken/wireless/wlan0/ctrl")
	if err != nil {
		t.Fatalf("generating the configuration: %v", err)
	}
	if !strings.Contains(got, "\tkey_mgmt=NONE\n") {
		t.Errorf("the configuration of an open network has no key_mgmt=NONE, want it:\n%s", got)
	}
	if !strings.Contains(got, "\tbgscan=\"simple:30:-70:3600\"\n") {
		t.Errorf("the configuration of an open network has no bgscan, want bgscan=\"simple:30:-70:3600\":\n%s", got)
	}
	for _, unwanted := range []string{"psk=", "sae_password="} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the configuration of an open network has %s, want none:\n%s", unwanted, got)
		}
	}
}

func TestWirelessConfigWritesEveryValueAsHex(t *testing.T) {
	// No value line in the generated file contains a character that
	// the parser reads as syntax.
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, `a "b" #c`, `say "hi" # now\n`)

	got, err := wirelessConfig(wpaPSK(`a "b" #c`), stateRoot, "/run/liken/wireless/wlan0/ctrl")
	if err != nil {
		t.Fatalf("generating the configuration: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		_, value, found := strings.Cut(line, "=")
		if !found || strings.HasPrefix(line, "ctrl_interface") || strings.HasPrefix(line, "\tkey_mgmt") ||
			strings.HasPrefix(line, "\tbgscan") {
			continue
		}
		if strings.ContainsAny(value, `"# `) {
			t.Errorf("the value line %q has a quote, a hash, or a space, want hex", line)
		}
	}
}

func TestWirelessConfigCountsTheLinesItMeansTo(t *testing.T) {
	// A passphrase that wrote a line of its own would add a line here.
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "homenet", "aaaaaaaa")
	plain, err := wirelessConfig(wpaPSK("homenet"), stateRoot, "/ctrl")
	if err != nil {
		t.Fatalf("generating the configuration: %v", err)
	}
	writePassphrase(t, imagePassphraseDir, "homenet", `a" b`+"\n\tkey_mgmt=NONE")
	injected, cerr := wirelessConfig(wpaPSK("homenet"), stateRoot, "/ctrl")
	if cerr == nil {
		t.Fatalf("wirelessConfig accepted a passphrase with a newline, want an error:\n%s", injected)
	}
	if strings.Count(plain, "\n") != 9 {
		t.Errorf("the configuration has %d lines, want 9:\n%s", strings.Count(plain, "\n"), plain)
	}
}

func TestWirelessConfigRefusesAPassphraseItCannotRender(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "homenet", "short\n")
	if _, err := wirelessConfig(wpaPSK("homenet"), stateRoot, "/ctrl"); err == nil {
		t.Fatal("wirelessConfig returned no error, want one for a passphrase of 5 bytes")
	}
}

func TestWirelessConfigRefusesWhenNoPassphraseFileExists(t *testing.T) {
	stateRoot := passphraseFiles(t)
	if _, err := wirelessConfig(wpaPSK("homenet"), stateRoot, "/ctrl"); err == nil {
		t.Fatal("wirelessConfig returned no error, want one for a wpa-psk network with no passphrase file")
	}
}

func TestWirelessConfigDefaultsToWPAPSKWhenTheSpecSaysNothing(t *testing.T) {
	stateRoot := passphraseFiles(t)
	if _, err := wirelessConfig(machine.WirelessSpec{SSID: "homenet"}, stateRoot, "/ctrl"); err == nil {
		t.Fatal("wirelessConfig returned no error, want one: an unset security field means wpa-psk, which needs a passphrase")
	}
}

// TestWirelessConfigDumpsForTheVendoredSupplicant writes each generated
// configuration into the directory in LIKEN_WPA_DUMP. Run the vendored
// wpa_supplicant on a file with no interface, and it parses the file
// and exits, which checks the file against its real parser.
func TestWirelessConfigDumpsForTheVendoredSupplicant(t *testing.T) {
	dir := os.Getenv("LIKEN_WPA_DUMP")
	if dir == "" {
		t.Skip("set LIKEN_WPA_DUMP to write the generated configurations out")
	}
	stateRoot := passphraseFiles(t)
	for name, spec := range map[string]machine.WirelessSpec{
		"psk":     wpaPSK("homenet"),
		"open":    {SSID: "guest", Security: machine.WirelessOpen},
		"awkward": wpaPSK(`a "b" #c`),
		"unicode": wpaPSK("café ☕"),
	} {
		writePassphrase(t, imagePassphraseDir, spec.SSID, `say "hi" # now\`+"\n")
		config, err := wirelessConfig(spec, stateRoot, "/run/liken/wireless/wlan0/ctrl")
		if err != nil {
			t.Fatalf("generating the %s configuration: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(config), 0o600); err != nil {
			t.Fatalf("writing the %s configuration: %v", name, err)
		}
	}
}
