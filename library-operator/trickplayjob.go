package main

// The trickplay Job of one Library and the claim its catalog agent runs on. It
// is a Job of its own, beside the enricher, because one title's decode runs for
// minutes and the enricher must not wait behind it.

import (
	"context"
)

// The worker this Job runs, which is its label value and its stage in a
// webhook's chain.
const workerTrickplay = "trickplay"

// The fixed part of every trickplay Job's name, and the claim its catalog agent
// runs on. A standing Job and a chain Job each add a suffix, and the claim
// keeps the fixed name, so one Library has one claim.
func trickplayJobName(library string) string {
	return library + "-" + workerTrickplay
}

func trickplayCatalogClaimName(library string) string {
	return trickplayJobName(library) + "-catalog"
}

// The volume the trickplay agent runs on. It is separate from the enricher's
// claim, so a trickplay Job never waits on the claim an enricher holds and the
// two run at once.
func buildTrickplayClaim(library *Library, catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            trickplayCatalogClaimName(library.Metadata.Name),
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

// The claim is provisioned once and never rewritten, the rule standClaim holds,
// because a claim's spec is immutable once it binds.
func (o *operator) standTrickplayClaim(ctx context.Context, library *Library, catalog *NamespaceCatalog) error {
	return o.standClaim(ctx, buildTrickplayClaim(library, catalog))
}

// The trickplay Job. The name is the caller's, because a Library runs the
// standing Job under the walk's name and a webhook's folder runs under the
// chain's.
func buildTrickplayJob(library *Library, name, path string,
	ffmpegImage, corrosionImage, busAddress, topicBase string) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            name,
			Namespace:       library.Metadata.Namespace,
			Labels:          workerLabels(library.Metadata.Name, workerTrickplay),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: trickplayPodTemplate(library, path,
				ffmpegImage, corrosionImage, busAddress, topicBase),
		},
	}
}

// The pod the trickplay Job runs: the catalog agent as a sidecar, and the one
// container that runs the trickplay fact. It takes a memory line and a CPU
// request of its own, because it decodes a video where every other container
// reads rows, and it runs on the ffmpeg image, because a VA-API driver is a
// shared library the operator's own image carries none of.
func trickplayPodTemplate(library *Library, path,
	ffmpegImage, corrosionImage, busAddress, topicBase string) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	// A trickplay container holds no Kubernetes credential. It reads its work
	// through the agent beside it, so nothing in this pod reads the API server.
	noToken := false

	tiles := factsContainer(library, trickplayContainerName, []string{factTrickplay},
		path, ffmpegImage, busAddress, topicBase)
	tiles.Resources.Requests["cpu"] = trickplayCPURequest
	tiles.Resources.Limits = map[string]string{"memory": trickplayMemoryLimit}

	spec := PodSpec{
		RestartPolicy:                 "Never",
		TerminationGracePeriodSeconds: &grace,
		AutomountServiceAccountToken:  &noToken,
		InitContainers:                []Container{catalogSidecar(corrosionImage)},
		Containers:                    []Container{tiles},
		Volumes: []Volume{
			{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
				ClaimName: trickplayCatalogClaimName(library.Metadata.Name),
			}},
			// The tiles go beside the media, so this Job mounts the volume
			// read-write as the enricher does.
			{Name: libraryVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
				ClaimName: library.Spec.screenClaim(),
			}},
		},
	}
	// The pod holds the render claim only where the Library names a render
	// block. With none it decodes in software.
	if library.Spec.Trickplay.Render != nil {
		spec.ResourceClaims = []PodResourceClaim{{
			Name:                      renderRequestName,
			ResourceClaimTemplateName: trickplayTemplateName(library.Metadata.Name),
		}}
		spec.Containers[0].Resources.Claims = []ResourceClaim{{Name: renderRequestName}}
	}

	return PodTemplateSpec{
		Metadata: ObjectMeta{
			Labels: withMemberLabel(workerLabels(library.Metadata.Name, workerTrickplay)),
		},
		Spec: spec,
	}
}
