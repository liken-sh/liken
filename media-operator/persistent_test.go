package main

import (
	"net/http"
	"strings"
	"testing"
)

// seedPersistent adds a claim and a pod to the cluster as an earlier pass
// would have created them, with the hashes of their specs.
func seedPersistent(t *testing.T, cluster *fakeCluster, claim *ResourceClaim, pod *Pod) {
	t.Helper()
	mustSucceed(t, stampTemplateHash(&claim.Metadata, claim.Spec))
	mustSucceed(t, stampTemplateHash(&pod.Metadata, pod.Spec))
	cluster.claims[claim.Metadata.Name] = claim
	cluster.pods[pod.Metadata.Name] = pod
}

// countRequests counts the requests of one method against one resource.
func countRequests(cluster *fakeCluster, method, kind string) int {
	count := 0
	for _, request := range cluster.requests {
		if strings.HasPrefix(request, method) && strings.Contains(request, "/"+kind+"/") {
			count++
		}
	}
	return count
}

// Writing the annotation does not change the hash, and building the same
// Remote again gives the same hash.
func TestTemplateHashIgnoresTheAnnotationItStamps(t *testing.T) {
	remote := remoteWithController()
	claim := buildRemoteClaim(remote)

	before, err := templateHash(claim.Spec)
	mustSucceed(t, err)
	mustSucceed(t, stampTemplateHash(&claim.Metadata, claim.Spec))

	after, err := templateHash(claim.Spec)
	mustSucceed(t, err)
	mustMatch(t, after, before)
	mustMatch(t, claim.Metadata.Annotations[templateHashAnnotation], before)

	again := buildRemoteClaim(remote)
	mustSucceed(t, stampTemplateHash(&again.Metadata, again.Spec))
	mustMatch(t, again.Metadata.Annotations[templateHashAnnotation], before)
}

func TestTemplateHashFollowsThePodSpec(t *testing.T) {
	player := playerWithDisplay()
	claim := buildIdleClaim(player, "display-draw")
	base, err := templateHash(plainIdlePod(player, claim, testBusAddress, testTopicBase, "America/New_York").Spec)
	mustSucceed(t, err)

	cases := []struct {
		name string
		base string
		pod  *Pod
	}{
		{"the idle image", base, buildIdlePod(player, claim, testBusAddress,
			testTopicBase, "America/New_York", resolveIdle(nil, nil, testIdleImage+"-next"), nil)},
		{"the timezone", base, plainIdlePod(player, claim, testBusAddress, testTopicBase, "Europe/Berlin")},
		{"the fade policy", base, buildIdlePod(player, claim, testBusAddress,
			testTopicBase, "America/New_York", resolveIdle(fadeAfter(60), nil, testIdleImage), nil)},
		{"a remote", base, buildIdlePod(player, claim, testBusAddress,
			testTopicBase, "America/New_York", resolveIdle(nil, nil, testIdleImage),
			[]idleRemoteTopics{{Events: remoteEventsTopic(testTopicBase, "house", "sofa")}})},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			hash, err := templateHash(one.pod.Spec)
			mustSucceed(t, err)
			if hash == one.base {
				t.Errorf("the hash is %[2]q after %[1]s changed, want a different hash", one.name, hash)
			}
		})
	}
}

func TestTemplateHashFollowsTheResourceSettings(t *testing.T) {
	player := playerWithDisplay()
	claim := buildIdleClaim(player, "display-draw")
	defaults, err := templateHash(resourceSettings(nil).apply(
		plainIdlePod(player, claim, testBusAddress, testTopicBase, "")).Spec)
	mustSucceed(t, err)

	patched, err := templateHash(customSettings().apply(
		plainIdlePod(player, claim, testBusAddress, testTopicBase, "")).Spec)
	mustSucceed(t, err)

	if patched == defaults {
		t.Errorf("the hash is %q after the settings changed, want a different hash", patched)
	}
}

func TestTemplateHashFollowsTheClaimSelector(t *testing.T) {
	remote := remoteWithController()
	base, err := templateHash(buildRemoteClaim(remote).Spec)
	mustSucceed(t, err)

	remote.Spec.Device.Selector = `device.attributes["bluetooth.liken.sh"].address == "7C:66"`
	edited, err := templateHash(buildRemoteClaim(remote).Spec)
	mustSucceed(t, err)

	if edited == base {
		t.Errorf("the hash is %q after the selector changed, want a different hash", edited)
	}
}

func TestReconcilePersistentKeepsAMatchingPair(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := remoteWithController()
	claim := buildRemoteClaim(remote)
	seedPersistent(t, cluster, claim, media.resources.apply(
		buildRemotePod(remote, claim, media.sidecarImage, media.busAddress, media.topicBase)))

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	mustMatch(t, countRequests(cluster, http.MethodDelete, "pods"), 0)
	mustMatch(t, countRequests(cluster, http.MethodDelete, "resourceclaims"), 0)
	mustMatch(t, countRequests(cluster, http.MethodPost, "namespaces"), 0)
}

