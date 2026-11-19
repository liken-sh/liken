package main

// An update to a watched object wakes the pass only when a field that the
// pass reads has changed. Each kind has a changeRule whose mark function
// encodes those fields as a string, and the update handler compares the
// mark of the old copy with the mark of the new one.
//
// A mark leaves out the fields that this operator writes, so that its own
// write does not wake the pass again: a Display's spec.override, and the
// session of a Receiver or a Sink.
//
// A mark that is not empty includes the UID and the deletion timestamp,
// because the start of a delete changes what the pass does, and because
// an object that was deleted and created again under the same name during
// a gap in the watch reaches the handler as an update. After such a gap, the informer reports
// each difference from its cache as an add, update, or delete event, so
// the same rules cover the changes made while the watch was down.

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/tools/cache"
)

// changeRule decides which events of one kind wake the pass.
type changeRule[T any] struct {
	// what names the kind in the error report of an object that fails
	// to convert.
	what string
	// ours reports whether the pass reads the object at all. A nil ours
	// accepts every object.
	ours func(T) bool
	// mark encodes the fields that the pass reads and that another
	// writer can change.
	mark func(T) string
	// added reports whether a new object wakes the pass. A nil added
	// wakes the pass for every new object.
	added func(T) bool
}

func (r changeRule[T]) handler(wake chan<- struct{}) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(object any) {
			item, err := informer.Convert[T](object)
			if err != nil {
				informer.Report(r.what, err)
				return
			}
			if r.matters(item) && (r.added == nil || r.added(item)) {
				signalWake(wake)
			}
		},
		UpdateFunc: func(before, after any) {
			item, err := informer.Convert[T](after)
			if err != nil {
				informer.Report(r.what, err)
				return
			}
			// The old copy failed to convert, and that failure was
			// already reported. Its fields are unknown, so treat the
			// update as a change.
			old, err := informer.Convert[T](before)
			if err != nil {
				if r.matters(item) {
					signalWake(wake)
				}
				return
			}
			if (r.matters(item) || r.matters(old)) && r.mark(item) != r.mark(old) {
				signalWake(wake)
			}
		},
		// A tombstone with no copy of the object does not show whether
		// the pass reads it. An extra pass costs less than a missed
		// deletion, so it wakes the pass.
		DeleteFunc: func(object any) {
			item, err := informer.Convert[T](object)
			if err != nil {
				informer.Report(r.what, err)
				signalWake(wake)
				return
			}
			if r.matters(item) {
				signalWake(wake)
			}
		},
	}
}

func (r changeRule[T]) matters(item T) bool {
	return r.ours == nil || r.ours(item)
}

func identity(meta ObjectMeta) string {
	return meta.UID + "|" + meta.DeletionTimestamp
}

// markOf returns the JSON encoding of a field, for comparing two copies.
func markOf(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "unmarshalable: " + err.Error()
	}
	return string(encoded)
}

// claimRule wakes the pass when the scheduler allocates or deallocates one
// of this operator's claims, and when one is deleted. A claim's spec is
// immutable, and this operator writes nothing else on a claim, so the mark
// covers only status.allocation. A claim that this operator has just
// created has no allocation, so its add event wakes nothing.
var claimRule = changeRule[ResourceClaim]{
	what: "the claims",
	ours: func(claim ResourceClaim) bool { return ownedByThisOperator(claim.Metadata) },
	mark: func(claim ResourceClaim) string {
		var allocation *DeviceAllocationResult
		if claim.Status != nil {
			allocation = claim.Status.Allocation
		}
		return identity(claim.Metadata) + "|" + markOf(allocation)
	},
	added: func(claim ResourceClaim) bool {
		return claim.Status != nil && claim.Status.Allocation != nil
	},
}

// ownedByThisOperator reports whether the object has an owner in this
// operator's API group. Every claim that this operator creates has a Play,
// a Player, or a Remote as its owner. The claims watch covers the whole
// cluster, and the claims of other programs must not wake the pass.
func ownedByThisOperator(meta ObjectMeta) bool {
	for _, owner := range meta.OwnerReferences {
		if strings.HasPrefix(owner.APIVersion, playResource.Group+"/") {
			return true
		}
	}
	return false
}

