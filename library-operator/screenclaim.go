package main

// The volume a screen's catalog agent runs on. Every screen in a
// namespace with one Catalog holds a claim of its own, sized from that
// Catalog and classed by its screens block, so a screen that restarts syncs
// a delta. A screen the scheduler cannot place where its volume is loses
// both the pod and the claim, and the next pass creates them again.

import (
	"context"
	"errors"
	"slices"
	"time"
)

// The claim one screen mounts, derived from the screen pod's name, so
// every pass names the same claim and the operator keeps no record of it.
func screenClaimName(player string) string {
	return screenPodName(player) + "-catalog"
}

// The two marks a screen's claim carries, which are two of the three
// guards on the one delete the operator sends for a claim.
func screenClaimLabels(player string) map[string]string {
	return map[string]string{
		scannerLabelKey: screenLabelValue,
		playerLabelKey:  player,
	}
}

// The claim a screen's agent runs on. It is ReadWriteOnce, because one
// agent writes one SQLite database, sized from the namespace Catalog, classed
// by spec.screens.storageClassName, and owned by the Player as the pod is. An
// empty StorageClassName is omitted, so the cluster's default binds it.
func buildScreenClaim(player *Player, catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            screenClaimName(player.Metadata.Name),
			Namespace:       player.Metadata.Namespace,
			Labels:          screenClaimLabels(player.Metadata.Name),
			OwnerReferences: []OwnerReference{playerOwner(player)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": catalogStorageSize(catalog)},
			},
			StorageClassName: catalog.Spec.Screens.StorageClassName,
		},
	}
}

// The claim is created when there is none and left alone when it
// stands, the rule standCatalogClaim follows, because a claim's spec is
// immutable once it binds. A conflict on the create means another writer got
// there first, which is success.
func (o *operator) standScreenClaim(ctx context.Context, player *Player, catalog *NamespaceCatalog) error {
	namespace, name := player.Metadata.Namespace, screenClaimName(player.Metadata.Name)

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	_, err = CreatePersistentVolumeClaim(ctx, o.client, buildScreenClaim(player, catalog))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// How long a screen pod may carry PodScheduled False before the
// operator takes its claim away. It is a variable so a test drives it in
// milliseconds.
var unschedulableGrace = 5 * time.Minute

// Whether the scheduler has refused this pod for longer than the
// grace. The verdict is the API server's own lastTransitionTime, so a
// restarted operator holds the verdict the one before it held, and no pass
// keeps a timer. A condition with no time is no verdict yet.
func unschedulablePastGrace(pod *Pod, now time.Time) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type != podScheduled || condition.Status != conditionIsFalse {
			continue
		}
		return !condition.LastTransitionTime.IsZero() &&
			now.Sub(condition.LastTransitionTime) > unschedulableGrace
	}
	return false
}

// The three guards on the delete: the claim's name is the derived
// screen claim name, it carries the screen name label and the player label,
// and its controller ownerReference names the Player with the UID this pass
// read. A Library's media claim matches none of the three.
func claimBelongsToScreen(claim *PersistentVolumeClaim, player *Player) bool {
	labels := claim.Metadata.Labels
	return claim.Metadata.Name == screenClaimName(player.Metadata.Name) &&
		labels[scannerLabelKey] == screenLabelValue &&
		labels[playerLabelKey] == player.Metadata.Name &&
		slices.Contains(claim.Metadata.OwnerReferences, playerOwner(player))
}

// A node-local volume binds to the node the pod first landed on, so a
// screen whose display moved is unschedulable for good. Past the grace, on a
// Bound claim of this Player's own, the operator deletes the pod and the
// claim, and the next pass creates both on the new node. Only a Bound claim
// is deleted, so a claim that never binds leaves the recovery quiet.
func (o *operator) recoverUnschedulableScreen(ctx context.Context, player *Player, pod *Pod, now time.Time) (bool, error) {
	if pod == nil || !unschedulablePastGrace(pod, now) {
		return false, nil
	}
	namespace, name := player.Metadata.Namespace, screenClaimName(player.Metadata.Name)

	claim, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if claim.Status.Phase != claimBound || !claimBelongsToScreen(claim, player) {
		return false, nil
	}
	if err := DeletePod(ctx, o.client, namespace, pod.Metadata.Name); err != nil {
		return false, err
	}
	return true, DeletePersistentVolumeClaim(ctx, o.client, namespace, name)
}
