package main

// Each Play has a retained status topic and a retained availability
// topic on the bus. This file clears both when the Play is deleted, so
// the broker keeps no retained message for a Play that does not exist.

import (
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// playFinalizer keeps a deleted Play in the API server until the
// operator has cleared its status and availability topics.
const playFinalizer = "media.liken.sh/bus-topics"

// addPlayFinalizer adds playFinalizer to a Play that does not have it. The patch
// sends the resourceVersion that the pass read, so a write by another
// client in between causes a conflict, and the next pass reads the Play
// again. On success, addPlayFinalizer stores the new resourceVersion in the
// caller's copy, so the status write later in the same pass uses the
// current version.
func (o *operator) addPlayFinalizer(play *Play) {
	if play.Metadata.hasFinalizer(playFinalizer) {
		return
	}
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	finalizers := play.Metadata.withFinalizer(playFinalizer)
	version, err := o.patchPlayFinalizers(play, finalizers)
	if err != nil {
		// The next pass reads the Play again after a conflict or when the
		// Play is gone, so neither is logged.
		if !errors.Is(err, apiclient.ErrConflict) && !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "holding play %s/%s: %v\n", namespace, name, err)
		}
		return
	}
	play.Metadata.Finalizers = finalizers
	play.Metadata.ResourceVersion = version
}

// removePlayFinalizer finishes a Play that is being deleted. It deletes the
// Play's pod, waits until the pod is gone, clears the topics, forgets
// the run's cached report, and then removes playFinalizer. Each early
// return leaves the finalizer in place, and a later pass calls
// removePlayFinalizer again.
//
// It waits for the pod because the sidecar publishes retained messages
// to the topics as it exits, and those would replace an earlier clear. It
// removes the finalizer only after Bus.Publish queued both clears,
// because Publish drops a message while the bus is disconnected or its
// queue is full.
func (o *operator) removePlayFinalizer(play *Play) {
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

	if !o.clearPlayTopics(namespace, name) {
		return
	}
	o.reports.forget(runKey(namespace, name))

	if !play.Metadata.hasFinalizer(playFinalizer) {
		return
	}
	if _, err := o.patchPlayFinalizers(play, play.Metadata.withoutFinalizer(playFinalizer)); err != nil {
		if !errors.Is(err, apiclient.ErrConflict) && !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "releasing play %s/%s: %v\n", namespace, name, err)
		}
		return
	}
	// Logged only here, so each deletion writes one line and not one per
	// pass.
	logLine(o.log, "play %s/%s: deleted, so its playback pod is gone and its topics are cleared", namespace, name)
}

// clearPlayTopics publishes an empty retained payload to the run's
// status and availability topics, which makes the broker delete their
// retained messages. It returns whether the bus queued both publishes.
func (o *operator) clearPlayTopics(namespace, name string) bool {
	status := o.bus.Publish(playStatusTopic(o.topicBase, namespace, name), nil, true)
	availability := o.bus.Publish(playAvailabilityTopic(o.topicBase, namespace, name), nil, true)
	return status && availability
}

// reclaimPlays clears the topics of runs whose Play no longer exists.
// It covers what playFinalizer cannot: a Play deleted before the
// operator added the finalizer to it, and a clear that Bus.Publish
// queued but the broker never received. QoS 0 has no acknowledgement,
// so a connection that fails during a write loses the clear.
//
// live has the key of every Play in this pass's list. A run in o.reports
// sent a bus message, so a run that is not in live can still have
// retained topics. A Play recreated under the same name is in live, so
// its run is left alone.
func (o *operator) reclaimPlays(live map[string]bool) {
	for _, key := range o.reports.stale(live) {
		// A run that is forgotten is not returned by stale again, so
		// forget it only after the bus queued the clear.
		namespace, name := splitRunKey(key)
		if !o.clearPlayTopics(namespace, name) {
			return
		}
		o.reports.forget(key)
	}
}

// patchPlayFinalizers patches one Play's finalizer list and returns the
// new resourceVersion. It records the version in o.view.plays.Versions,
// so a pass reads the Play from the API server until the watch delivers
// the patch.
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

// deletePlay deletes one Play and records in o.view.plays.Versions that
// the store's copy is out of date, so the next pass reads the Play from
// the API server. A finalizer can keep it there with a deletion
// timestamp.
func (o *operator) deletePlay(namespace, name string) error {
	return o.view.plays.Versions.Send(namespace+"/"+name, func() (string, error) {
		return "", DeletePlay(o.client, namespace, name)
	})
}
