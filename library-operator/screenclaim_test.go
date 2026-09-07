package main

// The two claims a screen holds, the pass that creates them before the
// pod, and the recovery of a screen the scheduler cannot place where
// its volumes are.

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

// A screen holds two claims: the catalog claim sized from spec.storage
// and the art claim sized from spec.screens.artCache. Both take the
// screens block's class, the screen and Player labels, and the Player
// as owner.
func TestScreenClaimsAreNamedSizedLabeledAndOwned(t *testing.T) {
	catalog := testCatalogWithScreens("2Gi", "local-path")
	catalog.Spec.Screens.ArtCache.Size = "4Gi"
	claims := screenClaims(denScreen(), catalog)

	cases := []struct{ name, size string }{
		{"den-tv-media-browser-catalog", "2Gi"},
		{"den-tv-media-browser-art", "4Gi"},
	}
	if len(claims) != len(cases) {
		t.Fatalf("claims = %d, want the catalog claim and the art claim", len(claims))
	}
	for index, one := range cases {
		claim := claims[index]
		if claim.Metadata.Name != one.name {
			t.Errorf("name = %q, want %s", claim.Metadata.Name, one.name)
		}
		if claim.Metadata.Namespace != testLibraryNamespace {
			t.Errorf("namespace = %q, want %s", claim.Metadata.Namespace, testLibraryNamespace)
		}
		if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteOnce {
			t.Errorf("accessModes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
		}
		if claim.Spec.Resources.Requests["storage"] != one.size {
			t.Errorf("storage = %q, want %s", claim.Spec.Resources.Requests["storage"], one.size)
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
		want := playerOwner(denScreen())
		if len(claim.Metadata.OwnerReferences) != 1 || claim.Metadata.OwnerReferences[0] != want {
			t.Errorf("ownerReferences = %+v, want %+v", claim.Metadata.OwnerReferences, want)
		}
	}
}

// An absent class is omitted, so the cluster's default StorageClass
// binds the claim, and an absent size takes the default of its own field.
func TestScreenClaimsOmitAnAbsentClassAndDefaultTheSizes(t *testing.T) {
	claims := screenClaims(denScreen(), testNamespaceCatalog())

	want := []string{defaultCatalogSize, defaultArtCacheSize}
	for index, claim := range claims {
		if claim.Spec.StorageClassName != "" {
			t.Errorf("storageClassName = %q, want it omitted for the default class", claim.Spec.StorageClassName)
		}
		if claim.Spec.Resources.Requests["storage"] != want[index] {
			t.Errorf("storage = %q, want %s", claim.Spec.Resources.Requests["storage"], want[index])
		}
	}
}

// The pass creates both claims before the pod, and each of the pod's
// two volumes names the claim it was made for.
func TestReconcileScreensStandsBothClaimsBeforeThePod(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
		[]Player{*player}, nil, nil, nil, testNow)

	cases := []struct{ claim, volume, size string }{
		{"den-tv-media-browser-catalog", catalogVolumeName, defaultCatalogSize},
		{"den-tv-media-browser-art", artCacheVolumeName, defaultArtCacheSize},
	}
	pod := cluster.heldPod("den-tv-media-browser")
	if pod == nil {
		t.Fatal("the pass stood no screen pod")
	}
	for _, one := range cases {
		claim := cluster.heldClaim(one.claim)
		if claim == nil {
			t.Fatalf("the pass provisioned no %s for the screen", one.claim)
		}
		if claim.Spec.Resources.Requests["storage"] != one.size {
			t.Errorf("storage = %q, want %s", claim.Spec.Resources.Requests["storage"], one.size)
		}
		source := podVolume(t, pod, one.volume).PersistentVolumeClaim
		if source == nil || source.ClaimName != one.claim {
			t.Errorf("%s volume = %+v, want %s", one.volume, source, one.claim)
		}
	}
	claimed := cluster.firstRequest(http.MethodPost, "persistentvolumeclaims")
	stood := cluster.firstRequest(http.MethodPost, "pods")
	if claimed < 0 || stood < 0 || claimed > stood {
		t.Errorf("the claim was created at request %d and the pod at %d, want the claim first", claimed, stood)
	}
}

// A claim that already stands is left alone, so a screen comes back on
// the catalog and the art it holds.
func TestReconcileScreensLeavesAnExistingScreenClaim(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)
	for _, claim := range boundScreenClaims(player) {
		cluster.claims[claim.Metadata.Name] = claim
	}

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
		[]Player{*player}, nil, nil, nil, testNow)

	if got := cluster.countRequests(http.MethodPost, "persistentvolumeclaims"); got != 0 {
		t.Errorf("creates = %d, want none over claims that already stand", got)
	}
}

