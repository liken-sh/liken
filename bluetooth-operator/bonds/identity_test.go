package bonds

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// testIdentity is the contents of the identity file of a radio that
// has privacy on.
const testIdentity = "[General]\nIdentityResolvingKey=0123456789ABCDEF0123456789ABCDEF\n"

// A radio that has never had privacy on has no identity file, and
// reading it is not an error.
func TestReadIdentityReturnsNothingForARadioWithNoFile(t *testing.T) {
	identity, err := ReadIdentity(t.TempDir(), address(t, testAdapter))
	if err != nil {
		t.Fatalf("reading the identity: %v", err)
	}
	if identity != nil {
		t.Errorf("identity is %q, want none", identity)
	}
}

// WriteIdentity writes the file where bluetoothd's load_irk reads it,
// with the owner-only mode that BlueZ uses for key files.
func TestWriteIdentityWritesTheFileBlueZReads(t *testing.T) {
	root := t.TempDir()
	adapter := address(t, testAdapter)

	if err := WriteIdentity(root, adapter, []byte(testIdentity)); err != nil {
		t.Fatalf("writing the identity: %v", err)
	}

	path := filepath.Join(root, "04:4A:69:66:92:27", "identity")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the mode of %s: %v", path, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode is %v, want 0600", info.Mode().Perm())
	}
	identity, err := ReadIdentity(root, adapter)
	if err != nil {
		t.Fatalf("reading the identity: %v", err)
	}
	if string(identity) != testIdentity {
		t.Errorf("identity is %q, want %q", identity, testIdentity)
	}
}

// The identity Secret is named for the radio and owned by its Adapter,
// so deleting the Adapter deletes the key along with the bonds.
func TestNewIdentitySecretIsNamedForTheRadioAndOwnedByItsAdapter(t *testing.T) {
	owner := Owner{APIVersion: "bluetooth.liken.sh/v1alpha1", Kind: "Adapter", Name: "04-4a-69-66-92-27", UID: "uid-adapter"}

	secret := NewIdentitySecret("bluetooth", address(t, testAdapter), []byte(testIdentity), owner)

	if secret.Metadata.Name != "bluetooth-identity-04-4a-69-66-92-27" {
		t.Errorf("Secret name is %q, want bluetooth-identity-04-4a-69-66-92-27", secret.Metadata.Name)
	}
	if len(secret.Metadata.OwnerReferences) != 1 || secret.Metadata.OwnerReferences[0] != owner {
		t.Errorf("ownerReferences are %+v, want only %+v", secret.Metadata.OwnerReferences, owner)
	}
	if string(secret.Identity()) != testIdentity {
		t.Errorf("Secret has identity %q, want %q", secret.Identity(), testIdentity)
	}
	if want := "/api/v1/namespaces/bluetooth/secrets/bluetooth-identity-04-4a-69-66-92-27"; IdentitySecretPath("bluetooth", address(t, testAdapter)) != want {
		t.Errorf("IdentitySecretPath is %q, want %q", IdentitySecretPath("bluetooth", address(t, testAdapter)), want)
	}
}

// bondfetch and the operator select a radio's bonds by AdapterLabel,
// so the identity Secret must not have that label or parse as a bond.
func TestTheIdentitySecretIsNeverABond(t *testing.T) {
	adapter := address(t, testAdapter)

	secret := NewIdentitySecret("bluetooth", adapter, []byte(testIdentity), Owner{})

	if value, found := secret.Metadata.Labels[AdapterLabel]; found {
		t.Errorf("labels are %v, and the selector %q matches them because %q is %q", secret.Metadata.Labels, AdapterSelector(adapter), AdapterLabel, value)
	}
	if secret.OneBond() {
		t.Error("OneBond is true for the identity Secret, want false")
	}
	if tree := secret.Tree(); len(tree) != 0 {
		t.Errorf("Tree is %v, want no bonds", tree)
	}
}

// Each new identity has its own random key, in the format that
// load_irk reads.
func TestNewIdentityReturnsANewKeyInTheFormBlueZReads(t *testing.T) {
	first, err := NewIdentity()
	if err != nil {
		t.Fatalf("creating the first identity: %v", err)
	}
	second, err := NewIdentity()
	if err != nil {
		t.Fatalf("creating the second identity: %v", err)
	}
	form := regexp.MustCompile(`^\[General\]\nIdentityResolvingKey=[0-9a-f]{32}\n$`)
	if !form.Match(first) || !form.Match(second) {
		t.Errorf("identities are %q and %q, want each to contain a 32-digit hex key under [General]", first, second)
	}
	if bytes.Equal(first, second) {
		t.Errorf("two new identities contain the same key %q, want different keys", first)
	}
}
