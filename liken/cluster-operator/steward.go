package main

// The pod steward keeps the OS's own pods up to date with the
// operating systems that run under them.
//
// Two container images are part of the OS: the operator's image and
// the log relays' image. image/build.sh bakes both into the
// initramfs, k3s imports them into containerd at boot, and
// imagePullPolicy: Never means a node can only ever run the builds
// that its own OS carries, or builds that an earlier OS on that node
// left in containerd's persistent store. That breaks the usual
// Kubernetes assumption that any node can pull any image, and every
// OS DaemonSet's design accounts for it:
//
//   - Each template pins a *stable* tag (liken.sh/operator:installed,
//     liken.sh/logs:installed). Every release tags its own build
//     with this stable tag, so the same pod spec resolves to each
//     node's own baked image, and applying a new release's manifests
//     does not change the image field at all.
//   - updateStrategy is OnDelete, so applying manifests never
//     deletes a running pod. A rolling update would recreate pods on
//     nodes whose OS does not carry the new image yet. For the
//     operator, that would kill the very pod each machine needs to
//     drive its own upgrade, and leave the machine unable to ever
//     take one.
//
// What is left is freshness. After a machine reboots into a new
// release, its existing pod objects predate the new manifests, and
// this steward deletes them so each DaemonSet can recreate its pod
// from the current template. Every stewarded DaemonSet carries a
// liken.sh/os-version annotation that names the release that shipped
// it. The template stamps this annotation onto its pods too. The
// steward refreshes a pod exactly when its machine reports that
// version in its facts, but the pod predates it. Both halves of that
// condition matter. A machine still on the old OS keeps its old pod,
// because evicting it would leave the machine without that pod's
// function at all. A machine ahead of the applied manifests also
// keeps its old pod, because a refresh would recreate another stale
// pod, and this would repeat every sweep until a leader running the
// new release applies manifests that can actually satisfy the
// machine.

import (
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// osVersionAnnotation names the liken release that a manifest, and
// the pods created from it, shipped with. image/build.sh substitutes
// the real version into each DaemonSet when it bakes the image.
const osVersionAnnotation = "liken.sh/os-version"

// machineOperatorDaemonSet names the stewarded DaemonSet whose
// applied os-version the rollout gate also reads (fleet.go and
// rollout.go). Both OS DaemonSets ship in the same manifests write,
// so one annotation answers for the applied release, and the machine
// operator's template is the one whose lag blocks a machine's
// actuation after a reboot.
const machineOperatorDaemonSet = "liken-machine-operator"

// stewardedDaemonSets lists the OS's own DaemonSets: the operator and
// the log relays. Their images are baked into the initramfs, so
// their pods need the steward's refresh after an upgrade. Each
// DaemonSet is expected to label its pods app: <name>, matching the
// selector its manifest declares.
var stewardedDaemonSets = []string{
	machineOperatorDaemonSet,
	"machine-logs",
}

// daemonSetsPath is the collection of DaemonSets in liken-system: the
// OS's own and the ones the features seed.
const daemonSetsPath = "/apis/apps/v1/namespaces/liken-system/daemonsets"

func daemonSetPodsPath(name string) string {
	return "/api/v1/namespaces/liken-system/pods?labelSelector=app%3D" + name
}

// decideRefresh computes the steward's whole judgment, over the
// sweep's inputs: which of one DaemonSet's pods to evict, so the
// DaemonSet recreates them from the current template. dsVersion is
// the os-version annotation on the DaemonSet itself. It names the
// release whose manifests are actually applied. An empty string,
// meaning no annotation or no DaemonSet, means there is no release
// to refresh toward.
//
// A pod that is already terminating is not evicted again. It stays in
// the pod copy until the kubelet stops it, and each sweep in that time
// would otherwise evict it once more.
func decideRefresh(dsVersion string, machines []machine.Machine, pods []kubernetes.Pod) []kubernetes.Pod {
	if dsVersion == "" {
		return nil
	}
	running := make(map[string]string, len(machines))
	for i := range machines {
		running[machines[i].Metadata.Name] = machines[i].Status.Version.Liken
	}
	var refresh []kubernetes.Pod
	for _, p := range pods {
		if p.Terminating() {
			continue // an earlier sweep evicted it, and the DaemonSet recreates it once it is gone
		}
		osVersion, known := running[p.Spec.NodeName]
		if !known || osVersion != dsVersion {
			continue // the machine is not yet running what the manifests shipped
		}
		if p.Metadata.Annotations[osVersionAnnotation] == dsVersion {
			continue // the pod already comes from this release's template
		}
		refresh = append(refresh, p)
	}
	return refresh
}

// stewardOSPods carries out the steward's work, run once per sweep,
// over every stewarded DaemonSet in turn. The steward's memory holds
// the terminating pods it has reported (stuckpods.go).
func stewardOSPods(r *fleetReader, machines []machine.Machine, steward *podSteward, now time.Time) {
	complete := true
	for _, name := range stewardedDaemonSets {
		complete = stewardDaemonSet(r, machines, name, steward, now) && complete
	}
	steward.settle(complete)
}

// daemonSetVersion reads one DaemonSet's os-version annotation: the
// release whose manifests are actually applied. It returns "" when
// the DaemonSet does not exist yet, or predates this annotation,
// because both cases mean there is nothing yet to compare a machine's
// running version against. Any other failure is an error, not "",
// because "" turns off the rollout's template gate, and a failed read
// does not show that the template is current. The rollout gate reads
// this same function for the machine-operator DaemonSet, on the same
// terms (fleet.go, rollout.go).
func daemonSetVersion(r *fleetReader, name string) (string, error) {
	ds, err := r.daemonSet(name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ds.Metadata.Annotations[osVersionAnnotation], nil
}

// stewardDaemonSet reads one DaemonSet's shipped version, lists its
// pods, and evicts the stale ones. It deliberately evicts pods
// instead of deleting them, because eviction is the same action
// liken already uses for drains, and the DaemonSet recreates the pod
// either way. For the relay pod, eviction also discards the pod's
// emptyDir resume cursors. So each OS upgrade re-sends the tail of
// that machine's log streams once, and the envelopes' seq field
// removes any duplicates.
//
// It answers whether it read the DaemonSet's pods, or found no
// DaemonSet to read them for.
func stewardDaemonSet(r *fleetReader, machines []machine.Machine, name string, steward *podSteward, now time.Time) bool {
	dsVersion, err := daemonSetVersion(r, name)
	if err != nil {
		fmt.Printf("reading the %s DaemonSet for the steward: %v\n", name, err)
		return false
	}
	if dsVersion == "" {
		return true // no DaemonSet, or nothing applied yet, to steward toward
	}
	pods, err := r.daemonSetPods(name)
	if err != nil {
		fmt.Printf("listing %s pods for the steward: %v\n", name, err)
		return false
	}
	steward.reportOverdue(pods, now)
	for _, p := range decideRefresh(dsVersion, machines, pods) {
		if err := kubernetes.EvictPod(r.client, p); err != nil {
			fmt.Printf("refreshing pod %s: %v\n", p.Metadata.Name, err)
		} else {
			fmt.Printf("pod %s on %s predates release %s; evicted for the DaemonSet to recreate\n",
				p.Metadata.Name, p.Spec.NodeName, dsVersion)
		}
	}
	return true
}
