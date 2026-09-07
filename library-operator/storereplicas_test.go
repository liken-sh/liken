package main

// What these tests read: the durable copies of a namespace's two
// stores. They read the names each copy takes, the shape of a copy
// beside the shape of the first one, the anti-affinity that keeps two
// copies of one store off one node, and what a pass takes down when a
// Catalog asks for fewer copies.

import (
	"net/http"
	"strings"
	"testing"
)

// Every copy of a store carries its own number, copy zero included, so
// one rule names the pod and the claim of every copy.
func TestEveryCopyOfAStoreCarriesItsNumber(t *testing.T) {
	catalog := housekeepingCatalog()

	cases := []struct {
		name  string
		store durableStore
		index int
		want  string
	}{
		{name: "the first catalog copy", store: catalogStoreOf(catalog), want: "house-catalog-catalog-0"},
		{name: "a third catalog copy", store: catalogStoreOf(catalog), index: 2, want: "house-catalog-catalog-2"},
		{name: "the first progress copy", store: progressStoreOf(catalog), want: "house-catalog-progress-0"},
		{name: "a third progress copy", store: progressStoreOf(catalog), index: 2, want: "house-catalog-progress-2"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := one.store.replicaName(one.index); got != one.want {
				t.Errorf("name = %q, want %q", got, one.want)
			}
		})
	}
}

// Every durable pod names its store, and every durable pod refuses a
// node that already holds a copy of that store. The rule is required
// and not preferred, so a third copy on a two-node cluster stays
// Pending and the status says so.
func TestEveryDurableCopyKeepsTwoCopiesOfOneStoreApart(t *testing.T) {
	catalog := housekeepingCatalog()

	cases := []struct {
		name string
		pod  *Pod
		want string
	}{
		{name: "the first catalog copy", pod: testCatalogPod(catalog, 0), want: catalogStoreLabelValue},
		{name: "a later catalog copy", pod: testCatalogPod(catalog, 2), want: catalogStoreLabelValue},
		{name: "the first progress copy", pod: testProgressPod(catalog, 0), want: progressStoreLabelValue},
		{name: "a later progress copy", pod: testProgressPod(catalog, 1), want: progressStoreLabelValue},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := one.pod.Metadata.Labels[storeLabelKey]; got != one.want {
				t.Errorf("labels = %v, want the store %q", one.pod.Metadata.Labels, one.want)
			}
			affinity := one.pod.Spec.Affinity
			if affinity == nil || affinity.PodAntiAffinity == nil {
				t.Fatalf("affinity = %+v, want the store's anti-affinity", affinity)
			}
			terms := affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
			if len(terms) != 1 {
				t.Fatalf("terms = %+v, want the one term on the store label", terms)
			}
			if terms[0].TopologyKey != hostnameTopologyKey {
				t.Errorf("topologyKey = %q, want %q", terms[0].TopologyKey, hostnameTopologyKey)
			}
			selector := terms[0].LabelSelector
			if selector == nil || selector.MatchLabels[storeLabelKey] != one.want {
				t.Errorf("selector = %+v, want the store label %q", selector, one.want)
			}
		})
	}
}

// The first copy of each store carries the role beside its agent, and
// every copy after it is the agent alone: one namespace has one
// reporter and one recorder, and the copies are there to hold the rows.
func TestOnlyTheFirstCopyOfAStoreRunsTheRoleBesideItsAgent(t *testing.T) {
	catalog := housekeepingCatalog()

	cases := []struct {
		name       string
		pod        *Pod
		sidecars   string
		containers string
	}{
		{
			name: "the first catalog copy", pod: testCatalogPod(catalog, 0),
			sidecars: catalogContainer, containers: reporterContainer,
		},
		{name: "a later catalog copy", pod: testCatalogPod(catalog, 1), containers: catalogContainer},
		{
			name: "the first progress copy", pod: testProgressPod(catalog, 0),
			sidecars: progressContainer, containers: recorderContainer,
		},
		{name: "a later progress copy", pod: testProgressPod(catalog, 1), containers: progressContainer},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := containerNames(one.pod.Spec.InitContainers); got != one.sidecars {
				t.Errorf("initContainers = %q, want %q", got, one.sidecars)
			}
			if got := containerNames(one.pod.Spec.Containers); got != one.containers {
				t.Fatalf("containers = %q, want %q", got, one.containers)
			}
			if policy := one.pod.Spec.Containers[0].RestartPolicy; policy != "" {
				t.Errorf("restartPolicy = %q, want none: only an initContainer takes one", policy)
			}
		})
	}
}

// The names of a list of containers as one string, so a test states the
// shape of a pod as the names a person reads in kubectl logs.
func containerNames(containers []Container) string {
	names := []string{}
	for _, container := range containers {
		names = append(names, container.Name)
	}
	return strings.Join(names, " ")
}

// A Catalog that asks for fewer copies loses the pods above the count it
// asks for. The first copy stays, and so does the claim every copy
// mounts.
func TestScalingDownTakesEveryCopyAboveTheCountAsked(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	standingCatalogCopies(cluster, catalog, 3)

	if err := testOperator(t, cluster).sweepStoreReplicas(t.Context(), catalog, catalogStoreOf(catalog), 1); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("house-catalog-catalog-0") == nil {
		t.Error("the sweep took the first copy, which the Catalog still asks for")
	}
	for _, name := range []string{"house-catalog-catalog-1", "house-catalog-catalog-2"} {
		if cluster.heldPod(name) != nil {
			t.Errorf("the pod %s stands, want it taken down", name)
		}
	}
	if cluster.heldClaim("house-catalog-catalog") == nil {
		t.Error("the sweep took the claim the copies that remain mount")
	}
	if got := cluster.countRequests(http.MethodDelete, "persistentvolumeclaims"); got != 0 {
		t.Errorf("claim deletes = %d, want none: one claim serves every copy", got)
	}
}

