package main

// Writing a new pairing back into that bond's Secret.
//
// bondfetch fills the pod's /var/lib/bluetooth from the Secrets before
// bluetoothd starts, and this is the other half: whatever bluetoothd
// writes into that tree afterwards goes back into the API, so the next
// pod reads it. The pod's copy is an emptyDir and goes when the pod
// goes, so a bond that never reaches the API is a controller somebody
// pairs again.
//
// One Secret carries one bond. Its owner is that bond's Pairing, so
// deleting the Pairing collects the keys, and the label on it names the
// adapter, so the init container can gather one radio's bonds without
// a list of paired devices. A bond with no Pairing yet is not
// written: an owner reference cannot be added to a Secret that has
// none, and the Pairing is created on the same pass or the next one.
//
// The trigger is the same signal set the slice reconcile runs on, and
// the same settle window (see bluez.go and main.go). Nothing here reads
// a signal's payload: a pass re-reads the whole tree, compares it with
// the Secrets, and writes on a difference, so one wake answers a burst
// and a signal that changed no key costs a read of a few kilobytes.
//
// A device's two files can land on different passes. bluetoothd writes
// the info file in the management callback that completes the pairing,
// and it writes the cache entry when it resolves the device's name and
// browses its services, which is a separate event. So a pass can read
// a bond with one file and the next pass reads it with two, and the
// backstop tick at 60 seconds carries the second file whether or not a
// signal announced it. The comparison is what makes that safe: a bond
// that gained a cache entry differs from its Secret, and any difference
// triggers a write.
//
// The settle window is what makes the read safe to take, and it is
// 1500 ms. BlueZ writes the key material synchronously in the
// management callback, through g_file_set_contents, which renames
// atomically, so no reader sees a torn file. But it writes [General]
// AddressType on a deferred g_idle_add path, and on restore
// load_devices reads AddressType first and interprets the rest of the
// file by it. A snapshot taken between the two loses that key, and a
// BLE device with a static random identity address then loads as
// BR/EDR and never reconnects again. A GLib idle callback runs at the
// next turn of the main loop, which is microseconds after the source
// is queued, so 1500 ms is three orders of magnitude more than the
// deferred write needs.
//
// A failed write is logged and reported, and nothing more happens.
// There is no retry with escalation, no taint for a bond that is not
// stored, and no second copy of the tree. The next trigger or the next
// backstop pass writes again, and the worst case is that somebody
// pairs a controller again.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	// namespaceVar names the namespace whose Secrets hold the bonds,
	// which the pod spec supplies through the downward API. bondfetch
	// reads the same variable for the same Secrets.
	namespaceVar = "POD_NAMESPACE"

	// bondsRootVar overrides the directory the bonds are read from, and
	// bondsRootDefault is where BlueZ keeps them. bondfetch has the same
	// pair, because the two programs share the volume and cannot share a
	// constant: both are package main.
	bondsRootVar     = "BLUETOOTH_BONDS_ROOT"
	bondsRootDefault = "/var/lib/bluetooth"
)

// bondsRoot answers with the tree both this operator and bluetoothd
// read.
func bondsRoot() string {
	if root := os.Getenv(bondsRootVar); root != "" {
		return root
	}
	return bondsRootDefault
}

// adapterAddressReader answers with the address of the adapter
// bluetoothd holds. persist takes one as a parameter rather than
// calling bluetoothd itself, so that a test can supply an answer,
// including ErrNoAdapter, without a bus.
type adapterAddressReader func() (bonds.Address, error)

// bondStore keeps each bond's Secret in step with the bonds on disk.
type bondStore struct {
	client    *Client
	namespace string
	root      string

	// adapter is the radio this pod's bondfetch restored. It is read
	// from bluetoothd the first time bluetoothd answers, and then it is
	// fixed for the life of the process. A pod serves one adapter, and
	// re-reading it would point a write at a radio whose keys this pod
	// never restored and whose tree is therefore not under root.
	adapter bonds.Address

	// reportedLegacy records that the operator has already named the
	// older per-adapter Secret. The migration leaves that object alone,
	// so the line is printed once for a person to act on rather than on
	// every pass.
	reportedLegacy bool
}

