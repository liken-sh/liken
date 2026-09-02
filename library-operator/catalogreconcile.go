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

// ReconcileCatalogs stands each namespace's catalog cluster from
// its one Catalog: the pod that holds the durable catalog and reports
// it, the claim under that pod, and the Service and EndpointSlice the
// agents find each other through. All four are owned by the Catalog,
// which is their real owner: they describe the namespace's one
// Corrosion cluster. A namespace with more than one Catalog marks every
// Catalog in it Blocked and stands nothing new. A failure in one
// namespace is reported, and the rest still stand.
//
// The members the pass hands in are every pod that holds a catalog
// agent, read once for the whole pass: the catalog pod, the pods of the
// Jobs that are running, and the screen pods.
func (o *operator) reconcileCatalogs(ctx context.Context, byNamespace map[string][]*NamespaceCatalog, members []Pod, now time.Time) {
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
		pod, err := o.standCatalogPod(ctx, catalog)
		if err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog pod in %s: %v\n", namespace, err)
		}
		if err := o.standCatalogService(ctx, namespace, owners); err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog service in %s: %v\n", namespace, err)
		}
		if err := o.standCatalogEndpoints(ctx, namespace, owners, members); err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog endpoints in %s: %v\n", namespace, err)
		}
		if err := o.writeCatalogStatus(ctx, catalog, standingCatalogStatus(catalog, pod, members, now)); err != nil {
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

// StandingCatalogStatus reports the cluster the Catalog stands:
// every member agent pod of the namespace and the storage size the
// agents were given. Ready follows the catalog pod alone, because that
// pod is what holds the durable catalog and reports it; a Job's pod
// comes and goes and a screen pod holds a copy, so neither decides
// whether the namespace's catalog stands.
func standingCatalogStatus(catalog *NamespaceCatalog, pod *Pod, pods []Pod, now time.Time) CatalogStatus {
	members := catalogMembers(catalog.Metadata.Namespace, pods)
	condition := Condition{
		Type:               catalogConditionReady,
		Status:             ConditionTrue,
		ObservedGeneration: catalog.Metadata.Generation,
		Reason:             catalogReasonStanding,
		Message:            fmt.Sprintf("the namespace catalog stands with %d member agents", len(members)),
	}
	if reason, message := catalogPodBlocker(pod); reason != "" {
		condition.Status = ConditionFalse
		condition.Reason = reason
		condition.Message = message
	}
	return CatalogStatus{
		Members:     members,
		StorageSize: catalogStorageSize(catalog),
		Conditions:  SetCondition(slices.Clone(catalog.Status.Conditions), condition, now),
	}
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
