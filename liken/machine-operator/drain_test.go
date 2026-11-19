package main

// Tests for the drain: which pods it evicts and in what order, when it
// clears the node, and which cordons it removes.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/liken/kubernetes"
)

var drainNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func pod(name, ns string, opts ...func(*kubernetes.Pod)) kubernetes.Pod {
	var p kubernetes.Pod
	p.Metadata.Name = name
	p.Metadata.Namespace = ns
	p.Status.Phase = "Running"
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

func ownedByDaemonSet(p *kubernetes.Pod) {
	p.Metadata.OwnerReferences = []kubernetes.OwnerReference{{Kind: "DaemonSet"}}
}

func mirror(p *kubernetes.Pod) {
	p.Metadata.Annotations = map[string]string{mirrorPodAnnotation: "true"}
}

// terminating marks a pod that an earlier pass evicted and that is
// still shutting down.
func terminating(p *kubernetes.Pod) {
	p.Metadata.DeletionTimestamp = "2026-07-06T11:59:30Z"
}

func completed(p *kubernetes.Pod) {
	p.Status.Phase = "Succeeded"
}

// servesDRA gives a pod the hostPath mounts of a DRA driver: the
// kubelet's registration directory and the plugin directory with the
// driver's socket.
func servesDRA(p *kubernetes.Pod) {
	p.Spec.Volumes = append(p.Spec.Volumes,
		kubernetes.PodVolume{Name: "registry", HostPath: &kubernetes.HostPathVolume{
			Path: "/var/lib/kubelet/plugins_registry"}},
		kubernetes.PodVolume{Name: "plugins", HostPath: &kubernetes.HostPathVolume{
			Path: "/var/lib/kubelet/plugins"}})
}

// holdsAClaim gives a pod a resource claim made from a template, like
// a pod that uses a device operator's devices.
func holdsAClaim(p *kubernetes.Pod) {
	p.Spec.ResourceClaims = []kubernetes.PodResourceClaim{
		{Name: "screen", ResourceClaimTemplateName: "screens"},
	}
}

// evicted returns the names of the pods that a step evicts, in order.
func evicted(step drainStep) string {
	var names []string
	for _, p := range step.evict {
		names = append(names, p.Metadata.Name)
	}
	return strings.Join(names, " ")
}

func tolerating(t kubernetes.Toleration) func(*kubernetes.Pod) {
	return func(p *kubernetes.Pod) { p.Spec.Tolerations = append(p.Spec.Tolerations, t) }
}

func TestEvictablePodsSkipsWhatCannotMove(t *testing.T) {
	pods := []kubernetes.Pod{
		pod("web", "default"),
		pod("liken-operator", "liken-system", ownedByDaemonSet),
		pod("etcd-shim", "kube-system", mirror),
		pod("job-done", "default", completed),
	}
	evictable := evictablePods(pods)
	if len(evictable) != 1 || evictable[0].Metadata.Name != "web" {
		t.Errorf("evictablePods = %+v, want only the pod web", evictable)
	}
}

func TestEvictablePodsLeavesAPodThatToleratesTheCordon(t *testing.T) {
	cases := []struct {
		name       string
		toleration kubernetes.Toleration
		moves      bool
	}{
		{"the key, Exists, NoSchedule", kubernetes.Toleration{Key: unschedulableTaint, Operator: "Exists", Effect: "NoSchedule"}, false},
		{"the key, every effect", kubernetes.Toleration{Key: unschedulableTaint, Operator: "Exists"}, false},
		{"the key, Equal with no value", kubernetes.Toleration{Key: unschedulableTaint, Operator: "Equal", Effect: "NoSchedule"}, false},
		{"the key, NoExecute only", kubernetes.Toleration{Key: unschedulableTaint, Operator: "Exists", Effect: "NoExecute"}, true},
		{"another key", kubernetes.Toleration{Key: "node.kubernetes.io/not-ready", Operator: "Exists"}, true},
		{"a blanket Exists", kubernetes.Toleration{Operator: "Exists"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evictable := evictablePods([]kubernetes.Pod{pod("apply", "liken-system", tolerating(c.toleration))})

			if moves := len(evictable) == 1; moves != c.moves {
				t.Errorf("evictablePods = %+v, want the pod to move = %v", evictable, c.moves)
			}
		})
	}
}

// drainNode builds a Node with the given cordon state. since is the
// draining-since annotation, or "" for none.
func drainNode(unschedulable bool, cordonedByUs bool, since string) *nodeObject {
	n := &nodeObject{}
	n.Metadata.Name = "node-4"
	n.Spec.Unschedulable = unschedulable
	n.Metadata.Annotations = map[string]string{}
	if cordonedByUs {
		n.Metadata.Annotations[cordonedAnnotation] = "true"
	}
	if since != "" {
		n.Metadata.Annotations[drainingSinceAnnotation] = since
	}
	return n
}

func TestDrainBeginsByCordoning(t *testing.T) {
	step := decideDrainStep(drainNode(false, false, ""), []kubernetes.Pod{pod("web", "default")}, drainNow)
	if step.patch == nil {
		t.Fatal("the step has no patch, want a patch that cordons the uncordoned node")
	}
	if !strings.Contains(string(step.patch), `"unschedulable":true`) {
		t.Errorf("the patch is %s, want it to set unschedulable to true", step.patch)
	}
	if !strings.Contains(string(step.patch), cordonedAnnotation) {
		t.Errorf("the patch is %s, want it to set the cordoned annotation", step.patch)
	}
	if step.clear {
		t.Error("the step is clear, want it not clear while the pod web remains")
	}
	if len(step.evict) != 1 {
		t.Errorf("the step evicts %+v, want the pod web evicted on this pass", step.evict)
	}
}

func TestDrainRespectsAHumanCordon(t *testing.T) {
	// A person cordoned the node before the drain started. The drain
	// records draining-since but not the cordoned annotation, so it
	// does not uncordon the node later.
	step := decideDrainStep(drainNode(true, false, ""), nil, drainNow)
	if step.patch == nil || strings.Contains(string(step.patch), cordonedAnnotation) {
		t.Errorf("the patch is %s, want a patch with draining-since and no cordoned annotation, because a person cordoned the node", step.patch)
	}
}

func TestDrainIsClearWhenNothingEvictableRemains(t *testing.T) {
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since),
		[]kubernetes.Pod{pod("liken-operator", "liken-system", ownedByDaemonSet)}, drainNow)
	if step.patch != nil {
		t.Errorf("the patch is %s, want none on a node that is cordoned and has draining-since", step.patch)
	}
	if !step.clear {
		t.Error("the step is not clear, want clear because only a DaemonSet pod remains")
	}
}

