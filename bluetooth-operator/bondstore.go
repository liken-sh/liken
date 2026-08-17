package main

// Carrying a new pairing back into the adapter's Secret.
//
// bondfetch fills the pod's /var/lib/bluetooth from the Secret before
// bluetoothd starts, and this is the other half: whatever bluetoothd
// writes into that tree afterwards goes back into the same Secret, so
// the next pod reads it. The pod's copy is an emptyDir and goes when
// the pod goes, so a bond that never reaches the API is a controller
// somebody pairs again.
//
// The trigger is the same signal set the slice reconcile runs on, and
// the same settle window (see bluez.go and main.go). Nothing here
// reads a signal's payload: a pass re-reads the whole tree, compares
// it with the Secret, and writes on a difference, so one wake answers
// a burst and a signal that changed no key costs a read of a few
// kilobytes.
//
// A device's two files can land on different passes. bluetoothd writes
// the info file in the management callback that completes the pairing,
// and it writes the cache entry when it resolves the device's name and
// browses its services, which is a separate event. So a pass can read
// a bond with one file and the next pass reads it with two, and the
// backstop tick at 60 seconds carries the second file whether or not a
// signal announced it. The comparison is what makes that safe: a tree
// that gained a cache entry differs from the Secret, and any
// difference triggers a write.
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
// deferred write needs. The settle stage also has a 10 second limit,
// which can end a burst early, and that costs nothing here: the pass
// compares what it read, and the next pass writes whatever the burst
// settled on.
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
	"path/filepath"

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

// bondStore keeps one adapter's Secret in step with the bonds on disk.
type bondStore struct {
	client    *Client
	namespace string
	root      string

	// adapter is the radio this pod's bondfetch restored. It is read
	// from bluetoothd the first time bluetoothd answers, and then it is
	// fixed for the life of the process. A pod serves one adapter, and
	// re-reading it would point a write at a radio whose keys this pod
	// never restored, whose tree is therefore not under root, and whose
	// Secret an empty read would then empty.
	adapter bonds.Address
}

// persist copies the adapter's bonds into the adapter's Secret when
// the two differ. It reports whether the pass left the Secret correct,
// so that the caller can run a failure again shortly.
func (s *bondStore) persist(readAdapter adapterAddressReader) bool {
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

	path := bonds.SecretPath(s.namespace, s.adapter)
	current, err := get[bonds.Secret](s.client, path)
	if errors.Is(err, ErrNotFound) {
		if len(tree) == 0 {
			// An adapter that has paired nothing. An empty Secret says
			// nothing that its absence does not, and bluetoothd creates
			// the tree at the first pairing.
			return true
		}
		return s.create(tree)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading %s: %v\n", bonds.SecretName(s.adapter), err)
		return false
	}

	stored := current.Tree()
	if stored.Same(tree) {
		return true
	}
	if len(tree) == 0 && !s.observedEmpty() {
		fmt.Fprintf(os.Stderr,
			"the bonds under %s are unreadable and %s holds %d; the Secret keeps them\n",
			s.adapterDirectory(), bonds.SecretName(s.adapter), len(stored))
		return false
	}
	return s.update(current.Metadata.ResourceVersion, stored, tree)
}

// observedEmpty reports whether an empty read is what the adapter
// really holds.
//
// bonds.ReadTree answers with an empty tree for an adapter that paired
// nothing and for an adapter whose directory is not there at all, and
// those are different facts. The directory is absent when the volume
// did not mount, and when the tree on disk belongs to a different
// radio than the one this store writes for. Neither of those says any
// device was unpaired, and emptying a Secret on either would lose keys
// that cannot be rebuilt.
//
// The directory is the authority because bluetoothd creates it when it
// registers the adapter, and keeps it for the adapter's own settings
// after the last device directory below it is gone. So an empty tree
// under a directory that is there is bluetoothd's own answer that
// nothing is paired.
//
// This is the rule the slice writes already follow: an empty paired
// set deletes the slice only when bluetoothd answered with an adapter
// present (see ErrNoAdapter in bluez.go), and never when the answer
// itself is missing.
func (s *bondStore) observedEmpty() bool {
	info, err := os.Stat(s.adapterDirectory())
	return err == nil && info.IsDir()
}

// adapterDirectory is where BlueZ keeps this adapter's tree.
func (s *bondStore) adapterDirectory() string {
	return filepath.Join(s.root, s.adapter.Directory())
}

// create puts the adapter's first bonds in the API. A create names the
// collection, which is the API's rule for every resource, where every
// other call here names the object.
func (s *bondStore) create(tree bonds.Tree) bool {
	body, err := json.Marshal(bonds.NewSecret(s.namespace, s.adapter, tree))
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding %s: %v\n", bonds.SecretName(s.adapter), err)
		return false
	}
	if err := s.client.RequestJSON(http.MethodPost, secretsPath(s.namespace), body, nil); err != nil {
		fmt.Fprintf(os.Stderr, "creating %s: %v\n", bonds.SecretName(s.adapter), err)
		return false
	}
	fmt.Printf("bonds: created %s with %d bonds\n", bonds.SecretName(s.adapter), len(tree))
	return true
}

// update replaces the stored bonds with the ones on disk.
//
// The write carries the resourceVersion from the read, so a second
// writer gets ErrConflict instead of losing the first writer's bonds,
// and the next pass reads again and writes again.
//
// A PUT replaces the whole object, so a tree with no devices in it
// clears the Secret's data. That is the write an unpairing produces,
// and observedEmpty is what has already established that the unpairing
// is real.
func (s *bondStore) update(resourceVersion string, stored, tree bonds.Tree) bool {
	secret := bonds.NewSecret(s.namespace, s.adapter, tree)
	secret.Metadata.ResourceVersion = resourceVersion
	body, err := json.Marshal(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding %s: %v\n", bonds.SecretName(s.adapter), err)
		return false
	}
	if err := s.client.RequestJSON(http.MethodPut, bonds.SecretPath(s.namespace, s.adapter), body, nil); err != nil {
		fmt.Fprintf(os.Stderr, "updating %s: %v\n", bonds.SecretName(s.adapter), err)
		return false
	}
	fmt.Printf("bonds: wrote %s, %d bonds, was %d\n", bonds.SecretName(s.adapter), len(tree), len(stored))
	return true
}

// secretsPath is the collection a create posts to. bonds.SecretPath
// names one Secret, and the collection is the path without the name.
func secretsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets"
}
