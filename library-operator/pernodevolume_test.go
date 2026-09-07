package main

// The tests of standClaim and the volume sweep: the volume the operator
// writes for a claim on a per-node class, the claim it writes beside it,
// the claims it writes on every other class, and the sweep that removes
// a volume whose claim is gone.

import (
	"net/http"
	"strings"
	"testing"
)

// perNodeCatalog builds a Catalog whose stores are on a per-node class,
// and seeds that class into the cluster.
func perNodeCatalog(cluster *fakeCluster) *NamespaceCatalog {
	seedStorageClass(cluster, "per-node", perNodeProvisioner)
	catalog := housekeepingCatalog()
	catalog.Spec.Storage.StorageClassName = "per-node"
	return catalog
}

// The volume names the driver, the handle, and the one claim it is
// reserved for. It is ReadWriteMany, because every node that mounts it
// holds a copy of its own, and Retain, so the operator and nothing else
// removes it.
func TestBuildPerNodeVolumeNamesTheDriverAndTheClaim(t *testing.T) {
	catalog := housekeepingCatalog()
	catalog.Spec.Storage.Size = "8Gi"
	catalog.Spec.Storage.StorageClassName = "per-node"

	volume := buildPerNodeVolume(buildCatalogPodClaim(catalog))

	if volume.Metadata.Name != "house-house-catalog-catalog" {
		t.Errorf("name = %q, want the namespace and the claim", volume.Metadata.Name)
	}
	labels := volume.Metadata.Labels
	if labels[claimNamespaceLabelKey] != "house" || labels[claimLabelKey] != "house-catalog-catalog" {
		t.Errorf("labels = %v, want the claim's namespace and name", labels)
	}
	if volume.Spec.CSI == nil || volume.Spec.CSI.Driver != perNodeProvisioner ||
		volume.Spec.CSI.VolumeHandle != volume.Metadata.Name {
		t.Errorf("csi = %+v, want the driver and the volume's own name as the handle", volume.Spec.CSI)
	}
	if volume.Spec.ClaimRef == nil || volume.Spec.ClaimRef.Namespace != "house" ||
		volume.Spec.ClaimRef.Name != "house-catalog-catalog" {
		t.Errorf("claimRef = %+v, want the claim it is reserved for", volume.Spec.ClaimRef)
	}
	if len(volume.Spec.AccessModes) != 1 || volume.Spec.AccessModes[0] != accessModeReadWriteMany {
		t.Errorf("accessModes = %v, want ReadWriteMany", volume.Spec.AccessModes)
	}
	if volume.Spec.Capacity["storage"] != "8Gi" {
		t.Errorf("capacity = %v, want the claim's size", volume.Spec.Capacity)
	}
	if volume.Spec.PersistentVolumeReclaimPolicy != reclaimRetain {
		t.Errorf("reclaim policy = %q, want Retain", volume.Spec.PersistentVolumeReclaimPolicy)
	}
	if volume.Spec.StorageClassName != "per-node" {
		t.Errorf("storageClassName = %q, want the claim's class", volume.Spec.StorageClassName)
	}
}

// On a per-node class the pass writes the volume first and the claim
// names it, so the claim binds to that volume and waits on no
// provisioner.
func TestStandClaimWritesTheVolumeBeforeTheClaim(t *testing.T) {
	cluster := newFakeCluster()
	catalog := perNodeCatalog(cluster)

	if err := testOperator(t, cluster).standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
		t.Fatal(err)
	}

	claim := cluster.heldClaim("house-catalog-catalog")
	if claim == nil {
		t.Fatal("the pass provisioned no claim")
	}
	if claim.Spec.VolumeName != "house-house-catalog-catalog" {
		t.Errorf("volumeName = %q, want the volume the pass wrote", claim.Spec.VolumeName)
	}
	if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteMany {
		t.Errorf("accessModes = %v, want ReadWriteMany", claim.Spec.AccessModes)
	}
	if cluster.heldVolume("house-house-catalog-catalog") == nil {
		t.Fatal("the pass wrote no volume for the claim")
	}
	volumes := cluster.firstRequest(http.MethodPost, "persistentvolumes")
	claims := cluster.firstRequest(http.MethodPost, "persistentvolumeclaims")
	if volumes < 0 || claims < 0 || volumes > claims {
		t.Errorf("the volume went at %d and the claim at %d, want the volume first", volumes, claims)
	}
}