func TestDrainForcesThroughAfterTheDeadline(t *testing.T) {
	since := drainNow.Add(-10 * time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since), []kubernetes.Pod{pod("stubborn", "default")}, drainNow)
	if !step.clear {
		t.Error("the step is not clear, want clear after the drain deadline")
	}
	if len(step.evict) != 0 {
		t.Errorf("the step evicts %+v, want no eviction after the drain deadline", step.evict)
	}
}

func TestDrainKeepsEvictingBeforeTheDeadline(t *testing.T) {
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since), []kubernetes.Pod{pod("web", "default")}, drainNow)
	if step.clear {
		t.Error("the step is clear, want it not clear while the pod web remains")
	}
	if len(step.evict) != 1 {
		t.Errorf("the step evicts %+v, want the pod web", step.evict)
	}
}

// draining runs a step on a node that the operator cordoned one
// minute ago.
func draining(pods ...kubernetes.Pod) drainStep {
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	return decideDrainStep(drainNode(true, true, since), pods, drainNow)
}

func TestDrainHoldsADriverUntilItsClaimHoldersLeave(t *testing.T) {
	step := draining(
		pod("display-operator", "displays", servesDRA),
		pod("movie-lg", "media", holdsAClaim),
		pod("web", "default"))
	if evicted(step) != "movie-lg web" {
		t.Errorf("the step evicts %q, want the driver display-operator to wait for the claim holder", evicted(step))
	}
	if step.remaining != 3 {
		t.Errorf("remaining = %d, want 3", step.remaining)
	}
	if step.clear {
		t.Error("the step is clear, want it not clear while 3 pods remain")
	}
}

func TestDrainEvictsADriverOnceNoClaimHolderRemains(t *testing.T) {
	step := draining(
		pod("display-operator", "displays", servesDRA),
		pod("web", "default"))
	if evicted(step) != "web display-operator" {
		t.Errorf("the step evicts %q, want the driver after the other pods because no pod holds a claim", evicted(step))
	}
}

