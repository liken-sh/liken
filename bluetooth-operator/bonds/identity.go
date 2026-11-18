package bonds

// The adapter's identity key, and the Secret that stores it.
//
// When Low Energy privacy is on, the radio advertises and connects
// from a resolvable private address that changes every few minutes.
// The address is derived from the radio's identity resolving key
// (IRK). A peer that paired with the radio keeps a copy of the key, and
// uses it to resolve each new address back to the radio. bluetoothd
// stores the key in <adapter>/identity, under [General]
// IdentityResolvingKey, and load_irk in BlueZ's src/adapter.c reads it.
// When the file has no key, bluetoothd generates one through the
// kernel's AF_ALG socket, which fails on a machine that does not load
// that socket's modules. So bondfetch writes a new key before
// bluetoothd starts (NewIdentity).
//
// The pod's storage tree is an emptyDir. Without a stored copy, each
// new pod would use a new key, and no bonded peer could resolve the
// radio's addresses. So the operator copies the file into a Secret,
// and bondfetch writes it back before bluetoothd starts. bondfetch
// generates a key only when privacy is on, so a radio that has never
// had privacy on has no identity file.
//
// bondfetch and the operator select a radio's bonds by AdapterLabel.
// The identity Secret has IdentityLabel instead, so that a bond
// selector never matches it. Both programs find it by its name, which
// contains the radio's address.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	identityFile = "identity"

	// IdentitySecretPrefix followed by the adapter's address is the
	// name of a radio's identity Secret.
	IdentitySecretPrefix = "bluetooth-identity-"

	// IdentityLabel is the label key whose value is the address of the
	// radio that an identity Secret belongs to.
	IdentityLabel = "bluetooth.liken.sh/identity"

	// identityKey is the key in the identity Secret's data whose value
	// is the identity file's bytes, unchanged.
	identityKey = "identity"
)

// ReadIdentity reads an adapter's identity file. It returns nil and no
// error when the file does not exist.
func ReadIdentity(root string, adapter Address) ([]byte, error) {
	identity, err := os.ReadFile(filepath.Join(root, adapter.Directory(), identityFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return identity, err
}

// NewIdentity returns the contents of an identity file with a new
// random key, in the format that load_irk reads: 32 lowercase hex
// digits under [General] IdentityResolvingKey, as BlueZ writes them.
func NewIdentity() ([]byte, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return []byte("[General]\nIdentityResolvingKey=" + hex.EncodeToString(key) + "\n"), nil
}

// WriteIdentity writes an adapter's identity file into a BlueZ storage
// tree, with the file and directory modes that BlueZ uses for key
// files.
func WriteIdentity(root string, adapter Address, identity []byte) error {
	directory := filepath.Join(root, adapter.Directory())
	if err := os.MkdirAll(directory, bondDirMode); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, identityFile), identity, bondFileMode)
}

func IdentitySecretName(adapter Address) string {
	return IdentitySecretPrefix + adapter.Key()
}

func IdentitySecretPath(namespace string, adapter Address) string {
	return SecretsPath(namespace) + "/" + IdentitySecretName(adapter)
}

// NewIdentitySecret builds a radio's identity Secret. The owner is the
// radio's Adapter, so deleting the Adapter deletes the Secret. An owner
// with no UID adds no owner reference.
func NewIdentitySecret(namespace string, adapter Address, identity []byte, owner Owner) *Secret {
	secret := &Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata: SecretMeta{
			Name:      IdentitySecretName(adapter),
			Namespace: namespace,
			Labels: map[string]string{
				nameLabel:     operatorName,
				IdentityLabel: adapter.Key(),
			},
		},
		Type: SecretType,
		Data: map[string][]byte{identityKey: identity},
	}
	if owner.UID != "" {
		secret.Metadata.OwnerReferences = []Owner{owner}
	}
	return secret
}

func (s *Secret) Identity() []byte {
	return s.Data[identityKey]
}
