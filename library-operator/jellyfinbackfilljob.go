package main

// the Job half of plan 50. A Catalog that names a Jellyfin server runs the
// backfill once, as a Job of the jellyfin role's container with the backfill
// subcommand on it, and the Catalog's status says where that run stands. The
// Job listens on no port and holds no Kubernetes credential, like the role
// beside it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// the Job takes the Catalog's name with the suffix -jellyfin-backfill, so
// every pass names the same Job and the operator keeps no record of it.
func jellyfinBackfillJobName(catalog string) string {
	return catalog + "-jellyfin-backfill"
}

// the Job one Catalog's backfill is, built from the Catalog and the
// operator's own settings alone. It carries the worker labels every Job of
// this operator carries, with the Catalog's name in the library label, so the
// pass reads it out of the one Job list it already made. Its pod runs to
// completion and holds no Kubernetes credential: everything it publishes
// crosses the bus.
func buildJellyfinBackfillJob(catalog *NamespaceCatalog, operatorImage, busAddress, topicBase, mediaBase string) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	grace := int64(scannerGracePeriod)
	noToken := false
	labels := workerLabels(catalog.Metadata.Name, workerJellyfinBackfill)
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            jellyfinBackfillJobName(catalog.Metadata.Name),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          labels,
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: PodTemplateSpec{
				Metadata: ObjectMeta{Labels: labels},
				Spec: PodSpec{
					// Never, because a Job's pod runs to completion, and a
					// restart in place would hide the failure the Job reports.
					RestartPolicy:                 "Never",
					TerminationGracePeriodSeconds: &grace,
					AutomountServiceAccountToken:  &noToken,
					Containers: []Container{
						jellyfinBackfillRole(catalog, operatorImage, busAddress, topicBase, mediaBase),
					},
				},
			},
		},
	}
}

// the jellyfin role with the standing half taken off it. It runs the same
// image against the same server on the same environment, in the backfill
// subcommand, and it takes no port, no readiness probe, and no listen
// address, because nothing posts to it.
func jellyfinBackfillRole(catalog *NamespaceCatalog, image, busAddress, topicBase, mediaBase string) Container {
	role := jellyfinRole(catalog, image, busAddress, topicBase, mediaBase)
	role.Command = []string{"/library-operator", jellyfinBackfillMode}
	role.Env = jellyfinEnv(catalog, busAddress, topicBase, mediaBase)
	role.Ports = nil
	role.ReadinessProbe = nil
	return role
}

// what the Catalog's status says about its Jellyfin server after this pass,
// and the one Job that gets it there. A Catalog that names no server reports
// nothing and loses the Job it stood before. Otherwise the pass reads the Job
// it already listed: a Job that succeeded finishes the backfill against this
// server, a Job that gave up past its backoff is deleted so the next pass
// creates it again, and a Job that is neither is still running. With no Job
// standing and no finish against this server, the pass creates the Job once
// every durable copy of the progress store is up, because a message the store
// misses is one nothing publishes again.
func (o *operator) standJellyfinBackfill(ctx context.Context, catalog *NamespaceCatalog,
	jobs []Job, progressPods []*Pod, now time.Time) *CatalogJellyfinStatus {
	namespace, name := catalog.Metadata.Namespace, catalog.Metadata.Name
	key := libraryKey(namespace, name)
	if catalog.Spec.Jellyfin == nil {
		delete(o.backfillStands, key)
		if err := o.retireJellyfinBackfill(ctx, namespace, name); err != nil {
			fmt.Fprintf(os.Stderr, "retiring the jellyfin backfill in %s: %v\n", namespace, err)
		}
		return nil
	}
	server := catalog.Spec.Jellyfin.URL
	held := jellyfinBackfillJobOf(jobs, namespace, name)
	if held == nil && backfilledFrom(catalog.Status.Jellyfin, server) {
		delete(o.backfillStands, key)
		return catalog.Status.Jellyfin
	}
	if held != nil {
		return o.readJellyfinBackfillJob(ctx, catalog, held, server, now)
	}
	if !progressStoreListening(progressPods) || !o.mayStandBackfill(key, now) {
		return &CatalogJellyfinStatus{Server: server, Backfill: backfillPending}
	}
	created := buildJellyfinBackfillJob(catalog, o.scannerImage, o.busAddress, o.topicBase, o.mediaTopicBase)
	if _, err := CreateJob(ctx, o.client, created); err != nil && !errors.Is(err, ErrConflict) {
		fmt.Fprintf(os.Stderr, "creating the jellyfin backfill job in %s: %v\n", namespace, err)
		return &CatalogJellyfinStatus{Server: server, Backfill: backfillPending}
	}
	return &CatalogJellyfinStatus{Server: server, Backfill: backfillRunning}
}

