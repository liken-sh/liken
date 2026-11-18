package main

// With privacy on, bluetoothd derives the radio's rotating address from
// the identity resolving key in <adapter>/identity, and every bonded
// peer has a copy of that key. The pod's BlueZ tree is an emptyDir, so
// persistIdentity copies the file into the Secret
// bluetooth-identity-<adapter>, and bondfetch writes it back before
// bluetoothd starts (bondfetch/privacy.go). After a reinstall, or when
// the radio moves to another machine, its peers still recognize it.
//
// bondfetch writes a new key only when privacy is on and no key is
// stored, so the Secret changes rarely. The store caches the contents
// it last wrote or read and compares the file with them on each pass,
// and sends no request while they match. So the Secret needs no watch.
//
// The Adapter owns the Secret, and the Secret is deleted with it. The
// store never deletes the Secret, so a radio that turns privacy off and
// then on again keeps the identity that its peers have stored.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// persistIdentity writes the identity file into the Secret when the
// contents differ. It returns false when the write must be tried again.
// adapter is the Adapter that this pass reconciled, or nil when the pass
// did not read one.
func (s *bondStore) persistIdentity(adapter *Adapter) bool {
	if s.adapter.IsZero() {
		// persist reads the adapter address earlier in the same pass,
		// and returns false when it cannot.
		return true
	}
	identity, err := bonds.ReadIdentity(s.root, s.adapter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the identity of %s: %v\n", s.adapter, err)
		return false
	}
	if identity == nil || (s.identity != nil && bytes.Equal(identity, s.identity)) {
		return true
	}
	if adapter == nil {
		// The Secret's owner reference needs the Adapter's UID.
		return false
	}

	name := bonds.IdentitySecretName(s.adapter)
	path := bonds.IdentitySecretPath(s.namespace, s.adapter)
	owner := bonds.Owner{APIVersion: pairingAPI, Kind: adapterKind, Name: adapter.Metadata.Name, UID: adapter.Metadata.UID}
	secret := bonds.NewIdentitySecret(s.namespace, s.adapter, identity, owner)

	current, err := apiclient.Get[bonds.Secret](s.client, path)
	method, target := http.MethodPut, path
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		method, target = http.MethodPost, bonds.SecretsPath(s.namespace)
	case err != nil:
		fmt.Fprintf(os.Stderr, "reading the Secret %s: %v\n", name, err)
		return false
	case bytes.Equal(current.Identity(), identity):
		s.identity = identity
		return true
	default:
		// With the resourceVersion that was read, a concurrent write
		// makes this update fail with a conflict, and the next pass
		// reads the Secret again.
		secret.Metadata.ResourceVersion = current.Metadata.ResourceVersion
	}

	body, err := json.Marshal(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding the Secret %s: %v\n", name, err)
		return false
	}
	if err := s.client.RequestJSON(method, target, body, nil); err != nil {
		fmt.Fprintf(os.Stderr, "writing the Secret %s: %v\n", name, err)
		return false
	}
	s.identity = identity
	fmt.Printf("bonds: wrote the Secret %s for the identity of %s\n", name, s.adapter)
	return true
}
