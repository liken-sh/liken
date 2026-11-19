package main

// The idle pod and a Remote's reader pod have restartPolicy Always, so
// their own programs never end them: the kubelet restarts a container
// that exits. A pod of either kind is Failed or Succeeded only when the
// kubelet ended it, and no restart policy starts it again, so the
// operator deletes it and creates it again.
//
// A device claim pins each of these pods to one node, so the scheduler
// puts the replacement on the node that ended the old pod. So the
// replacement waits, by the rule of kubernetes/endedpods. The ended pod
// stays during the wait, so a person can read its status with kubectl
// describe.

import (
	"errors"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/endedpods"
)

// triesAnnotation counts the pods in a row before this one that the
// kubelet ended. A pod created for any other reason has no count. The
// template hash covers the spec alone, so the count does not change it.
const triesAnnotation = "media.liken.sh/tries"

// podEnded reports whether the kubelet ended the pod.
func podEnded(pod *Pod) bool {
	return endedpods.Ended(pod.Status.Phase)
}

// endOf returns what the rule of kubernetes/endedpods reads of a pod.
// The lastState of a container counts, because a container that the
// kubelet restarted can hold its latest finishedAt there.
func endOf(pod *Pod) endedpods.Pod {
	end := endedpods.Pod{
		Phase:   pod.Status.Phase,
		Reason:  pod.Status.Reason,
		Created: parseTime(pod.Metadata.CreationTimestamp),
		Tries:   endedpods.Tries(pod.Metadata.Annotations, triesAnnotation),
	}
	for _, c := range pod.Status.Conditions {
		end.Conditions = append(end.Conditions, endedpods.Condition{Type: c.Type, Status: c.Status, LastTransitionTime: parseTime(c.LastTransitionTime)})
	}
	for _, status := range pod.Status.ContainerStatuses {
		for _, state := range []ContainerState{status.State, status.LastState} {
			if state.Terminated != nil {
				end.Finished = append(end.Finished, parseTime(state.Terminated.FinishedAt))
			}
		}
	}
	return end
}

// parseTime returns the time of an RFC 3339 timestamp, and the zero time
// for a timestamp that is empty or does not parse.
func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
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
// The Event goes out for the first ended pod of a row
// (endedpods.Pod.FirstOfLoop), and for a pod whose template changed,
// which starts a new row.
func (o *operator) replaceEnded(want persistentPair, ended *Pod, changed bool) error {
	now := time.Now()
	end := endOf(ended)
	if at := end.ReplaceAt(); !changed && now.Before(at) {
		o.requeueAfter(at.Sub(now))
		return nil
	}
	if err := DeletePod(o.client, want.namespace, want.podName); err != nil {
		return err
	}
	cause := endedPodCause(ended)
	if !changed {
		endedpods.SetTries(want.pod.Metadata.Annotations, triesAnnotation, end.Tries+1)
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
	if changed || end.FirstOfLoop() {
		o.recorder.Warning(want.owner, reasonPodRecreated,
			"Deleted pod "+want.podName+" and created it again, because "+cause)
	}
	return nil
}

// endedPodCause describes an ended pod with the phase, the reason, and
// the message that the kubelet wrote, such as Evicted and the resource
// the node lacked. The sentence names the phase and the reason itself,
// so it takes the message alone from endedpods.Words.
func endedPodCause(pod *Pod) string {
	cause := "the kubelet ended the pod with phase " + pod.Status.Phase
	if pod.Status.Reason != "" {
		cause += " and reason " + pod.Status.Reason
	}
	if message := endedpods.Words("", pod.Status.Message); message != "" {
		cause += ": " + message
	}
	return cause
}
