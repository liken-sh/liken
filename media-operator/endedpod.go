package main

// The idle pod and a Remote's reader pod have restartPolicy Always, so
// their own programs never end them: the kubelet restarts a container
// that exits. A pod of either kind is Failed or Succeeded only when the
// kubelet ended it, for an eviction, a rejection at admission, a
// graceful node shutdown, or a preemption. No restart policy starts
// such a pod again, so the operator deletes it and creates it again.
//
// A device claim pins each of these pods to one node, so the scheduler
// puts the replacement on the node that ended the old pod. When the
// kubelet rejected the pod at admission, for OutOfcpu or NodeAffinity,
// no taint keeps the replacement away, and the kubelet can reject it at
// once. So the replacement waits, and the wait doubles for each pod in a
// row that the kubelet ended, to a limit of 5 minutes. The wait counts
// from the ended pod's creation, so a pod that ran for longer than the
// wait is replaced at once. The ended pod stays during the wait, so a
// person can read its status with kubectl describe.

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// triesAnnotation counts the pods in a row before this one that the
// kubelet ended. The count is in the pod and not in memory, so a
// restarted operator waits as long as the one before it. A pod created
// for any other reason has no count, which reads as zero. The template
// hash covers the spec alone, so the count does not change it.
const triesAnnotation = "media.liken.sh/tries"

// The wait before an ended pod is replaced: firstEndedWait for a pod
// with no count, doubled for each count, to maxEndedWait.
const (
	firstEndedWait = 10 * time.Second
	maxEndedWait   = 5 * time.Minute
)

// podEnded reports whether the kubelet ended the pod.
func podEnded(pod *Pod) bool {
	return pod.Status.Phase == podFailed || pod.Status.Phase == podSucceeded
}

// podTries returns the pod's count of earlier pods that the kubelet
// ended.
func podTries(pod *Pod) int {
	count, _ := strconv.Atoi(pod.Metadata.Annotations[triesAnnotation])
	return max(count, 0)
}

// endedWait returns the wait after a pod with tries earlier ended pods.
// The loop stops at the limit, so a large count cannot overflow the
// duration.
func endedWait(tries int) time.Duration {
	wait := firstEndedWait
	for range tries {
		if wait >= maxEndedWait {
			break
		}
		wait *= 2
	}
	return min(wait, maxEndedWait)
}

// replaceEnded deletes the ended pod and creates want.pod in its place,
// with the count of tries raised by one. Before the wait is over, it
// changes nothing and schedules a pass at the deadline. When changed is
// true, the template differs from the ended pod's, so the replacement
// does not wait and has no count.
//
// The API server removes a deleted pod in a terminal phase at once, so
// the create in the same pass succeeds. When the old pod still exists,
// because a finalizer keeps it, the create answers 409, and the pass
// that runs after the pod is gone creates it with no count. An operator
// restart between the delete and the create also loses the count. Each
// case starts the wait again at 10 seconds.
//
// A retry loop posts its first failure and not each retry. So the Event
// goes out for the first ended pod of a row, and for a pod that ran for
// longer than maxEndedWait before the kubelet ended it, whose end is a
// new failure.
func (o *operator) replaceEnded(want persistentPair, ended *Pod, changed bool) error {
	now := time.Now()
	created, _ := time.Parse(time.RFC3339, ended.Metadata.CreationTimestamp)
	tries := podTries(ended)
	if changed {
		tries = 0
	}
	at := created.Add(endedWait(tries))
	if !changed && now.Before(at) {
		o.requeueAfter(at.Sub(now))
		return nil
	}
	if err := DeletePod(o.client, want.namespace, want.podName); err != nil {
		return err
	}
	cause := endedPodCause(ended)
	if !changed {
		want.pod.Metadata.Annotations[triesAnnotation] = strconv.Itoa(tries + 1)
	}
	if _, err := CreatePod(o.client, want.pod); err != nil {
		if !errors.Is(err, apiclient.ErrConflict) {
			return err
		}
		logLine(o.log, "%s: deleted pod %s, because %s, and the API server still has it, so a later pass creates it",
			want.subject, want.podName, cause)
		return nil
	}
	logLine(o.log, "%s: deleted pod %s and created it again, because %s", want.subject, want.podName, cause)
	if tries == 0 || ranFor(ended, created) > maxEndedWait {
		o.recorder.Warning(want.owner, reasonPodRecreated,
			"Deleted pod "+want.podName+" and created it again, because "+cause)
	}
	return nil
}

// ranFor returns how long the pod ran before the kubelet ended it: from
// its creation to the lastTransitionTime of its DisruptionTarget
// condition, or else to the latest finishedAt of a terminated container.
// A pod with neither never ran, because the kubelet rejected it at
// admission, so it ran for zero time. The pod's age at the replace is not
// a measure, because a pass runs after the deadline and the API server
// cuts creationTimestamp to whole seconds, so a rejected pod would always
// read as older than the limit.
func ranFor(pod *Pod, created time.Time) time.Duration {
	var stopped time.Time
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "DisruptionTarget" && condition.Status == "True" {
			stopped, _ = time.Parse(time.RFC3339, condition.LastTransitionTime)
		}
	}
	if stopped.IsZero() {
		for _, status := range pod.Status.ContainerStatuses {
			for _, state := range []ContainerState{status.State, status.LastState} {
				if state.Terminated == nil {
					continue
				}
				finished, err := time.Parse(time.RFC3339, state.Terminated.FinishedAt)
				if err == nil && finished.After(stopped) {
					stopped = finished
				}
			}
		}
	}
	if stopped.IsZero() || stopped.Before(created) {
		return 0
	}
	return stopped.Sub(created)
}

// endedPodCause describes an ended pod with the phase, the reason, and
// the message that the kubelet wrote, such as Evicted and the resource
// the node lacked. The kubelet's message ends in a period, which the
// sentence around it does not take.
func endedPodCause(pod *Pod) string {
	cause := "the kubelet ended the pod with phase " + pod.Status.Phase
	if pod.Status.Reason != "" {
		cause += " and reason " + pod.Status.Reason
	}
	if message := strings.TrimSuffix(strings.TrimSpace(pod.Status.Message), "."); message != "" {
		cause += ": " + message
	}
	return cause
}
