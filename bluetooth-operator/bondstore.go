package main

// Copies the bonds that bluetoothd writes under /var/lib/bluetooth into
// Secrets, so that the next pod can restore them. The directory is an
// emptyDir, deleted with the pod, and bondfetch, the init container,
// fills it from the Secrets before bluetoothd starts. A bond that never
// reaches a Secret is lost, and the controller must be paired again.
//
// Each bond has its own Secret, owned by the bond's Peripheral, so
// deleting the Peripheral deletes the keys. The Secret is labeled with
// the adapter's address, so bondfetch can list one radio's bonds
// without knowing which devices are paired.
//
// A pass reads the whole tree and writes each bond that differs from its
// Secret. It does not look at which signal woke it, so one pass after
// settleWindow (main.go) covers a burst of signals.
//
// BlueZ writes the info file when pairing completes, and the cache entry
// later, after it resolves the device's name and browses its services.
// No signal reports the cache entry, so the backstop pass
// (backstopInterval) copies it.
//
// settleWindow also keeps a pass from reading a half-written info file.
// BlueZ writes the key material through g_file_set_contents, which
// renames atomically. It writes AddressType in [General] later, from a
// g_idle_add callback. When BlueZ loads a bond, load_devices reads
// AddressType first, so a copy taken before that callback runs makes a
// BLE device with a static random address load as BR/EDR, and it never
// reconnects. The idle callback runs on the next iteration of the GLib
// main loop, well inside the window.
//
// A failed write is logged and persist returns false. The loop retries
// the pass, and the backstop pass writes again after that. Nothing else
// recovers, because the worst result is that someone pairs the
// controller again.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

const (
	// namespaceVar holds the namespace of the bond Secrets. The pod spec
	// sets it through the downward API, and bondfetch reads it too.
	namespaceVar = "POD_NAMESPACE"

	// bondfetch declares the same pair, because both programs are
	// package main and cannot share a constant.
	bondsRootVar     = "BLUETOOTH_BONDS_ROOT"
	bondsRootDefault = "/var/lib/bluetooth"
)

func bondsRoot() string {
	if root := os.Getenv(bondsRootVar); root != "" {
		return root
	}
	return bondsRootDefault
}

// adapterAddressReader returns the address of bluetoothd's adapter. It
// is a parameter so that a test can return an address or ErrNoAdapter
// without D-Bus.
type adapterAddressReader func() (bonds.Address, error)

// bondStore writes each bond on disk into that bond's Secret.
type bondStore struct {
	client    *apiclient.Client
	namespace string
	root      string

	// relays has each controller's evdev capability snapshot, which the
	// store writes into the bond's Secret.
	relays *relays

	// adapter is read from bluetoothd once. bondfetch restored one
	// radio's tree under root, and a second read could return another
	// radio whose bonds this pod never restored.
	adapter bonds.Address

	// watchSecrets starts the watch of one radio's bond Secrets. When it
	// is nil, the store reads every Secret from the API server.
	watchSecrets func(adapter bonds.Address) informer.View

	// secretVersions records the resourceVersion of each Secret that
	// this store wrote or read, so that an older copy in secrets is not
	// used.
	secrets        informer.View
	secretVersions *memo.Versions

	// identity is the adapter's identity file as this store last wrote
	// or read it in the identity Secret, nil until the first read
	// (identity.go).
	identity []byte

	// reportedOlderSecret stops the lookup of the older per-adapter
	// Secret after it was logged or found missing.
	reportedOlderSecret bool
}

