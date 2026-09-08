package main

// Catalogreconcile.go stands the namespace catalog. A pass reconciles each
// namespace's one Catalog into the catalog Service, the EndpointSlice, and
// the Catalog's own status. A namespace with more than one Catalog stands
// nothing new this pass: every Catalog in it is marked Blocked, and the
// Service and the slice that already stand are left as they are.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"time"
)

// reconcileCatalogs stands each namespace's catalog cluster from its one
// Catalog: the durable copies that hold the catalog, of which the first
// reports it, the claim under each copy, and the Service and EndpointSlice
// the agents find each other through. Every object is owned by the
// Catalog, which is their real owner: they describe the namespace's one
// Corrosion cluster. A namespace with more than one Catalog marks every
// Catalog in it Blocked and stands nothing new. A failure in one namespace
// is reported, and the rest still stand.
//
// Each namespace stands a second Corrosion cluster beside the catalog,
// the progress store, with durable copies, a claim under each, a Service,
// and a slice of its own. The two clusters never mix: the catalog is
// rebuilt by a rescan and progress is not, so they hold different
// durability rules.
//
// A Catalog that names a Jellyfin server also stands the jellyfin pod and its
// Service, one pair per namespace. A Catalog that names none stands neither,
// and the pass deletes the pair it stood before.
//
// the same Catalog stands one more thing, once: the backfill Job that carries
// what Jellyfin already held into the progress store. The pass reads the
// worker Jobs it was handed to tell where that run stands, and reports it on
// the Catalog's own status.
//
// The members the pass hands in are every pod that holds a catalog agent,
// read once for the whole pass: the durable copies of the catalog, the
// pods of the Jobs that are running, and the screen pods. The progress
// agents and the nodes are read here, because this step alone needs them.
func (o *operator) reconcileCatalogs(ctx context.Context, byNamespace map[string][]*NamespaceCatalog,
	members []Pod, jobs []Job, now time.Time) {
	// A list that fails costs this pass its progress slices and nothing
	// else. The pods stand, and the next pass writes the slices.
	progressMembers, err := ListProgressMemberPods(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing progress member pods: %v\n", err)
		progressMembers = &PodList{}
	}
	// The nodes are read for the heal alone, so a list that fails costs
	// this pass its heal and nothing else. The copies stand, and the next
	// pass reads the nodes again.
	nodes, err := ListNodes(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing nodes: %v\n", err)
		nodes = &NodeList{}
	}
	// The heal goes before the stand, so a copy freed from a dead node is
	// stood again by the same pass that freed it.
	o.healStrandedStoreReplicas(ctx, append(slices.Clone(members), progressMembers.Items...), nodes.Items, now)
	for _, namespace := range slices.Sorted(maps.Keys(byNamespace)) {
		catalogs := byNamespace[namespace]
		if len(catalogs) != 1 {
			for _, catalog := range catalogs {
				if err := o.writeCatalogStatus(ctx, catalog, blockedCatalogStatus(catalog, catalogs, now)); err != nil {
					fmt.Fprintf(os.Stderr, "marking the catalog %s/%s blocked: %v\n",
						catalog.Metadata.Namespace, catalog.Metadata.Name, err)
				}
			}
			continue
		}
		catalog := catalogs[0]
		owners := []OwnerReference{catalogObjectOwner(catalog)}
		// The pod is stood before the status is written, so a Catalog
		// that has just been created reports its own pod on the pass
		// that made it rather than one tick later.
		catalogPods, err := o.standCatalogPods(ctx, catalog)
		if err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog pods in %s: %v\n", namespace, err)
		}
		if err := o.standCatalogService(ctx, namespace, owners); err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog service in %s: %v\n", namespace, err)
		}
		if err := o.standCatalogEndpoints(ctx, namespace, owners, members); err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog endpoints in %s: %v\n", namespace, err)
		}
		progressPods, err := o.standProgressPods(ctx, catalog)
		if err != nil {
			fmt.Fprintf(os.Stderr, "standing the progress pods in %s: %v\n", namespace, err)
		}
		if err := o.standProgressService(ctx, namespace, owners); err != nil {
			fmt.Fprintf(os.Stderr, "standing the progress service in %s: %v\n", namespace, err)
		}
		if err := o.standProgressEndpoints(ctx, namespace, owners, progressMembers.Items); err != nil {
			fmt.Fprintf(os.Stderr, "standing the progress endpoints in %s: %v\n", namespace, err)
		}
		// The jellyfin pair stands beside the progress store while
		// the Catalog names a Jellyfin server, and comes down when it
		// names none.
		if err := o.standJellyfin(ctx, catalog); err != nil {
			fmt.Fprintf(os.Stderr, "standing the jellyfin role in %s: %v\n", namespace, err)
		}
		// the one-time backfill stands after the pair, on the Jobs
		// this pass already listed and the copies of the progress
		// store it just stood. It reports where it stands rather than
		// failing, because a backfill that cannot run costs the
		// Catalog nothing else.
		backfill := o.standJellyfinBackfill(ctx, catalog, jobs, progressPods, now)
		// The classes are read before the status is built, so a Catalog
		// that asks for copies a class cannot hold reports that on the
		// same pass.
		reason, message, err := o.blockedStore(ctx, catalog)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the storage classes in %s: %v\n", namespace, err)
		}
		status := standingCatalogStatus(catalog, catalogPods, progressPods, members,
			blocker{reason: reason, message: message}, now)
		// the Jellyfin half is set here and not built with the rest,
		// because it is the verdict of the step above and not a
		// reading of the pods this status reports.
		status.Jellyfin = backfill
		if err := o.writeCatalogStatus(ctx, catalog, status); err != nil {
			fmt.Fprintf(os.Stderr, "writing the catalog status in %s: %v\n", namespace, err)
		}
	}
}

