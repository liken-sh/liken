package main

// The functions here report whether this operator's pod was created
// from an older release's template. After a reboot into a new release,
// the new binary can run in the old pod spec for a while, and
// awaitingPodRefresh in conditions.go reports a mount missing in that
// time as AwaitingPodRefresh, not as a fault.

import "github.com/liken-sh/liken/liken/kubernetes"

// osVersionAnnotation holds the release that a pod's template was
// built for. image/build.sh writes the release into it on the
// DaemonSet and the pod template of manifests/machine-operator.yaml.
const osVersionAnnotation = "liken.sh/os-version"

// ownPodPath returns the path that lists this node's machine-operator
// pod. The server applies both selectors, because kubernetes.Pod does
// not decode labels, so the client cannot filter by label.
func ownPodPath(nodeName string) string {
	return "/api/v1/namespaces/liken-system/pods?labelSelector=app%3Dliken-machine-operator&fieldSelector=spec.nodeName%3D" + nodeName
}

// decidePodStale reports whether a pod's osVersionAnnotation differs
// from runningVersion, the release in this boot's facts.
//
// It returns false when it cannot tell: with no pod, with no
// annotation, or with an empty runningVersion. A wrong true would hide
// a real fault behind AwaitingPodRefresh.
func decidePodStale(pods []kubernetes.Pod, runningVersion string) bool {
	if runningVersion == "" {
		return false
	}
	for _, p := range pods {
		if v, ok := p.Metadata.Annotations[osVersionAnnotation]; ok {
			return v != runningVersion
		}
	}
	return false
}

// ownPodIsStale returns false when the list fails, for the reason
// decidePodStale gives.
func ownPodIsStale(r *reader, nodeName, runningVersion string) bool {
	pods, err := r.operatorPods(nodeName)
	if err != nil {
		return false
	}
	return decidePodStale(pods, runningVersion)
}