// sliceRule wakes the pass when a driver publishes, changes, or deletes a
// ResourceSlice. A slice has a spec and no status, so its generation
// changes when its devices change, and not when only its labels change.
var sliceRule = changeRule[ResourceSlice]{
	what: "the ResourceSlices",
	mark: func(slice ResourceSlice) string {
		return identity(slice.Metadata) + "|" + strconv.FormatInt(slice.Metadata.Generation, 10)
	},
}

// displayRule wakes the pass when display-operator writes a Display's
// status. The only field of the spec that the pass reads is the override
// that this operator writes, so the mark leaves the spec out.
var displayRule = changeRule[Display]{
	what: "the Displays",
	mark: func(display Display) string {
		return identity(display.Metadata) + "|" + markOf(display.Status)
	},
}

// receiverRule wakes the pass when a person changes a Receiver's inputs or
// volume scale, or when equipment-operator changes any field of its status
// other than the session. This operator writes the session in both the
// spec and the status, so the mark leaves it out. The mark includes each
// zone's volume, because the pass gives each level that the receiver
// reports to the volume engine.
var receiverRule = changeRule[Receiver]{
	what: "the Receivers",
	mark: func(receiver Receiver) string {
		status := receiver.Status
		status.Session = nil
		return identity(receiver.Metadata) + "|" + markOf(receiver.Spec.Inputs) + "|" +
			markOf(receiver.Spec.Volume) + "|" + markOf(status)
	},
}

// sinkRule wakes the pass when a person changes a Sink's volume scale, or
// when audio-operator reports a new level or mute, which the pass gives to
// the volume engine. The mark leaves out the session, which this operator
// writes, and the rest of the status, which the pass does not read.
var sinkRule = changeRule[Sink]{
	what: "the Sinks",
	mark: func(sink Sink) string {
		return identity(sink.Metadata) + "|" + markOf(sink.Spec.Volume) + "|" + markOf(sink.Status.Observed)
	},
}

// televisionRule wakes the pass when equipment-operator's CEC node pod
// writes a new status.screenAsk, which the pass publishes to the Player
// (screenask.go).
var televisionRule = changeRule[Television]{
	what: "the Televisions",
	mark: func(television Television) string {
		return identity(television.Metadata) + "|" + markOf(television.Status.ScreenAsk)
	},
}

// podRule wakes the pass when one of this operator's pods is deleted, and
// when a playback pod's status changes, because the pass sets a Play's
// phase from its pod's status. PodStatus decodes only the fields that the
// pass reads, so a change to another field, such as a probe time, leaves
// the mark the same. The mark leaves out the labels, because this operator
// writes the media.liken.sh/ending label.
//
// Of any other pod, such as an idle pod or a reader pod, the pass reads
// only whether the kubelet ended it, because it replaces such a pod
// (endedpod.go). So the mark of such a pod is empty until the pod is
// Failed or Succeeded, and a move between Pending and Running wakes
// nothing. A new pod wakes the pass when it is a playback pod past the
// Pending phase or any other pod that the kubelet ended. That case comes from a gap in the watch, when
// the informer reports a pod that it had not cached as an add event. The
// pod that this operator creates starts Pending, so its add event wakes
// nothing.
var podRule = changeRule[Pod]{
	what: "the pods",
	mark: func(pod Pod) string {
		if isPlaybackPod(pod) {
			return identity(pod.Metadata) + "|" + markOf(pod.Status)
		}
		if podEnded(&pod) {
			return identity(pod.Metadata) + "|" + pod.Status.Phase
		}
		return ""
	},
	added: func(pod Pod) bool {
		if isPlaybackPod(pod) {
			return pod.Status.Phase != "" && pod.Status.Phase != podPending
		}
		return podEnded(&pod)
	},
}

func isPlaybackPod(pod Pod) bool {
	return pod.Metadata.Labels[playbackLabelKey] == playbackLabelValue
}