func TestDrainWaitsForATerminatingPodWithoutEvictingAgain(t *testing.T) {
	step := draining(pod("web", "default", terminating), pod("api", "default"))
	if evicted(step) != "api" {
		t.Errorf("the step evicts %q, want only api, because web is already terminating", evicted(step))
	}
	if step.remaining != 2 {
		t.Errorf("remaining = %d, want 2, because a terminating pod stays in the count", step.remaining)
	}
}

func TestDrainHoldsADriverWhileItsClaimHolderTerminates(t *testing.T) {
	// The kubelet unprepares the claim while the claim holder
	// terminates, so the driver stays until that pod leaves the listing.
	step := draining(
		pod("display-operator", "displays", servesDRA),
		pod("movie-lg", "media", holdsAClaim, terminating))
	if evicted(step) != "" {
		t.Errorf("the step evicts %q, want none, because the driver waits for the terminating claim holder", evicted(step))
	}
	if step.clear {
		t.Error("the step is clear, want it not clear while 2 pods remain")
	}
}

// The kubelet reports a terminal phase only after it unprepares the
// pod's claims, so a completed claim user does not hold back the
// driver.
func TestDrainEvictsADriverOnceItsClaimHolderCompleted(t *testing.T) {
	step := draining(
		pod("display-operator", "displays", servesDRA),
		pod("movie-lg", "media", holdsAClaim, terminating, completed))
	if evicted(step) != "display-operator" {
		t.Errorf("the step evicts %q, want display-operator, because the completed claim holder's claim is unprepared", evicted(step))
	}
}

func TestDrainWaitsForATerminatingDriverWithoutEvictingAgain(t *testing.T) {
	step := draining(pod("display-operator", "displays", servesDRA, terminating), pod("web", "default"))
	if evicted(step) != "web" {
		t.Errorf("the step evicts %q, want only web, because the driver is already terminating", evicted(step))
	}
	if step.remaining != 2 {
		t.Errorf("remaining = %d, want 2, because a terminating driver stays in the count", step.remaining)
	}
}

func TestDrainEvictsADriverThatHoldsItsOwnClaimWithTheDrivers(t *testing.T) {
	step := draining(pod("display-operator", "displays", servesDRA, holdsAClaim))
	if evicted(step) != "display-operator" {
		t.Errorf("the step evicts %q, want the driver that holds a claim to be evicted with the drivers", evicted(step))
	}
}

func TestDRADriverPodsAreThoseMountingTheKubeletsPluginDirectories(t *testing.T) {
	if !servesDRAPlugin(pod("display-operator", "displays", servesDRA)) {
		t.Error("servesDRAPlugin is false, want true for a pod that mounts the plugin registry directory")
	}
	plugin := pod("audio-operator", "audio")
	plugin.Spec.Volumes = []kubernetes.PodVolume{{Name: "socket",
		HostPath: &kubernetes.HostPathVolume{Path: "/var/lib/kubelet/plugins/audio.liken.sh"}}}
	if !servesDRAPlugin(plugin) {
		t.Error("servesDRAPlugin is false, want true for a pod that mounts a directory under the plugins directory")
	}
	logs := pod("log-reader", "default")
	logs.Spec.Volumes = []kubernetes.PodVolume{
		{Name: "logs", HostPath: &kubernetes.HostPathVolume{Path: "/var/log"}},
		{Name: "scratch"},
	}
	if servesDRAPlugin(logs) {
		t.Error("servesDRAPlugin is true, want false for a pod that mounts only /var/log")
	}
	if servesDRAPlugin(pod("web", "default")) {
		t.Error("servesDRAPlugin is true, want false for a pod with no volumes")
	}
}

func TestUncordonOnlyTakesBackOurOwnCordon(t *testing.T) {
	if !decideUncordon(drainNode(true, true, "")) {
		t.Error("decideUncordon is false, want true for a cordon that the operator applied")
	}
	if decideUncordon(drainNode(true, false, "")) {
		t.Error("decideUncordon is true, want false for a cordon that a person applied")
	}
	if decideUncordon(drainNode(false, false, "")) {
		t.Error("decideUncordon is true, want false for a node that is schedulable")
	}
}

