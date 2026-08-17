package main

// The Adapter object: the cluster's record of one Bluetooth radio.
//
// The operator creates the object for the radio its pod claimed, and
// adopts one that is already there. Adoption is also the migration
// path, because an operator that starts against a machine with bonds
// already in bluetoothd creates the same objects it would have created
// by pairing them.
//
// The object's name is the radio's own address, so the object follows
// the radio. It is not owned by the Node and not owned by liken's
// Machine: an owner reference binds to one UID, a reinstall registers
// the Node again under a new UID, and garbage collection would then
// sweep the Adapter and every bond under it as a side effect of
// reinstalling the operating system. The radio's current location is
// reported in status.
//
// Adoption is also what repairs a forced deletion. Somebody who patches
// the finalizer off a live radio's Adapter loses the objects to the
// cascade, and the next pass creates the Adapter again, adopts every
// bond bluetoothd still holds as a Pairing, and writes each bond's
// Secret from the tree on disk. bluetoothd keeps the keys on its own
// disk, so no change to these objects can lose them.

import (
	"fmt"
	"os"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// ensureAdapter makes the Adapter object agree with the radio this pod
// holds, and returns the object that owns every Pairing.
func (i *inventory) ensureAdapter(state adapterState) (*Adapter, error) {
	name := state.Address.Key()
	adapter, err := get[Adapter](i.client, adapterPath(name))
	if err == ErrNotFound {
		adapter, err = i.createAdapter(state)
	}
	if err != nil {
		return nil, err
	}

	if adapter.Metadata.deleting() {
		// The radio is here, so the delete is refused. A cascade would
		// take every Pairing under this Adapter and every bond Secret
		// under those, which is a mass unpair of hardware that is
		// working. The object stays, with the reason in its status, until
		// the radio is gone.
		return adapter, i.refuseDeletion(adapter, state)
	}

	if !adapter.Metadata.holds(adapterFinalizer) {
		// An object a person created by hand, or one this operator
		// created before it could patch the finalizer on.
		version, err := patchFinalizers(i.client, adapterPath(name), adapter.Metadata.ResourceVersion,
			adapter.Metadata.with(adapterFinalizer))
		if err != nil {
			return nil, fmt.Errorf("holding %s against deletion: %w", name, err)
		}
		adapter.Metadata.Finalizers = adapter.Metadata.with(adapterFinalizer)
		adapter.Metadata.ResourceVersion = version
	}

	if err := i.reconcileAdapterAlias(adapter, state); err != nil {
		fmt.Fprintf(os.Stderr, "naming the radio %q: %v\n", adapter.Spec.Alias, err)
	}
	if err := i.writeAdapterStatus(adapter, AdapterStatus{
		Address: state.Address.Directory(),
		Node:    i.nodeName,
		Powered: state.Powered,
	}); err != nil {
		return nil, err
	}
	return adapter, nil
}

// createAdapter puts a radio in the API for the first time.
//
// The create carries the finalizer, rather than a second write
// patching it on. In the window between those two writes a delete
// would cascade to nothing yet, and stating the finalizer once is
// simpler than repairing its absence.
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
	if err == ErrConflict {
		// Another writer created it between the read and this write, so
		// read it again. Nothing more is needed.
		return get[Adapter](i.client, adapterPath(name))
	}
	if err != nil {
		return nil, fmt.Errorf("creating the Adapter for %s: %w", state.Address, err)
	}
	fmt.Printf("adapter: created %s for the radio at %s\n", name, state.Address)
	return adapter, nil
}

