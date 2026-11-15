package main

// workerpods.go deletes the pods of a heavy-fact worker that have finished
// their video. A worker is an Indexed Job with one completion per video
// (factworkerjob.go), and Kubernetes keeps every pod of a Job until the Job
// itself is deleted. Each worker pod also holds a ResourceClaim for its GPU,
// which Kubernetes makes from the pod's claim template. So the operator
// deletes each finished pod as soon as its Job has counted it, and the
// garbage collector deletes the pod's claim with it, through the claim's
// owner reference.
//
// This delete is a workaround for Kubernetes behavior. Kubernetes' DRA
// documentation says that the ResourceClaim controller in
// kube-controller-manager deletes a pod's generated ResourceClaim when the
// pod finishes. On a cluster that ran k3s v1.37.2, the controller instead
// deleted and created the claims of finished worker pods again in a loop,
// and new worker pods waited minutes for their own claims. No upstream
// report of that exact loop was found. Two open reports are about the same
// code:
//
//   - https://github.com/kubernetes/kubernetes/issues/142870: the
//     controller's cache keeps generated claims that were deleted. The fix
//     is https://github.com/kubernetes/kubernetes/pull/142871.
//   - https://github.com/kubernetes/kubernetes/issues/141274: the
//     controller adds finished pods back to a claim's reservedFor.
//
// Kubernetes has no setting that removes a Job's pods one at a time as they
// finish. ttlSecondsAfterFinished deletes the whole Job only after the whole
// Job ends, and a worker Job can run for days. The kube-controller-manager
// flag --terminated-pod-gc-threshold counts the finished pods of every
// workload in the cluster, so a lower threshold would also delete the pods
// of other people's workloads. When the cluster's Kubernetes no longer
// loops on the claims of finished pods, this delete can be removed.
//
// The Job controller adds the finalizer batch.kubernetes.io/job-tracking to
// every pod it creates. It removes the finalizer only after it has recorded
// the pod in the Job's status. So a pod without the finalizer is safe to
// delete: the Job keeps the pod's completion, and the controller does not
// start that index again.
//
// The operator keeps a pod that failed, because a person reads its logs to
// learn why the worker failed. It touches only the pods of Indexed worker
// Jobs. The fit of the titles model runs in a worker Job that is not
// Indexed, and its one pod stays until the Job is deleted.

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
)

// The label Kubernetes puts on each pod of an Indexed Job, whose value is
// the pod's completion index. Only the pods of an Indexed Job carry it.
const completionIndexLabel = "batch.kubernetes.io/job-completion-index"

// The finalizer the Job controller holds on each pod until it has counted
// the pod in the Job's status.
const jobTrackingFinalizer = "batch.kubernetes.io/job-tracking"

// The selector of the pods of this operator's Indexed worker Jobs: the
// operator's worker label, and the completion index label with any value.
const indexedWorkerPodsSelector = scannerLabelKey + "=" + workerLabelValue + "," + completionIndexLabel

// countedWorkerPod is true for a pod that exited zero and that its Job has
// counted. A pod that is already being deleted is left alone, so the pass
// does not send a second delete for it.
func countedWorkerPod(pod *Pod) bool {
	return pod.Status.Phase == "Succeeded" && !pod.Metadata.holds(jobTrackingFinalizer) &&
		!pod.Metadata.deleting()
}

// deleteCountedWorkerPods deletes every counted pod of the worker Jobs, and
// writes one log line for each Job whose pods it deleted. A delete that fails
// is reported and the pass carries on, because the next pass reads the pod
// again. The first pass on a cluster with a backlog of finished pods deletes
// the whole backlog, one request after another.
func (o *operator) deleteCountedWorkerPods(ctx context.Context, pods []Pod) {
	deleted := map[workerJob]int{}
	for index := range pods {
		pod := &pods[index]
		if !countedWorkerPod(pod) {
			continue
		}
		if err := DeletePod(ctx, o.client, pod.Metadata.Namespace, pod.Metadata.Name); err != nil {
			fmt.Fprintf(os.Stderr, "deleting the finished worker pod %s/%s: %v\n",
				pod.Metadata.Namespace, pod.Metadata.Name, err)
			continue
		}
		deleted[workerPodJob(pod)]++
	}
	for _, job := range slices.SortedFunc(maps.Keys(deleted), workerJob.compare) {
		o.logf("library %s/%s: deleted %s of the %s worker %s, because the Job has counted them",
			job.namespace, job.library, counted(deleted[job], "finished pod"), job.fact, job.name)
	}
}

// The worker Job a pod belongs to, read from the labels the Job's pod
// template and the Job controller put on the pod.
type workerJob struct {
	namespace, library, fact, name string
}

func workerPodJob(pod *Pod) workerJob {
	labels := pod.Metadata.Labels
	return workerJob{namespace: pod.Metadata.Namespace, library: labels[libraryLabelKey],
		fact: labels[workerLabelKey], name: labels[jobNameLabel]}
}

// compare orders the log lines by namespace and Job name.
func (j workerJob) compare(other workerJob) int {
	return cmp.Or(cmp.Compare(j.namespace, other.namespace), cmp.Compare(j.name, other.name))
}
