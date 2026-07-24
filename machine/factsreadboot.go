package machine

// The read side of the boot subtree. Boot holds the configuration this
// boot ran under: the four manifest records, the actuated storage, and
// the four standing rejections. It is the half of drift detection that
// only init can supply, so the operator reads it here and compares it
// against the cluster's copies.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func (t FactsTree) readBoot() (BootStatus, error) {
	b := BootStatus{}

	var err error
	if b.Time, err = t.readTime("boot/time"); err != nil {
		return BootStatus{}, err
	}

	manifest, err := t.readRecordFact("boot/manifest")
	if err != nil {
		return BootStatus{}, err
	}
	b.ManifestSource = ManifestSource(manifest["source"])
	b.ManifestHash = manifest["hash"]

	cluster, err := t.readRecordFact("boot/clusterManifest")
	if err != nil {
		return BootStatus{}, err
	}
	b.ClusterManifestSource = ManifestSource(cluster["source"])
	b.ClusterManifestHash = cluster["hash"]

	credentials, err := t.readRecordFact("boot/credentials")
	if err != nil {
		return BootStatus{}, err
	}
	b.CredentialsSource = ManifestSource(credentials["source"])
	b.CredentialsHash = credentials["hash"]

	imports, err := t.readRecordFact("boot/imports")
	if err != nil {
		return BootStatus{}, err
	}
	b.ImportsSource = ManifestSource(imports["source"])
	b.ImportsHash = imports["hash"]
	b.ImportsDiscarded = imports["discarded"] == "true"

	if b.Slot, err = t.readFact("boot/slot"); err != nil {
		return BootStatus{}, err
	}
	if b.Restarts, err = t.readInt("boot/restarts"); err != nil {
		return BootStatus{}, err
	}
	if b.Modules, err = t.readListFact("boot/modules"); err != nil {
		return BootStatus{}, err
	}
	if b.Storage, err = t.readBootStorage(); err != nil {
		return BootStatus{}, err
	}
	if b.Rejection, err = t.readRejection(RejectMachine); err != nil {
		return BootStatus{}, err
	}
	if b.ClusterRejection, err = t.readRejection(RejectCluster); err != nil {
		return BootStatus{}, err
	}
	if b.SystemRejection, err = t.readRejection(RejectSystem); err != nil {
		return BootStatus{}, err
	}
	if b.CredentialsRejection, err = t.readRejection(RejectCredentials); err != nil {
		return BootStatus{}, err
	}
	return b, nil
}

// readBootStorage reads the actuated storage spec, one directory for
// each declared role. A role directory that is absent means the spec
// did not declare that role, so its pointer stays nil.
func (t FactsTree) readBootStorage() (StorageSpec, error) {
	spec := StorageSpec{}
	roleFields := map[StorageRoleName]**StorageRole{
		BIOSBootRole:         &spec.BIOSBoot,
		BootHomeRole:         &spec.BootHome,
		SystemARole:          &spec.SystemA,
		SystemBRole:          &spec.SystemB,
		MachineStateRole:     &spec.MachineState,
		MachineEphemeralRole: &spec.MachineEphemeral,
		ClusterStateRole:     &spec.ClusterState,
		PodStorageRole:       &spec.PodStorage,
		PodEphemeralRole:     &spec.PodEphemeral,
	}
	for _, name := range StorageRoleNames {
		base := filepath.Join("boot", "storage", string(name))
		if _, err := os.Stat(filepath.Join(t.Dir, base)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return StorageSpec{}, err
		}
		role := &StorageRole{}
		var err error
		if role.Device, err = t.readFact(filepath.Join(base, "device")); err != nil {
			return StorageSpec{}, err
		}
		if role.Size, err = t.readFact(filepath.Join(base, "size")); err != nil {
			return StorageSpec{}, err
		}
		*roleFields[name] = role
	}
	return spec, nil
}

// readRejection reads one standing quarantine record. A record
// directory that is absent means the document has no standing
// rejection, so the pointer stays nil.
func (t FactsTree) readRejection(kind RejectionKind) (*Rejection, error) {
	base := filepath.Join("boot", string(kind))
	if _, err := os.Stat(filepath.Join(t.Dir, base)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	r := &Rejection{}
	var err error
	if r.Hash, err = t.readFact(filepath.Join(base, "hash")); err != nil {
		return nil, err
	}
	if r.Reason, err = t.readFact(filepath.Join(base, "reason")); err != nil {
		return nil, err
	}
	at, err := t.readTime(filepath.Join(base, "rejectedAt"))
	if err != nil {
		return nil, err
	}
	if at != nil {
		r.RejectedAt = *at
	}
	return r, nil
}
