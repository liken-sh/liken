package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// The Secret that holds the test radio's identity file.
const (
	testIdentityPath = "/api/v1/namespaces/bluetooth/secrets/bluetooth-identity-04-4a-69-66-92-27"
	testIdentity     = "[General]\nIdentityResolvingKey=0123456789ABCDEF0123456789ABCDEF\n"
)

// The stored identity file goes back where bluetoothd reads it, so the
// radio presents the identity its peers already know.
func TestRestoreIdentityWritesTheStoredFile(t *testing.T) {
	secret := bonds.NewIdentitySecret("bluetooth", testAddress, []byte(testIdentity), bonds.Owner{})
	api := testAPI(t, apiObjects(t, map[string]any{testIdentityPath: secret}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root, "device"); err != nil {
		t.Fatalf("restoreIdentity: %v", err)
	}

	identity, err := os.ReadFile(filepath.Join(root, "04:4A:69:66:92:27", "identity"))
	if err != nil {
		t.Fatalf("reading the identity file: %v", err)
	}
	if string(identity) != testIdentity {
		t.Errorf("identity = %q, want %q", identity, testIdentity)
	}
}

// A radio with privacy off and no identity Secret gets no file, because
// bluetoothd reads the key only when privacy is on.
func TestRestoreIdentityWritesNothingWithPrivacyOff(t *testing.T) {
	api := testAPI(t, apiObjects(t, map[string]any{}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root, "off"); err != nil {
		t.Fatalf("restoreIdentity: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("it wrote %v for a radio with no identity Secret", entries)
	}
}

// A read that fails is not a radio with no identity. bluetoothd would
// write a new key, and every bonded peer would hold a key for an
// identity the radio does not present.
func TestRestoreIdentityFailsWhenTheSecretCannotBeRead(t *testing.T) {
	api := testAPI(t, failingAPI())

	if err := restoreIdentity(api, "bluetooth", testAddress, t.TempDir(), "device"); err == nil {
		t.Fatal("restoreIdentity reported success for a Secret it could not read")
	}
}

// A radio with privacy on and no stored key gets a new random key
// before bluetoothd starts. bluetoothd would make the key itself
// through the kernel's AF_ALG socket, which needs crypto modules that a
// machine may not load, and then it starts with privacy off.
func TestRestoreIdentityWritesANewKeyWhenPrivacyIsOn(t *testing.T) {
	api := testAPI(t, apiObjects(t, map[string]any{}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root, "device"); err != nil {
		t.Fatalf("restoreIdentity: %v", err)
	}

	identity, err := os.ReadFile(filepath.Join(root, "04:4A:69:66:92:27", "identity"))
	if err != nil {
		t.Fatalf("reading the identity file: %v", err)
	}
	if !regexp.MustCompile(`^\[General\]\nIdentityResolvingKey=[0-9a-f]{32}\n$`).Match(identity) {
		t.Errorf("identity = %q, want one 32-digit hex key under [General]", identity)
	}
}
