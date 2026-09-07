package main

// pernodevolume.go stands every claim this operator writes, and the
// PersistentVolume a claim on a per-node class binds to. The per-node
// driver provisions nothing: whoever wants a volume of that class writes
// the PersistentVolume, and the claim names it. So the operator writes
// the volume first and the claim after it. A claim on any other class is
// written with no volume, and the cluster's provisioner binds it. Every
// volume the operator writes carries its labels, and the sweep at the end
// of a pass removes the ones whose claim is gone.

import (
	"context"
	"errors"
	"fmt"
)

// The provisioner of a per-node StorageClass. The operator reads the
// provisioner and never matches the class name, because a cluster names
// its classes as it likes.
const perNodeProvisioner = "per-node.liken.sh"

// The labels every volume this operator writes carries: the namespace of
// the claim the volume belongs to, and the claim's name. A
// PersistentVolume is cluster-scoped, so no ownerReference can tie it to
// a Catalog, a Library, or a Player. The labels are the mark the sweep
// selects on in place of an owner.
const (
	claimNamespaceLabelKey = "library.liken.sh/claim-namespace"
	claimLabelKey          = "library.liken.sh/claim"
)

// The selector the sweep lists with. It names the two label keys and no
// value, so one request answers every volume this operator wrote, in
// every namespace.
const operatorVolumeQuery = "labelSelector=" + claimNamespaceLabelKey + "," + claimLabelKey

// perNodeVolumeName names the volume one claim binds to. A
// PersistentVolume is cluster-scoped, so the name carries the claim's
// namespace. The driver's design names the volume handle after the
// PersistentVolume, so this name is the handle as well.
func perNodeVolumeName(namespace, claim string) string {
	return namespace + "-" + claim
}

func volumeClaimLabels(namespace, claim string) map[string]string {
	return map[string]string{claimNamespaceLabelKey: namespace, claimLabelKey: claim}
}

// buildPerNodeVolume builds the volume a per-node claim binds to. It
// names the driver, the handle, the claim's class and size, and a
// claimRef that reserves the volume for that one claim. The access mode
// is ReadWriteMany, because every node that mounts the volume holds a
// copy of its own. The reclaim policy is Retain, so the volume stays when
// the claim goes, and the sweep in this file is what removes it.
func buildPerNodeVolume(claim *PersistentVolumeClaim) *PersistentVolume {
	namespace, name := claim.Metadata.Namespace, claim.Metadata.Name
	volume := perNodeVolumeName(namespace, name)
	return &PersistentVolume{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolume",
		Metadata: ObjectMeta{
			Name:   volume,
			Labels: volumeClaimLabels(namespace, name),
		},
		Spec: PersistentVolumeSpec{
			StorageClassName:              claim.Spec.StorageClassName,
			AccessModes:                   []string{accessModeReadWriteMany},
			Capacity:                      map[string]string{"storage": claim.Spec.Resources.Requests["storage"]},
			PersistentVolumeReclaimPolicy: reclaimRetain,
			ClaimRef:                      &ClaimReference{Namespace: namespace, Name: name},
			CSI:                           &CSIPersistentVolumeSource{Driver: perNodeProvisioner, VolumeHandle: volume},
		},
	}
}

// standClaim creates a claim when there is none and leaves an existing
// one alone, because a claim's spec is immutable once it binds. A claim
// on a per-node class gets its volume first, and then names that volume
// in spec.volumeName with ReadWriteMany, so it binds at once and waits on
// no provisioner. A claim on any other class is written as it was built.
// A conflict on the claim create means another writer got there first,
// which is success.
func (o *operator) standClaim(ctx context.Context, claim *PersistentVolumeClaim) error {
	namespace, name := claim.Metadata.Namespace, claim.Metadata.Name

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}

	perNode, err := o.classIsPerNode(ctx, claim.Spec.StorageClassName)
	if err != nil {
		return err
	}
	if perNode {
		if err := o.standPerNodeVolume(ctx, claim); err != nil {
			return err
		}
		claim.Spec.AccessModes = []string{accessModeReadWriteMany}
		claim.Spec.VolumeName = perNodeVolumeName(namespace, name)
	}

	_, err = CreatePersistentVolumeClaim(ctx, o.client, claim)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// standPerNodeVolume writes the volume one per-node claim binds to. A