// CatalogObjectOwner is the ownerReference the catalog Service and
// EndpointSlice carry. One Catalog per namespace owns both, so it is the
// controller, and the garbage collector removes them when the Catalog is
// deleted.
func catalogObjectOwner(catalog *NamespaceCatalog) OwnerReference {
	return OwnerReference{
		APIVersion: catalogAPIVersion,
		Kind:       "Catalog",
		Name:       catalog.Metadata.Name,
		UID:        catalog.Metadata.UID,
		Controller: true,
	}
}

// standingCatalogStatus reports the cluster the Catalog stands: every
// member agent pod of the namespace, the storage size the agents were
// given, the copies of each store that are up beside the copies the
// Catalog asks for, and one entry per screen pod with the two claims it
// runs on.
//
// Ready follows the durable copies of the catalog alone, because those
// pods hold the catalog and report it. A Job's pod comes and goes, and a
// screen pod holds a copy, so neither decides whether the namespace's
// catalog stands. Ready is True when every copy is up, and False on the
// first copy that is not, with that copy's own reason. A Catalog that
// asks for copies on a class that cannot hold them is False with the
// class verdict before any copy is read.
func standingCatalogStatus(catalog *NamespaceCatalog, catalogPods, progressPods []*Pod, pods []Pod,
	blocked blocker, now time.Time) CatalogStatus {
	members := catalogMembers(catalog.Metadata.Namespace, pods)
	condition := Condition{
		Type:               catalogConditionReady,
		Status:             ConditionTrue,
		ObservedGeneration: catalog.Metadata.Generation,
		Reason:             catalogReasonStanding,
		Message:            fmt.Sprintf("the namespace catalog stands with %d member agents", len(members)),
	}
	if reason, message := blocked.or(catalogPodsBlocker(catalogPods)); reason != "" {
		condition.Status = ConditionFalse
		condition.Reason = reason
		condition.Message = message
	}
	return CatalogStatus{
		Members:     members,
		StorageSize: catalogStorageSize(catalog),
		Replicas: CatalogReplicas{
			Catalog:  storeReplicaCount(catalogPods, catalogContainer, catalogReplicaCount(catalog)),
			Progress: storeReplicaCount(progressPods, progressContainer, progressReplicaCount(catalog)),
		},
		Screens:    catalogScreens(catalog.Metadata.Namespace, pods),
		Conditions: SetCondition(slices.Clone(catalog.Status.Conditions), condition, now),
	}
}

// blocker is one verdict on why a Catalog is not Ready: the reason a
// program matches on, and the message a person reads. An empty reason is
// no verdict.
type blocker struct {
	reason  string
	message string
}

