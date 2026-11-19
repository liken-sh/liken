package main

// What these tests read: an ended pod on a node that is not Ready. A
// store copy there is left to the heal of a stranded copy (storeheal.go),
// which waits out its grace, because a node-local class keeps the copy's
// data on that node. A Jellyfin pod and a screen pod keep no store, so
// they are replaced on any node.

import (
	"net/http"
	"testing"
	"time"
)

// endedOnNode is endedCluster with the ended pod's node in a verdict
// that the test names.
func endedOnNode(pod *Pod, verdict string, since time.Duration) *fakeCluster {
	cluster := endedCluster(pod)
	cluster.nodes[endedNode] = nodeAt(endedNode, verdict, since)
	return cluster
}

func TestAnEndedStoreCopyOnANodeThatIsNotReadyIsLeftToTheHeal(t *testing.T) {
	kinds := map[string]endedPod{}
	for _, one := range endedPods() {
		kinds[one.name] = one
	}
	cases := []struct {
		kind     string
		verdict  string
		replaced bool
	}{
		{kind: "the catalog pod", verdict: "False"},
		{kind: "the progress pod", verdict: "Unknown"},
		{kind: "the catalog pod", verdict: nodeReadyVerdict, replaced: true},
		{kind: "the progress pod", verdict: nodeReadyVerdict, replaced: true},
		{kind: "the Jellyfin pod", verdict: "False", replaced: true},
		{kind: "the screen pod", verdict: "Unknown", replaced: true},
	}
	for _, one := range cases {
		kind := kinds[one.kind]
		t.Run(one.kind+" on a node that is "+one.verdict, func(t *testing.T) {
			operator := testOperator(t, newFakeCluster())
			pod := ended(kind.build(operator), podFailed, testNow.Add(-time.Hour), 0)
			cluster := endedOnNode(pod, one.verdict, 2*time.Minute)

			kind.stand(t, testOperator(t, cluster), testNow)

			if got := cluster.countRequests(http.MethodDelete, "pods") == 1; got != one.replaced {
				t.Errorf("the pass replaced the pod: %v, want %v", got, one.replaced)
			}
		})
	}
}

// The whole pass over a Catalog leaves an ended copy on a NotReady node
// alone inside the grace, and the heal takes it with no grace period
// after.
func TestTheHealTakesAnEndedCopyAfterItsGrace(t *testing.T) {
	cases := []struct {
		name    string
		since   time.Duration
		deletes int
		forced  int
	}{
		{name: "inside the grace", since: 2 * time.Minute},
		{name: "past the grace", since: 11 * time.Minute, deletes: 1, forced: 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := seedCatalog(cluster, "house", "house")
			copied := copyOnNode(cluster, catalog, 0, "nuc-2")
			copied.Status = PodStatus{Phase: podFailed, Reason: "Terminated"}
			cluster.nodes["nuc-2"] = nodeAt("nuc-2", "False", one.since)

			testOperator(t, cluster).reconcileCatalogs(t.Context(),
				oneNamespace("house", catalog), []Pod{*copied}, nil, testNow)

			if got := len(cluster.forcedDeletes); got != one.forced {
				t.Errorf("forced deletes = %v, want %d", cluster.forcedDeletes, one.forced)
			}
			if got := cluster.countRequests(http.MethodDelete, "pods"); got != one.deletes {
				t.Errorf("pod deletes = %d, want %d", got, one.deletes)
			}
		})
	}
}

// A node whose Ready condition has no transition time gives the heal no
// time to measure its grace from, so the heal never takes a copy on it.
// The pass then counts the grace from the end of the copy, and replaces
// the copy after it.
func TestAnEndedCopyOnANodeWithNoVerdictTimeIsReplacedAfterTheGrace(t *testing.T) {
	cases := []struct {
		name     string
		ran      time.Duration
		replaced bool
	}{
		{name: "an end 2 minutes ago", ran: 58 * time.Minute},
		{name: "an end 11 minutes ago", ran: 49 * time.Minute, replaced: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			pod := endedAfter(ended(testCatalogPod(housekeepingCatalog(), 0), podFailed,
				testNow.Add(-time.Hour), 0), one.ran, "a disruption")
			cluster := endedCluster(pod)
			cluster.nodes[endedNode] = &Node{Metadata: ObjectMeta{Name: endedNode}, Status: NodeStatus{
				Conditions: []NodeCondition{{Type: nodeConditionReady, Status: "Unknown"}},
			}}

			if _, err := testOperator(t, cluster).standCatalogPod(t.Context(), housekeepingCatalog(), 0, testNow); err != nil {
				t.Fatal(err)
			}

			if got := cluster.countRequests(http.MethodDelete, "pods") == 1; got != one.replaced {
				t.Errorf("the pass replaced the copy: %v, want %v", got, one.replaced)
			}
		})
	}
}
