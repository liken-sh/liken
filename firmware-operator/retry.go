package main

// A scan pod or a rehearsal pod writes nothing to a device, so when the
// kubelet ends one (endedByKubelet in pod.go), the operator records no
// failure and runs the same attempt again in a next pod. An apply pod
// and an upload pod write to a device, and the operator never runs
// either again on its own, because the kubelet can end such a pod at a
// shutdown before the Machine reports the new boot, and a next pod
// would then write on a boot that its attempt was not made for. For the
// same reason, a scan or a rehearsal whose pod ended at a node shutdown
// is not run again either: the machine's next boot is a new attempt.
//
// A scan pod and a rehearsal pod name their node in spec.nodeName, so
// the scheduler does not check them, and the kubelet can reject the
// next pod again at once. So the next pod waits, by the rule of
// kubernetes/endedpods.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/endedpods"
)

// retry is a pod that the kubelet ended, and the time at which the
// operator creates the attempt's next pod and deletes this one.
type retry struct {
	ended pod
	at    time.Time
}

// retryOf returns the retry of a pod that the kubelet ended. When the
// wait is already over, the next pod starts now.
func retryOf(p pod, now time.Time) retry {
	return retry{ended: p, at: later(p.end().ReplaceAt(), now)}
}

func (r retry) due(now time.Time) bool { return !now.Before(r.at) }

// first reports whether the ended pod is its attempt's first. Only the
// first next pod of an attempt posts an Event, because the record's
// message says that the attempt runs again, and a retry loop posts its
// first failure and its recovery.
func (r retry) first() bool { return r.ended.tries() == 0 }

// maxTaskPods is the most pods that one attempt runs when the kubelet
// ends each of them. An eviction can be the pod's own fault, such as a
// pod that writes more than the sizeLimit of its emptyDir, and without
// a limit that attempt would run again for ever with no warning.
const maxTaskPods = 5

// retryable reports whether the operator runs the attempt of a pod that
// the kubelet ended again in a next pod. A pod that ended at a node
// shutdown, or the last pod that maxTaskPods allows, takes the
// judgement of any other end.
func (p pod) retryable() bool {
	return p.endedByKubelet() && !p.endedAtShutdown() && p.tries()+1 < maxTaskPods
}

// stoppedAfter adds to the message of a judged end that the operator
// stopped running the attempt again, when the kubelet ended its last
// pod.
func stoppedAfter(message string, p pod, kind string) string {
	if !p.endedByKubelet() || p.endedAtShutdown() || p.tries()+1 < maxTaskPods {
		return message
	}
	return fmt.Sprintf("%s. The kubelet ended %d %s pods of this attempt, and the operator stops after %d",
		strings.TrimSuffix(message, "."), p.tries()+1, kind, maxTaskPods)
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// triesKey records how many earlier pods of the same attempt the
// kubelet ended. Each next pod copies the count and raises it by one.
const triesKey = "firmware.liken.sh/tries"

// tries returns the pod's count of earlier pods that the kubelet ended.
// A pod with no count is the attempt's first.
func (p pod) tries() int {
	return endedpods.Tries(p.Metadata.Annotations, triesKey)
}

// end returns what the rule of kubernetes/endedpods reads of a pod. The
// operator reads no time of a condition or a container, because it
// posts the Event of the first next pod alone (retry.first) and needs
// no measure of how long a pod ran.
func (p pod) end() endedpods.Pod {
	end := endedpods.Pod{
		Phase:   p.Status.Phase,
		Reason:  p.Status.Reason,
		Created: parseTime(p.Metadata.CreationTimestamp),
		Tries:   p.tries(),
	}
	for _, c := range p.Status.Conditions {
		end.Conditions = append(end.Conditions, endedpods.Condition{Type: c.Type, Status: c.Status})
	}
	return end
}

// nextTry returns the pod that runs the same attempt after the kubelet
// ended p. It has the same labels, owners, annotations, and spec, the
// count of tries raised by one, and a name of its own, so the operator
// creates it before it deletes p and a restarted operator finds one of
// the two. The operator writes every field of the spec of a scan pod
// and a rehearsal pod, including spec.nodeName, so the copy is the pod
// that the operator built.
func nextTry(p pod) pod {
	tries := p.tries() + 1
	notes := maps.Clone(p.Metadata.Annotations)
	endedpods.SetTries(notes, triesKey, tries)
	return pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata: objectMeta{
			Name:            tryName(p.Metadata.Name, tries),
			Namespace:       p.Metadata.Namespace,
			Labels:          maps.Clone(p.Metadata.Labels),
			Annotations:     notes,
			OwnerReferences: slices.Clone(p.Metadata.OwnerReferences),
		},
		Spec: p.Spec,
	}
}

// tryName returns the name of an attempt's pod for a count of tries.
// The first pod keeps the attempt's name. Each later pod replaces the
// hash that ends the name with a hash of the name and the count, so the
// name keeps its length and its readable start, and a second create of
// the same try fails with 409 Conflict.
func tryName(name string, tries int) string {
	if tries == 0 {
		return name
	}
	base := name
	if cut := strings.LastIndex(name, "-"); cut >= 0 {
		base = name[:cut]
	}
	sum := sha256.Sum256([]byte(name + "\x00" + strconv.Itoa(tries)))
	return base + "-" + hex.EncodeToString(sum[:6])
}

// replaceEnded creates next and then deletes the pod that the kubelet
// ended. It reports whether its create made the pod. A 409 on the
// create means that an earlier pass created next and stopped before
// the delete, and that pass posted the Event.
func (o *operator) replaceEnded(ended, next pod) (bool, error) {
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(next.Metadata.Namespace), mustJSON(next), nil)
	created := err == nil
	if err != nil && !errors.Is(err, apiclient.ErrConflict) {
		return false, fmt.Errorf("creating the pod %s in place of %s: %w", next.Metadata.Name, ended.Metadata.Name, err)
	}
	return created, o.remove(ended)
}
