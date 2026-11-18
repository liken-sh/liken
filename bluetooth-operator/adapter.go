package main

// An Adapter is the cluster's record of one Bluetooth radio. The
// operator creates the Adapter for the radio its pod uses, or adopts
// an existing Adapter with that name.
//
// The Adapter is named for the radio's address, so the same object
// follows the radio from machine to machine, and status.node records
// the machine it is on now. It has no owner reference. Reinstalling
// the operating system registers the Node again with a new UID, so
// garbage collection would delete an Adapter owned by the old Node,
// and every bond under it.
//
// Deleting an Adapter unpairs every device under it. The cascade
// deletes its Peripherals, and the operator removes the bond of each
// deleting Peripheral from bluetoothd (unpair.go). So while the radio
// is present, the finalizer stays and refuseDeletion reports why.
//
// When the operator starts on a machine whose bluetoothd already has
// bonds, it creates the Adapter, a Peripheral for each bond, and each
// bond's Secret from bluetoothd's files.

import (
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// ensureAdapter creates or adopts the Adapter for the radio this pod
// uses, updates its status, and returns it.
func (i *inventory) ensureAdapter(state adapterState) (*Adapter, error) {
	name := state.Address.Key()
	adapter, err := informer.ReadOne[Adapter](i.client, i.cache.adapters, name, adapterPath(name))
	if errors.Is(err, apiclient.ErrNotFound) {
		adapter, err = i.createAdapter(state)
	}
	if err != nil {
		return nil, err
	}

	if !adapter.Metadata.deleting() {
		if err := i.reconcileAdapterAlias(adapter, state); err != nil {
			fmt.Fprintf(os.Stderr, "setting the alias of the radio to %q: %v\n", adapter.Spec.Alias, err)
		}
	}
	// Apply spec.btmon first so that the status write below reports the
	// value the btmon container will read.
	i.applyBtmon(adapter)

	err = i.settleAdapter(adapter, state)
	if apiclient.Stale(err) {
		// Another writer changed or deleted the object after the cached
		// copy was read. Read it again and retry once.
		adapter, err = informer.ReadFresh[Adapter](i.client, i.cache.adapters.Versions, name, adapterPath(name))
		if errors.Is(err, apiclient.ErrNotFound) {
			adapter, err = i.createAdapter(state)
		}
		if err != nil {
			return nil, err
		}
		err = i.settleAdapter(adapter, state)
	}
	if err != nil {
		return nil, err
	}
	return adapter, nil
}

// settleAdapter adds the finalizer to the Adapter if it lacks one and
// writes its status. For an Adapter that is being deleted, it calls
// refuseDeletion instead.
func (i *inventory) settleAdapter(adapter *Adapter, state adapterState) error {
	name := adapter.Metadata.Name
	if adapter.Metadata.deleting() {
		// The radio is present, so the deletion must wait. The cascade
		// would delete every Peripheral and bond Secret under this
		// Adapter while their devices are still in use.
		return i.refuseDeletion(adapter, state)
	}

	if !adapter.Metadata.hasFinalizer(adapterFinalizer) {
		// A person created this Adapter by hand without the finalizer.
		version, err := patchFinalizers(i.client, adapterPath(name), adapter.Metadata.ResourceVersion,
			adapter.Metadata.withFinalizer(adapterFinalizer))
		if err != nil {
			return fmt.Errorf("adding the finalizer to the Adapter %s: %w", name, err)
		}
		i.cache.adapters.Versions.Note(name, version)
		adapter.Metadata.Finalizers = adapter.Metadata.withFinalizer(adapterFinalizer)
		adapter.Metadata.ResourceVersion = version
	}

	return i.writeAdapterStatus(adapter, AdapterStatus{
		Address: state.Address.Directory(),
		Node:    i.nodeName,
		Powered: state.Powered,
		Privacy: i.reportedPrivacy(adapter),
		Btmon:   i.reportedBtmon(adapter),
	})
}

func (i *inventory) reportedPrivacy(adapter *Adapter) string {
	if i.privacy == "" {
		return adapter.Status.Privacy
	}
	return i.privacy
}

// createAdapter creates the Adapter for a radio with the finalizer in
// the create request, so the Adapter never exists without it.
func (i *inventory) createAdapter(state adapterState) (*Adapter, error) {
	name := state.Address.Key()
	adapter, err := createObject(i.client, adaptersPath(), &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata: ObjectMeta{
			Name:       name,
			Labels:     map[string]string{bonds.AdapterLabel: name},
			Finalizers: []string{adapterFinalizer},
		},
	})
	if err == apiclient.ErrConflict {
		// Another writer created it after our read, so use that copy.
		return informer.ReadFresh[Adapter](i.client, i.cache.adapters.Versions, name, adapterPath(name))
	}
	if err != nil {
		return nil, fmt.Errorf("creating the Adapter for %s: %w", state.Address, err)
	}
	i.cache.adapters.Versions.Note(name, adapter.Metadata.ResourceVersion)
	fmt.Printf("adapter: created %s for the radio at %s\n", name, state.Address)
	return adapter, nil
}

