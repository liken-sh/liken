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
// radio's address. It writes the value into the pod's settings volume,
// and start-bluetoothd writes main.conf from it.
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
	"os"
	"path/filepath"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	// defaultSettings is the pod's settings volume. start-bluetoothd
	// reads the privacy file in it, and the operator mounts it
	// read-only. settingsVar overrides the directory, the same way
	// rootVar overrides the bonds tree.
	settingsVar     = "BLUETOOTH_SETTINGS_ROOT"
	defaultSettings = "/var/run/bluetooth.liken.sh/settings"

	// privacyFile is the file in the settings volume that holds the
	// value of the Adapter's spec.privacy.
	privacyFile = "privacy"

	// adaptersPath is the collection of the operator's Adapters. An
	// Adapter is cluster-scoped and named for its radio's address.
	adaptersPath = "/apis/bluetooth.liken.sh/v1alpha1/adapters/"

	// privacyOff is BlueZ's default, and the value of an empty
	// spec.privacy.
	privacyOff = "off"
)

// adapterSpec is the part of an Adapter that this program reads. The
// API server's schema refuses any value outside BlueZ's five, and
// start-bluetoothd refuses one too, so this program passes the value
// on as it reads it.
type adapterSpec struct {
	Spec struct {
		Privacy string `json:"privacy,omitempty"`
	} `json:"spec"`
}

// writePrivacy reads the radio's Adapter and writes its spec.privacy
// into the settings volume.
//
// An Adapter that does not exist yet is off, because the operator
// creates it on its first pass, after bluetoothd has started. Any
// other failure to read it is an error, the same as a failure to read
// the bonds, so the pod stays in Init and bluetoothd does not start
// with a value that a person did not choose.
func writePrivacy(api *apiclient.Client, adapter bonds.Address, settings string) error {
	privacy := privacyOff
	object, err := apiclient.Get[adapterSpec](api, adaptersPath+adapter.Key())
	switch {
	case err == nil:
		if object.Spec.Privacy != "" {
			privacy = object.Spec.Privacy
		}
	case errors.Is(err, apiclient.ErrNotFound):
	default:
		return fmt.Errorf("reading the Adapter for %s: %w", adapter, err)
	}

	path := filepath.Join(settings, privacyFile)
	if err := os.WriteFile(path, []byte(privacy+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("bondfetch: privacy is %s for %s\n", privacy, adapter)
	return nil
}

// restoreIdentity writes the radio's stored identity file into the tree
// bluetoothd reads.
//
// A radio with no identity Secret has never had privacy on, so it gets
// no file. Any other failure to read the Secret is an error. bluetoothd
// would write a new key when privacy is on, and every bonded peer would
// then hold a key for an identity that the radio does not present.
func restoreIdentity(api *apiclient.Client, namespace string, adapter bonds.Address, root string) error {
	secret, err := apiclient.Get[bonds.Secret](api, bonds.IdentitySecretPath(namespace, adapter))
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the identity of %s: %w", adapter, err)
	}
	identity := secret.Identity()
	if len(identity) == 0 {
		// A Secret with no key restores nothing. bluetoothd writes a new
		// key when privacy is on, and the operator stores it.
		return nil
	}
	if err := bonds.WriteIdentity(root, adapter, identity); err != nil {
		return fmt.Errorf("writing the identity of %s under %s: %w", adapter, root, err)
	}
	fmt.Printf("bondfetch: restored the identity of %s\n", adapter)
	return nil
}
