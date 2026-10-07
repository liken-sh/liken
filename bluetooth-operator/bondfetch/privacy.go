package main

// The radio's privacy setting and its identity key, which bluetoothd
// reads once at start.
//
// A person turns Low Energy privacy on for one radio with the
// Adapter's spec.privacy. bluetoothd reads the Privacy key from its
// main.conf and sends it to the kernel when the adapter starts, and the
// kernel accepts it only while the radio is powered off. So the value
// has to be in place before bluetoothd starts, and this program is the
// part of the pod that runs then and holds an API client and the
// radio's address. It writes the value into the pod's settings volume
// (settings.go), and start-bluetoothd writes main.conf from it.
//
// The settings file also tells the operator which value bluetoothd
// started with. The operator compares it with spec.privacy on each
// pass, and deletes its own pod when the two differ, so this program
// runs again and writes the new value.
//
// With privacy on, bluetoothd derives the radio's rotating address
// from the key in <adapter>/identity. This program writes that file
// back from its Secret, so the radio keeps the identity its bonded
// peers know (bonds/identity.go).

import (
	"errors"
	"fmt"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// privacyOff is BlueZ's default, and the value of an empty
// spec.privacy.
const privacyOff = "off"

// restoreIdentity writes the radio's identity file into the tree
// bluetoothd reads: the stored file when the radio has one, and a new
// random key when privacy is on and nothing is stored.
//
// bluetoothd makes a missing key itself (generate_and_write_irk in
// BlueZ's src/adapter.c), but it draws the random bytes through the
// kernel's AF_ALG socket. Ubuntu's kernel builds that socket's support
// as modules, and on a machine that does not load them bluetoothd logs
// "Failed to open crypto" and starts with privacy off. A key that this
// program writes needs no kernel crypto, and the operator stores it in
// the identity Secret on its first pass.
//
// A radio with privacy off and no stored file gets no file, because
// bluetoothd reads the key only when privacy is on. Any failure to
// read the Secret is an error, because a new key would replace the one
// that the radio's bonded peers hold.
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
