package main

// catalogclaim.go provisions the durable catalog volume. The operator
// provisions one catalog PersistentVolumeClaim per scanner pod. It is
// ReadWriteOnce, sized from the namespace Catalog, and owned by the Library,
// so the claim survives a pod roll and is garbage-collected with the
// Library.

import (
	"context"
	"errors"
)

// scannerCatalogClaimName is the durable catalog volume one Library's
// scanner pod mounts. It is derived from the Library name, so every pass
// names the same claim and the operator keeps no record of it.
//
// The suffix carries the schema revision. Corrosion refuses to change
// the primary key of a database it already holds, and an old database
// started against the new schema starts quietly stale: it logs the
// refusal, serves the old tables, and fails every write of the new
// shape, one request at a time. So a fresh database is the migration,
// and the versioned name is what provisions one.
func scannerCatalogClaimName(library string) string {
	return library + "-catalog-v2"
}

// buildCatalogClaim writes the catalog claim one Library's scanner takes.
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
			Labels:          scannerLabels(library.Metadata.Name),
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