// refuseDeletion keeps the finalizer on and writes the reason into
// status.
//
// A deletionTimestamp cannot be removed from an object, so the
// operator cannot reverse the delete; it can only report the refusal.
// The object stays in Terminating, its status names the radio that
// blocks the delete, and the Pairings and Secrets under it are
// untouched. A person who really means to retire the radio unplugs
// it, and the next pass lets the deletion through.
func (i *inventory) refuseDeletion(adapter *Adapter, state adapterState) error {
	reason := fmt.Sprintf(
		"the radio at %s is present on node %s; deleting this Adapter would unpair every device under it",
		state.Address, i.nodeName)
	if adapter.Status.DeletionRefused == reason {
		return nil
	}
	fmt.Fprintf(os.Stderr, "adapter: refusing to delete %s: %s\n", adapter.Metadata.Name, reason)
	return i.writeAdapterStatus(adapter, AdapterStatus{
		Address:         state.Address.Directory(),
		Node:            i.nodeName,
		Powered:         state.Powered,
		DeletionRefused: reason,
	})
}

// releaseDepartedAdapters releases an Adapter whose radio this
// machine no longer has.
//
// Only the machine that last held the radio releases its Adapter, and
// status.node records which machine that is. A second machine's
// operator leaves the object alone: a radio carried to that machine is
// adopted there, its status.node then names that machine, and the
// machine that no longer has the radio is no longer named.
//
// present is the radio this pod holds right now, and the zero address
// means it holds none. The Adapter for the present radio takes the
// refusal above. Every other deleting Adapter that names this node
// belongs to a radio that is gone, so its cascade is the intended
// cleanup.
func (i *inventory) releaseDepartedAdapters(present bonds.Address) {
	list, err := get[AdapterList](i.client, adaptersPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the Adapters: %v\n", err)
		return
	}
	for _, adapter := range list.Items {
		if !adapter.Metadata.deleting() || !adapter.Metadata.holds(adapterFinalizer) {
			continue
		}
		if adapter.Status.Node != i.nodeName {
			continue
		}
		if !present.IsZero() && adapter.Metadata.Name == present.Key() {
			continue
		}
		path := adapterPath(adapter.Metadata.Name)
		if _, err := patchFinalizers(i.client, path, adapter.Metadata.ResourceVersion,
			adapter.Metadata.without(adapterFinalizer)); err != nil {
			fmt.Fprintf(os.Stderr, "releasing %s for deletion: %v\n", adapter.Metadata.Name, err)
			continue
		}
		fmt.Printf("adapter: released %s, whose radio is no longer on %s; its Pairings and their Secrets go with it\n",
			adapter.Metadata.Name, i.nodeName)
	}
}

// reconcileAdapterAlias carries spec.alias into BlueZ's Adapter1.Alias,
// which is the name the radio broadcasts about itself, so a
// discoverable window announces itself under the operator's chosen
// name.
//
// An empty spec.alias states nothing, so the radio keeps whatever name
// bluetoothd gave it. Writing an empty string into Adapter1.Alias
// resets the alias to the system name in BlueZ, and that would turn
// an unset field into an instruction.
func (i *inventory) reconcileAdapterAlias(adapter *Adapter, state adapterState) error {
	if adapter.Spec.Alias == "" || adapter.Spec.Alias == state.Alias {
		return nil
	}
	if err := i.radio.SetAdapterAlias(adapter.Spec.Alias); err != nil {
		return err
	}
	fmt.Printf("adapter: %s now answers to %q\n", adapter.Metadata.Name, adapter.Spec.Alias)
	return nil
}

// writeAdapterStatus writes the status when it differs from what the
// object already carries, and writes nothing when it does not. The
// pass runs on every bus signal and every kernel event, and an
// unconditional write would send a request to the API server on each
// of them.
func (i *inventory) writeAdapterStatus(adapter *Adapter, status AdapterStatus) error {
	if adapter.Status == status {
		return nil
	}
	adapter.Status = status
	adapter.APIVersion, adapter.Kind = pairingAPI, adapterKind
	if err := replaceStatus(i.client, adapterPath(adapter.Metadata.Name), adapter); err != nil {
		return fmt.Errorf("writing the status of %s: %w", adapter.Metadata.Name, err)
	}
	return nil
}