// A release that changes only the image deletes the pod and keeps the
// claim, so a sleeping controller keeps its allocation. The next pass
// creates the pod with the new image.
func TestReconcilePersistentReplacesThePodAlone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := remoteWithController()
	claim := buildRemoteClaim(remote)
	seedPersistent(t, cluster, claim,
		buildRemotePod(remote, claim, "registry.example/sidecar:old", media.busAddress, media.topicBase))
	stamped := claim.Metadata.Annotations[templateHashAnnotation]

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	if _, exists := cluster.pods["sofa-remote"]; exists {
		t.Errorf("the pod still exists, want it deleted (requests: %v)", cluster.requests)
	}
	held, exists := cluster.claims["sofa-remote-devices"]
	if !exists {
		t.Fatalf("the claim no longer exists, want it kept (requests: %v)", cluster.requests)
	}
	mustMatch(t, held.Metadata.Annotations[templateHashAnnotation], stamped)
	mustMatch(t, countRequests(cluster, http.MethodDelete, "resourceclaims"), 0)

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	replacement, exists := cluster.pods["sofa-remote"]
	if !exists {
		t.Fatalf("the pod does not exist, want a replacement (requests: %v)", cluster.requests)
	}
	mustMatch(t, replacement.Spec.Containers[0].Image, media.sidecarImage)
}

func TestReconcilePersistentReplacesBothWhenTheClaimDiverges(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := remoteWithController()
	claim := buildRemoteClaim(remote)
	seedPersistent(t, cluster, claim,
		buildRemotePod(remote, claim, media.sidecarImage, media.busAddress, media.topicBase))

	remote.Spec.Device.Selector = `device.attributes["bluetooth.liken.sh"].address == "7C:66"`
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	if _, exists := cluster.pods["sofa-remote"]; exists {
		t.Errorf("the pod still exists, want it deleted with the stale claim (requests: %v)", cluster.requests)
	}
	if _, exists := cluster.claims["sofa-remote-devices"]; exists {
		t.Errorf("the stale claim still exists, want it deleted (requests: %v)", cluster.requests)
	}

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	rebuilt, exists := cluster.claims["sofa-remote-devices"]
	if !exists {
		t.Fatalf("the claim does not exist, want a replacement (requests: %v)", cluster.requests)
	}
	selector := rebuilt.Spec.Devices.Requests[0].Exactly.Selectors[0].CEL.Expression
	mustMatch(t, selector, `device.attributes["bluetooth.liken.sh"].address == "7C:66"`)
}

// An object from an older release has no hash annotation, so the first
// pass replaces it.
func TestReconcilePersistentReplacesAnUnstampedObject(t *testing.T) {
	cases := []struct {
		name       string
		strip      func(claim *ResourceClaim, pod *Pod)
		claimAfter bool
	}{
		{"the claim", func(claim *ResourceClaim, pod *Pod) { claim.Metadata.Annotations = nil }, false},
		{"the pod", func(claim *ResourceClaim, pod *Pod) { pod.Metadata.Annotations = nil }, true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			remote := remoteWithController()
			claim := buildRemoteClaim(remote)
			pod := buildRemotePod(remote, claim, media.sidecarImage, media.busAddress, media.topicBase)
			seedPersistent(t, cluster, claim, pod)
			one.strip(claim, pod)

			mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

			if _, exists := cluster.pods["sofa-remote"]; exists {
				t.Errorf("the pod of the unstamped pair still exists, want it deleted (requests: %v)", cluster.requests)
			}
			_, exists := cluster.claims["sofa-remote-devices"]
			mustMatch(t, exists, one.claimAfter)
		})
	}
}

func TestReconcilePersistentLeavesATerminatingPairAlone(t *testing.T) {
	cases := []struct {
		name string
		mark func(claim *ResourceClaim, pod *Pod)
	}{
		{"the claim", func(claim *ResourceClaim, pod *Pod) { claim.Metadata.DeletionTimestamp = "2026-08-24T12:00:00Z" }},
		{"the pod", func(claim *ResourceClaim, pod *Pod) { pod.Metadata.DeletionTimestamp = "2026-08-24T12:00:00Z" }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			remote := remoteWithController()
			claim := buildRemoteClaim(remote)
			stale := buildRemotePod(remote, claim, "registry.example/sidecar:old", media.busAddress, media.topicBase)
			seedPersistent(t, cluster, claim, stale)
			one.mark(claim, stale)

			mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

			mustMatch(t, countRequests(cluster, http.MethodDelete, "pods"), 0)
			mustMatch(t, countRequests(cluster, http.MethodDelete, "resourceclaims"), 0)
			mustMatch(t, countRequests(cluster, http.MethodPost, "namespaces"), 0)
		})
	}
}

