package main

// trailersjob.go is the trailers Job of one Library and the claim its catalog
// agent runs on. It is a Job of its own, beside the enricher, because one
// title's pull is bytes over the internet and the enricher must not wait
// behind it.

import (
	"context"
)

// The worker this Job runs as, which is its label value and the worker its
// tally rows are recorded under.
const workerTrailers = "trailers"

// The fixed part of every trailers Job's name, and the claim its catalog agent
// runs on, so one Library has one claim.
func trailersJobName(library string) string {
	return library + "-" + workerTrailers
}

func trailersCatalogClaimName(library string) string {
	return trailersJobName(library) + "-catalog"
}

// The volume the trailers agent runs on, separate from the enricher's, so
// neither Job waits on the claim the other holds.
func buildTrailersClaim(library *Library, catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            trailersCatalogClaimName(library.Metadata.Name),
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

// The claim is provisioned once and never rewritten, which is the rule
// standClaim holds, because a claim's spec is immutable once it binds.
func (o *operator) standTrailersClaim(ctx context.Context, library *Library, catalog *NamespaceCatalog) error {
	return o.standClaim(ctx, buildTrailersClaim(library, catalog))
}

// The trailers Job, named by the caller.
func buildTrailersJob(library *Library, providers providerSet, languages []string,
	name, path, ffmpegImage, corrosionImage string) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            name,
			Namespace:       library.Metadata.Namespace,
			Labels:          workerLabels(library.Metadata.Name, workerTrailers),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: trailersPodTemplate(library, providers, languages, path,
				ffmpegImage, corrosionImage),
		},
	}
}

// The pod the trailers Job runs: the catalog agent as a sidecar, and the one
// container that runs the trailerfile fact. It runs on the ffmpeg image,
// because the remux and the check are ffmpeg and ffprobe, and it mounts the
// volume read-write, because the file lands beside the title.
func trailersPodTemplate(library *Library, providers providerSet, languages []string,
	path, ffmpegImage, corrosionImage string) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	// This container holds no Kubernetes credential. It reads its work through
	// the agent beside it, so nothing in this pod reads the API server.
	noToken := false

	pulls := factsContainer(library, trailerFileContainerName, []string{factTrailerFile},
		path, ffmpegImage)
	pulls.Resources.Limits = map[string]string{"memory": trailersMemoryLimit}
	// The source order and the address of every site travel with the keys, so
	// the container asks the sites the Library names.
	pulls.Env = append(pulls.Env, providerEnv(library, providers, languages)...)
	// This Job is its own worker, so its tally rows are recorded beside the
	// enricher's and its own next run sweeps them.
	pulls.Env = append(pulls.Env, EnvVar{Name: libraryWorkerVariable, Value: workerTrailers})

	return PodTemplateSpec{
		Metadata: ObjectMeta{
			Labels: withMemberLabel(workerLabels(library.Metadata.Name, workerTrailers)),
		},
		Spec: PodSpec{
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			InitContainers:                []Container{catalogSidecar(corrosionImage)},
			Containers:                    []Container{pulls},
			Volumes: []Volume{
				{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: trailersCatalogClaimName(library.Metadata.Name),
				}},
				{Name: libraryVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: library.Spec.screenClaim(),
				}},
			},
		},
	}
}