// A namespace with no single Catalog has no size to read, so the pod
// keeps its emptyDirs and the pass provisions nothing.
func TestReconcileScreensWithNoCatalogKeepsTheEmptyDir(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, nil,
		[]Player{*player}, nil, nil, nil, testNow)

	if got := cluster.countRequests(http.MethodPost, "persistentvolumeclaims"); got != 0 {
		t.Errorf("creates = %d, want none in a namespace with no Catalog", got)
	}
	pod := cluster.heldPod("den-tv-media-browser")
	if pod == nil {
		t.Fatal("the pass stood no screen pod")
	}
	for _, name := range []string{catalogVolumeName, artCacheVolumeName} {
		if podVolume(t, pod, name).EmptyDir == nil {
			t.Errorf("%s volume = %+v, want an emptyDir", name, podVolume(t, pod, name))
		}
	}
}

// A create another writer got to first is success, and the next pass
// reads the claim it made.
func TestStandScreenClaimAcceptsAConflict(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true

	err := testOperator(t, cluster).standScreenClaims(t.Context(), denScreen(), testNamespaceCatalog())

	if err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}
}

// A read that fails for any other reason is reported, because the pass
// cannot tell whether the claim exists.
func TestStandScreenClaimReportsAFailedRead(t *testing.T) {
	cases := []string{"den-tv-media-browser-catalog", "den-tv-media-browser-art"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.broken[claimPath(testLibraryNamespace, name)] = http.StatusInternalServerError

			err := testOperator(t, cluster).standScreenClaims(t.Context(), denScreen(), testNamespaceCatalog())

			if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
				t.Fatalf("err = %v, want the server's own message", err)
			}
		})
	}
}

// A screen's claims on a per-node class bind to a volume of their own,
// so the pod they belong to is pinned to no node, and the recovery leaves
// the pod and both claims in place.
func TestReconcileScreensKeepsAnUnschedulableScreenOnAPerNodeClass(t *testing.T) {
	shortUnschedulableGrace(t)
	cluster := newFakeCluster()
	seedStorageClass(cluster, "per-node", perNodeProvisioner)
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)
	catalog.Spec.Screens.StorageClassName = "per-node"
	pod := unschedulableScreenPod(player, catalog, testNow.Add(-time.Minute))
	cluster.pods[pod.Metadata.Name] = pod
	for _, claim := range screenClaims(player, catalog) {
		claim.Status.Phase = claimBound
		cluster.claims[claim.Metadata.Name] = claim
	}

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
		[]Player{*player}, nil, nil, []Pod{*pod}, testNow)

	if cluster.heldPod("den-tv-media-browser") == nil {
		t.Error("the pass took a screen pod whose claims pin it to no node")
	}
	for _, name := range []string{"den-tv-media-browser-catalog", "den-tv-media-browser-art"} {
		if cluster.heldClaim(name) == nil {
			t.Errorf("the pass took the claim %s, which pins the pod to no node", name)
		}
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

// The screen's own claims, bound to volumes on the node the pod first
// landed on.
func boundScreenClaims(player *Player) []*PersistentVolumeClaim {
	claims := screenClaims(player, testNamespaceCatalog())
	for _, claim := range claims {
		claim.Status.Phase = claimBound
	}
	return claims
}

// Past the grace, on a Bound catalog claim of this Player's own, the
// pass deletes the pod and both claims. In every other state it deletes
// none of them.
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
			claims := boundScreenClaims(player)
			if one.claim != nil {
				one.claim(claims[0])
			}
			for _, claim := range claims {
				cluster.claims[claim.Metadata.Name] = claim
			}

			testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
				[]Player{*player}, nil, nil, []Pod{*pod}, testNow)

			if gone := cluster.heldPod("den-tv-media-browser") == nil; gone != one.wantOut {
				t.Errorf("the pod is gone = %v, want %v", gone, one.wantOut)
			}
			for _, claim := range claims {
				if gone := cluster.heldClaim(claim.Metadata.Name) == nil; gone != one.wantOut {
					t.Errorf("%s is gone = %v, want %v", claim.Metadata.Name, gone, one.wantOut)
				}
			}
		})
	}
}

