package main

// The two claims a screen holds. Every screen in a namespace with one
// Catalog holds a catalog claim and an art claim of its own, both sized
// from that Catalog and classed by its screens block, so a screen that
// restarts syncs a delta and draws the wall from art it already scaled.
// A screen the scheduler cannot place where its volumes are loses the
// pod and both claims, and the next pass creates them again.

import (
	"context"
	"errors"
	"slices"
	"time"
)

// The claims one screen mounts, derived from the screen pod's name, so
// every pass names the same claims and the operator keeps no record of them.
func screenClaimName(player string) string {
	return screenPodName(player) + "-catalog"
}

// The claim the screen's scaled art is on, named beside the catalog
// claim.
func screenArtClaimName(player string) string {
	return screenPodName(player) + "-art"
}

// The two marks a screen's claims carry, which are two of the three
// guards on every delete the operator sends for a claim.
func screenClaimLabels(player string) map[string]string {
	return map[string]string{
		scannerLabelKey: screenLabelValue,
		playerLabelKey:  player,
	}
}

// One of a screen's claims. It is ReadWriteOnce, because one writer
// holds one volume. It is classed by spec.screens.storageClassName, so
// both claims land on the machine that holds the display, and owned by
// the Player as the pod is. An empty StorageClassName is omitted, so
// the cluster's default binds it.
func buildScreenClaim(player *Player, catalog *NamespaceCatalog, name, size string) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            name,
			Namespace:       player.Metadata.Namespace,
			Labels:          screenClaimLabels(player.Metadata.Name),
			OwnerReferences: []OwnerReference{playerOwner(player)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": size},
			},
			StorageClassName: catalog.Spec.Screens.StorageClassName,
		},
	}
}

// The two claims of one screen, in the order the pass creates them:
// the rows the agent syncs, then the art the browser scaled.
func screenClaims(player *Player, catalog *NamespaceCatalog) []*PersistentVolumeClaim {
	name := player.Metadata.Name
	return []*PersistentVolumeClaim{
		buildScreenClaim(player, catalog, screenClaimName(name), catalogStorageSize(catalog)),
		buildScreenClaim(player, catalog, screenArtClaimName(name), artCacheSize(catalog)),
	}
}

// Both of a screen's claims stand before its pod, because a pod that
// named a claim nothing had created would stay Pending until the next
// pass.
func (o *operator) standScreenClaims(ctx context.Context, player *Player, catalog *NamespaceCatalog) error {
	for _, claim := range screenClaims(player, catalog) {
		if err := o.standScreenClaim(ctx, claim); err != nil {
			return err
		}
	}
	return nil
}

// A claim is created when there is none and left alone when it
// stands, the rule standCatalogClaim follows, because a claim's spec is
// immutable once it binds. A conflict on the create means another writer got
// there first, which is success.
func (o *operator) standScreenClaim(ctx context.Context, claim *PersistentVolumeClaim) error {
	namespace, name := claim.Metadata.Namespace, claim.Metadata.Name

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	_, err = CreatePersistentVolumeClaim(ctx, o.client, claim)
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
func claimBelongsToScreen(claim *PersistentVolumeClaim, player *Player, name string) bool {
	labels := claim.Metadata.Labels
	return claim.Metadata.Name == name &&
		labels[scannerLabelKey] == screenLabelValue &&
		labels[playerLabelKey] == player.Metadata.Name &&
		slices.Contains(claim.Metadata.OwnerReferences, playerOwner(player))
}

// The whole verdict on one claim of a screen: the three guards, and
// Bound. Bound is the loop guard: a claim that never binds must leave
// the recovery quiet, or every pass would delete and create it again.
func screenClaimIsRecoverable(claim *PersistentVolumeClaim, player *Player, name string) bool {
	return claim.Status.Phase == claimBound && claimBelongsToScreen(claim, player, name)
}

// One claim of a screen goes when it passes every guard. An absent
// claim is success, because the recovery deletes both claims and either
// may already be gone.
func (o *operator) deleteScreenClaim(ctx context.Context, player *Player, name string) error {
	namespace := player.Metadata.Namespace

	claim, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !screenClaimIsRecoverable(claim, player, name) {
		return nil
	}
	return DeletePersistentVolumeClaim(ctx, o.client, namespace, name)
}

// A node-local volume binds to the node the pod first landed on, so a
// screen whose display moved is unschedulable for good. Past the grace,
// on a Bound catalog claim of this Player's own, the operator deletes
// the pod, the catalog claim, and the art claim, and the next pass
// creates all three on the new node. The catalog claim is the one the
// recovery reads, because it is the claim the pod cannot start without.
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
	if !screenClaimIsRecoverable(claim, player, name) {
		return false, nil
	}
	if err := DeletePod(ctx, o.client, namespace, pod.Metadata.Name); err != nil {
		return false, err
	}
	if err := DeletePersistentVolumeClaim(ctx, o.client, namespace, name); err != nil {
		return true, err
	}
	return true, o.deleteScreenClaim(ctx, player, screenArtClaimName(player.Metadata.Name))
}