// the verdict of the Job that stands. A Job that succeeded keeps the time it
// first finished against this server, so a status the TTL has not yet cleared
// the Job from does not move its own clock forward every pass.
func (o *operator) readJellyfinBackfillJob(ctx context.Context, catalog *NamespaceCatalog,
	job *Job, server string, now time.Time) *CatalogJellyfinStatus {
	namespace, name := catalog.Metadata.Namespace, catalog.Metadata.Name
	switch {
	case job.Status.Succeeded > 0:
		delete(o.backfillStands, libraryKey(namespace, name))
		return &CatalogJellyfinStatus{
			Server:     server,
			Backfill:   backfillFinished,
			Backfilled: backfilledAt(catalog.Status.Jellyfin, server, now),
		}
	case job.gaveUp():
		if err := o.retireJellyfinBackfill(ctx, namespace, name); err != nil {
			fmt.Fprintf(os.Stderr, "deleting the failed jellyfin backfill job in %s: %v\n", namespace, err)
		}
		return &CatalogJellyfinStatus{Server: server, Backfill: backfillFailed}
	}
	return &CatalogJellyfinStatus{Server: server, Backfill: backfillRunning}
}

// whether the status already reports a finished backfill against this same
// server. A Catalog whose spec.jellyfin.url changed is a different server,
// and its backfill runs again.
func backfilledFrom(status *CatalogJellyfinStatus, server string) bool {
	return status != nil && status.Server == server && status.Backfill == backfillFinished
}

// the time the backfill finished: the one the status already carries against
// this server, or this pass's own, in UTC.
func backfilledAt(status *CatalogJellyfinStatus, server string, now time.Time) string {
	if backfilledFrom(status, server) && status.Backfilled != "" {
		return status.Backfilled
	}
	return now.UTC().Format(time.RFC3339)
}

// the backfill Job of one Catalog out of the Jobs the pass listed, or nil
// when none stands.
func jellyfinBackfillJobOf(jobs []Job, namespace, catalog string) *Job {
	held := jobsOf(jobs, namespace, catalog, workerJellyfinBackfill)
	if len(held) == 0 {
		return nil
	}
	return &held[0]
}

// whether the progress store is up to record the backfill. Every durable copy
// counts, on the same terms storeReplicaCount counts one: the pod runs and
// the kubelet marks every container of it ready, its Corrosion agent
// included. A namespace that stands no copy is not listening.
func progressStoreListening(pods []*Pod) bool {
	if len(pods) == 0 {
		return false
	}
	for _, pod := range pods {
		if pod == nil || pod.Status.Phase != podRunning ||
			!everyContainerReadyBeside(pod, progressContainer) {
			return false
		}
	}
	return true
}

// the recreate backoff of the backfill Job, on the curve the cleanup Job's
// stand follows. A Job that keeps failing is created again on a wait that
// doubles to the cap, so a server the backfill cannot read does not cost a
// Job every pass.
func (o *operator) mayStandBackfill(key string, now time.Time) bool {
	state := o.backfillStands[key]
	if now.Before(state.next) {
		return false
	}
	state.count++
	state.next = now.Add(cleanupBackoffDelay(state.count))
	o.backfillStands[key] = state
	return true
}

// the backfill Job of a Catalog that no longer asks for one, and of one whose
// Job gave up. An already-absent Job is success, the rule DeleteJob follows.
func (o *operator) retireJellyfinBackfill(ctx context.Context, namespace, catalog string) error {
	return DeleteJob(ctx, o.client, namespace, jellyfinBackfillJobName(catalog))
}