// The art claim's delete carries the same three guards and the Bound
// check, so a claim that fails one of them stands while the pod and the
// catalog claim go.
func TestReconcileScreensGuardsTheArtClaimDelete(t *testing.T) {
	cases := []struct {
		name    string
		art     func(*PersistentVolumeClaim)
		wantOut bool
	}{
		{name: "every guard passes", wantOut: true},
		{
			name: "the claim never bound",
			art:  func(claim *PersistentVolumeClaim) { claim.Status.Phase = "Pending" },
		},
		{
			name: "the claim carries another name",
			art:  func(claim *PersistentVolumeClaim) { claim.Metadata.Name = "movies-catalog" },
		},
		{
			name: "the claim carries no screen label",
			art:  func(claim *PersistentVolumeClaim) { delete(claim.Metadata.Labels, scannerLabelKey) },
		},
		{
			name: "the claim names another Player",
			art:  func(claim *PersistentVolumeClaim) { claim.Metadata.Labels[playerLabelKey] = "kitchen-tv" },
		},
		{
			name: "the owner holds another UID",
			art: func(claim *PersistentVolumeClaim) {
				claim.Metadata.OwnerReferences[0].UID = "an-older-den-tv"
			},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			shortUnschedulableGrace(t)
			cluster := newFakeCluster()
			player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
			catalog := seedCatalog(cluster, "house-catalog", testLibraryNamespace)
			pod := unschedulableScreenPod(player, catalog, testNow.Add(-time.Minute))
			cluster.pods[pod.Metadata.Name] = pod
			claims := boundScreenClaims(player)
			if one.art != nil {
				one.art(claims[1])
			}
			cluster.claims["den-tv-media-browser-catalog"] = claims[0]
			cluster.claims["den-tv-media-browser-art"] = claims[1]

			testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
				[]Player{*player}, nil, nil, []Pod{*pod}, testNow)

			if cluster.heldPod("den-tv-media-browser") != nil {
				t.Error("the pod stands, want the recovery to have taken it")
			}
			if cluster.heldClaim("den-tv-media-browser-catalog") != nil {
				t.Error("the catalog claim stands, want the recovery to have taken it")
			}
			if gone := cluster.heldClaim("den-tv-media-browser-art") == nil; gone != one.wantOut {
				t.Errorf("the art claim is gone = %v, want %v", gone, one.wantOut)
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
		{
			name:   "the art claim cannot be deleted",
			broken: http.MethodDelete + " " + claimPath(testLibraryNamespace, "den-tv-media-browser-art"),
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
			for _, claim := range boundScreenClaims(player) {
				cluster.claims[claim.Metadata.Name] = claim
			}
			cluster.broken[one.broken] = http.StatusInternalServerError

			testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace, catalog,
				[]Player{*player, *standing}, nil, nil, []Pod{*pod}, testNow)

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
