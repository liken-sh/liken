package main

// What these tests read: the heal of a durable copy stranded on a node
// that is gone. A copy's claim binds to the machine the copy first
// landed on, so a copy on a machine that stays NotReady is deleted with
// its claim, and the reconcile stands both again elsewhere.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// A node with its Ready verdict as the API server last wrote it.
func nodeAt(name, verdict string, since time.Duration) *Node {
	return &Node{
		Metadata: ObjectMeta{Name: name},
		Status: NodeStatus{Conditions: []NodeCondition{{
			Type:               nodeConditionReady,
			Status:             verdict,
			LastTransitionTime: testNow.Add(-since),
		}}},
	}
}

// One durable copy of the catalog, standing on a node, with the claim
// under it, as a pass reads them.
func copyOnNode(cluster *fakeCluster, catalog *NamespaceCatalog, index int, node string) *Pod {
	pod := testCatalogPod(catalog, index)
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		panic(err)
	}
	pod.Spec.NodeName = node
	pod.Status = PodStatus{Phase: podRunning, PodIP: "10.42.4.4"}
	cluster.pods[pod.Metadata.Name] = pod
	claim := buildCatalogPodClaim(catalog, index)
	cluster.claims[claim.Metadata.Name] = claim
	return pod
}

// A copy whose node has been NotReady past the grace loses its pod and
// its claim, and the pass stands both again.
func TestAStrandedCopyLosesItsPodAndItsClaim(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	catalog.Spec.Storage.Replicas = 2
	stranded := copyOnNode(cluster, catalog, 1, "nuc-2")
	cluster.nodes["nuc-2"] = nodeAt("nuc-2", "False", 11*time.Minute)

	testOperator(t, cluster).reconcileCatalogs(t.Context(),
		oneNamespace("house", catalog), []Pod{*stranded}, testNow)

	if got := cluster.countRequests(http.MethodDelete, "pods"); got != 1 {
		t.Errorf("pod deletes = %d, want the one stranded copy", got)
	}
	if got := cluster.forcedDeletes; len(got) != 1 || !strings.HasSuffix(got[0], "/house-catalog-catalog-1") {
		t.Errorf("forced deletes = %v, want the stranded copy with no grace period", got)
	}
	if got := cluster.countRequests(http.MethodDelete, "persistentvolumeclaims"); got != 1 {
		t.Errorf("claim deletes = %d, want the stranded copy's claim", got)
	}
	stood := cluster.heldPod("house-catalog-catalog-1")
	if stood == nil {
		t.Fatal("the pass stood no copy in place of the stranded one")
	}
	if stood.Spec.NodeName != "" {
		t.Errorf("nodeName = %q, want a fresh copy the scheduler places", stood.Spec.NodeName)
	}
	if cluster.heldClaim("house-catalog-catalog-1") == nil {
		t.Error("the pass provisioned no claim for the copy it stood")
	}
}

// A node that answers, a node that has been quiet for less than the
// grace, a copy with no node yet, and a pod that is no copy of a store
// are all left where they are.
func TestTheHealLeavesEveryCopyItHasNoVerdictOn(t *testing.T) {
	catalog := housekeepingCatalog()

	cases := []struct {
		name    string
		verdict string
		since   time.Duration
		node    string
		labels  map[string]string
	}{
		{name: "a node that answers", verdict: "True", since: time.Hour, node: "nuc-2"},
		{name: "a node quiet for five minutes", verdict: "False", since: 5 * time.Minute, node: "nuc-2"},
		{name: "a copy with no node yet", verdict: "False", since: time.Hour},
		{
			name: "a pod that is no copy of a store", verdict: "False", since: time.Hour, node: "nuc-2",
			labels: map[string]string{scannerLabelKey: workerLabelValue},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			pod := copyOnNode(cluster, catalog, 1, one.node)
			if one.labels != nil {
				pod.Metadata.Labels = one.labels
			}
			cluster.nodes["nuc-2"] = nodeAt("nuc-2", one.verdict, one.since)
			operator := testOperator(t, cluster)

			operator.healStrandedStoreReplicas(t.Context(), []Pod{*pod},
				[]Node{*cluster.nodes["nuc-2"]}, testNow)

			if got := cluster.countRequests(http.MethodDelete, "pods"); got != 0 {
				t.Errorf("pod deletes = %d, want none", got)
			}
			if cluster.heldClaim("house-catalog-catalog-1") == nil {
				t.Error("the heal took the claim of a copy it must leave alone")
			}
		})
	}
}

