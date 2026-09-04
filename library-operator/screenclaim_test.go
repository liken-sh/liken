package main

// These tests read the claim a screen's catalog agent runs on, the
// pass that creates it before the pod, and the recovery of a screen the
// scheduler cannot place where its volume is.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The Catalog a screen's claim is built from: the size every agent in
// the namespace takes, and the class the screens block names.
func testCatalogWithScreens(size, class string) *NamespaceCatalog {
	catalog := testNamespaceCatalog()
	catalog.Spec.Storage.Size = size
	catalog.Spec.Screens.StorageClassName = class
	return catalog
}

// The claim is named for the screen pod, ReadWriteOnce, sized from the
// Catalog, classed by the screens block, labeled with the screen and the
// Player, and owned by the Player.
func TestBuildScreenClaimIsOwnedByThePlayerAndSizedByTheCatalog(t *testing.T) {
	claim := buildScreenClaim(denScreen(), testCatalogWithScreens("2Gi", "local-path"))

	if claim.Metadata.Name != "den-tv-media-browser-catalog" {
		t.Errorf("name = %q, want den-tv-media-browser-catalog", claim.Metadata.Name)
	}
	if claim.Metadata.Namespace != testLibraryNamespace {
		t.Errorf("namespace = %q, want %s", claim.Metadata.Namespace, testLibraryNamespace)
	}
	if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteOnce {
		t.Errorf("accessModes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
	}
	if claim.Spec.Resources.Requests["storage"] != "2Gi" {
		t.Errorf("storage = %q, want the Catalog's size", claim.Spec.Resources.Requests["storage"])
	}
	if claim.Spec.StorageClassName != "local-path" {
		t.Errorf("storageClassName = %q, want the screens' class", claim.Spec.StorageClassName)
	}
	if claim.Metadata.Labels[scannerLabelKey] != screenLabelValue {
		t.Errorf("labels = %v, want the screen name label", claim.Metadata.Labels)
	}
	if claim.Metadata.Labels[playerLabelKey] != "den-tv" {
		t.Errorf("labels = %v, want the player label", claim.Metadata.Labels)
	}
	want := []OwnerReference{playerOwner(denScreen())}
	if len(claim.Metadata.OwnerReferences) != 1 || claim.Metadata.OwnerReferences[0] != want[0] {
		t.Errorf("ownerReferences = %+v, want %+v", claim.Metadata.OwnerReferences, want)
	}
}

// An absent class is omitted, so the cluster's default StorageClass
// binds the claim, and an absent size takes the Catalog's default.
func TestBuildScreenClaimOmitsAnAbsentClassAndDefaultsTheSize(t *testing.T) {
	claim := buildScreenClaim(denScreen(), testNamespaceCatalog())

	if claim.Spec.StorageClassName != "" {
		t.Errorf("storageClassName = %q, want it omitted for the default class", claim.Spec.StorageClassName)
	}
	if claim.Spec.Resources.Requests["storage"] != defaultCatalogSize {
		t.Errorf("storage = %q, want the default", claim.Spec.Resources.Requests["storage"])
	}
}

// The pass creates the claim before the pod, and the pod's catalog
// volume names it.
func TestReconcileScreensStandsTheClaimBeforeThePod(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
		[]Player{*player}, nil, nil, testNow)

	claim := cluster.heldClaim("den-tv-media-browser-catalog")
	if claim == nil {
		t.Fatal("the pass provisioned no claim for the screen")
	}
	if claim.Spec.Resources.Requests["storage"] != defaultCatalogSize {
		t.Errorf("storage = %q, want the Catalog's size", claim.Spec.Resources.Requests["storage"])
	}
	pod := cluster.heldPod("den-tv-media-browser")
	if pod == nil {
		t.Fatal("the pass stood no screen pod")
	}
	source := podVolume(t, pod, catalogVolumeName).PersistentVolumeClaim
	if source == nil || source.ClaimName != "den-tv-media-browser-catalog" {
		t.Errorf("catalog volume = %+v, want the screen's claim", source)
	}
	claimed := cluster.firstRequest(http.MethodPost, "persistentvolumeclaims")
	stood := cluster.firstRequest(http.MethodPost, "pods")
	if claimed < 0 || stood < 0 || claimed > stood {
		t.Errorf("the claim was created at request %d and the pod at %d, want the claim first", claimed, stood)
	}
}

