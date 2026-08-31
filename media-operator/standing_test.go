package main

// These tests cover the template hash and the rule both standing pairs
// follow: a stale object is deleted and the next pass creates the
// replacement, a claim divergence takes the pod with it, and a pair that
// matches the template is left as it stands.

import (
	"net/http"
	"strings"
	"testing"
)

// seedStanding puts one claim and one pod in the cluster the way a
// previous pass left them, stamped with the hashes of the specs given.
func seedStanding(t *testing.T, cluster *fakeCluster, claim *ResourceClaim, pod *Pod) {
	t.Helper()
	mustSucceed(t, stampTemplateHash(&claim.Metadata, claim.Spec))
	mustSucceed(t, stampTemplateHash(&pod.Metadata, pod.Spec))
	cluster.claims[claim.Metadata.Name] = claim
	cluster.pods[pod.Metadata.Name] = pod
}

// countRequests counts the requests of one method against one kind of
// object, so a test reads what a pass did rather than what it left.
func countRequests(cluster *fakeCluster, method, kind string) int {
	count := 0
	for _, request := range cluster.requests {
		if strings.HasPrefix(request, method) && strings.Contains(request, "/"+kind+"/") {
			count++
		}
	}
	return count
}

// The hash reads the spec alone, so stamping the annotation does not
// change it, and a second build of the same Remote stamps the same value.
func TestTemplateHashIgnoresTheAnnotationItStamps(t *testing.T) {
	remote := standingRemote()
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

// A release that changes the player image, and a household that changes
// the zone the idle clock reads, each change the pod hash, which is what
// rolls the standing pod.
func TestTemplateHashFollowsThePodSpec(t *testing.T) {
	player := standingIdlePlayer()
	claim := buildIdleClaim(player, "display-draw")
	base, err := templateHash(plainIdlePod(player, claim, testImage, testBusAddress, testTopicBase, "America/New_York").Spec)
	mustSucceed(t, err)

	cases := []struct {
		name string
		pod  *Pod
	}{
		{"the operator image", plainIdlePod(player, claim, testImage+"-next", testBusAddress, testTopicBase, "America/New_York")},
		{"the idle image", buildIdlePod(player, claim, testImage, testBusAddress,
			testTopicBase, "America/New_York", resolveIdle(nil, nil, testIdleImage+"-next"), nil)},
		{"the timezone", plainIdlePod(player, claim, testImage, testBusAddress, testTopicBase, "Europe/Berlin")},
		{"the fade policy", buildIdlePod(player, claim, testImage, testBusAddress, testTopicBase,
			"America/New_York", resolveIdle(fadeAfter(60), nil, testIdleImage), nil)},
		{"a remote", buildIdlePod(player, claim, testImage, testBusAddress, testTopicBase,
			"America/New_York", resolveIdle(nil, nil, testIdleImage),
			[]idleRemoteTopics{{Events: remoteEventsTopic(testTopicBase, "house", "sofa")}})},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			hash, err := templateHash(one.pod.Spec)
			mustSucceed(t, err)
			if hash == base {
				t.Errorf("%s changed and the hash stayed %q", one.name, hash)
			}
		})
	}
}

// An edit to the Remote's device selector changes the claim hash, which
// is what replaces the immutable claim and the pod that holds it.
func TestTemplateHashFollowsTheClaimSelector(t *testing.T) {
	remote := standingRemote()
	base, err := templateHash(buildRemoteClaim(remote).Spec)
	mustSucceed(t, err)

	remote.Spec.Device.Selector = `device.attributes["bluetooth.liken.sh"].address == "7C:66"`
	edited, err := templateHash(buildRemoteClaim(remote).Spec)
	mustSucceed(t, err)

	if edited == base {
		t.Errorf("the selector changed and the hash stayed %q", edited)
	}
}

// A pair that matches the template is left as it stands: the pass reads
// both objects and writes nothing.
func TestReconcileStandingKeepsAMatchingPair(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := standingRemote()
	claim := buildRemoteClaim(remote)
	seedStanding(t, cluster, claim, buildRemotePod(remote, claim, media.image, media.busAddress, media.topicBase))

	mustSucceed(t, media.reconcileRemote(remote))

	mustMatch(t, countRequests(cluster, http.MethodDelete, "pods"), 0)
	mustMatch(t, countRequests(cluster, http.MethodDelete, "resourceclaims"), 0)
	mustMatch(t, countRequests(cluster, http.MethodPost, "namespaces"), 0)
}