// A Catalog that asks for the copies it already stands takes nothing
// down, so a settled namespace is quiet.
func TestASettledStoreTakesNothingDown(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	standingCatalogCopies(cluster, catalog, 2)

	if err := testOperator(t, cluster).sweepStoreReplicas(t.Context(), catalog, catalogStoreOf(catalog), 2); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodDelete, "pods"); got != 0 {
		t.Errorf("deletes = %d, want none", got)
	}
}

// The store label is the guard on the delete: a pod that takes a copy's
// name and carries no store label is another writer's, and the sweep
// leaves it where it is.
func TestTheSweepLeavesWhatCarriesNoStoreLabel(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	cluster.pods["house-catalog-catalog-1"] = &Pod{
		Metadata: ObjectMeta{Name: "house-catalog-catalog-1", Namespace: "house"},
	}

	if err := testOperator(t, cluster).sweepStoreReplicas(t.Context(), catalog, catalogStoreOf(catalog), 1); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("house-catalog-catalog-1") == nil {
		t.Error("the sweep took a pod that carries no store label")
	}
}

// A read the server refuses ends the sweep, so the pass reports the
// failure rather than reading an absent copy as one that is gone.
func TestTheSweepReportsAReadTheServerRefuses(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	cluster.broken["/api/v1/namespaces/house/pods/house-catalog-catalog-1"] = http.StatusInternalServerError

	err := testOperator(t, cluster).sweepStoreReplicas(t.Context(), catalog, catalogStoreOf(catalog), 1)

	if err == nil {
		t.Fatal("err = nil, want the failure the sweep could not read past")
	}
}

// standingCatalogCopies puts the pods of one catalog store and the claim
// they mount into the cluster, so a test scales down from a store that
// exists.
func standingCatalogCopies(cluster *fakeCluster, catalog *NamespaceCatalog, copies int) {
	claim := buildCatalogPodClaim(catalog)
	cluster.claims[claim.Metadata.Name] = claim
	for index := range copies {
		pod := testCatalogPod(catalog, index)
		// The stamp is what a pass compares against, so a copy without
		// one would read as stale and be replaced on the pass that read
		// it rather than left where it stands.
		if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
			panic(err)
		}
		cluster.pods[pod.Metadata.Name] = pod
	}
}

// The message a Catalog carries while it asks for copies a class cannot
// hold names that class, the store, and the count it asked for. A
// Catalog that names no class is told the cluster's default class.
func TestTheBlockedMessageNamesTheClassAndTheCount(t *testing.T) {
	cases := []struct {
		name  string
		class string
		want  string
	}{
		{name: "a class the Catalog names", class: "local-path", want: `the class "local-path"`},
		{name: "no class at all", want: "the cluster's default class"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog := housekeepingCatalog()
			catalog.Spec.Storage.StorageClassName = one.class

			message := blockedCopiesMessage(catalogStoreOf(catalog), 3)

			if !strings.Contains(message, one.want) {
				t.Errorf("message = %q, want it to name %s", message, one.want)
			}
			if !strings.Contains(message, "house-catalog-catalog") || !strings.Contains(message, "3") {
				t.Errorf("message = %q, want the store and the count it asked for", message)
			}
		})
	}
}

// Both stores are read, so a Catalog whose progress store alone is on a
// class that is not per-node reports the progress store.
func TestTheProgressStoreBlocksOnItsOwnClass(t *testing.T) {
	cluster := newFakeCluster()
	seedStorageClass(cluster, "per-node", perNodeProvisioner)
	seedStorageClass(cluster, "local-path", "rancher.io/local-path")
	catalog := housekeepingCatalog()
	catalog.Spec.Storage.StorageClassName = "per-node"
	catalog.Spec.Storage.Replicas = 2
	catalog.Spec.Progress.StorageClassName = "local-path"
	catalog.Spec.Progress.Replicas = 2

	reason, message, err := testOperator(t, cluster).blockedStore(t.Context(), catalog)
	if err != nil {
		t.Fatal(err)
	}

	if reason != catalogReasonClassNotPerNode || !strings.Contains(message, "house-catalog-progress") {
		t.Errorf("reason = %q, message = %q, want the progress store blocked", reason, message)
	}
}

// A class the API server refuses to answer is the failure the pass
// reports, from the verdict as from the count.
func TestTheCopiesReportAClassTheServerRefuses(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	catalog.Spec.Storage.StorageClassName = "per-node"
	catalog.Spec.Storage.Replicas = 2
	cluster.broken[storageClassesPath+"/per-node"] = http.StatusInternalServerError

	_, _, err := testOperator(t, cluster).blockedStore(t.Context(), catalog)

	if err == nil {
		t.Fatal("err = nil, want the failure the pass could not read past")
	}
}

// A delete the server refuses ends the sweep, so the pass reports the
// failure and the next pass takes the copy down.
func TestTheSweepReportsADeleteTheServerRefuses(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	standingCatalogCopies(cluster, catalog, 2)
	cluster.broken["DELETE /api/v1/namespaces/house/pods/house-catalog-catalog-1"] =
		http.StatusInternalServerError

	err := testOperator(t, cluster).sweepStoreReplicas(t.Context(), catalog, catalogStoreOf(catalog), 1)

	if err == nil {
		t.Fatal("err = nil, want the refusal the sweep could not read past")
	}
}
