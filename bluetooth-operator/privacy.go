package main

// Low Energy privacy for this pod's radio.
//
// With privacy on, the radio advertises and connects from a resolvable
// private address that changes periodically, so a device that never
// paired with the radio cannot track it. The Adapter's spec.privacy
// turns it on.
//
// bluetoothd reads Privacy from main.conf only when it starts, and the
// kernel accepts the privacy setting only while the radio is powered
// off, so a new value needs a new bluetoothd. bondfetch writes the
// value into the pod's settings volume before bluetoothd starts
// (bondfetch/privacy.go), so the file there holds the value that the
// running bluetoothd started with.
//
// When spec.privacy differs from that file, the operator deletes its
// own pod, and the DaemonSet creates a new one whose bondfetch writes
// the new value. Restarting only the bluetoothd container would not
// help, because bondfetch is the init container and runs only when a
// pod starts. The delete waits for a pass that stored every bond in
// its Secret, because the bonds directory is an emptyDir that the
// delete empties, and a bond not yet stored would be lost.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
)

const (
	// settingsRoot is the pod's settings volume. bondfetch writes it
	// and start-bluetoothd and start-btmon read it. This container
	// writes only its btmon file (btmon.go).
	settingsRoot = "/var/run/bluetooth.liken.sh/settings"

	// privacyFile is the file in the settings volume that records the
	// value bluetoothd started with.
	privacyFile = "privacy"

	// privacyOff is BlueZ's default, and the value that an empty
	// spec.privacy means.
	privacyOff = "off"

	// podNameVar is the environment variable that holds this pod's
	// name, set from the downward API. privacyRestart deletes the pod
	// by this name.
	podNameVar = "POD_NAME"
)

// readStartedPrivacy returns the privacy value that bluetoothd started
// with, read from the settings volume. It returns an empty string when
// the file is missing, as in a pod with no settings volume. There,
// start-bluetoothd starts bluetoothd with privacy off, and no bondfetch
// writes a new value, so a restart cannot change it.
func readStartedPrivacy(settings string) (string, error) {
	contents, err := os.ReadFile(filepath.Join(settings, privacyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(contents)), nil
}

// wantedPrivacy returns the Adapter's spec.privacy, or off when it is
// empty.
func wantedPrivacy(adapter *Adapter) string {
	if adapter.Spec.Privacy == "" {
		return privacyOff
	}
	return adapter.Spec.Privacy
}

// privacyRestart deletes this pod when the Adapter's spec.privacy
// differs from the value that bluetoothd started with.
type privacyRestart struct {
	client    *apiclient.Client
	namespace string
	pod       string
	recorder  *events.Recorder

	// The pod keeps running for a few seconds after the delete.
	// announced and deleted keep the passes in that time from posting
	// a second Event or sending a second delete. A failed delete is
	// sent again on the next pass, without a second Event.
	announced bool
	deleted   bool
}

// apply deletes this pod when the Adapter's spec.privacy differs from
// started, the value bluetoothd started with, and stored reports that
// this pass stored every bond and the identity file. It does nothing
// when the pass read no Adapter or the pod has no settings file.
func (r *privacyRestart) apply(adapter *Adapter, started string, stored bool) {
	if r.deleted || adapter == nil || started == "" {
		return
	}
	wanted := wantedPrivacy(adapter)
	if wanted == started {
		return
	}
	if !stored {
		fmt.Printf("privacy: the Adapter %s sets privacy %s, so the pod restarts after a pass that stores every bond\n",
			adapter.Metadata.Name, wanted)
		return
	}
	// Queue the Event before sending the delete. The recorder writes
	// from its own goroutine, and the kubelet stops this container soon
	// after the delete, so an Event queued later could be lost.
	if !r.announced {
		r.recorder.Normal(adapterReference(adapter), reasonPrivacyChanged, fmt.Sprintf(
			"spec.privacy changed from %s to %s, so the operator is deleting pod %s to start bluetoothd with the new value",
			started, wanted, r.pod))
		r.announced = true
	}
	if err := deleteObject(r.client, podPath(r.namespace, r.pod)); err != nil {
		fmt.Fprintf(os.Stderr, "privacy: deleting pod %s to apply privacy %s: %v\n", r.pod, wanted, err)
		return
	}
	r.deleted = true
	fmt.Printf("privacy: deleted pod %s, so the next pod starts bluetoothd with privacy %s\n", r.pod, wanted)
}

func podPath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/pods/" + name
}