// An image-only release changes the pod and not the claim. The pass
// deletes the pod alone, so the claim keeps the allocation that holds a
// sleeping controller, and the next pass creates the pod on the new
// image.
func TestReconcileStandingReplacesThePodAlone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := standingRemote()
	claim := buildRemoteClaim(remote)
	seedStanding(t, cluster, claim, buildRemotePod(remote, claim, "registry.example/player:old", media.busAddress, media.topicBase))
	stamped := claim.Metadata.Annotations[templateHashAnnotation]

	mustSucceed(t, media.reconcileRemote(remote))

	if _, stands := cluster.pods["sofa-remote"]; stands {
		t.Errorf("the stale pod stands: %v", cluster.requests)
	}
	held, stands := cluster.claims["sofa-remote-devices"]
	if !stands {
		t.Fatalf("the claim was deleted with the pod: %v", cluster.requests)
	}
	mustMatch(t, held.Metadata.Annotations[templateHashAnnotation], stamped)
	mustMatch(t, countRequests(cluster, http.MethodDelete, "resourceclaims"), 0)

	mustSucceed(t, media.reconcileRemote(remote))

	replacement, stands := cluster.pods["sofa-remote"]
	if !stands {
		t.Fatalf("no replacement pod was created: %v", cluster.requests)
	}
	mustMatch(t, replacement.Spec.Containers[0].Image, media.image)
}

// A claim the Remote reshaped is stale, and so is the pod that holds its
// allocation. The pass deletes both, the pod first, because a claim in
// use stays terminating until every pod that holds it is gone.
func TestReconcileStandingReplacesBothWhenTheClaimDiverges(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := standingRemote()
	claim := buildRemoteClaim(remote)
	seedStanding(t, cluster, claim, buildRemotePod(remote, claim, media.image, media.busAddress, media.topicBase))

	remote.Spec.Device.Selector = `device.attributes["bluetooth.liken.sh"].address == "7C:66"`
	mustSucceed(t, media.reconcileRemote(remote))

	if _, stands := cluster.pods["sofa-remote"]; stands {
		t.Errorf("the pod stands over a stale claim: %v", cluster.requests)
	}
	if _, stands := cluster.claims["sofa-remote-devices"]; stands {
		t.Errorf("the stale claim stands: %v", cluster.requests)
	}

	mustSucceed(t, media.reconcileRemote(remote))

	rebuilt, stands := cluster.claims["sofa-remote-devices"]
	if !stands {
		t.Fatalf("no replacement claim was created: %v", cluster.requests)
	}
	selector := rebuilt.Spec.Devices.Requests[0].Exactly.Selectors[0].CEL.Expression
	mustMatch(t, selector, `device.attributes["bluetooth.liken.sh"].address == "7C:66"`)
}

// An object an older release created carries no stamp, which never
// equals a hash, so the first pass that carries the stamp rolls it once.
// An unstamped claim takes its pod with it; an unstamped pod goes alone.
func TestReconcileStandingRollsAnUnstampedObject(t *testing.T) {
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
			remote := standingRemote()
			claim := buildRemoteClaim(remote)
			pod := buildRemotePod(remote, claim, media.image, media.busAddress, media.topicBase)
			seedStanding(t, cluster, claim, pod)
			one.strip(claim, pod)

			mustSucceed(t, media.reconcileRemote(remote))

			if _, stands := cluster.pods["sofa-remote"]; stands {
				t.Errorf("the unstamped pair kept its pod: %v", cluster.requests)
			}
			_, stands := cluster.claims["sofa-remote-devices"]
			mustMatch(t, stands, one.claimAfter)
		})
	}
}

// A delete this operator sent is in progress while the deletionTimestamp
// stands. The pass leaves the whole pair alone until the delete
// completes, so one divergence causes one delete and not one per pass.
func TestReconcileStandingLeavesATerminatingPairAlone(t *testing.T) {
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
			remote := standingRemote()
			claim := buildRemoteClaim(remote)
			stale := buildRemotePod(remote, claim, "registry.example/player:old", media.busAddress, media.topicBase)
			seedStanding(t, cluster, claim, stale)
			one.mark(claim, stale)

			mustSucceed(t, media.reconcileRemote(remote))

			mustMatch(t, countRequests(cluster, http.MethodDelete, "pods"), 0)
			mustMatch(t, countRequests(cluster, http.MethodDelete, "resourceclaims"), 0)
			mustMatch(t, countRequests(cluster, http.MethodPost, "namespaces"), 0)
		})
	}
}

// The idle pair follows the same rule as the remote pair. An edit to the
// Player's friendly name changes the idle pod's environment, so the pod
// rolls and the claim stays.
func TestReconcileIdleRollsThePodOnAPlayerEdit(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	claim := buildIdleClaim(player, media.idleDisplayClass)
	seedStanding(t, cluster, claim,
		plainIdlePod(player, claim, media.image, media.busAddress, media.topicBase, "America/New_York"))

	player.Spec.DisplayName = "Studio Lab"
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if _, stands := cluster.pods["theater-idle"]; stands {
		t.Errorf("the stale idle pod stands: %v", cluster.requests)
	}
	if _, stands := cluster.claims["theater-idle-devices"]; !stands {
		t.Fatalf("the idle claim was deleted with the pod: %v", cluster.requests)
	}

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	replacement, stands := cluster.pods["theater-idle"]
	if !stands {
		t.Fatalf("no replacement idle pod was created: %v", cluster.requests)
	}
	env := map[string]string{}
	for _, entry := range replacement.Spec.Containers[0].Env {
		env[entry.Name] = entry.Value
	}
	mustMatch(t, env[idlePlayerNameVariable], "Studio Lab")
}
