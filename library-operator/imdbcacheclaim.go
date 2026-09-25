package main

// imdbcacheclaim.go is the operator's side of the cache of an imdb provider's
// dataset files: the one claim the provider owns, and the volume the enricher
// mounts it through. Every run reads a whole file, and several Jobs a day can
// need the same file, so a node downloads each version of a file once and
// every later run on that node reads it from disk.

import (
	"context"
	"fmt"
)

// The claim of one provider, in the provider's namespace.
func datasetsClaimName(provider string) string {
	return provider + "-datasets"
}

// The four files the rating and the credits read take 1.2 GB, and the
// replacement of one file needs room for a second copy of it, at most 784 MB
// for title.principals. The rest leaves room for the files to grow.
const datasetsClaimSize = "3Gi"

// The provider owns the claim, so a deleted provider deletes its cache with
// it.
func metadataProviderOwner(provider *MetadataProvider) OwnerReference {
	return OwnerReference{
		APIVersion: metadataProviderAPIVersion,
		Kind:       "MetadataProvider",
		Name:       provider.Metadata.Name,
		UID:        provider.Metadata.UID,
		Controller: true,
	}
}

// The claim is on a per-node class, whatever class the libraries use. A
// per-node claim is ReadWriteMany, so a Job on any node mounts it, and each
// node's directory fills the first time a run on that node needs a file.
// standClaim writes the volume and sets the access mode.
func buildDatasetsClaim(provider *MetadataProvider, class string) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            datasetsClaimName(provider.Metadata.Name),
			Namespace:       provider.Metadata.Namespace,
			OwnerReferences: []OwnerReference{metadataProviderOwner(provider)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": datasetsClaimSize},
			},
			StorageClassName: class,
		},
	}
}

// The Cached verdict, and the claim behind it. A claim on any other class is
// ReadWriteOnce, and a Job that mounts it and its own catalog claim on
// another node never starts. So with no per-node class the operator makes no
// claim, and each run reads the files from IMDb directly. The claim follows
// the create-once rule of every claim this operator writes.
func (o *operator) standDatasetsCache(ctx context.Context, provider *MetadataProvider) Condition {
	classes, err := ListStorageClasses(ctx, o.client)
	if err != nil {
		return Condition{Type: conditionCached, Status: ConditionFalse, Reason: reasonClaimFailed,
			Message: "could not list the StorageClasses: " + err.Error()}
	}
	names := perNodeClassNames(classes.Items)
	if len(names) == 0 {
		return Condition{Type: conditionCached, Status: ConditionFalse, Reason: reasonNoPerNodeClass,
			Message: "no StorageClass has the provisioner " + perNodeProvisioner +
				", so each run reads the files from IMDb"}
	}
	claim := buildDatasetsClaim(provider, names[0])
	if err := o.standClaim(ctx, claim); err != nil {
		return Condition{Type: conditionCached, Status: ConditionFalse, Reason: reasonClaimFailed,
			Message: fmt.Sprintf("could not create the claim %s: %v", claim.Metadata.Name, err)}
	}
	return Condition{Type: conditionCached, Status: ConditionTrue, Reason: reasonPerNodeClass,
		Message: fmt.Sprintf("the claim %s on the class %s holds the files", claim.Metadata.Name, names[0])}
}

// Where the nfo container mounts the cache, and the variable that names that
// path. A container that reads no variable reads the files from IMDb with no
// cache and logs nothing about it, because the cluster has no per-node class.
const (
	datasetsVolumeName    = "datasets"
	datasetsMountPath     = "/var/cache/liken/datasets"
	datasetsCacheVariable = "IMDB_CACHE"
)

// The mount of the nfo container. The container mounts the cache read-write
// only when the Library's sources name a Ready imdb provider that holds a
// claim. A container with no mount reads the files from IMDb directly.
//
// The Job builder holds no report, so the mount does not depend on whether the
// fact has gaps in this run. A run with no gaps sends no request and writes
// nothing to the claim.
func mountDatasetsCache(library *Library, providers providerSet, nfo *Container) {
	if datasetsCacheProvider(library, providers) == nil {
		return
	}
	nfo.VolumeMounts = append(nfo.VolumeMounts,
		VolumeMount{Name: datasetsVolumeName, MountPath: datasetsMountPath})
	nfo.Env = append(nfo.Env, EnvVar{Name: datasetsCacheVariable, Value: datasetsMountPath})
}

// The pod's volume for that mount, or none.
func datasetsCacheVolumes(library *Library, providers providerSet) []Volume {
	provider := datasetsCacheProvider(library, providers)
	if provider == nil {
		return nil
	}
	return []Volume{{Name: datasetsVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
		ClaimName: datasetsClaimName(provider.Metadata.Name),
	}}}
}

// The first Ready imdb source of the Library whose Cached condition is True.
func datasetsCacheProvider(library *Library, providers providerSet) *MetadataProvider {
	provider := imdbSource(library, providers)
	if provider == nil || !provider.cached() {
		return nil
	}
	return provider
}