// persist writes each bond on disk into its Secret when the two differ.
// It returns false when any bond is not stored, so that the loop retries
// the pass and the privacy restart waits.
//
// owners maps each device to the Peripheral that owns its Secret, and
// unpairing has the devices that are being unpaired. The object
// reconcile computes both.
func (s *bondStore) persist(readAdapter adapterAddressReader, owners map[bonds.Address]OwnerReference, unpairing map[bonds.Address]bool) bool {
	if s.adapter.IsZero() {
		address, err := readAdapter()
		if errors.Is(err, ErrNoAdapter) {
			// bluetoothd claims its bus name before it publishes its
			// adapter.
			return false
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the address of the adapter: %v\n", err)
			return false
		}
		s.useAdapter(address)
	}

	tree, err := bonds.ReadTree(s.root, s.adapter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the bond files under %s: %v\n", s.root, err)
		return false
	}
	s.reportOlderSecret()

	allStored := true
	for device, files := range tree {
		if unpairing[device] {
			continue
		}
		owner, owned := owners[device]
		if !owned {
			// The bond's Peripheral does not exist yet, and a Secret
			// with no owner is never deleted.
			allStored = false
			continue
		}
		if !s.persistBond(device, files, owner) {
			allStored = false
		}
	}
	return allStored
}

// useAdapter sets the store's adapter and starts the watch of its Secrets.
func (s *bondStore) useAdapter(address bonds.Address) {
	s.adapter = address
	if s.watchSecrets != nil {
		s.secrets = s.watchSecrets(address)
		s.secretVersions = memo.New()
	}
}

// restore loads every stored evdev capability snapshot into the relays
// at startup, so that each bonded controller has its virtual input node
// before the first pass publishes the slice. A failure is only logged,
// because the relay reads the capabilities again from the evdev node
// when the controller next connects.
//
// The snapshots are in the Secrets and not in the BlueZ tree, because
// BlueZ owns that tree. restore lists from the API server, because the
// watch that useAdapter starts may not have read anything yet.
func (s *bondStore) restore(readAdapter adapterAddressReader) {
	if s.adapter.IsZero() {
		address, err := readAdapter()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the address of the adapter to restore the relays: %v\n", err)
			return
		}
		s.useAdapter(address)
	}
	list, err := apiclient.Get[bonds.SecretList](s.client, byAdapter(bonds.SecretsPath(s.namespace), s.adapter.Key()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the bond Secrets to restore the relays: %v\n", err)
		return
	}
	for _, secret := range list.Items {
		for device, snapshot := range secret.Snapshots() {
			s.relays.restore(macFromDeviceName(device.Key()), snapshot)
		}
	}
}

// persistBond writes one bond's Secret when its files differ from the
// files on disk, or its evdev snapshot differs from the relay's.
//
// The relay reads the snapshot from the controller's evdev node, which
// exists only while the controller is connected. Until the first
// connection the Secret has no snapshot, and the `no-input-node` taint
// keeps a claim on that controller Pending.
func (s *bondStore) persistBond(device bonds.Address, files bonds.Files, owner OwnerReference) bool {
	name := bonds.BondSecretName(device)
	path := bonds.BondSecretPath(s.namespace, device)
	snapshot := s.relays.snapshot(macFromDeviceName(device.Key()))
	// A Secret created on the previous pass may not be in the watch yet.
	// readBond then reads the API server, so the Secret is not created
	// twice.
	key := s.namespace + "/" + name
	current, err := s.readBond(key, path)
	if errors.Is(err, apiclient.ErrNotFound) {
		return s.create(device, files, snapshot, owner)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the Secret %s: %v\n", name, err)
		return false
	}
	if current.Tree()[device].Equal(files) && bytes.Equal(current.Snapshot(device), snapshot) {
		return true
	}
	err = s.update(current, device, files, snapshot, owner)
	if errors.Is(err, apiclient.ErrConflict) {
		// Another writer changed the Secret after the copy was read.
		// Read it again, and write only if it still differs.
		current, err = s.fetchBond(key, path)
		if err == nil && current.Tree()[device].Equal(files) && bytes.Equal(current.Snapshot(device), snapshot) {
			return true
		}
		if err == nil {
			err = s.update(current, device, files, snapshot, owner)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "updating the Secret %s: %v\n", name, err)
		return false
	}
	return true
}

// readBond returns one bond's Secret from the watch's store when the
// store has a current copy, and from the API server otherwise.
func (s *bondStore) readBond(key, path string) (*bonds.Secret, error) {
	if cached, ok := informer.Cached[bonds.Secret](s.secrets, key); ok && s.secretVersions.Current(key, cached.Metadata.ResourceVersion) {
		return cached, nil
	}
	return s.fetchBond(key, path)
}

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

