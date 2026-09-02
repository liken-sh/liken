package main

// The durable catalog volumes. There is one per Library, which
// its worker Jobs mount in turn, and one per namespace, which the
// catalog pod holds. Both are ReadWriteOnce, because one agent writes
// one SQLite database, and both are sized from the namespace Catalog,
// because every agent holds the whole namespace's catalog.

import (
	"context"
	"errors"
)

// scannerCatalogClaimName is the durable catalog volume one Library's
// worker Jobs mount. It is derived from the Library name, so every pass
// names the same claim and the operator keeps no record of it. Its
// ReadWriteOnce is what serializes one library's Jobs.
//
// The claim must hold a database whose schema matches this release.
// Corrosion refuses to change the primary key of a database it already
// holds, and an old database started against a new schema starts
// quietly stale: it logs the refusal, serves the old tables, and fails
// every write of the new shape, one request at a time. The catalog is
// derived, so the cure is cheap: delete the claims when a release
// changes a primary key, and the next pass provisions fresh ones that
// one full walk refills.
func scannerCatalogClaimName(library string) string {
	return library + "-catalog"
}

// buildCatalogClaim writes the catalog claim one Library's workers take.
// It is ReadWriteOnce, because one agent writes one SQLite database. It is
// sized from the namespace Catalog, because each agent holds the whole
// namespace's catalog. It is owned by the Library, so it survives a pod roll
// and is collected with the Library. An empty StorageClassName is omitted,
// so the cluster's default StorageClass binds it.
func buildCatalogClaim(library *Library, catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            scannerCatalogClaimName(library.Metadata.Name),
			Namespace:       library.Metadata.Namespace,
			Labels:          libraryLabels(library.Metadata.Name),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": catalogStorageSize(catalog)},
			},
			StorageClassName: catalog.Spec.Storage.StorageClassName,
		},
	}
}

// standCatalogClaim creates the catalog claim when there is none and leaves
// an existing one alone. A PersistentVolumeClaim's spec is immutable once it
// binds, so the operator provisions the claim rather than reconciling it. A
// size a later Catalog grows to reaches a new claim, not this one. A conflict
// on the create means another writer got there first, which is success.
func (o *operator) standCatalogClaim(ctx context.Context, library *Library, catalog *NamespaceCatalog) error {
	namespace := library.Metadata.Namespace
	name := scannerCatalogClaimName(library.Metadata.Name)

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}

	_, err = CreatePersistentVolumeClaim(ctx, o.client, buildCatalogClaim(library, catalog))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// The durable catalog volume the namespace's catalog pod holds,
// named from the Catalog, unless the Catalog names a claim of its own.
func catalogPodClaimName(catalog string) string {
	return catalog + "-catalog"
}

// The claim the catalog pod mounts: the one the Catalog names,
// or the one the operator provisions when it names none.
func catalogClaimFor(catalog *NamespaceCatalog) string {
	if catalog.Spec.Storage.ClaimName != "" {
		return catalog.Spec.Storage.ClaimName
	}
	return catalogPodClaimName(catalog.Metadata.Name)
}

// The catalog pod's own claim, owned by the Catalog, so the
// garbage collector takes it with the Catalog and the standing catalog
// survives every roll of the pod.
func buildCatalogPodClaim(catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            catalogPodClaimName(catalog.Metadata.Name),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          catalogPodLabels(),
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": catalogStorageSize(catalog)},
			},
			StorageClassName: catalog.Spec.Storage.StorageClassName,
		},
	}
}
