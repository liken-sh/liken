package bonds

// The adapter's own identity key, and the Secret that keeps it.
//
// With Low Energy privacy on, a radio advertises and connects from a
// resolvable private address that it changes every few minutes. The
// address comes from the radio's identity resolving key (IRK), and a
// peer that paired with the radio holds a copy of that key, so the
// peer resolves each new address back to the radio. bluetoothd keeps
// the key in <adapter>/identity, under [General]
// IdentityResolvingKey. load_irk in BlueZ's src/adapter.c reads it,
// and writes a new random key when the file has none.
//
// The pod's tree is an emptyDir, so without a stored copy each new pod
// writes a new key, and every bonded peer then holds a key for an
// identity the radio does not present. So the operator writes the
// file into its own Secret, and bondfetch writes it back before
// bluetoothd starts. A radio with privacy off has no file, because
// bluetoothd reads the key only when privacy is on.
//
// The Secret carries no adapter label. bondfetch and the operator
// gather a radio's bonds by that label, and this Secret holds no bond.
// It has an identity label of its own, so a person can list the
// identity Secrets, and its name states the radio, which is how both
// programs read it.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// identityFile is the adapter's own file that holds its IRK.
	identityFile = "identity"

	// IdentitySecretPrefix and the adapter's address name the Secret
	// that holds one radio's identity file.
	IdentitySecretPrefix = "bluetooth-identity-"

	// IdentityLabel names the radio whose identity a Secret holds. It
	// is a different key from AdapterLabel, so the bond selector never
	// matches it.
	IdentityLabel = "bluetooth.liken.sh/identity"

	// identityKey is the one key of the identity Secret. Its value is
	// the identity file, byte for byte.
	identityKey = "identity"
)

// ReadIdentity reads an adapter's identity file. An adapter with no
// file answers nil and no error, which is the state of every radio
// with privacy off.
func ReadIdentity(root string, adapter Address) ([]byte, error) {
	identity, err := os.ReadFile(filepath.Join(root, adapter.Directory(), identityFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return identity, err
}

// WriteIdentity writes an adapter's identity file into a BlueZ storage
// tree, with the modes BlueZ gives a file that holds a key.
func WriteIdentity(root string, adapter Address, identity []byte) error {
	directory := filepath.Join(root, adapter.Directory())
	if err := os.MkdirAll(directory, bondDirMode); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, identityFile), identity, bondFileMode)
}

// IdentitySecretName is the name of the Secret that holds one radio's
// identity file.
func IdentitySecretName(adapter Address) string {
	return IdentitySecretPrefix + adapter.Key()
}

// IdentitySecretPath is the API server's URL for one radio's identity
// Secret.
func IdentitySecretPath(namespace string, adapter Address) string {
	return SecretsPath(namespace) + "/" + IdentitySecretName(adapter)
}

// NewIdentitySecret builds the object the operator writes for one
// radio's identity file. The owner is the radio's Adapter.
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

// Identity answers the identity file an identity Secret holds.
func (s *Secret) Identity() []byte {
	return s.Data[identityKey]
}
