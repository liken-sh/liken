package main

// bondfetch sets up an adapter's Low Energy privacy before bluetoothd
// starts, because bluetoothd reads both the setting and the identity
// key only at start.
//
// spec.privacy on the Adapter becomes the Privacy key of bluetoothd's
// main.conf. bluetoothd sends it to the kernel when it starts the
// adapter, and the kernel accepts it only while the adapter is powered
// off. bondfetch runs before bluetoothd, writes spec.privacy into the
// pod's settings volume (settings.go), and start-bluetoothd writes
// main.conf from that file. The file also records the value that
// bluetoothd started with. When spec.privacy changes, the operator
// deletes its own pod, so bondfetch runs again with the new value.
//
// With privacy on, bluetoothd derives the adapter's rotating address
// from the key in <adapter>/identity. bondfetch restores that file from
// its Secret, so the adapter keeps the identity that its bonded peers
// have stored (bonds/identity.go).

import (
	"errors"
	"fmt"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// privacyOff is the value for an empty spec.privacy, and BlueZ's
// default.
const privacyOff = "off"

// restoreIdentity writes the adapter's identity file into the tree that
// bluetoothd reads. It writes the stored key when the Secret has one,
// and a new random key when privacy is on and no key is stored. With
// privacy off and no stored key, it writes nothing.
//
// bluetoothd creates a missing key itself (generate_and_write_irk in
// BlueZ's src/adapter.c), but it reads the random bytes from the
// kernel's AF_ALG socket. Ubuntu's kernel builds that support as
// modules, and on a machine that does not load them, bluetoothd logs
// "Failed to open crypto" and starts with privacy off. A key generated
// here needs no kernel crypto, and the operator copies it into the
// identity Secret on its first pass.
//
// An error other than not found from reading the Secret fails the
// restore, because a new key would replace the one that the bonded
// peers have.
func restoreIdentity(api *apiclient.Client, namespace string, adapter bonds.Address, root, privacy string) error {
	secret, err := apiclient.Get[bonds.Secret](api, bonds.IdentitySecretPath(namespace, adapter))
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		return fmt.Errorf("reading the identity of %s: %w", adapter, err)
	}
	var identity []byte
	if err == nil {
		identity = secret.Identity()
	}
	if len(identity) > 0 {
		if err := bonds.WriteIdentity(root, adapter, identity); err != nil {
			return fmt.Errorf("writing the identity of %s under %s: %w", adapter, root, err)
		}
		fmt.Printf("bondfetch: restored the identity of %s\n", adapter)
		return nil
	}
	if privacy == privacyOff {
		return nil
	}
	identity, err = bonds.NewIdentity()
	if err != nil {
		return fmt.Errorf("making an identity key for %s: %w", adapter, err)
	}
	if err := bonds.WriteIdentity(root, adapter, identity); err != nil {
		return fmt.Errorf("writing the identity of %s under %s: %w", adapter, root, err)
	}
	fmt.Printf("bondfetch: wrote a new identity key for %s, because privacy is %s and none is stored\n", adapter, privacy)
	return nil
}
