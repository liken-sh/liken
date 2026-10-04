package main

// The claims, the ResourceSlices, the Displays, the Receivers, the
// Sinks, the Televisions, and the operator's own pods wake the pass when a change reaches a field the
// pass reads, and a change to anything else wakes nothing. Each rule names those fields as a
// mark, and the handler compares the mark of the copy the informer held
// with the mark of the new copy. The informer hands an update both, so
// the handler keeps no copy of its own.
//
// Each mark leaves out what this operator writes itself, so its own
// write does not wake the pass that made it: a Display's spec.override,
// a Receiver's session in its spec and its status, and a Sink's session
// in its status. A claim's spec
// is immutable, and the operator writes nothing else on a claim, so a
// claim's mark is what the scheduler writes. Each mark holds the UID
// and the deletion mark too. After a gap in the watch, an object
// deleted and created again with the same name reaches the handler as
// an update, and a deletion request changes what the pass does.
//
// After a gap, the informer reports each difference from what it held
// as an addition, an update, or a deletion, so the same rules cover a
// change made while the watch was down.

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/tools/cache"
)

// changeRule is the wake rule for one kind.
type changeRule[T any] struct {
	what string
	// ours reports whether the pass reads the object at all. A nil ours
	// is every object.
	ours func(T) bool
	// mark answers the fields of the object the pass reads and another
	// writer changes.
	mark func(T) string
	// added reports whether a new object wakes the pass. A nil added
	// wakes on every new object.
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
				poke(wake)
			}
		},
		UpdateFunc: func(before, after any) {
			item, err := informer.Convert[T](after)
			if err != nil {
				informer.Report(r.what, err)
				return
			}
			// A held copy that does not convert was reported when it
			// arrived. Nothing says what it held, so the change counts.
			old, err := informer.Convert[T](before)
			if err != nil {
				if r.matters(item) {
					poke(wake)
				}
				return
			}
			if (r.matters(item) || r.matters(old)) && r.mark(item) != r.mark(old) {
				poke(wake)
			}
		},
		// A removal whose tombstone holds no copy wakes the pass, because
		// nothing says whose object it was, and one extra pass costs less
		// than a change the pass never reads.
		DeleteFunc: func(object any) {
			item, err := informer.Convert[T](object)
			if err != nil {
				informer.Report(r.what, err)
				poke(wake)
				return
			}
			if r.matters(item) {
				poke(wake)
			}
		},
	}
}

func (r changeRule[T]) matters(item T) bool {
	return r.ours == nil || r.ours(item)
}

// identity is the part of every mark that names the object's life: its
// UID and its deletion mark.
func identity(meta ObjectMeta) string {
	return meta.UID + "|" + meta.DeletionTimestamp
}

// markOf is a field's JSON, which compares the way the API server
// stores the field.
func markOf(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "unmarshalable: " + err.Error()
	}
	return string(encoded)
}

// claimRule wakes the pass when the scheduler allocates one of this
// operator's claims or releases the allocation, and when one goes away.
// The pass reads the allocation to resolve a unit's screen, its Sinks,
// and a controller's Peripheral. A claim this operator just created
// holds no allocation, so its creation wakes nothing.
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

// ownedByThisOperator reports whether a Play, a Player, or a Remote owns
// the object, which is true of every claim this operator creates. The
// claims watch covers the whole cluster, and the other claims wake
// nothing.
func ownedByThisOperator(meta ObjectMeta) bool {
	for _, owner := range meta.OwnerReferences {
		if strings.HasPrefix(owner.APIVersion, playResource.Group+"/") {
			return true
		}
	}
	return false
}

// sliceRule wakes the pass when a driver publishes, changes, or
// withdraws a ResourceSlice. The pass reads a slice's devices to name a
// unit's monitor and machine. A slice holds only a spec, so its
// generation counts every change the pass could read.
var sliceRule = changeRule[ResourceSlice]{
	what: "the ResourceSlices",
	mark: func(slice ResourceSlice) string {
		return identity(slice.Metadata) + "|" + strconv.FormatInt(slice.Metadata.Generation, 10)
	},
}

// displayRule wakes the pass when the display-operator writes a
// Display's status: whether a panel is connected, and what it shows.
// The Screen condition and the panel both read it. The spec holds only
// the override this operator applies, so the mark leaves it out.
var displayRule = changeRule[Display]{
	what: "the Displays",
	mark: func(display Display) string {
		return identity(display.Metadata) + "|" + markOf(display.Status)
	},
}

// receiverRule wakes the pass when a person rewires a Receiver's inputs
// or its volume scale, or when the equipment-operator writes its power,
// its input, its zones, or its conditions. The session, in the spec or
// the status, is this operator's to write, and the pass decides it from
// its own record, so the mark leaves it out. A zone's volume is in the
// mark, because the pass hands each report to the volume engine.
var receiverRule = changeRule[Receiver]{
	what: "the Receivers",
	mark: func(receiver Receiver) string {
		status := receiver.Status
		status.Session = nil
		return identity(receiver.Metadata) + "|" + markOf(receiver.Spec.Inputs) + "|" +
			markOf(receiver.Spec.Volume) + "|" + markOf(status)
	},
}

// sinkRule wakes the pass when a person changes a Sink's volume scale,
// or when the audio-operator reports a new level or mute. The pass
// hands each report to the volume engine. The session is this
// operator's own write, and the rest of the status changes nothing the
// pass reads, so the mark leaves both out.
var sinkRule = changeRule[Sink]{
	what: "the Sinks",
	mark: func(sink Sink) string {
		return identity(sink.Metadata) + "|" + markOf(sink.Spec.Volume) + "|" + markOf(sink.Status.Observed)
	},
}

// televisionRule wakes the pass when the CEC node workload writes a new
// ask for a Player's screen, which the pass relays (screenask.go).
var televisionRule = changeRule[Television]{
	what: "the Televisions",
	mark: func(television Television) string {
		return identity(television.Metadata) + "|" + markOf(television.Status.ScreenAsk)
	},
}

// podRule wakes the pass when one of this operator's pods goes away,
// and when a playback pod's status changes. The pass derives a Play's
// phase from its pod's status: Pending with the scheduler's message,
// Running, Succeeded, Failed, and a display container that keeps
// restarting. The Pod struct holds only the status fields the pass
// reads, so the mark changes only when one of them does, and not when
// a probe time moves. The ending label is this operator's own write,
// and the mark leaves the labels out.
//
// An idle pod and a reader pod restart their containers in place, and
// the pass reads nothing of their status, so only their removal leaves
// the pass something to do. A new pod wakes the pass only when it is a
// playback pod that has already started: after a gap in the watch, the
// informer reports a pod it did not hold as an addition, whatever
// happened to it in the gap. The API server gives every pod it creates
// the Pending phase, so this operator's own create wakes nothing.
var podRule = changeRule[Pod]{
	what: "the pods",
	mark: func(pod Pod) string {
		if !isPlaybackPod(pod) {
			return ""
		}
		return identity(pod.Metadata) + "|" + markOf(pod.Status)
	},
	added: func(pod Pod) bool {
		return isPlaybackPod(pod) && pod.Status.Phase != "" && pod.Status.Phase != podPending
	},
}

func isPlaybackPod(pod Pod) bool {
	return pod.Metadata.Labels[playbackLabelKey] == playbackLabelValue
}
