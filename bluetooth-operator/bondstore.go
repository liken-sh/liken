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
// One Secret holds one bond. Its owner is that bond's Peripheral, so
// deleting the Peripheral collects the keys, and the label on it names
// the adapter, so the init container can gather one radio's bonds
// without a list of paired devices. A bond with no Peripheral yet is
// not written: an owner reference cannot be added to a Secret that has
// none, and the Peripheral is created on the same pass or the next one.
//
// The trigger is the same signal set the slice reconcile runs on, and
// the same settle window (see bluez.go and main.go). Nothing here reads
// a signal's payload: a pass re-reads the whole tree, compares it with
// the Secrets, and writes on a difference. One wake answers a burst,
// and a signal that changed no key costs a read of a few kilobytes.
//
// A device's two files can land on different passes. bluetoothd writes
// the info file in the management callback that completes the pairing,
// and it writes the cache entry when it resolves the device's name and
// browses its services, which is a separate event. So a pass can read
// a bond with one file and the next pass reads it with two, and the
// backstop tick at 60 seconds stores the second file whether or not a
// signal announced it. The comparison makes that safe: a bond that
// gained a cache entry differs from its Secret, and any difference
// triggers a write.
//
// The settle window makes the read safe to take, and it is
// 1500 ms. BlueZ writes the key material synchronously in the
// management callback, through g_file_set_contents, which renames
// atomically, so no reader reads a torn file. But it writes [General]
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
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
	client    *apiclient.Client
	namespace string
	root      string

	// relays is the input relay. The store reads each controller's
	// evdev capability snapshot out of the relay and writes it into
	// that bond's Secret, and restore hands the stored snapshots back
	// to the relay when this pod starts.
	relays *relays

	// adapter is the radio this pod's bondfetch restored. It is read
	// from bluetoothd the first time bluetoothd answers, and then it is
	// fixed for the life of the process. A pod serves one adapter, and
	// re-reading it would point a write at a radio whose keys this pod
	// never restored and whose tree is therefore not under root.
	adapter bonds.Address

	// watchSecrets starts the watch of one radio's bond Secrets, and
	// answers its store. It runs once, when the store first learns the
	// radio, because the radio is fixed from then on. A store with no
	// watch reads every Secret from the API server.
	watchSecrets func(adapter bonds.Address) informer.View

	// secrets is the watch's store, which persist reads in place of the
	// API server, and secretVersions the memo of the copies this store
	// wrote or read (objectcache.go).
	secrets        informer.View
	secretVersions *memo.Versions

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
// owners names the Peripheral that owns each bond's Secret, and
// unpairing names the bonds a teardown is working through. Both come
// from the inventory pass, which runs first.
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
		s.learn(address)
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
			// The bond has no Peripheral yet, which is the state between
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

// learn fixes the radio this store serves, and starts the watch of its
// Secrets.
func (s *bondStore) learn(address bonds.Address) {
	s.adapter = address
	if s.watchSecrets != nil {
		s.secrets = s.watchSecrets(address)
		s.secretVersions = memo.New()
	}
}

// restore hands every stored evdev capability snapshot to the input
// relay, so that each bonded controller has its virtual node before
// the first pass publishes the slice. It runs once, at startup.
//
// The snapshots come from the API and not from the tree bondfetch
// restored, because the snapshot is this operator's own document and
// BlueZ owns that tree. A failure here costs nothing permanent: the
// controller's next connect reads its capabilities from the real node
// again.
//
// The list goes to the API server. It runs once, before the watch of
// the Secrets has read anything.
func (s *bondStore) restore(readAdapter adapterAddressReader) {
	if s.adapter.IsZero() {
		address, err := readAdapter()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the adapter's address for the relays: %v\n", err)
			return
		}
		s.learn(address)
	}
	list, err := apiclient.Get[bonds.SecretList](s.client, byAdapter(bonds.SecretsPath(s.namespace), s.adapter.Key()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the stored bonds for the relays: %v\n", err)
		return
	}
	for _, secret := range list.Items {
		for device, snapshot := range secret.Snapshots() {
			s.relays.restore(macFromDeviceName(device.Key()), snapshot)
		}
	}
}

// persistBond writes one bond's Secret when it differs from the bond on
// disk, or when the controller's evdev capabilities differ from the
// snapshot the Secret holds.
//
// The snapshot is written on the pass after the controller first
// connects, because the relay reads a controller's capabilities from
// its real evdev node and that node exists only while the controller
// is on the air. Until then the key is absent, and the no-input-node
// taint parks a claim on that controller.
func (s *bondStore) persistBond(device bonds.Address, files bonds.Files, owner OwnerReference) bool {
	name := bonds.BondSecretName(device)
	path := bonds.BondSecretPath(s.namespace, device)
	snapshot := s.relays.snapshot(macFromDeviceName(device.Key()))
	// A Secret the store does not hold is read from the API server, so
	// a Secret this operator created on the pass before, which the watch
	// has not delivered yet, is compared and not created twice.
	key := s.namespace + "/" + name
	current, err := s.readBond(key, path)
	if errors.Is(err, apiclient.ErrNotFound) {
		return s.create(device, files, snapshot, owner)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading %s: %v\n", name, err)
		return false
	}
	if current.Tree()[device].Equal(files) && bytes.Equal(current.Snapshot(device), snapshot) {
		return true
	}
	err = s.update(current, device, files, snapshot, owner)
	if errors.Is(err, apiclient.ErrConflict) {
		// The copy from the store was older than the API server's, such
		// as one from before this operator's own last write. The fresh
		// copy is compared, and written once more when it still differs.
		current, err = s.fetchBond(key, path)
		if err == nil && current.Tree()[device].Equal(files) && bytes.Equal(current.Snapshot(device), snapshot) {
			return true
		}
		if err == nil {
			err = s.update(current, device, files, snapshot, owner)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "updating %s: %v\n", name, err)
		return false
	}
	return true
}

