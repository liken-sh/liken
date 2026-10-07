package bonds

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// testIdentity is an identity file as bluetoothd writes it when privacy
// is on and the adapter has no key yet.
const testIdentity = "[General]\nIdentityResolvingKey=0123456789ABCDEF0123456789ABCDEF\n"

// A radio with privacy off has no identity file, and that is not an
// error.
func TestReadIdentityAnswersNothingForARadioWithNoFile(t *testing.T) {
	identity, err := ReadIdentity(t.TempDir(), address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if identity != nil {
		t.Errorf("identity = %q, want none", identity)
	}
}

// The restore writes the file where bluetoothd's load_irk reads it,
// with the owner-only mode BlueZ gives every file that holds a key.
func TestWriteIdentityWritesTheFileBlueZReads(t *testing.T) {
	root := t.TempDir()
	adapter := address(t, testAdapter)

	if err := WriteIdentity(root, adapter, []byte(testIdentity)); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}

	path := filepath.Join(root, "04:4A:69:66:92:27", "identity")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	identity, err := ReadIdentity(root, adapter)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if string(identity) != testIdentity {
		t.Errorf("identity = %q, want %q", identity, testIdentity)
	}
}

// The identity Secret is named for the radio and owned by its Adapter,
// so deleting the Adapter collects the key with the bonds.
func TestNewIdentitySecretIsNamedForTheRadioAndOwnedByItsAdapter(t *testing.T) {
	owner := Owner{APIVersion: "bluetooth.liken.sh/v1alpha1", Kind: "Adapter", Name: "04-4a-69-66-92-27", UID: "uid-adapter"}

	secret := NewIdentitySecret("bluetooth", address(t, testAdapter), []byte(testIdentity), owner)

	if secret.Metadata.Name != "bluetooth-identity-04-4a-69-66-92-27" {
		t.Errorf("name = %q", secret.Metadata.Name)
	}
	if len(secret.Metadata.OwnerReferences) != 1 || secret.Metadata.OwnerReferences[0] != owner {
		t.Errorf("ownerReferences = %+v", secret.Metadata.OwnerReferences)
	}
	if string(secret.Identity()) != testIdentity {
		t.Errorf("Identity = %q", secret.Identity())
	}
	if want := "/api/v1/namespaces/bluetooth/secrets/bluetooth-identity-04-4a-69-66-92-27"; IdentitySecretPath("bluetooth", address(t, testAdapter)) != want {
		t.Errorf("IdentitySecretPath = %q, want %q", IdentitySecretPath("bluetooth", address(t, testAdapter)), want)
	}
}

// bondfetch and the operator gather a radio's bonds by the adapter
// label, so the identity Secret must not carry that label with the
// radio's value. Its data must not read as a bond either.
func TestTheIdentitySecretIsNeverABond(t *testing.T) {
	adapter := address(t, testAdapter)

	secret := NewIdentitySecret("bluetooth", adapter, []byte(testIdentity), Owner{})

	if value, found := secret.Metadata.Labels[AdapterLabel]; found {
		t.Errorf("labels = %v, which the selector %q matches when %q is %q", secret.Metadata.Labels, AdapterSelector(adapter), AdapterLabel, value)
	}
	if secret.OneBond() {
		t.Error("the identity Secret reads as one bond")
	}
	if tree := secret.Tree(); len(tree) != 0 {
		t.Errorf("Tree = %v, want no bonds", tree)
	}
}

// Each new identity holds its own random key, in the form load_irk
// reads, so two radios never share an identity.
func TestNewIdentityHoldsAFreshKeyInTheFormBlueZReads(t *testing.T) {
	first, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	form := regexp.MustCompile(`^\[General\]\nIdentityResolvingKey=[0-9a-f]{32}\n$`)
	if !form.Match(first) || !form.Match(second) {
		t.Errorf("identities %q and %q, want one 32-digit hex key under [General]", first, second)
	}
	if bytes.Equal(first, second) {
		t.Errorf("two new identities hold the same key %q", first)
	}
}