// volume of that name may already exist, left by a claim of the same
// name that is gone: a Player, a Library, or a Catalog that was deleted
// and declared again. That volume is Released, or its claimRef holds the
// old claim's uid, so the binder would never give it to the fresh claim,
// and the sweep would delete it under a claim that named it. The
// operator deletes such a volume and writes a fresh one before it writes
// the claim. A volume that is Bound, or one whose claimRef names the
// claim with no uid yet, is the right volume and stays. A not-found read
// after the conflict means another writer removed the volume between the
// two requests, so the operator writes it again.
//
// The pass never goes on to the claim without a volume it has seen or
// written, because a claim whose volumeName names a missing volume stays
// Pending, and standClaim leaves an existing claim alone forever.
func (o *operator) standPerNodeVolume(ctx context.Context, claim *PersistentVolumeClaim) error {
	_, err := CreatePersistentVolume(ctx, o.client, buildPerNodeVolume(claim))
	if !errors.Is(err, ErrConflict) {
		return err
	}
	name := perNodeVolumeName(claim.Metadata.Namespace, claim.Metadata.Name)
	standing, err := GetPersistentVolume(ctx, o.client, name)
	if errors.Is(err, ErrNotFound) {
		return o.rewritePerNodeVolume(ctx, claim)
	}
	if err != nil {
		return err
	}
	if !volumeIsSpent(standing) {
		return nil
	}
	if err := DeletePersistentVolume(ctx, o.client, name); err != nil {
		return err
	}
	return o.rewritePerNodeVolume(ctx, claim)
}

// rewritePerNodeVolume writes the volume a second time, after the first
// write met a volume that is gone or spent. A conflict here means the
// deleted volume is still going away under its protection finalizer, so
// the pass reports it and the next pass writes the volume and then the
// claim. Success here would write a claim that names a volume about to
// vanish.
func (o *operator) rewritePerNodeVolume(ctx context.Context, claim *PersistentVolumeClaim) error {
	_, err := CreatePersistentVolume(ctx, o.client, buildPerNodeVolume(claim))
	if errors.Is(err, ErrConflict) {
		name := perNodeVolumeName(claim.Metadata.Namespace, claim.Metadata.Name)
		return fmt.Errorf("volume %s is still going away", name)
	}
	return err
}

// volumeIsSpent reports whether a volume belongs to a claim that is gone.
// A Released volume is spent. So is a volume that is not Bound and whose
// claimRef carries a uid, because the binder writes that uid when it
// binds, and a claim it names is deleted or the volume would be Bound.
func volumeIsSpent(volume *PersistentVolume) bool {
	if volume.Status.Phase == volumeBound {
		return false
	}
	return volume.Status.Phase == volumeReleased ||
		(volume.Spec.ClaimRef != nil && volume.Spec.ClaimRef.UID != "")
}

// classIsPerNode reports whether the per-node driver serves the class a
// claim names. A claim that names no class takes the cluster's default,
// which the operator does not read, so it is not per-node. A class the
// cluster does not serve is not per-node either: the claim stays
// Pending, and the person who named the class reads why on the claim.
//
// The answer is kept for the rest of the pass, because one pass writes
// many claims on the same few classes. The pass clears the map when it
// starts, so a class a person edits is read again on the next pass, and
// the operator watches no storageclasses.
func (o *operator) classIsPerNode(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	if perNode, held := o.perNodeClasses[name]; held {
		return perNode, nil
	}
	class, err := GetStorageClass(ctx, o.client, name)
	if errors.Is(err, ErrNotFound) {
		class = &StorageClass{}
	} else if err != nil {
		return false, err
	}
	perNode := class.Provisioner == perNodeProvisioner
	o.perNodeClasses[name] = perNode
	return perNode, nil
}

// sweepReleasedVolumes deletes every volume this operator wrote whose
// claim is gone. A deleted claim leaves its volume Released, and no
// controller deletes a Retain volume, so this sweep is what frees the
// copies the driver holds on each node. The delete takes only a volume
// that carries both labels with a value, so a volume another writer
// labeled in part is left where it is.
func (o *operator) sweepReleasedVolumes(ctx context.Context) error {
	volumes, err := ListPersistentVolumes(ctx, o.client, operatorVolumeQuery)
	if err != nil {
		return err
	}
	for index := range volumes.Items {
		volume := &volumes.Items[index]
		labels := volume.Metadata.Labels
		if labels[claimNamespaceLabelKey] == "" || labels[claimLabelKey] == "" {
			continue
		}
		if volume.Status.Phase != volumeReleased {
			continue
		}
		if err := DeletePersistentVolume(ctx, o.client, volume.Metadata.Name); err != nil {
			return err
		}
	}
	return nil
}