// Editing the Player's display name changes the idle pod's environment,
// so the pod is replaced and the claim is kept.
func TestReconcileIdleReplacesThePodOnAPlayerEdit(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	claim := buildIdleClaim(player, media.idleDisplayClass)
	seedPersistent(t, cluster, claim,
		plainIdlePod(player, claim, media.busAddress, media.topicBase, "America/New_York"))

	player.Spec.DisplayName = "Studio Lab"
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if _, exists := cluster.pods["theater-idle"]; exists {
		t.Errorf("the idle pod still exists, want it deleted (requests: %v)", cluster.requests)
	}
	if _, exists := cluster.claims["theater-idle-devices"]; !exists {
		t.Fatalf("the idle claim no longer exists, want it kept (requests: %v)", cluster.requests)
	}

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	replacement, exists := cluster.pods["theater-idle"]
	if !exists {
		t.Fatalf("the idle pod does not exist, want a replacement (requests: %v)", cluster.requests)
	}
	env := map[string]string{}
	for _, entry := range replacement.Spec.Containers[0].Env {
		env[entry.Name] = entry.Value
	}
	mustMatch(t, env[idlePlayerNameVariable], "Studio Lab")
}

// reservedBy sets status.reservedFor on a claim to the named pods, as
// the scheduler does when a pod is scheduled with the claim.
func reservedBy(claim *ResourceClaim, pods ...string) {
	claim.Status = &ResourceClaimStatus{}
	for _, pod := range pods {
		claim.Status.ReservedFor = append(claim.Status.ReservedFor,
			ClaimConsumer{Resource: "pods", Name: pod, UID: pod + "-uid"})
	}
}

// deleteOrder returns the resource and name of each delete request, in
// the order they were sent.
func deleteOrder(cluster *fakeCluster) []string {
	var sent []string
	for _, request := range cluster.requests {
		if !strings.HasPrefix(request, http.MethodDelete) {
			continue
		}
		parts := strings.Split(request, "/")
		sent = append(sent, parts[len(parts)-2]+"/"+parts[len(parts)-1])
	}
	return sent
}

// Replacing a claim deletes every pod that holds it first, including a
// pod that another operator created.
func TestReconcilePersistentDeletesTheClaimHoldersBeforeTheClaim(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}
	claim := buildIdleClaim(player, media.idleDisplayClass)
	mustSucceed(t, stampTemplateHash(&claim.Metadata, claim.Spec))
	reservedBy(claim, "browser")
	cluster.claims[claim.Metadata.Name] = claim
	cluster.pods["browser"] = &Pod{Metadata: ObjectMeta{Name: "browser", Namespace: "house"}}

	player.Spec.Render = nil
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if _, exists := cluster.pods["browser"]; exists {
		t.Errorf("the delegate's pod still exists, want it deleted before the stale claim (requests: %v)", cluster.requests)
	}
	if _, exists := cluster.claims["theater-idle-devices"]; exists {
		t.Errorf("the stale claim still exists, want it deleted (requests: %v)", cluster.requests)
	}
	mustMatchAll(t, deleteOrder(cluster), []string{"pods/browser", "resourceclaims/theater-idle-devices"})
}

// A holder that the API server no longer has counts as deleted, and the
// claim is deleted next.
func TestReconcilePersistentReadsAnAbsentHolderAsGone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}
	claim := buildIdleClaim(player, media.idleDisplayClass)
	mustSucceed(t, stampTemplateHash(&claim.Metadata, claim.Spec))
	reservedBy(claim, "browser")
	cluster.claims[claim.Metadata.Name] = claim

	player.Spec.Render = nil
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if _, exists := cluster.claims["theater-idle-devices"]; exists {
		t.Errorf("the stale claim still exists, want it deleted (requests: %v)", cluster.requests)
	}
}

// When spec.idle.controller names another operator, the idle client pod
// is deleted although its hash is unchanged, and the claim is kept for
// that operator's pod.
func TestReconcilePersistentDeletesAPodItNoLongerWants(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	claim := buildIdleClaim(player, media.idleDisplayClass)
	seedPersistent(t, cluster, claim, plainIdlePod(player, claim, testBusAddress, testTopicBase, "America/New_York"))

	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if _, exists := cluster.pods["theater-idle"]; exists {
		t.Errorf("the idle client pod still exists, want it deleted (requests: %v)", cluster.requests)
	}
	if _, exists := cluster.claims["theater-idle-devices"]; !exists {
		t.Errorf("the claim no longer exists, want it kept (requests: %v)", cluster.requests)
	}
}
