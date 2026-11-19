package main

// refreshOSPods evicts the pods of the OS's own DaemonSets that were
// created from an older release's template, once their machine runs the
// new release, so that each DaemonSet creates them again from the
// current template.
//
// image/build.sh bakes the images of these DaemonSets into the OS, and
// k3s imports them into containerd at boot. Their pods use
// imagePullPolicy: Never, so a node runs only the images that its own
// OS, or an earlier OS on that node, imported. Each template names a
// tag such as liken.sh/machine-operator:installed, which every release
// puts on its own build, so the same pod spec runs each node's own
// image. The updateStrategy is OnDelete, because a rolling update would
// recreate pods on nodes whose OS does not have the new image yet, and
// would delete the machine-operator pod that a machine needs to run its
// own upgrade.
//
// With OnDelete, a pod made from the old template keeps running after
// its machine reboots into the new release. Each DaemonSet and its pod
// template have the annotation liken.sh/os-version, the release whose
// manifests are applied. refreshOSPods evicts a pod only when the
// machine's status reports that release and the pod's annotation is a
// different one. A machine still on the old OS keeps its old pod,
// because the new template is for a release that its OS does not have.
// A machine that runs a newer release than the applied manifests
// keeps its old pod too, because the DaemonSet would only recreate
// another old pod, on every pass, until a leader applies the newer
// manifests.

import (
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// image/build.sh writes the release into this annotation of each OS
// manifest when it builds the OS image.
const osVersionAnnotation = "liken.sh/os-version"

// The rollout also reads the os-version of this DaemonSet (fleet.go),
// because a machine cannot act on a reboot turn while the
// machine-operator template is older than the release it boots.
const machineOperatorDaemonSet = "liken-machine-operator"

// refreshedDaemonSets lists the OS DaemonSets whose pods refreshOSPods
// evicts. It finds the pods of each one by the label app: <name>.
var refreshedDaemonSets = []string{
	machineOperatorDaemonSet,
	"machine-logs",
}

const daemonSetsPath = "/apis/apps/v1/namespaces/liken-system/daemonsets"

func daemonSetPodsPath(name string) string {
	return "/api/v1/namespaces/liken-system/pods?labelSelector=app%3D" + name
}

// decideRefresh returns the pods of one DaemonSet to evict. dsVersion
// is the DaemonSet's os-version annotation, and when it is empty no pod
// is evicted. A terminating pod stays in the listing until the kubelet
// stops it, so it is skipped, or every pass would evict it again.
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
			continue
		}
		osVersion, known := running[p.Spec.NodeName]
		if !known || osVersion != dsVersion {
			continue
		}
		if p.Metadata.Annotations[osVersionAnnotation] == dsVersion {
			continue
		}
		refresh = append(refresh, p)
	}
	return refresh
}

// refreshOSPods evicts the old pods of each DaemonSet in
// refreshedDaemonSets. overduePods records which overdue terminating
// pods a pass has already logged (stuckpods.go).
func refreshOSPods(r *fleetReader, machines []machine.Machine, overduePods *overduePodReports, now time.Time) {
	complete := true
	for _, name := range refreshedDaemonSets {
		complete = refreshDaemonSetPods(r, machines, name, overduePods, now) && complete
	}
	overduePods.settle(complete)
}

// daemonSetVersion returns a DaemonSet's os-version annotation, or ""
// when the DaemonSet does not exist or has no annotation. Any other
// read failure is an error and not "", because the rollout (fleet.go)
// treats "" as a template with no release to wait for.
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

// refreshDaemonSetPods evicts the old pods of one DaemonSet. It returns
// false when a read failed. Evicting a log relay pod deletes the
// emptyDir that holds its resume cursors, so after each OS upgrade the
// relay sends the end of the machine's logs again, and the seq field of
// each envelope lets a reader drop the duplicates.
func refreshDaemonSetPods(r *fleetReader, machines []machine.Machine, name string, overduePods *overduePodReports, now time.Time) bool {
	dsVersion, err := daemonSetVersion(r, name)
	if err != nil {
		fmt.Printf("reading the DaemonSet %s to evict its old pods: %v\n", name, err)
		return false
	}
	if dsVersion == "" {
		return true
	}
	pods, err := r.daemonSetPods(name)
	if err != nil {
		fmt.Printf("listing the pods of the DaemonSet %s to evict the old ones: %v\n", name, err)
		return false
	}
	overduePods.reportOverdue(pods, now)
	for _, p := range decideRefresh(dsVersion, machines, pods) {
		if err := kubernetes.EvictPod(r.client, p); err != nil {
			fmt.Printf("evicting the pod %s: %v\n", p.Metadata.Name, err)
		} else {
			fmt.Printf("evicted the pod %s on %s because it was created from the template of an older release than %s, so the DaemonSet creates it again\n",
				p.Metadata.Name, p.Spec.NodeName, dsVersion)
		}
	}
	return true
}