// refuseDeletion keeps the finalizer on a deleting Adapter and writes
// the reason into status.deletionRefused.
//
// Kubernetes cannot cancel a deletion once deletionTimestamp is set,
// so the Adapter keeps its deletionTimestamp, and it and the
// Peripherals and Secrets under it remain until the finalizer goes.
// To remove a radio, unplug it. releaseDepartedAdapters then removes
// the finalizer.
func (i *inventory) refuseDeletion(adapter *Adapter, state adapterState) error {
	reason := fmt.Sprintf(
		"the radio at %s is present on node %s, and deleting this Adapter would unpair every device under it",
		state.Address, i.nodeName)
	if adapter.Status.DeletionRefused == reason {
		return nil
	}
	fmt.Fprintf(os.Stderr, "adapter: refusing to delete %s: %s\n", adapter.Metadata.Name, reason)
	return i.writeAdapterStatus(adapter, AdapterStatus{
		Address:         state.Address.Directory(),
		Node:            i.nodeName,
		Powered:         state.Powered,
		Privacy:         i.reportedPrivacy(adapter),
		Btmon:           i.reportedBtmon(adapter),
		DeletionRefused: reason,
	})
}

// releaseDepartedAdapters removes the finalizer from every deleting
// Adapter whose radio has left this machine, so that the deletion
// completes and deletes the Peripherals and Secrets under it.
//
// Only the machine named in status.node releases an Adapter. When a
// radio moves, the operator on its new machine adopts the Adapter and
// writes its own node name into status.node, so the old machine no
// longer releases it.
//
// present is the radio this pod uses now, or the zero address when
// there is none.
func (i *inventory) releaseDepartedAdapters(present bonds.Address) {
	adapters, err := i.listAdapters()
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the Adapters: %v\n", err)
		return
	}
	for _, adapter := range adapters {
		if !i.departed(adapter, present) {
			continue
		}
		released, err := i.releaseAdapter(adapter, present)
		if err != nil {
			fmt.Fprintf(os.Stderr, "removing the finalizer from the Adapter %s: %v\n", adapter.Metadata.Name, err)
			continue
		}
		if released {
			fmt.Printf("adapter: removed the finalizer from %s, because its radio is no longer on %s, so Kubernetes deletes it with its Peripherals and their Secrets\n",
				adapter.Metadata.Name, i.nodeName)
		}
	}
}

func (i *inventory) listAdapters() ([]Adapter, error) {
	return informer.List[Adapter](i.client, i.cache.adapters, adaptersPath(), adapterPath)
}

func (i *inventory) departed(adapter Adapter, present bonds.Address) bool {
	if !adapter.Metadata.deleting() || !adapter.Metadata.hasFinalizer(adapterFinalizer) {
		return false
	}
	if adapter.Status.Node != i.nodeName {
		return false
	}
	return present.IsZero() || adapter.Metadata.Name != present.Key()
}

// releaseAdapter removes the finalizer from one departed Adapter and
// reports whether it did. On a conflict it reads the Adapter again and
// removes the finalizer only if departed still returns true, because
// another node may have adopted the radio in the meantime. When the
// Adapter no longer exists, it returns false and no error.
func (i *inventory) releaseAdapter(adapter Adapter, present bonds.Address) (bool, error) {
	name := adapter.Metadata.Name
	path := adapterPath(name)
	version, err := patchFinalizers(i.client, path, adapter.Metadata.ResourceVersion, adapter.Metadata.withoutFinalizer(adapterFinalizer))
	if errors.Is(err, apiclient.ErrConflict) {
		current, readErr := informer.ReadFresh[Adapter](i.client, i.cache.adapters.Versions, name, path)
		if readErr != nil {
			err = readErr
		} else if !i.departed(*current, present) {
			return false, nil
		} else {
			version, err = patchFinalizers(i.client, path, current.Metadata.ResourceVersion, current.Metadata.withoutFinalizer(adapterFinalizer))
		}
	}
	if errors.Is(err, apiclient.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	i.cache.adapters.Versions.Note(name, version)
	return true, nil
}

// reconcileAdapterAlias writes spec.alias to BlueZ's Adapter1.Alias,
// which is the name the radio advertises while it is discoverable.
//
// It leaves the alias alone when spec.alias is empty, because BlueZ
// resets the alias to the system name when it receives an empty
// string.
func (i *inventory) reconcileAdapterAlias(adapter *Adapter, state adapterState) error {
	if adapter.Spec.Alias == "" || adapter.Spec.Alias == state.Alias {
		return nil
	}
	if err := i.radio.SetAdapterAlias(adapter.Spec.Alias); err != nil {
		return err
	}
	fmt.Printf("adapter: set the alias of the radio of %s to %q\n", adapter.Metadata.Name, adapter.Spec.Alias)
	return nil
}

// writeAdapterStatus replaces the status only when it differs from the
// current one, because most passes change nothing and a pass runs
// after each burst of D-Bus signals and kernel events.
func (i *inventory) writeAdapterStatus(adapter *Adapter, status AdapterStatus) error {
	if sameStatus(adapter.Status, status) {
		return nil
	}
	adapter.Status = status
	adapter.APIVersion, adapter.Kind = pairingAPI, adapterKind
	if err := apiclient.ReplaceStatus(i.client, adapterPath(adapter.Metadata.Name), adapter); err != nil {
		return fmt.Errorf("writing the status of the Adapter %s: %w", adapter.Metadata.Name, err)
	}
	i.cache.adapters.Versions.Note(adapter.Metadata.Name, adapter.Metadata.ResourceVersion)
	return nil
}

// sameStatus reports whether two statuses are equal. It compares the
// values that Btmon points to, and a nil Btmon differs from a pointer
// to false.
func sameStatus(a, b AdapterStatus) bool {
	btmonA, btmonB := a.Btmon, b.Btmon
	a.Btmon, b.Btmon = nil, nil
	if a != b || (btmonA == nil) != (btmonB == nil) {
		return false
	}
	return btmonA == nil || *btmonA == *btmonB
}