// A claim on any other class is ReadWriteOnce, with no volume and no
// volumeName. A class the cluster does not serve is one of them, and so
// is a claim that names no class at all.
func TestStandClaimWritesNoVolumeOnAnotherClass(t *testing.T) {
	cases := []struct {
		name  string
		class string
	}{
		{name: "a class of another provisioner", class: "local-path"},
		{name: "a class the cluster does not serve", class: "no-such-class"},
		{name: "no class at all"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			seedStorageClass(cluster, "local-path", "rancher.io/local-path")
			catalog := housekeepingCatalog()
			catalog.Spec.Storage.StorageClassName = one.class

			if err := testOperator(t, cluster).standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
				t.Fatal(err)
			}

			claim := cluster.heldClaim("house-catalog-catalog")
			if claim == nil {
				t.Fatal("the pass provisioned no claim")
			}
			if claim.Spec.VolumeName != "" {
				t.Errorf("volumeName = %q, want the binder to fill it in", claim.Spec.VolumeName)
			}
			if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteOnce {
				t.Errorf("accessModes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
			}
			if got := cluster.countRequests(http.MethodPost, "persistentvolumes"); got != 0 {
				t.Errorf("volumes = %d, want none on a class that is not per-node", got)
			}
		})
	}
}

// A claim whose name was used before finds the old volume still there,
// Released, with the uid of the claim the binder gave it in its
// claimRef. The pass replaces that volume, so the fresh claim binds to
// one reserved for it.
func TestStandClaimReplacesTheVolumeOfAClaimThatIsGone(t *testing.T) {
	cluster := newFakeCluster()
	catalog := perNodeCatalog(cluster)
	operator := testOperator(t, cluster)
	if err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
		t.Fatal(err)
	}
	if err := DeletePersistentVolumeClaim(t.Context(), operator.client, "house", "house-catalog-catalog"); err != nil {
		t.Fatal(err)
	}

	if err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
		t.Fatal(err)
	}

	volume := cluster.heldVolume("house-house-catalog-catalog")
	if volume == nil {
		t.Fatal("the pass left no volume for the claim it wrote")
	}
	if volume.Status.Phase == volumeReleased {
		t.Errorf("phase = %q, want a volume no claim has held", volume.Status.Phase)
	}
	if volume.Spec.ClaimRef == nil || volume.Spec.ClaimRef.UID != "" {
		t.Errorf("claimRef = %+v, want one reserved for the claim and bound to nothing", volume.Spec.ClaimRef)
	}
	if got := cluster.countRequests(http.MethodDelete, "persistentvolumes"); got != 1 {
		t.Errorf("volume deletes = %d, want the one volume of the claim that is gone", got)
	}
	claim := cluster.heldClaim("house-catalog-catalog")
	if claim == nil || claim.Spec.VolumeName != "house-house-catalog-catalog" {
		t.Errorf("claim = %+v, want it naming the volume the pass wrote", claim)
	}
}

// A volume that no claim has held is kept, and so is one that is Bound,
// because each is the volume the claim the pass writes binds to.
func TestStandClaimKeepsAVolumeNoClaimHasLeft(t *testing.T) {
	cases := []struct {
		name  string
		phase string
		uid   string
	}{
		{name: "a volume waiting for its claim"},
		{name: "a volume already bound to it", phase: claimBound, uid: "house-catalog-catalog-uid"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := perNodeCatalog(cluster)
			standing := buildPerNodeVolume(buildCatalogPodClaim(catalog))
			standing.Status.Phase = one.phase
			standing.Spec.ClaimRef.UID = one.uid
			cluster.volumes[standing.Metadata.Name] = encodeVolume(standing)

			if err := testOperator(t, cluster).standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
				t.Fatal(err)
			}

			if got := cluster.countRequests(http.MethodDelete, "persistentvolumes"); got != 0 {
				t.Errorf("volume deletes = %d, want the standing volume kept", got)
			}
			volume := cluster.heldVolume("house-house-catalog-catalog")
			if volume == nil || volume.Spec.ClaimRef.UID != one.uid {
				t.Errorf("volume = %+v, want the one that already stood", volume)
			}
			claim := cluster.heldClaim("house-catalog-catalog")
			if claim == nil || claim.Spec.VolumeName != "house-house-catalog-catalog" {
				t.Errorf("claim = %+v, want it naming the volume that stood", claim)
			}
		})
	}
}

// A read or a delete the server refuses over the existing volume is
// reported, and the claim is not written, because the pass cannot tell
// what the claim would bind to.
func TestStandClaimReportsWhatTheServerRefusesOverAStandingVolume(t *testing.T) {
	cases := []struct {
		name    string
		request string
	}{
		{name: "the read", request: "GET " + volumesPath + "/house-house-catalog-catalog"},
		{name: "the delete", request: "DELETE " + volumesPath + "/house-house-catalog-catalog"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := perNodeCatalog(cluster)
			operator := testOperator(t, cluster)
			if err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
				t.Fatal(err)
			}
			if err := DeletePersistentVolumeClaim(t.Context(), operator.client, "house", "house-catalog-catalog"); err != nil {
				t.Fatal(err)
			}
			cluster.broken[one.request] = http.StatusInternalServerError

			err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog))

			if err == nil {
				t.Fatal("err = nil, want the failure the pass could not read past")
			}
			if cluster.heldClaim("house-catalog-catalog") != nil {
				t.Error("the pass wrote a claim over a volume it could not read")
			}
		})
	}
}