func (s *bondStore) create(device bonds.Address, files bonds.Files, snapshot []byte, owner OwnerReference) bool {
	name := bonds.BondSecretName(device)
	secret := bonds.NewBondSecret(s.namespace, s.adapter, device, files, snapshot, bondOwner(owner))
	body, err := json.Marshal(secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encoding the Secret %s: %v\n", name, err)
		return false
	}
	created := &bonds.Secret{}
	if err := s.client.RequestJSON(http.MethodPost, bonds.SecretsPath(s.namespace), body, created); err != nil {
		fmt.Fprintf(os.Stderr, "creating the Secret %s: %v\n", name, err)
		return false
	}
	s.secretVersions.Note(s.namespace+"/"+name, created.Metadata.ResourceVersion)
	fmt.Printf("bonds: created the Secret %s for the bond with %s\n", name, device)
	return true
}

// update replaces one bond's Secret. The request sends the
// resourceVersion of current, so when another writer changed the Secret
// since, the API server refuses the write with apiclient.ErrConflict.
func (s *bondStore) update(current *bonds.Secret, device bonds.Address, files bonds.Files, snapshot []byte, owner OwnerReference) error {
	name := bonds.BondSecretName(device)
	secret := bonds.NewBondSecret(s.namespace, s.adapter, device, files, snapshot, bondOwner(owner))
	secret.Metadata.ResourceVersion = current.Metadata.ResourceVersion
	body, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("encoding the Secret as JSON: %w", err)
	}
	written := &bonds.Secret{}
	if err := s.client.RequestJSON(http.MethodPut, bonds.BondSecretPath(s.namespace, device), body, written); err != nil {
		return err
	}
	s.secretVersions.Note(s.namespace+"/"+name, written.Metadata.ResourceVersion)
	fmt.Printf("bonds: wrote the Secret %s\n", name)
	return nil
}

// reportOlderSecret logs once that the older per-adapter Secret still
// exists. The operator does not delete it. bondfetch reads both the
// per-adapter and the per-bond Secrets, so a bond that is only in the
// older Secret still restores, and persist then copies it into a
// per-bond Secret. A person deletes the older Secret after the
// controllers reconnect from the per-bond Secrets.
func (s *bondStore) reportOlderSecret() {
	if s.reportedOlderSecret {
		return
	}
	_, err := apiclient.Get[bonds.Secret](s.client, bonds.SecretPath(s.namespace, s.adapter))
	if err != nil {
		// Any error other than a 404 is left for the next pass.
		s.reportedOlderSecret = errors.Is(err, apiclient.ErrNotFound)
		return
	}
	s.reportedOlderSecret = true
	fmt.Printf("bonds: the Secret %s holds this adapter's bonds in the older per-adapter layout. "+
		"Delete it once the per-bond Secrets have restored a controller\n", bonds.SecretName(s.adapter))
}

// bondOwner converts an OwnerReference into a bonds.Owner, because the
// bonds package cannot import package main.
func bondOwner(owner OwnerReference) bonds.Owner {
	return bonds.Owner{
		APIVersion: owner.APIVersion,
		Kind:       owner.Kind,
		Name:       owner.Name,
		UID:        owner.UID,
	}
}

// watchBondSecrets watches one radio's bond Secrets until ctx ends, with
// the label selector that restore lists by. persist reads the watch's
// store, so a pass sends no request for a Secret that has not changed.
//
// The watch has no handler, so a change to a Secret does not wake the
// loop. The tree on disk is the source of truth, and the next pass
// writes again a Secret that someone deleted or edited.
func watchBondSecrets(ctx context.Context, client dynamic.Interface, namespace string, adapter bonds.Address) informer.View {
	source := informer.Source{Resource: secretResource, Namespace: namespace, LabelSelector: adapterSelector(adapter.Key())}
	return informer.Start(ctx, client, source, informer.Options{}).View()
}