func TestUncordonMessageReportsWhetherTheMachineRebooted(t *testing.T) {
	since := drainNow.Format(time.RFC3339)
	after, before := drainNow.Add(time.Minute), drainNow.Add(-time.Hour)
	cases := []struct {
		name string
		node *nodeObject
		boot *time.Time
		want string
	}{
		{"a boot after the drain", drainNode(true, true, since), &after,
			"Uncordoned the Node node-4 after its reboot"},
		{"the boot the drain began on", drainNode(true, true, since), &before,
			"Uncordoned the Node node-4 without a reboot, because no request waits for one"},
		{"a drain with no recorded start", drainNode(true, true, ""), &before,
			"Uncordoned the Node node-4 after its reboot"},
		{"no boot record", drainNode(true, true, since), nil,
			"Uncordoned the Node node-4 after its reboot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uncordonMessage(c.node, c.boot); got != c.want {
				t.Errorf("uncordonMessage = %q, want %q", got, c.want)
			}
		})
	}
}

// drainAPI is a fake API server that records pod listings, evictions,
// and Node patches. It can fail the listing or the patch, which are
// the read and the write that a drain step depends on.
type drainAPI struct {
	pods      []kubernetes.Pod
	listFail  bool
	patchFail bool
	evictions []string
	patched   string
}

func (fake *drainAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			if fake.listFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": fake.pods})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/eviction"):
			fake.evictions = append(fake.evictions, r.URL.Path)
		case r.Method == http.MethodPatch:
			if fake.patchFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			body := make([]byte, 4096)
			n, _ := r.Body.Read(body)
			fake.patched = string(body[:n])
		}
	})
}

// rebootingConvergence is a convergence that requests a reboot, which
// the drain holds back until the node is clear.
func rebootingConvergence() convergence {
	return convergence{
		condition:     notConverged("SpecConverged", "RebootRequested", "reboot requested to apply the staged spec"),
		requestReboot: true,
	}
}

func TestGateThroughDrainHoldsWhenPodsCannotBeListed(t *testing.T) {
	fake := &drainAPI{listFail: true}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	conv := gateThroughDrain(&reader{client: client}, drainNode(true, true, since), rebootingConvergence(), drainNow, machineEvents{}, nil)
	if conv.requestReboot {
		t.Error("the reboot is requested, want it held while the node's pods cannot be listed")
	}
	if conv.condition.Reason != "Draining" {
		t.Errorf("the condition is %+v, want the reason Draining", conv.condition)
	}
}

// A drain holds the reboot whenever it cannot list the pods of a live
// Node: the server refuses the connection, responds slower than the
// client's timeout, or returns an error status. A slow API server,
// such as one whose etcd has lost quorum, must not make the machine
// reboot and stop its pods in spite of their disruption budgets.
func TestGateThroughDrainHoldsWhateverStopsThePodList(t *testing.T) {
	cases := []struct {
		name   string
		client func(*testing.T) *apiclient.Client
	}{
		{"a server that refuses the dial", func(t *testing.T) *apiclient.Client {
			closed := apiservertest.Start(t, http.NotFoundHandler())
			closed.SetDown(true)
			return apiclient.New(apiservertest.Host, closed.Client(), "")
		}},
		// This client has the 15-second timeout of the in-cluster
		// client (requestTimeout in liken/kubernetes/apiclient.go).
		{"a server slower than the timeout", func(t *testing.T) *apiclient.Client {
			slow := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
			return apiclient.New(apiservertest.Host, &http.Client{Transport: slow.Client().Transport, Timeout: 15 * time.Second}, "")
		}},
		{"a server that answers an error", func(t *testing.T) *apiclient.Client {
			return testClient(t, (&drainAPI{listFail: true}).handler())
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conv := gateThroughDrain(&reader{client: c.client(t)}, drainNode(true, true, drainNow.Format(time.RFC3339)), rebootingConvergence(), drainNow, machineEvents{}, nil)
				if conv.requestReboot || conv.condition.Reason != "Draining" {
					t.Errorf("the condition is %+v, want the reboot held with the reason Draining", conv.condition)
				}
			})
		})
	}
}

func TestGateThroughDrainHoldsWhenTheCordonFails(t *testing.T) {
	fake := &drainAPI{patchFail: true}
	client := testClient(t, fake.handler())
	conv := gateThroughDrain(&reader{client: client}, drainNode(false, false, ""), rebootingConvergence(), drainNow, machineEvents{}, nil)
	if conv.requestReboot {
		t.Error("the reboot is requested, want it held because the cordon failed and new pods can still land on the node")
	}
	if conv.condition.Reason != "Draining" {
		t.Errorf("the condition is %+v, want the reason Draining", conv.condition)
	}
}

