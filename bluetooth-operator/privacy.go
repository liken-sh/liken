package main

// Low Energy privacy for the radio this pod holds.
//
// With privacy on, the radio advertises and connects from a resolvable
// private address that it changes every few minutes, so a device that
// never paired with it cannot recognize it from one appearance to the
// next. A person turns it on with the Adapter's spec.privacy.
//
// bluetoothd reads its Privacy key once, from main.conf, and sends it
// to the kernel when the adapter starts. The kernel accepts the privacy
// command only while the radio is powered off. So a new value takes
// effect only when bluetoothd starts again, and bondfetch writes the
// value into the pod's settings volume before that start
// (bondfetch/privacy.go). The file in the volume records the value
// that the running bluetoothd started with.
//
// On each pass, the operator compares spec.privacy with that file.
// When they differ, it deletes its own pod, and the DaemonSet creates a
// new one, whose bondfetch writes the new value. The delete waits for a
// pass that stored every bond, because the pod's bonds tree is an
// emptyDir, and a pairing from the last seconds would go with it.
//
// Restarting only the bluetoothd container would not apply the value,
// because the file in the volume stays as it is until bondfetch runs
// again, and bondfetch runs only in a new pod.

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
	// settingsRoot is the pod's settings volume, which this container
	// mounts read-only. bondfetch writes it, and start-bluetoothd reads
	// it.
	settingsRoot = "/var/run/bluetooth.liken.sh/settings"

	// privacyFile is the file in the settings volume that holds the
	// value bluetoothd started with.
	privacyFile = "privacy"

	// privacyOff is BlueZ's default, and the value of an empty
	// spec.privacy.
	privacyOff = "off"

	// podNameVar names this pod, which the pod spec supplies through the
	// downward API. The restart deletes the pod by this name.
	podNameVar = "POD_NAME"
)

// readStartedPrivacy answers the value that bluetoothd started with.
//
// A missing file answers an empty value and no error. That is a pod
// with no settings volume, where start-bluetoothd started bluetoothd
// with privacy off. The restart cannot apply a value to such a pod,
// because no bondfetch in it writes the file.
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

// wantedPrivacy answers the value an Adapter asks for. An empty field
// is off.
func wantedPrivacy(adapter *Adapter) string {
	if adapter.Spec.Privacy == "" {
		return privacyOff
	}
	return adapter.Spec.Privacy
}

// privacyRestart deletes this pod when the Adapter's spec.privacy
// differs from the value bluetoothd started with.
type privacyRestart struct {
	client    *apiclient.Client
	namespace string
	pod       string
	recorder  *events.Recorder

	// announced records that the Event is posted, and deleted records
	// that the API server accepted the delete. The pod runs for some
	// seconds after the delete, and the passes in that time must not
	// post a second Event or send a second delete. A delete that failed
	// is sent again on the next pass, with no second Event.
	announced bool
	deleted   bool
}

// apply compares the Adapter's spec.privacy with the value bluetoothd
// started with, and deletes this pod when they differ. stored reports
// whether this pass stored every bond and the identity file.
//
// A nil Adapter is a pass that did not read one, and an empty started
// value is a pod with no settings file. Neither one restarts.
func (r *privacyRestart) apply(adapter *Adapter, started string, stored bool) {
	if r.deleted || adapter == nil || started == "" {
		return
	}
	wanted := wantedPrivacy(adapter)
	if wanted == started {
		return
	}
	if !stored {
		fmt.Printf("privacy: %s asks for %s; the pod restarts after a pass that stores every bond\n",
			adapter.Metadata.Name, wanted)
		return
	}
	// The Event is queued before the delete is sent. The recorder
	// writes from its own goroutine, and the kubelet stops this
	// container soon after the delete lands, so an Event queued after
	// the delete could go with the container.
	if !r.announced {
		r.recorder.Normal(adapterReference(adapter), reasonPrivacyChanged, fmt.Sprintf(
			"privacy changes from %s to %s; deleting pod %s so that bluetoothd starts with the new value",
			started, wanted, r.pod))
		r.announced = true
	}
	if err := deleteObject(r.client, podPath(r.namespace, r.pod)); err != nil {
		fmt.Fprintf(os.Stderr, "deleting pod %s to apply privacy %s: %v\n", r.pod, wanted, err)
		return
	}
	r.deleted = true
	fmt.Printf("privacy: deleted pod %s; the next pod starts bluetoothd with privacy %s\n", r.pod, wanted)
}

// podPath is the API server's URL for one pod.
func podPath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/pods/" + name
}