// A claim that already exists is left alone, and no volume is written
// for it, because the volume behind it exists as well.
func TestStandClaimLeavesAnExistingClaim(t *testing.T) {
	cluster := newFakeCluster()
	catalog := perNodeCatalog(cluster)
	cluster.claims["house-catalog-catalog"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "house-catalog-catalog", Namespace: "house"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}

	if err := testOperator(t, cluster).standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPost, "persistentvolumes"); got != 0 {
		t.Errorf("volumes = %d, want none over a claim that already stands", got)
	}
}

// A volume create the API server refuses as a conflict, over a volume
// no read can find, is reported and no claim is written. The volume is
// gone between the two requests, and a claim that named it would stay
// Pending forever.
func TestStandClaimReportsAVolumeThatVanishedAfterAConflict(t *testing.T) {
	cluster := newFakeCluster()
	catalog := perNodeCatalog(cluster)
	cluster.refuseCreate = true

	err := testOperator(t, cluster).standClaim(t.Context(), buildCatalogPodClaim(catalog))
	if err == nil {
		t.Fatal("err = nil, want the vanished volume reported")
	}
	if len(cluster.claims) != 0 {
		t.Errorf("claims = %d, want none written", len(cluster.claims))
	}
}

// A class read or a volume create the API server refuses is reported,
// and no claim is written, because the pass cannot tell what the claim
// would bind to.
func TestStandClaimReportsAClassItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the class", path: storageClassesPath + "/per-node"},
		{name: "the volume", path: volumesPath},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := perNodeCatalog(cluster)
			cluster.broken[one.path] = http.StatusInternalServerError

			err := testOperator(t, cluster).standClaim(t.Context(), buildCatalogPodClaim(catalog))

			if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
				t.Fatalf("err = %v, want the server's own message", err)
			}
			if cluster.heldClaim("house-catalog-catalog") != nil {
				t.Error("the pass wrote a claim it could not write a volume for")
			}
		})
	}
}

// One class is read once per pass, however many claims name it, and the
// next pass reads it again.
func TestTheClassIsReadOncePerPass(t *testing.T) {
	cluster := newFakeCluster()
	catalog := perNodeCatalog(cluster)
	operator := testOperator(t, cluster)

	for range 2 {
		if err := operator.standClaim(t.Context(), buildProgressClaim(catalog)); err != nil {
			t.Fatal(err)
		}
	}
	clear(operator.perNodeClasses)
	if err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodGet, "storageclasses"); got != 2 {
		t.Errorf("class reads = %d, want one for each pass", got)
	}
}

// A deleted claim leaves its volume Released, and one sweep deletes it,
// so the driver removes every copy of it on every node.
func TestTheSweepDeletesTheVolumeOfAClaimThatIsGone(t *testing.T) {
	cluster := newFakeCluster()
	catalog := perNodeCatalog(cluster)
	operator := testOperator(t, cluster)
	if err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
		t.Fatal(err)
	}
	if err := DeletePersistentVolumeClaim(t.Context(), operator.client, "house", "house-catalog-catalog"); err != nil {
		t.Fatal(err)
	}

	if err := operator.sweepReleasedVolumes(t.Context()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldVolume("house-house-catalog-catalog") != nil {
		t.Error("the volume of a claim that is gone stands, want it swept")
	}
}

// A volume whose claim still exists is left alone, and so is a volume
// that carries neither of the operator's labels.
func TestTheSweepLeavesEveryVolumeButItsOwnReleasedOnes(t *testing.T) {
	cluster := newFakeCluster()
	catalog := perNodeCatalog(cluster)
	operator := testOperator(t, cluster)
	if err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
		t.Fatal(err)
	}
	cluster.volumes["pv-of-another-writer"] = `{"metadata":{"name":"pv-of-another-writer"},` +
		`"status":{"phase":"Released"},"spec":{"nfs":{"server":"syn.example","path":"/srv/media"}}}`

	if err := operator.sweepReleasedVolumes(t.Context()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldVolume("house-house-catalog-catalog") == nil {
		t.Error("the sweep took the volume of a claim that stands")
	}
	if cluster.heldVolume("pv-of-another-writer") == nil {
		t.Error("the sweep took a volume that carries none of the operator's labels")
	}
}

