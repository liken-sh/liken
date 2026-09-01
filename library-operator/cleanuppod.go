package main

// The cleanup pod is what a deleting Library becomes on its way out:
// the scanner pod with the walk taken off it. It runs the same image
// in its cleanup role, beside the same Corrosion sidecar with the
// same probes, and it mounts no media volume, because it reads no
// media.
//
// It mounts the departing library's own catalog claim, not a fresh
// one. That claim already holds every row of the namespace's
// catalog, so the agent starts with nothing to sync and the sweep
// acts on local rows at once. The claim is ReadWriteOnce, which is
// also why the scanner pod must be gone before this pod can start.

import (
	"context"
	"errors"
	"time"
)

// The container's name reaches a person through kubectl logs, so it
// says what the container does.
const cleanupContainer = "cleanup"

// The name label a cleanup pod carries. It is not the scanner's
// value, because the webhook Service selects a Library's scanner pod
// on that pair and must never route to the cleanup pod in its place.
const cleanupLabelValue = "library-cleanup"

// cleanupPodName is derived from the Library, so every pass names
// the same pod and the operator keeps no record of what it created.
func cleanupPodName(library string) string {
	return library + "-cleanup"
}

// cleanupLabels pairs a name of the pod's own with the same library
// label the scanner carries, so one selector lists both of a
// Library's pods.
func cleanupLabels(library string) map[string]string {
	return map[string]string{
		scannerLabelKey: cleanupLabelValue,
		libraryLabelKey: library,
	}
}

// buildCleanupPod is a function of the Library and the operator's
// settings alone, so the same inputs always name the same pod and
// the operator never runs an image the settings did not name.
func buildCleanupPod(library *Library, scannerImage, corrosionImage string) *Pod {
	grace := int64(scannerGracePeriod)
	// The sweeper holds no Kubernetes credential, the way the scanner
	// holds none: everything it needs arrives in its environment.
	noToken := false
	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            cleanupPodName(library.Metadata.Name),
			Namespace:       library.Metadata.Namespace,
			Labels:          cleanupLabels(library.Metadata.Name),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: PodSpec{
			// Never rather than Always, because a restart would hide
			// the failure the operator reports as Blocked. The
			// operator recreates a failed pod itself, on a backoff.
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			InitContainers: []Container{
				catalogSidecar(corrosionImage),
			},
			Containers: []Container{
				cleanupSidecar(library, scannerImage),
			},
			Volumes: []Volume{
				{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: scannerCatalogClaimName(library.Metadata.Name),
				}},
			},
		},
	}
}

// cleanupSidecar is the container that runs the sweep. It learns its
// library from its environment alone, and it reads the catalog API
// at the same loopback address the scanner reads.
func cleanupSidecar(library *Library, image string) Container {
	return Container{
		Name:    cleanupContainer,
		Image:   image,
		Command: []string{"/library-operator", cleanupMode},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
			{Name: libraryNameVariable, Value: library.Metadata.Name},
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// standCleanupPod answers with the pod that stands after the pass.
// There is no template hash here, the one departure from the scanner
// pod's shape: this pod exists for one teardown, and a rebuild would
// restart the sweep it is running.
func (o *operator) standCleanupPod(ctx context.Context, library *Library) (*Pod, error) {
	desired := buildCleanupPod(library, o.scannerImage, o.corrosionImage)
	namespace, name := desired.Metadata.Namespace, desired.Metadata.Name

	live, err := GetPod(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		if !o.mayStandCleanup(libraryKey(namespace, library.Metadata.Name)) {
			return nil, nil
		}
		created, err := CreatePod(ctx, o.client, desired)
		if errors.Is(err, ErrConflict) {
			// Another writer created the pod first, which is the
			// state this create was for; the next pass reads it.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return created, nil
	}
	if err != nil {
		return nil, err
	}

	// A pod on its way out counts as still present, the rule the
	// scanner pod follows, so one divergence causes one delete.
	if live.Metadata.DeletionTimestamp != "" {
		return live, nil
	}

	// A failed pod is deleted here and created again on a later
	// pass, because the ReadWriteOnce claim admits one holder at a
	// time and the failed pod is that holder until it goes.
	if live.Status.Phase == podFailed {
		if err := DeletePod(ctx, o.client, namespace, name); err != nil {
			return nil, err
		}
	}
	return live, nil
}

// The recreate backoff for a cleanup pod that keeps failing. The
// first stand is immediate, and each later one waits double the last,
// up to the cap, the same shape the kubelet's own crash backoff has.
var (
	cleanupBackoffBase = 10 * time.Second
	cleanupBackoffCap  = 5 * time.Minute
)

// cleanupStand is one departing library's stand count and the
// earliest time it may stand its cleanup pod again.
type cleanupStand struct {
	count int
	next  time.Time
}

// mayStandCleanup never answers no forever: the wait grows to the
// cap and stops there, so the pass keeps trying for as long as the
// Library is deleting.
func (o *operator) mayStandCleanup(key string) bool {
	now := time.Now()
	state := o.cleanupStands[key]
	if now.Before(state.next) {
		return false
	}
	state.count++
	state.next = now.Add(cleanupBackoffDelay(state.count))
	o.cleanupStands[key] = state
	return true
}

// cleanupBackoffDelay is the base doubled once per stand, capped, so
// a long run of failures never overflows the duration.
func cleanupBackoffDelay(count int) time.Duration {
	delay := cleanupBackoffBase
	for range count - 1 {
		delay *= 2
		if delay >= cleanupBackoffCap {
			return cleanupBackoffCap
		}
	}
	return delay
}