// A claim that already stands is left alone, so a screen comes back on
// the catalog it holds.
func TestReconcileScreensLeavesAnExistingScreenClaim(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)
	cluster.claims["den-tv-media-browser-catalog"] = boundScreenClaim(player)

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
		[]Player{*player}, nil, nil, testNow)

	if got := cluster.countRequests(http.MethodPost, "persistentvolumeclaims"); got != 0 {
		t.Errorf("creates = %d, want none over a claim that already stands", got)
	}
}

// A namespace with no single Catalog has no size to read, so the pod
// keeps its emptyDir and the pass provisions nothing.
func TestReconcileScreensWithNoCatalogKeepsTheEmptyDir(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, nil,
		[]Player{*player}, nil, nil, testNow)

	if got := cluster.countRequests(http.MethodPost, "persistentvolumeclaims"); got != 0 {
		t.Errorf("creates = %d, want none in a namespace with no Catalog", got)
	}
	pod := cluster.heldPod("den-tv-media-browser")
	if pod == nil {
		t.Fatal("the pass stood no screen pod")
	}
	if podVolume(t, pod, catalogVolumeName).EmptyDir == nil {
		t.Errorf("catalog volume = %+v, want an emptyDir", podVolume(t, pod, catalogVolumeName))
	}
}

// A create another writer got to first is success, and the next pass
// reads the claim it made.
func TestStandScreenClaimAcceptsAConflict(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true

	err := testOperator(t, cluster).standScreenClaim(t.Context(), denScreen(), testNamespaceCatalog())

	if err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}
}

// A read that fails for any other reason is reported, because the pass
// cannot tell whether the claim exists.
func TestStandScreenClaimReportsAFailedRead(t *testing.T) {
	cluster := newFakeCluster()
	cluster.broken[claimPath(testLibraryNamespace, "den-tv-media-browser-catalog")] = http.StatusInternalServerError

	err := testOperator(t, cluster).standScreenClaim(t.Context(), denScreen(), testNamespaceCatalog())

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// The grace is a variable, so a test drives it in milliseconds.
func shortUnschedulableGrace(t *testing.T) {
	t.Helper()
	held := unschedulableGrace
	unschedulableGrace = time.Millisecond
	t.Cleanup(func() { unschedulableGrace = held })
}

// A screen pod as the scheduler left it, with the PodScheduled
// condition the API server wrote and the time it wrote it.
func unschedulableScreenPod(player *Player, catalog *NamespaceCatalog, since time.Time) *Pod {
	pod := buildScreenPod(player, nil, catalog, testBrowserImage, testCorrosionImage, defaultTopicBase, "")
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		panic(err)
	}
	pod.Status = PodStatus{
		Phase: podPending,
		Conditions: []PodCondition{{
			Type:               podScheduled,
			Status:             conditionIsFalse,
			Reason:             "Unschedulable",
			LastTransitionTime: since,
		}},
	}
	return pod
}

// The screen's own claim, bound to a volume on the node the pod first
// landed on.
func boundScreenClaim(player *Player) *PersistentVolumeClaim {
	claim := buildScreenClaim(player, testNamespaceCatalog())
	claim.Status.Phase = claimBound
	return claim
}

