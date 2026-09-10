package main

// The ending label is how a finished run reaches the compositor. The
// sidecar publishes the ending and then holds mpv alive for its exit
// grace, so there is a live surface for that half second. The label is
// what the compositor acts on: a Layout whose film region excludes
// media.liken.sh/ending stops matching the pod the moment the label
// lands, and the region's exit fade takes the film off the screen.
//
// The Play's status is not the signal. A Layout matches pods, and
// display-operator reads none of this operator's kinds, so the mark
// goes onto the pod itself.

import (
	"errors"
	"fmt"
	"os"
)

// labelEnding patches the ending label onto one Play's playback pod,
// once. It runs first in the pass for that Play, ahead of the retire
// that deletes the pod, because the fade needs the label while the pod
// still draws. The ending report is what wakes that pass, so the label
// lands within milliseconds of the film's last frame.
//
// A patch for a pod that has already gone answers ErrNotFound. That
// run's fade is over or never started, so the memo records it as done.
// A patch that fails for any other reason is reported and tried again
// on the next pass.
func (o *operator) labelEnding(play *Play) {
	namespace, name := play.Metadata.Namespace, play.Metadata.Name
	key := runKey(namespace, name)
	if o.endingLabeled[key] || !o.reports.endedFor(namespace, name) {
		return
	}
	pod := podName(name)
	err := PatchPodLabels(o.client, namespace, pod,
		map[string]string{endingLabelKey: endingLabelValue})
	if err != nil && !errors.Is(err, ErrNotFound) {
		fmt.Fprintf(os.Stderr, "labeling the ending on pod %s/%s: %v\n",
			namespace, pod, err)
		return
	}
	o.endingLabeled[key] = true
}
