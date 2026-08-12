package main

// The pod-freshness guard: whether this operator's own pod predates
// the release it is running.
//
// System pods run the stable image tag :installed, and their
// DaemonSets update on OnDelete rather than a rolling update
// (cluster-operator/steward.go explains why). A reboot restarts the
// container into a new binary without touching the pod spec around
// it. Only a leader's boot rewrites the AddOn manifests that produce
// a fresh template, so a follower that reboots first runs the new
// binary inside the old pod spec until the pod steward refreshes the
// pod, seconds after the first leader boots that release.
// conditions.go reads
// the verdict this file computes to judge a missing-mount failure as
// that ordinary lag instead of a fault on the machine.

import "github.com/liken-sh/liken/kubernetes"

// osVersionAnnotation names the release a pod's template shipped
// with. image/build.sh stamps this annotation onto the
// machine-operator DaemonSet and its pod template
// (manifests/machine-operator.yaml), and cluster-operator/steward.go
// reads the same name off the DaemonSet to know which pods to
// refresh.
const osVersionAnnotation = "liken.sh/os-version"

// ownPodPath asks the API for this node's own machine-operator pod:
// liken-system's pods labeled app=liken-machine-operator, filtered
// again by spec.nodeName so the server never sends any other node's
// pod over the wire. kubernetes.Pod carries no labels, only
// annotations, so the label filtering happens in the query string
// here rather than in a second pass over the response, the same way
// cluster-operator/steward.go filters a DaemonSet's pods by its app
// label.
func ownPodPath(nodeName string) string {
	return "/api/v1/namespaces/liken-system/pods?labelSelector=app%3Dliken-machine-operator&fieldSelector=spec.nodeName%3D" + nodeName
}

// decidePodStale judges whether this operator's own pod predates the
// release it is running. pods is that one pod's listing, already
// narrowed to this node and this DaemonSet's label by ownPodPath.
// runningVersion is the release this boot's facts report
// (status.Version.Liken in reconcile.go). A pod's os-version
// annotation names the template it was created from, and the two
// disagreeing is exactly the state this guard exists for.
//
// No pod, or a pod carrying no annotation, both read as current. A
// pod that predates this annotation, from before this design existed,
// must not be judged stale forever, and a machine with no pod to find
// yet, early in a boot, has nothing to judge as stale either. An
// empty runningVersion also reads as current: facts that carry no
// version cannot support a verdict, and a wrong "stale" here would
// hide a real fault behind AwaitingPodRefresh.
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

// ownPodIsStale is the thin read around decidePodStale. A list that
// fails, for example because the API server is briefly unreachable,
// reads the same as a pod with no annotation: current. The guard this
// feeds must never manufacture a fault where none exists, so an API
// error here can only ever hide a real fault behind ApplyFailed's
// ordinary reporting, never behind a false AwaitingPodRefresh.
func ownPodIsStale(c *kubernetes.Client, nodeName, runningVersion string) bool {
	pods, err := kubernetes.List[kubernetes.Pod](c, ownPodPath(nodeName))
	if err != nil {
		return false
	}
	return decidePodStale(pods, runningVersion)
}