// Past the grace, on a bound claim of this Player's own, the pass
// deletes the pod and the claim, and it deletes neither in every other state.
func TestReconcileScreensRecoversAnUnschedulableScreen(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		pod     func(*Pod)
		claim   func(*PersistentVolumeClaim)
		wantOut bool
	}{
		{name: "past the grace on a bound claim", age: time.Minute, wantOut: true},
		{name: "inside the grace", age: 0},
		{
			name: "the pod was scheduled", age: time.Minute,
			pod: func(pod *Pod) { pod.Status.Conditions[0].Status = "True" },
		},
		{
			name: "the condition carries no time", age: time.Minute,
			pod: func(pod *Pod) { pod.Status.Conditions[0].LastTransitionTime = time.Time{} },
		},
		{
			name: "the claim never bound", age: time.Minute,
			claim: func(claim *PersistentVolumeClaim) { claim.Status.Phase = "Pending" },
		},
		{
			name: "the claim carries another name", age: time.Minute,
			claim: func(claim *PersistentVolumeClaim) { claim.Metadata.Name = "movies-catalog" },
		},
		{
			name: "the claim carries no screen label", age: time.Minute,
			claim: func(claim *PersistentVolumeClaim) { delete(claim.Metadata.Labels, scannerLabelKey) },
		},
		{
			name: "the claim names another Player", age: time.Minute,
			claim: func(claim *PersistentVolumeClaim) { claim.Metadata.Labels[playerLabelKey] = "kitchen-tv" },
		},
		{
			name: "the owner holds another UID", age: time.Minute,
			claim: func(claim *PersistentVolumeClaim) { claim.Metadata.OwnerReferences[0].UID = "an-older-den-tv" },
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			shortUnschedulableGrace(t)
			cluster := newFakeCluster()
			player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
			catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)
			pod := unschedulableScreenPod(player, catalog, testNow.Add(-one.age))
			if one.pod != nil {
				one.pod(pod)
			}
			cluster.pods[pod.Metadata.Name] = pod
			claim := boundScreenClaim(player)
			if one.claim != nil {
				one.claim(claim)
			}
			cluster.claims[claim.Metadata.Name] = claim

			testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
				[]Player{*player}, nil, []Pod{*pod}, testNow)

			if gone := cluster.heldPod("den-tv-media-browser") == nil; gone != one.wantOut {
				t.Errorf("the pod is gone = %v, want %v", gone, one.wantOut)
			}
			if gone := cluster.heldClaim(claim.Metadata.Name) == nil; gone != one.wantOut {
				t.Errorf("the claim is gone = %v, want %v", gone, one.wantOut)
			}
		})
	}
}

// A failure in the recovery is reported and the pass carries on to the
// next Player's screen.
func TestReconcileScreensCarriesOnPastAFailedRecovery(t *testing.T) {
	cases := []struct {
		name      string
		broken    string
		wantPodIn bool
	}{
		{
			name:      "the claim cannot be read",
			broken:    http.MethodGet + " " + claimPath(testLibraryNamespace, "den-tv-media-browser-catalog"),
			wantPodIn: true,
		},
		{
			name:      "the pod cannot be deleted",
			broken:    http.MethodDelete + " " + podsPath(testLibraryNamespace) + "/den-tv-media-browser",
			wantPodIn: true,
		},
		{
			name:   "the claim cannot be deleted",
			broken: http.MethodDelete + " " + claimPath(testLibraryNamespace, "den-tv-media-browser-catalog"),
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			shortUnschedulableGrace(t)
			cluster := newFakeCluster()
			player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
			standing := seedPlayer(cluster, "kitchen-tv", testLibraryNamespace, screenController)
			catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)
			pod := unschedulableScreenPod(player, catalog, testNow.Add(-time.Minute))
			cluster.pods[pod.Metadata.Name] = pod
			cluster.claims["den-tv-media-browser-catalog"] = boundScreenClaim(player)
			cluster.broken[one.broken] = http.StatusInternalServerError

			testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
				[]Player{*player, *standing}, nil, []Pod{*pod}, testNow)

			if held := cluster.heldPod("den-tv-media-browser") != nil; held != one.wantPodIn {
				t.Errorf("the pod stands = %v, want %v", held, one.wantPodIn)
			}
			if cluster.heldPod("kitchen-tv-media-browser") == nil {
				t.Error("the pass stopped at the broken screen")
			}
		})
	}
}

// An absent claim is success, because the operator deletes a claim to
// replace it and a delete that races another pass must not fail.
func TestDeletePersistentVolumeClaimReadsAnAbsentClaimAsSuccess(t *testing.T) {
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	if err := DeletePersistentVolumeClaim(t.Context(), client, "house", "den-tv-media-browser-catalog"); err != nil {
		t.Fatalf("err = %v, want an absent claim to read as success", err)
	}
}

// The delete names the claim in its own namespace, in the core group.
func TestDeletePersistentVolumeClaimDeletesByName(t *testing.T) {
	client, recorded := recordingAPI(t, PersistentVolumeClaim{})

	if err := DeletePersistentVolumeClaim(t.Context(), client, "house", "den-tv-media-browser-catalog"); err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodDelete,
		"/api/v1/namespaces/house/persistentvolumeclaims/den-tv-media-browser-catalog")
}