// A list or a delete the server refuses is the failure the sweep
// reports, because the volumes it could not read are still there.
func TestTheSweepReportsWhatTheServerRefuses(t *testing.T) {
	cases := []struct {
		name    string
		request string
	}{
		{name: "the list", request: volumesPath},
		{name: "the delete", request: "DELETE " + volumesPath + "/house-house-catalog-catalog"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := perNodeCatalog(cluster)
			operator := testOperator(t, cluster)
			if err := operator.standClaim(t.Context(), buildCatalogPodClaim(catalog)); err != nil {
				t.Fatal(err)
			}
			if err := DeletePersistentVolumeClaim(t.Context(), operator.client, "house", "house-catalog-catalog"); err != nil {
				t.Fatal(err)
			}
			cluster.broken[one.request] = http.StatusInternalServerError

			err := operator.sweepReleasedVolumes(t.Context())

			if err == nil {
				t.Fatal("err = nil, want the failure the sweep could not read past")
			}
		})
	}
}

// A pass sweeps its own Released volumes, so a namespace whose Catalog
// is deleted loses the volumes behind its stores.
func TestAPassSweepsTheReleasedVolumes(t *testing.T) {
	cluster := newFakeCluster()
	cluster.volumes["house-house-catalog-catalog"] = `{"metadata":{"name":"house-house-catalog-catalog",` +
		`"labels":{"` + claimNamespaceLabelKey + `":"house","` + claimLabelKey + `":"house-catalog-catalog"}},` +
		`"status":{"phase":"Released"},"spec":{"csi":{"driver":"` + perNodeProvisioner + `"}}}`

	testOperator(t, cluster).pass()

	if cluster.heldVolume("house-house-catalog-catalog") != nil {
		t.Error("the pass left a released volume of its own")
	}
}

// Every claim the operator writes goes through standClaim, so each of
// them binds to a volume of its own on a per-node class.
func TestEveryClaimTheOperatorWritesGoesThroughStandClaim(t *testing.T) {
	cases := []struct {
		name   string
		stand  func(*operator, *NamespaceCatalog) error
		volume string
	}{
		{
			name:   "the scanner claim of a Library",
			volume: "house-movies-catalog",
			stand: func(o *operator, catalog *NamespaceCatalog) error {
				return o.standCatalogClaim(t.Context(), studioMovies(), catalog)
			},
		},
		{
			name:   "the enrichment claim of a Library",
			volume: "house-movies-enrich-catalog",
			stand: func(o *operator, catalog *NamespaceCatalog) error {
				return o.standEnrichClaim(t.Context(), studioMovies(), catalog)
			},
		},
		{
			name:   "the catalog store",
			volume: "house-house-catalog-catalog",
			stand: func(o *operator, catalog *NamespaceCatalog) error {
				return o.standCatalogPodClaim(t.Context(), catalog)
			},
		},
		{
			name:   "the progress store",
			volume: "house-house-catalog-progress",
			stand: func(o *operator, catalog *NamespaceCatalog) error {
				return o.standProgressClaim(t.Context(), catalog)
			},
		},
		{
			name:   "the claims of a screen",
			volume: "house-den-tv-media-browser-catalog",
			stand: func(o *operator, catalog *NamespaceCatalog) error {
				return o.standScreenClaims(t.Context(), denScreen(), catalog)
			},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := perNodeCatalog(cluster)
			catalog.Spec.Screens.StorageClassName = "per-node"

			if err := one.stand(testOperator(t, cluster), catalog); err != nil {
				t.Fatal(err)
			}

			volume := cluster.heldVolume(one.volume)
			if volume == nil {
				t.Fatalf("the pass wrote no volume %s", one.volume)
			}
			if volume.Spec.CSI == nil || volume.Spec.CSI.Driver != perNodeProvisioner {
				t.Errorf("csi = %+v, want the per-node driver", volume.Spec.CSI)
			}
		})
	}
}

// The class is read by name from the cluster-scoped storage.k8s.io
// group.
func TestGetStorageClassReadsTheClassByName(t *testing.T) {
	client, recorded := recordingAPI(t, StorageClass{
		Metadata:    ObjectMeta{Name: "per-node"},
		Provisioner: perNodeProvisioner,
	})

	class, err := GetStorageClass(t.Context(), client, "per-node")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/apis/storage.k8s.io/v1/storageclasses/per-node")
	if class.Provisioner != perNodeProvisioner {
		t.Errorf("provisioner = %q, want the driver the class names", class.Provisioner)
	}
}

// A volume that is already gone is success, because two passes may sweep
// the same volume.
func TestDeletePersistentVolumeAcceptsAnAbsentVolume(t *testing.T) {
	cluster := newFakeCluster()

	if err := DeletePersistentVolume(t.Context(), testOperator(t, cluster).client, "no-such-volume"); err != nil {
		t.Fatalf("err = %v, want an absent volume to read as success", err)
	}
}
