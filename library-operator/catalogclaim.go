package main

// The catalog volumes. There is one per Library, which its worker Jobs
// mount in turn, and one for the durable copies of the namespace's
// catalog, which every copy mounts. All are sized from the namespace
// Catalog, because every agent holds the whole namespace's catalog. Each
// is built ReadWriteOnce, and standClaim writes it ReadWriteMany on a
// per-node class.

import (
	"context"
)

// scannerCatalogClaimName is the durable catalog volume one Library's
// worker Jobs mount. It is derived from the Library name, so every pass
// names the same claim and the operator keeps no record of it. On a class
// that is not per-node the claim is ReadWriteOnce, and that is what
// serializes one library's Jobs.
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
// It asks for ReadWriteOnce, and standClaim writes ReadWriteMany in its
// place on a per-node class. It is sized from the namespace Catalog,
// because each agent holds the whole namespace's catalog. It is owned by
// the Library, so it survives a pod roll and is collected with the
// Library. It binds to the libraries' class, because it is a working copy
// and not the catalog of record. An empty class is omitted, so the
// cluster's default StorageClass binds it.
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
			StorageClassName: libraryStorageClass(catalog),
		},
	}
}

// standCatalogClaim provisions the claim one Library's workers mount,
// through standClaim. A size a later Catalog grows to reaches a new claim
// and not this one, because a claim's spec is immutable once it binds.
func (o *operator) standCatalogClaim(ctx context.Context, library *Library, catalog *NamespaceCatalog) error {
	return o.standClaim(ctx, buildCatalogClaim(library, catalog))
}

// catalogClaimFor names the claim every durable copy of the catalog
// mounts: the one the Catalog names, or the one the operator provisions
// when it names none. Every copy mounts the same claim. On a per-node
// class that gives each copy a directory of its own on the node it runs
// on. On any other class the claim binds to one node, so one copy
// stands.
func catalogClaimFor(catalog *NamespaceCatalog) string {
	if catalog.Spec.Storage.ClaimName != "" {
		return catalog.Spec.Storage.ClaimName
	}
	return catalogStoreOf(catalog).base
}

// The catalog pod's own claim, owned by the Catalog, so the
// garbage collector takes it with the Catalog and the standing catalog
// survives every roll of the pod.
//
// There is one claim for the store, at the store's own name, on the
// class and the size every copy shares.
func buildCatalogPodClaim(catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            catalogStoreOf(catalog).base,
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
