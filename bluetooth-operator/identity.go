package main

// Writing the radio's identity file into its own Secret.
//
// With privacy on, bluetoothd derives the radio's rotating address from
// the identity resolving key in <adapter>/identity, and every bonded
// peer holds a copy of that key. The pod's tree is an emptyDir, so the
// store pass writes the file into the Secret
// bluetooth-identity-<adapter>, and bondfetch writes it back before the
// next bluetoothd starts (bonds/identity.go). A reinstall or a radio
// moved to another machine then keeps the identity its peers know.
//
// bluetoothd writes the file once, the first time it starts with
// privacy on and finds no key. So the Secret is written about once in
// the radio's life. The store keeps the copy it last wrote or read, and
// compares the file with that copy on each pass. A pass that finds no
// change sends no request, and the Secret needs no watch.
//
// The Adapter owns the Secret, so deleting the Adapter collects the
// key with the bonds. The store never deletes the Secret. A radio with
// privacy off has no use for the key, and keeps it, so a radio that
// turns privacy on again presents the identity its peers already know.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// persistIdentity writes the adapter's identity file into its Secret
// when the two differ. It reports whether the key is stored, or the
// radio has none. adapter is the Adapter this pass reconciled, and nil
// when the pass did not read one.
func (s *bondStore) persistIdentity(adapter *Adapter) bool {
	if s.adapter.IsZero() {
		// The bond pass of the same pass learns the radio, and it reports
		// the failure when it cannot.
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
		// The Secret needs its owner's UID, and a later pass has it.
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
		fmt.Fprintf(os.Stderr, "reading %s: %v\n", name, err)
		return false
	case bytes.Equal(current.Identity(), identity):
		s.identity = identity
		return true
	default:
		// The write states the version it read, so a second writer gets
		// a conflict, and the next pass reads the Secret again.
		secret.Metadata.ResourceVersion = current.Metadata.ResourceVersion
	}

	body, err := json.Marshal(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding %s: %v\n", name, err)
		return false
	}
	if err := s.client.RequestJSON(method, target, body, nil); err != nil {
		fmt.Fprintf(os.Stderr, "writing %s: %v\n", name, err)
		return false
	}
	s.identity = identity
	fmt.Printf("bonds: wrote %s for the identity of %s\n", name, s.adapter)
	return true
}