// persist copies each bond on disk into that bond's own Secret when the
// two differ. It reports whether the pass left every bond stored, so
// that the caller can run a failure again shortly.
//
// owners names the Pairing that owns each bond's Secret, and unpairing
// names the bonds a teardown is working through. Both come from the
// inventory pass, which runs first.
func (s *bondStore) persist(readAdapter adapterAddressReader, owners map[bonds.Address]OwnerReference, unpairing map[bonds.Address]bool) bool {
	if s.adapter.IsZero() {
		address, err := readAdapter()
		if errors.Is(err, ErrNoAdapter) {
			// The startup window, which publisher.reconcile already
			// reports on the same pass. bluetoothd publishes its object
			// tree a moment after it claims its bus name.
			return false
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the adapter's address: %v\n", err)
			return false
		}
		s.adapter = address
	}

	tree, err := bonds.ReadTree(s.root, s.adapter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the bonds under %s: %v\n", s.root, err)
		return false
	}
	s.reportLegacySecret()

	stored := true
	for device, files := range tree {
		if unpairing[device] {
			continue
		}
		owner, owned := owners[device]
		if !owned {
			// The bond has no Pairing yet, which is the state between
			// bluetoothd writing the keys and the inventory pass adopting
			// them. A Secret written now would have no owner, and nothing
			// would ever collect it.
			stored = false
			continue
		}
		if !s.persistBond(device, files, owner) {
			stored = false
		}
	}
	return stored
}

// persistBond writes one bond's Secret when it differs from the bond on
// disk.
func (s *bondStore) persistBond(device bonds.Address, files bonds.Files, owner OwnerReference) bool {
	name := bonds.BondSecretName(device)
	path := bonds.BondSecretPath(s.namespace, device)
	current, err := get[bonds.Secret](s.client, path)
	if errors.Is(err, ErrNotFound) {
		return s.create(device, files, owner)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading %s: %v\n", name, err)
		return false
	}
	if current.Tree()[device].Equal(files) {
		return true
	}
	return s.update(current, device, files, owner)
}

// create puts one bond in the API for the first time. A create names
// the collection, which is the API's rule for every resource, where
// every other call here names the object.
func (s *bondStore) create(device bonds.Address, files bonds.Files, owner OwnerReference) bool {
	name := bonds.BondSecretName(device)
	secret := bonds.NewBondSecret(s.namespace, s.adapter, device, files, bondOwner(owner))
	body, err := json.Marshal(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding %s: %v\n", name, err)
		return false
	}
	if err := s.client.RequestJSON(http.MethodPost, bonds.SecretsPath(s.namespace), body, nil); err != nil {
		fmt.Fprintf(os.Stderr, "creating %s: %v\n", name, err)
		return false
	}
	fmt.Printf("bonds: created %s for the bond with %s\n", name, device)
	return true
}

// update replaces one bond's stored files with the ones on disk.
//
// The write carries the resourceVersion from the read, so a second
// writer gets ErrConflict instead of losing the first writer's bond,
// and the next pass reads again and writes again.
func (s *bondStore) update(current *bonds.Secret, device bonds.Address, files bonds.Files, owner OwnerReference) bool {
	name := bonds.BondSecretName(device)
	secret := bonds.NewBondSecret(s.namespace, s.adapter, device, files, bondOwner(owner))
	secret.Metadata.ResourceVersion = current.Metadata.ResourceVersion
	body, err := json.Marshal(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding %s: %v\n", name, err)
		return false
	}
	if err := s.client.RequestJSON(http.MethodPut, bonds.BondSecretPath(s.namespace, device), body, nil); err != nil {
		fmt.Fprintf(os.Stderr, "updating %s: %v\n", name, err)
		return false
	}
	fmt.Printf("bonds: wrote %s\n", name)
	return true
}

// reportLegacySecret names the older per-adapter Secret once, if one is
// still there.
//
// The migration does not delete it. bondfetch reads both layouts, so a
// bond that is only in the old Secret still restores, and this
// operator writes the per-bond Secrets from the tree that restore
// produced. Deleting the old object is a person's act, after a drill
// has shown the controllers reconnecting from the new ones.
func (s *bondStore) reportLegacySecret() {
	if s.reportedLegacy {
		return
	}
	_, err := get[bonds.Secret](s.client, bonds.SecretPath(s.namespace, s.adapter))
	if err != nil {
		// An absent Secret is the ordinary state, and any other failure
		// is reported by the reads that matter.
		s.reportedLegacy = errors.Is(err, ErrNotFound)
		return
	}
	s.reportedLegacy = true
	fmt.Printf("bonds: %s still holds this adapter's bonds in the older layout; "+
		"delete it once the per-bond Secrets have restored a controller\n", bonds.SecretName(s.adapter))
}

// bondOwner turns the Pairing this operator holds into the owner
// reference a Secret carries. The two structs carry the same fields,
// and the bonds package has its own because it cannot import this one.
func bondOwner(owner OwnerReference) bonds.Owner {
	return bonds.Owner{
		APIVersion: owner.APIVersion,
		Kind:       owner.Kind,
		Name:       owner.Name,
		UID:        owner.UID,
	}
}