// A node the API server has written no Ready verdict for is no verdict
// at all, so the copies on it stay where they are.
func TestANodeWithNoVerdictStrandsNothing(t *testing.T) {
	silent := &Node{Metadata: ObjectMeta{Name: "nuc-2"}}
	timeless := &Node{
		Metadata: ObjectMeta{Name: "nuc-3"},
		Status:   NodeStatus{Conditions: []NodeCondition{{Type: nodeConditionReady, Status: "Unknown"}}},
	}

	stranded := strandedNodes([]Node{*silent, *timeless}, testNow)

	if len(stranded) != 0 {
		t.Errorf("stranded = %v, want none", stranded)
	}
}

// A node that has said Unknown past the grace is as gone as one that
// has said False, because the kubelet answers neither way.
func TestANodeThatSaysUnknownPastTheGraceIsStranded(t *testing.T) {
	stranded := strandedNodes([]Node{*nodeAt("nuc-2", "Unknown", 11*time.Minute)}, testNow)

	if !stranded["nuc-2"] {
		t.Errorf("stranded = %v, want the node that stopped answering", stranded)
	}
}

// The claim a person makes and the Catalog names carries no store
// label, so the heal takes the pod and leaves the claim.
func TestTheHealLeavesAClaimItDidNotProvision(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	pod := copyOnNode(cluster, catalog, 0, "nuc-2")
	cluster.claims["house-catalog-catalog-0"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "house-catalog-catalog-0", Namespace: "house"},
	}

	if err := testOperator(t, cluster).healStoreReplica(t.Context(), pod); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("house-catalog-catalog-0") != nil {
		t.Error("the stranded pod stands, want it taken down")
	}
	if cluster.heldClaim("house-catalog-catalog-0") == nil {
		t.Error("the heal took a claim that carries no store label")
	}
}

// A read the server refuses is reported, so the pass does not read a
// failure as a claim that is already gone.
func TestTheHealReportsAReadTheServerRefuses(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	pod := copyOnNode(cluster, catalog, 1, "nuc-2")
	cluster.broken["GET /api/v1/namespaces/house/persistentvolumeclaims/house-catalog-catalog-1"] =
		http.StatusInternalServerError

	err := testOperator(t, cluster).healStoreReplica(t.Context(), pod)

	if err == nil {
		t.Fatal("err = nil, want the failure the heal could not read past")
	}
}

// A delete the server refuses is reported, and the copies beside it
// still heal, because one stranded copy must not cost the others.
func TestTheHealCarriesOnFromARefusedDelete(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	first := copyOnNode(cluster, catalog, 1, "nuc-2")
	second := copyOnNode(cluster, catalog, 2, "nuc-2")
	cluster.broken["DELETE /api/v1/namespaces/house/pods/house-catalog-catalog-1"] =
		http.StatusInternalServerError
	node := nodeAt("nuc-2", "False", 11*time.Minute)

	testOperator(t, cluster).healStrandedStoreReplicas(t.Context(),
		[]Pod{*first, *second}, []Node{*node}, testNow)

	if cluster.heldPod("house-catalog-catalog-1") == nil {
		t.Error("the copy the server refused to delete is gone")
	}
	if cluster.heldPod("house-catalog-catalog-2") != nil {
		t.Error("the copy beside it stands, want it healed")
	}
}

// A node list the server refuses costs the pass its heal and nothing
// else: the copies stand, and the next pass reads the nodes again.
func TestReconcileCatalogsStandsTheStoresWhenTheNodeListFails(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	cluster.broken[nodesPath] = http.StatusInternalServerError

	testOperator(t, cluster).reconcileCatalogs(t.Context(), oneNamespace("house", catalog), nil, testNow)

	if cluster.heldPod("house-catalog-catalog-0") == nil {
		t.Error("the pass stood no catalog pod after the node list failed")
	}
}

// A copy whose claim is already gone heals to the pod alone, because
// the heal deletes both and either may already have gone.
func TestTheHealTakesAPodWhoseClaimIsAlreadyGone(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	pod := copyOnNode(cluster, catalog, 1, "nuc-2")
	delete(cluster.claims, "house-catalog-catalog-1")

	if err := testOperator(t, cluster).healStoreReplica(t.Context(), pod); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("house-catalog-catalog-1") != nil {
		t.Error("the stranded pod stands, want it taken down")
	}
}