func TestGateThroughDrainEvictsAndHolds(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("web", "default")}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	conv := gateThroughDrain(&reader{client: client}, drainNode(true, true, since), rebootingConvergence(), drainNow, machineEvents{}, nil)
	if conv.requestReboot {
		t.Error("the reboot is requested, want it held while the pod web remains")
	}
	want := "/api/v1/namespaces/default/pods/web/eviction"
	if len(fake.evictions) != 1 || fake.evictions[0] != want {
		t.Errorf("evictions = %v, want [%s]", fake.evictions, want)
	}
	if conv.condition.Reason != "Draining" || !strings.Contains(conv.condition.Message, "Pods left to evict: 1") {
		t.Errorf("the condition is %+v, want the reason Draining and a message with 1 pod left", conv.condition)
	}
}

func TestGateThroughDrainEvictsTheClaimHolderAndNotYetTheDriver(t *testing.T) {
	// If the driver were evicted first, the kubelet's unprepare call
	// for the claim holder would fail, and that pod would never finish
	// terminating.
	fake := &drainAPI{pods: []kubernetes.Pod{
		pod("display-operator", "displays", servesDRA),
		pod("movie-lg", "media", holdsAClaim),
	}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	conv := gateThroughDrain(&reader{client: client}, drainNode(true, true, since), rebootingConvergence(), drainNow, machineEvents{}, nil)
	want := "/api/v1/namespaces/media/pods/movie-lg/eviction"
	if len(fake.evictions) != 1 || fake.evictions[0] != want {
		t.Errorf("evictions = %v, want only [%s]", fake.evictions, want)
	}
	if conv.requestReboot {
		t.Error("the reboot is requested, want it held while 2 pods remain")
	}
	if !strings.Contains(conv.condition.Message, "Pods left to evict: 2") {
		t.Errorf("the condition is %+v, want a message with 2 pods left, counting the driver that waits", conv.condition)
	}
}

func TestGateThroughDrainEvictsTheDriverOnceTheClaimHolderIsGone(t *testing.T) {
	// This is the pass after the claim holder finished terminating and
	// left this node's pod listing.
	fake := &drainAPI{pods: []kubernetes.Pod{pod("display-operator", "displays", servesDRA)}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	gateThroughDrain(&reader{client: client}, drainNode(true, true, since), rebootingConvergence(), drainNow, machineEvents{}, nil)
	want := "/api/v1/namespaces/displays/pods/display-operator/eviction"
	if len(fake.evictions) != 1 || fake.evictions[0] != want {
		t.Errorf("evictions = %v, want [%s]", fake.evictions, want)
	}
}

func TestGateThroughDrainCordonsAnEmptyNodeAndReleases(t *testing.T) {
	fake := &drainAPI{}
	client := testClient(t, fake.handler())
	conv := gateThroughDrain(&reader{client: client}, drainNode(false, false, ""), rebootingConvergence(), drainNow, machineEvents{}, nil)
	if !strings.Contains(fake.patched, `"unschedulable":true`) {
		t.Errorf("the patch is %s, want it to set unschedulable to true", fake.patched)
	}
	if !conv.requestReboot {
		t.Error("the reboot is held, want it requested because the cordoned node has no pods to move")
	}
}

func TestGateThroughDrainReleasesAClearNode(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("liken-operator", "liken-system", ownedByDaemonSet)}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	conv := gateThroughDrain(&reader{client: client}, drainNode(true, true, since), rebootingConvergence(), drainNow, machineEvents{}, nil)
	if !conv.requestReboot {
		t.Error("the reboot is held, want it requested because only a DaemonSet pod remains")
	}
	if conv.condition.Reason != "RebootRequested" {
		t.Errorf("the condition is %+v, want the reason RebootRequested from the convergence", conv.condition)
	}
}

func TestListPodsOnNodeFiltersByNodeName(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("web", "default")}}
	client := testClient(t, fake.handler())
	pods, err := kubernetes.ListPodsOnNode(client, "node-4")
	if err != nil || len(pods) != 1 {
		t.Fatalf("ListPodsOnNode = %v, %v, want 1 pod and no error", pods, err)
	}
}

func TestEvictPodPostsTheEvictionSubresource(t *testing.T) {
	fake := &drainAPI{}
	client := testClient(t, fake.handler())
	if err := kubernetes.EvictPod(client, pod("web", "default")); err != nil {
		t.Fatalf("evicting the pod web: %v", err)
	}
	want := "/api/v1/namespaces/default/pods/web/eviction"
	if len(fake.evictions) != 1 || fake.evictions[0] != want {
		t.Errorf("evictions = %v, want [%s]", fake.evictions, want)
	}
}