// or returns this verdict when it holds one, and the verdict passed in
// when it does not. The class verdict comes first, because a copy the
// operator never stood is the reason the Catalog waits, and a pod
// verdict says nothing about the copies that are missing.
func (b blocker) or(reason, message string) (string, string) {
	if b.reason != "" {
		return b.reason, b.message
	}
	return reason, message
}

// The first copy of the catalog that is not up, so the condition names
// one thing the namespace waits on and not a list. A copy the scheduler
// cannot place carries the scheduler's own words, which is the sentence a
// person acts on.
func catalogPodsBlocker(pods []*Pod) (string, string) {
	for _, pod := range pods {
		if reason, message := catalogPodBlocker(pod); reason != "" {
			return reason, message
		}
	}
	return "", ""
}

// The screen pods of the namespace, in Player order, out of the member
// pods the pass read. A screen carries the name label of its own kind, so the
// catalog pod and a Job's pod are not screens.
func catalogScreens(namespace string, pods []Pod) []CatalogScreen {
	screens := []CatalogScreen{}
	for index := range pods {
		pod := &pods[index]
		if pod.Metadata.Namespace != namespace ||
			pod.Metadata.Labels[scannerLabelKey] != screenLabelValue {
			continue
		}
		screens = append(screens, CatalogScreen{
			Player:   pod.Metadata.Labels[playerLabelKey],
			Claim:    screenClaimOf(pod, catalogVolumeName),
			ArtClaim: screenClaimOf(pod, artCacheVolumeName),
			Node:     pod.Spec.NodeName,
			Phase:    pod.Status.Phase,
		})
	}
	sort.Slice(screens, func(one, other int) bool {
		return screens[one].Player < screens[other].Player
	})
	return screens
}

// The claim behind one of a screen pod's volumes, read off the pod
// itself, because the pod is what states which volume it has. A volume
// that is an emptyDir names no claim.
func screenClaimOf(pod *Pod, volumeName string) string {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == volumeName && volume.PersistentVolumeClaim != nil {
			return volume.PersistentVolumeClaim.ClaimName
		}
	}
	return ""
}

// BlockedCatalogStatus marks a Catalog Blocked when its namespace holds more
// than one, with a condition that names the conflict. The Catalog stands no
// cluster, so it reports no members.
func blockedCatalogStatus(catalog *NamespaceCatalog, catalogs []*NamespaceCatalog, now time.Time) CatalogStatus {
	condition := Condition{
		Type:               catalogConditionReady,
		Status:             ConditionFalse,
		ObservedGeneration: catalog.Metadata.Generation,
		Reason:             catalogReasonManyCatalogs,
		Message:            manyCatalogsMessage(catalogs),
	}
	return CatalogStatus{
		StorageSize: catalogStorageSize(catalog),
		Conditions:  SetCondition(slices.Clone(catalog.Status.Conditions), condition, now),
	}
}

// CatalogMembers is the member agent pods of the namespace's
// cluster: the catalog pod, the pods of the Jobs that are running, and
// the screen pods, by name, sorted so two passes read the same list.
func catalogMembers(namespace string, pods []Pod) []string {
	members := []string{}
	for index := range pods {
		if pods[index].Metadata.Namespace == namespace {
			members = append(members, pods[index].Metadata.Name)
		}
	}
	sort.Strings(members)
	return members
}

// WriteCatalogStatus writes only a status that differs from the one the
// Catalog carries, the rule writeLibraryStatus also follows, so a write on
// every pass does not wake the catalogs watch that wakes the pass. A conflict
// means another writer got there first, which the next pass reads.
func (o *operator) writeCatalogStatus(ctx context.Context, catalog *NamespaceCatalog, desired CatalogStatus) error {
	same, err := sameCatalogStatus(catalog.Status, desired)
	if err != nil || same {
		return err
	}
	catalog.Status = desired
	_, err = PutCatalogStatus(ctx, o.client, catalog)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// SameCatalogStatus compares the marshaled form, because that is what the
// API server stores and what each field's omitempty decides.
func sameCatalogStatus(current, desired CatalogStatus) (bool, error) {
	was, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	wants, err := json.Marshal(desired)
	if err != nil {
		return false, err
	}
	return string(was) == string(wants), nil
}
