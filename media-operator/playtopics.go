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

	"github.com/liken-sh/liken/kubernetes/apiclient"
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
	version, err := o.patchPlayFinalizers(play, finalizers)
	if err != nil {
		// A conflict and an absent Play are both states the next pass reads
		// again, so neither is reported here.
		if !errors.Is(err, apiclient.ErrConflict) && !errors.Is(err, apiclient.ErrNotFound) {
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
// certain: the release publishes first and releases second. A clear the
// bus did not queue stops the release short. The bus drops a QoS 0
// publish while it holds no session and when its write queue is full, and
// the Play would then be gone with its topics standing.
func (o *operator) releasePlay(play *Play) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	if err := DeletePod(o.client, namespace, podName(name)); err != nil {
		fmt.Fprintf(os.Stderr, "deleting the pod of play %s/%s: %v\n", namespace, name, err)
		return
	}
	if _, err := GetPod(o.client, namespace, podName(name)); !errors.Is(err, apiclient.ErrNotFound) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the pod of play %s/%s: %v\n", namespace, name, err)
		}
		return
	}

	// A clear is a QoS 0 publish, which Bus.Publish drops while the client
	// holds no session and when its write queue is full. The finalizer is
	// what makes the clear certain, so a release whose clear the bus did
	// not queue keeps the finalizer and leaves the work to a later pass.
	if !o.clearPlayTopics(namespace, name) {
		return
	}
	o.reports.forget(runKey(namespace, name))

	if !play.Metadata.holds(playFinalizer) {
		return
	}
	if _, err := o.patchPlayFinalizers(play, play.Metadata.without(playFinalizer)); err != nil {
		if !errors.Is(err, apiclient.ErrConflict) && !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "releasing play %s/%s: %v\n", namespace, name, err)
		}
		return
	}
	// The finalizer comes off once, so this is the one line a stop earns.
	logLine(o.log, "play %s/%s: deleted, so its playback pod is gone and its topics are cleared", namespace, name)
}

// clearPlayTopics empties the two retained topics one run stood on the
// bus. An empty retained payload is how a writer clears a topic. It
// answers whether the bus queued both clears, and a caller that answered
// false clears both again on a later pass.
func (o *operator) clearPlayTopics(namespace, name string) bool {
	status := o.bus.Publish(playStatusTopic(o.topicBase, namespace, name), nil, true)
	availability := o.bus.Publish(playAvailabilityTopic(o.topicBase, namespace, name), nil, true)
	return status && availability
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
		// sweep forgets a run only once the bus queued its clear. The bus
		// drops a clear while it holds no session and when its write queue is
		// full, and the run then keeps its place on the desk for the next
		// pass.
		namespace, name := splitRunKey(key)
		if !o.clearPlayTopics(namespace, name) {
			return
		}
		o.reports.forget(key)
	}
}

// patchPlayFinalizers writes one Play's finalizer list from the copy the
// pass holds, and notes the version the API server answers in the Play
// store's memo, so the next pass does not read the copy the patch
// replaced.
func (o *operator) patchPlayFinalizers(play *Play, finalizers []string) (string, error) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	var version string
	err := o.view.plays.Versions.Send(playKey(play), func() (string, error) {
		var err error
		version, err = PatchPlayFinalizers(o.client, namespace, name, play.Metadata.ResourceVersion, finalizers)
		return version, err
	})
	return version, err
}

// deletePlay deletes one Play. The memo then holds no version of it, so
// the next pass reads the Play from the API server: a Play that a
// finalizer keeps is there with its deletion mark, and a Play the
// delete removed is absent, whatever the store still holds.
func (o *operator) deletePlay(namespace, name string) error {
	return o.view.plays.Versions.Send(namespace+"/"+name, func() (string, error) {
		return "", DeletePlay(o.client, namespace, name)
	})
}