// readBond answers one bond's Secret: the store's copy when it holds a
// current one, and the API server's copy when it does not.
func (s *bondStore) readBond(key, path string) (*bonds.Secret, error) {
	if held, ok := informer.Cached[bonds.Secret](s.secrets, key); ok && s.secretVersions.Current(key, held.Metadata.ResourceVersion) {
		return held, nil
	}
	return s.fetchBond(key, path)
}

// fetchBond reads one bond's Secret from the API server and notes its
// version.
func (s *bondStore) fetchBond(key, path string) (*bonds.Secret, error) {
	var fresh *bonds.Secret
	err := s.secretVersions.Send(key, func() (string, error) {
		var err error
		if fresh, err = apiclient.Get[bonds.Secret](s.client, path); err != nil {
			return "", err
		}
		return fresh.Metadata.ResourceVersion, nil
	})
	return fresh, err
}

// create puts one bond in the API for the first time. A create names
// the collection, which is the API's rule for every resource, where
// every other call here names the object.
func (s *bondStore) create(device bonds.Address, files bonds.Files, snapshot []byte, owner OwnerReference) bool {
	name := bonds.BondSecretName(device)
	secret := bonds.NewBondSecret(s.namespace, s.adapter, device, files, snapshot, bondOwner(owner))
	body, err := json.Marshal(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding %s: %v\n", name, err)
		return false
	}
	created := &bonds.Secret{}
	if err := s.client.RequestJSON(http.MethodPost, bonds.SecretsPath(s.namespace), body, created); err != nil {
		fmt.Fprintf(os.Stderr, "creating %s: %v\n", name, err)
		return false
	}
	s.secretVersions.Note(s.namespace+"/"+name, created.Metadata.ResourceVersion)
	fmt.Printf("bonds: created %s for the bond with %s\n", name, device)
	return true
}

// update replaces one bond's stored files with the ones on disk.
//
// The write includes the resourceVersion from the read, so a second
// writer gets apiclient.ErrConflict instead of losing the first writer's bond,
// and the caller reads the Secret again.
func (s *bondStore) update(current *bonds.Secret, device bonds.Address, files bonds.Files, snapshot []byte, owner OwnerReference) error {
	name := bonds.BondSecretName(device)
	secret := bonds.NewBondSecret(s.namespace, s.adapter, device, files, snapshot, bondOwner(owner))
	secret.Metadata.ResourceVersion = current.Metadata.ResourceVersion
	body, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("encoding: %w", err)
	}
	written := &bonds.Secret{}
	if err := s.client.RequestJSON(http.MethodPut, bonds.BondSecretPath(s.namespace, device), body, written); err != nil {
		return err
	}
	s.secretVersions.Note(s.namespace+"/"+name, written.Metadata.ResourceVersion)
	fmt.Printf("bonds: wrote %s\n", name)
	return nil
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
	_, err := apiclient.Get[bonds.Secret](s.client, bonds.SecretPath(s.namespace, s.adapter))
	if err != nil {
		// An absent Secret is the ordinary state, and any other failure
		// is reported by the reads that matter.
		s.reportedLegacy = errors.Is(err, apiclient.ErrNotFound)
		return
	}
	s.reportedLegacy = true
	fmt.Printf("bonds: %s still holds this adapter's bonds in the older layout; "+
		"delete it once the per-bond Secrets have restored a controller\n", bonds.SecretName(s.adapter))
}

// bondOwner turns the Peripheral this operator holds into the owner
// reference a Secret needs. The two structs hold the same fields, and
// the bonds package has its own because it cannot import this one.
func bondOwner(owner OwnerReference) bonds.Owner {
	return bonds.Owner{
		APIVersion: owner.APIVersion,
		Kind:       owner.Kind,
		Name:       owner.Name,
		UID:        owner.UID,
	}
}

// watchBondSecrets keeps one radio's bond Secrets in a store until the
// context ends. persist reads the store on every pass, where it would
// otherwise read each bond's Secret from the API server, and a watch
// with no change to send costs no read. No change to a Secret wakes
// the loop, because the Secrets follow the tree on disk and not the
// other way round: the next pass of any kind writes a Secret again that
// somebody deleted or edited.
//
// The watch selects by the adapter label in this pod's namespace, the
// same selection restore lists. A Secret that does not convert is
// logged when the pass reads it, and the pass reads that one from the
// API server.
func watchBondSecrets(ctx context.Context, client dynamic.Interface, namespace string, adapter bonds.Address) informer.View {
	source := informer.Source{Resource: secretResource, Namespace: namespace, LabelSelector: adapterSelector(adapter.Key())}
	return informer.Start(ctx, client, source, informer.Options{}).View()
}
