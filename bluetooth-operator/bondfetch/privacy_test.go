package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// The Secret that has the test adapter's identity file.
const (
	testIdentityPath = "/api/v1/namespaces/bluetooth/secrets/bluetooth-identity-04-4a-69-66-92-27"
	testIdentity     = "[General]\nIdentityResolvingKey=0123456789ABCDEF0123456789ABCDEF\n"
)

// The stored identity file is written where bluetoothd reads it, so the
// adapter keeps the identity its peers have stored.
func TestRestoreIdentityWritesTheStoredFile(t *testing.T) {
	secret := bonds.NewIdentitySecret("bluetooth", testAddress, []byte(testIdentity), bonds.Owner{})
	api := testAPI(t, apiObjects(t, map[string]any{testIdentityPath: secret}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root, "device"); err != nil {
		t.Fatalf("restoring the identity: %v", err)
	}

	identity, err := os.ReadFile(filepath.Join(root, "04:4A:69:66:92:27", "identity"))
	if err != nil {
		t.Fatalf("reading the identity file: %v", err)
	}
	if string(identity) != testIdentity {
		t.Errorf("identity = %q, want %q", identity, testIdentity)
	}
}

// With privacy off and no identity Secret, no file is written, because
// bluetoothd reads the key only when privacy is on.
func TestRestoreIdentityWritesNothingWithPrivacyOff(t *testing.T) {
	api := testAPI(t, apiObjects(t, map[string]any{}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root, "off"); err != nil {
		t.Fatalf("restoring the identity: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading the directory %s: %v", root, err)
	}
	if len(entries) != 0 {
		t.Errorf("restoreIdentity wrote %v for an adapter with no identity Secret, want no files", entries)
	}
}

// A failed read of the Secret is an error, not an adapter with no
// identity. Writing a new key would leave every bonded peer with a key
// for an identity the adapter no longer has.
func TestRestoreIdentityFailsWhenTheSecretCannotBeRead(t *testing.T) {
	api := testAPI(t, failingAPI())

	if err := restoreIdentity(api, "bluetooth", testAddress, t.TempDir(), "device"); err == nil {
		t.Fatal("restoreIdentity returned nil for an unreadable Secret, want an error")
	}
}

// With privacy on and no stored key, a new random key is written before
// bluetoothd starts, so bluetoothd does not create one through AF_ALG.
func TestRestoreIdentityWritesANewKeyWhenPrivacyIsOn(t *testing.T) {
	api := testAPI(t, apiObjects(t, map[string]any{}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root, "device"); err != nil {
		t.Fatalf("restoring the identity: %v", err)
	}

	identity, err := os.ReadFile(filepath.Join(root, "04:4A:69:66:92:27", "identity"))
	if err != nil {
		t.Fatalf("reading the identity file: %v", err)
	}
	if !regexp.MustCompile(`^\[General\]\nIdentityResolvingKey=[0-9a-f]{32}\n$`).Match(identity) {
		t.Errorf("identity = %q, want one 32-digit hex key under [General]", identity)
	}
}
