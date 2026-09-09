package main

// The two retained topics one Play stands on the bus, and the finalizer
// that makes clearing them certain. The operator holds
// media.liken.sh/bus-topics on every Play. When the Play is deleted, the
// operator deletes the pod, waits for the pod to be gone, clears the
// status and the availability, and only then takes the finalizer off. So a
// Play is never gone from the API server while its topics stand on the
// broker. reclaimPlays is the sweep behind it, for a Play deleted before
// this operator held a finalizer and for a clear that left the operator
// and never reached the broker.

import (
	"errors"
	"fmt"
	"os"
)

// The finalizer this operator holds on every Play. It keeps the Play until
// the operator has cleared the run's retained status and availability
// topics. A Play that is gone while its topics stand leaves a report on the
// broker that reads as a run nobody can find.
const playFinalizer = "media.liken.sh/bus-topics"

// holdPlay puts the operator's finalizer on a Play that does not carry it.
// The patch names the resourceVersion the pass read the Play at, so a
// write another program made first is a conflict the next pass reads
// again. The answered resourceVersion goes back on the caller's copy, so
// the status write later in this pass carries the version the API server
// now holds.
func (o *operator) holdPlay(play *Play) {
	if play.Metadata.holds(playFinalizer) {
		return
	}
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	finalizers := play.Metadata.with(playFinalizer)
	version, err := PatchPlayFinalizers(o.client, namespace, name,
		play.Metadata.ResourceVersion, finalizers)
	if err != nil {
		// A conflict and an absent Play are both states the next pass reads
		// again, so neither is reported here.
		if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			fmt.Fprintf(os.Stderr, "holding play %s/%s: %v\n", namespace, name, err)
		}
		return
	}
	play.Metadata.Finalizers = finalizers
	play.Metadata.ResourceVersion = version
}

// releasePlay is the whole ending of a deleting Play. It deletes the pod
// so the unit's claim frees at once, waits for that pod to be gone, clears
// the run's retained status and availability, forgets the run, and only
// then takes the finalizer off.
//
// The wait matters because the sidecar publishes its own closing messages
// as it exits. A clear the operator published while the pod still ran
// would be overwritten by them, and the topics would stand again.
//
// The finalizer goes last because the clear is what it exists to make
// certain: the release publishes first and releases second. A session the
// client does not hold is the one state that stops the release short. A
// QoS 0 publish with no session is dropped, and the Play would then be
// gone with its topics standing.
func (o *operator) releasePlay(play *Play) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if err := DeletePod(o.client, namespace, podName(name)); err != nil {
		fmt.Fprintf(os.Stderr, "deleting the pod of play %s/%s: %v\n", namespace, name, err)
		return
	}
	if _, err := GetPod(o.client, namespace, podName(name)); !errors.Is(err, ErrNotFound) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the pod of play %s/%s: %v\n", namespace, name, err)
		}
		return
	}

	// A clear is a QoS 0 publish, which Bus.Publish drops while the client
	// holds no session. The finalizer is what makes the clear certain, so a
	// release with no session publishes nothing, keeps the finalizer, and
	// leaves the work to a later pass.
	if !o.bus.Connected() {
		return
	}
	o.clearPlayTopics(namespace, name)
	o.reports.forget(runKey(namespace, name))

	if !play.Metadata.holds(playFinalizer) {
		return
	}
	if _, err := PatchPlayFinalizers(o.client, namespace, name, play.Metadata.ResourceVersion,
		play.Metadata.without(playFinalizer)); err != nil {
		if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			fmt.Fprintf(os.Stderr, "releasing play %s/%s: %v\n", namespace, name, err)
		}
	}
}

// clearPlayTopics empties the two retained topics one run stood on the
// bus. An empty retained payload is how a writer clears a topic.
func (o *operator) clearPlayTopics(namespace, name string) {
	o.bus.Publish(playStatusTopic(o.topicBase, namespace, name), nil, true)
	o.bus.Publish(playAvailabilityTopic(o.topicBase, namespace, name), nil, true)
}

// reclaimPlays is the sweep behind the finalizer. It is for two runs: one
// whose Play was deleted before the operator held a finalizer on it, and
// one whose clear left the operator but never reached the broker, because
// a QoS 0 publish carries no acknowledgement and a session that fails
// mid-write loses whatever it had not yet sent.
//
// The sweep waits for nothing. The desk offers a run only when the pass's
// own list of every Play does not hold it, so the Play is gone from the
// API server and no subscriber is waiting to read its final state. A Play
// recreated under the same name is in that list, so the sweep passes over
// it.
func (o *operator) reclaimPlays(live map[string]bool) {
	for _, key := range o.reports.stale(live) {
		// Forgetting a run is what stops the sweep offering it again, so the
		// sweep does neither the clear nor the forget while the client holds no
		// session. The run keeps its place on the desk and the next pass sweeps
		// it.
		if !o.bus.Connected() {
			return
		}
		namespace, name := splitRunKey(key)
		o.clearPlayTopics(namespace, name)
		o.reports.forget(key)
	}
}
