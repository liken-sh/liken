package main

// A scan is a Job. The full walk runs from a CronJob on the
// Library's schedule, and a folder scan runs from a Job the webhook
// creates for one path. Both run the same pod on the Library's own
// catalog claim, whose ReadWriteOnce admits one of them at a time.

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"
)

// How many pods Kubernetes replaces before the Job itself fails,
// and how long a finished Job stays for a person to read its logs.
const (
	scanBackoffLimit = 2
	scanJobTTL       = 3600
)

// A walk that runs past its next turn skips that turn, because
// the claim admits one writer and a second walk would only wait on it.
const forbidConcurrency = "Forbid"

// How many finished Jobs of each outcome the CronJob keeps. One
// success is the last good run, and three failures are enough to read a
// pattern out of.
const (
	successfulJobsKept = 1
	failedJobsKept     = 3
)

// The schedule one Library's full walk runs on, named from the
// Library, so every pass names the same CronJob.
func scanCronJobName(library string) string {
	return library + "-scan"
}

// The Job one webhook becomes. The hash is of the path and the
// time, so two webhooks for one folder are two Jobs and no create ever
// collides with a Job that is still running.
func folderScanJobName(library, path string, now time.Time) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(path))
	_, _ = sum.Write([]byte(strconv.FormatInt(now.UnixNano(), 10)))
	return scanCronJobName(library) + "-" + strconv.FormatUint(sum.Sum64(), 36)
}

// The schedule the Library's full walk runs on, built from the
// Library and the operator's own settings alone, so two passes over an
// unchanged Library build the same object.
func buildScanCronJob(library *Library, scannerImage, corrosionImage, busAddress, topicBase string) *CronJob {
	successes, failures := int32(successfulJobsKept), int32(failedJobsKept)
	return &CronJob{
		APIVersion: batchAPIVersion,
		Kind:       "CronJob",
		Metadata: ObjectMeta{
			Name:            scanCronJobName(library.Metadata.Name),
			Namespace:       library.Metadata.Namespace,
			Labels:          workerLabels(library.Metadata.Name, workerScan),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: CronJobSpec{
			Schedule:                   library.Spec.scanSchedule(),
			ConcurrencyPolicy:          forbidConcurrency,
			SuccessfulJobsHistoryLimit: &successes,
			FailedJobsHistoryLimit:     &failures,
			JobTemplate: JobTemplateSpec{
				Metadata: ObjectMeta{Labels: workerLabels(library.Metadata.Name, workerScan)},
				Spec:     scanJobSpec(library, "", scannerImage, corrosionImage, busAddress, topicBase),
			},
		},
	}
}

// The Job one held webhook path becomes, owned by the Library so
// the garbage collector takes it with the Library.
func buildFolderScanJob(library *Library, path string, now time.Time, scannerImage, corrosionImage, busAddress, topicBase string) *Job {
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            folderScanJobName(library.Metadata.Name, path, now),
			Namespace:       library.Metadata.Namespace,
			Labels:          workerLabels(library.Metadata.Name, workerScan),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: scanJobSpec(library, path, scannerImage, corrosionImage, busAddress, topicBase),
	}
}

// The spec both scan Jobs share, which differ in the scan path
// alone: empty is the full walk, and a path is the one folder to
// rescan.
func scanJobSpec(library *Library, path, scannerImage, corrosionImage, busAddress, topicBase string) JobSpec {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	return JobSpec{
		BackoffLimit:            &backoff,
		TTLSecondsAfterFinished: &ttl,
		Template:                scanPodTemplate(library, path, scannerImage, corrosionImage, busAddress, topicBase),
	}
}

// The CronJob is created when there is none and rewritten when
// the pass builds a different one, which is how a changed schedule or a
// changed image reaches the cluster. The stamped hash is what tells the
// two apart, the rule standPod follows for a pod.
func (o *operator) standScanCronJob(ctx context.Context, library *Library) (*CronJob, error) {
	desired := buildScanCronJob(library, o.scannerImage, o.corrosionImage, o.busAddress, o.topicBase)
	if err := stampTemplateHash(&desired.Metadata, desired.Spec); err != nil {
		return nil, err
	}
	namespace, name := desired.Metadata.Namespace, desired.Metadata.Name

	live, err := GetCronJob(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		created, err := CreateCronJob(ctx, o.client, desired)
		if errors.Is(err, ErrConflict) {
			// Another pass, or another copy of this operator, created
			// it first, which is success. The next pass reads it.
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
	if sameTemplate(&live.Metadata, &desired.Metadata) {
		return live, nil
	}
	desired.Metadata.ResourceVersion = live.Metadata.ResourceVersion
	written, err := UpdateCronJob(ctx, o.client, desired)
	if errors.Is(err, ErrConflict) {
		return live, nil
	}
	return written, err
}

// The schedule of a Library that no longer stands one goes, so a
// Library whose claim or Catalog went away stops walking a volume it no
// longer reports. An absent CronJob is success.
func (o *operator) stopScanCronJob(ctx context.Context, library *Library) error {
	return DeleteCronJob(ctx, o.client, library.Metadata.Namespace,
		scanCronJobName(library.Metadata.Name))
}

// The Jobs of one Library and one worker, out of the whole
// cluster's Jobs the pass listed.
func jobsOf(jobs []Job, namespace, library, worker string) []Job {
	held := []Job{}
	for index := range jobs {
		job := &jobs[index]
		if job.Metadata.Namespace != namespace {
			continue
		}
		if job.Metadata.Labels[libraryLabelKey] != library {
			continue
		}
		if job.Metadata.Labels[workerLabelKey] != worker {
			continue
		}
		held = append(held, *job)
	}
	return held
}

// Whether a full walk of this Library has a pod running. A full
// walk is a Job the CronJob created, which is the ownerReference the
// CronJob controller writes, so a folder scan is never mistaken for
// one.
func fullWalkRunning(jobs []Job, namespace, library string) bool {
	for _, job := range jobsOf(jobs, namespace, library, workerScan) {
		if job.active() && ownedByCronJob(&job) {
			return true
		}
	}
	return false
}

// Whether any scan of this Library has a pod running, which is
// what a departure waits out before it sweeps the rows a scan would
// write again.
func scanRunning(jobs []Job, namespace, library string) bool {
	for _, job := range jobsOf(jobs, namespace, library, workerScan) {
		if job.active() {
			return true
		}
	}
	return false
}

func ownedByCronJob(job *Job) bool {
	for _, owner := range job.Metadata.OwnerReferences {
		if owner.Kind == "CronJob" {
			return true
		}
	}
	return false
}

// The held webhook paths of one Library become Jobs here, one
// Job per path, and each path is dropped once its Job exists. A path
// held while a full walk runs stays held: the walk covers it, and the
// claim would admit no second writer anyway.
func (o *operator) serveHeldPaths(ctx context.Context, library *Library, jobs []Job, now time.Time) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if fullWalkRunning(jobs, namespace, name) {
		return nil
	}
	for _, path := range o.paths.held(namespace, name) {
		job := buildFolderScanJob(library, path, now,
			o.scannerImage, o.corrosionImage, o.busAddress, o.topicBase)
		if _, err := CreateJob(ctx, o.client, job); err != nil && !errors.Is(err, ErrConflict) {
			return fmt.Errorf("creating the scan job for %s: %w", path, err)
		}
		o.paths.release(namespace, name, path)
	}
	return nil
}
