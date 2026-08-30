package main

// These tests take one pass over one Library against an API server
// that answers as Kubernetes does. They read what the pass wrote into
// the cluster: the scanner pod it created, the pod it replaced, and
// the status it reported.

import (
	"net/http"
	"testing"
)

// standingPod builds the pod a previous pass left behind: the template
// this operator builds, stamped with its hash, in the phase the
// kubelet reports.
func standingPod(t *testing.T, library *Library, phase string, ready bool) *Pod {
	t.Helper()
	pod := buildScannerPod(library, testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		t.Fatal(err)
	}
	pod.Status = PodStatus{Phase: phase, ContainerStatuses: []ContainerStatus{
		{Name: scannerContainer, Ready: ready},
		{Name: catalogContainer, Ready: ready},
	}}
	return pod
}

// conditionOf reads one condition out of a status, so a test says
// which condition it means and reads its whole verdict.
func conditionOf(t *testing.T, status LibraryStatus, conditionType string) Condition {
	t.Helper()
	for _, condition := range status.Conditions {
		if condition.Type == conditionType {
			return condition
		}
	}
	t.Fatalf("the status carries no %s condition: %+v", conditionType, status.Conditions)
	return Condition{}
}

// A claim that is missing, unbound, or bound to a volume that has gone
// is the cluster's own state and not a failure. Each one reports its
// own reason, and none of them gets a scanner pod, because there is
// nothing to mount.
func TestReconcileReportsStorageThatIsNotBound(t *testing.T) {
	cases := []struct {
		name   string
		claim  *PersistentVolumeClaim
		reason string
	}{
		{name: "no claim", reason: reasonClaimNotFound},
		{
			name: "a claim waiting on a volume",
			claim: &PersistentVolumeClaim{
				Metadata: ObjectMeta{Name: "films", Namespace: "house"},
				Status:   PersistentVolumeClaimStatus{Phase: "Pending"},
			},
			reason: reasonClaimUnbound,
		},
		{
			name: "a claim the binder has not answered",
			claim: &PersistentVolumeClaim{
				Metadata: ObjectMeta{Name: "films", Namespace: "house"},
			},
			reason: reasonClaimUnbound,
		},
		{
			name: "a claim whose volume is gone",
			claim: &PersistentVolumeClaim{
				Metadata: ObjectMeta{Name: "films", Namespace: "house"},
				Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-films"},
				Status:   PersistentVolumeClaimStatus{Phase: claimBound},
			},
			reason: reasonVolumeNotFound,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			delete(cluster.claims, "films")
			delete(cluster.volumes, "pv-films")
			if one.claim != nil {
				cluster.claims["films"] = one.claim
			}

			if err := testOperator(t, cluster).reconcile(library); err != nil {
				t.Fatal(err)
			}

			status := cluster.heldLibrary("films").Status
			bound := conditionOf(t, status, conditionBound)
			if bound.Status != ConditionFalse || bound.Reason != one.reason {
				t.Errorf("Bound = %+v, want False with %s", bound, one.reason)
			}
			if bound.Message == "" {
				t.Error("the Bound condition carries no message")
			}
			if ready := conditionOf(t, status, conditionReady); ready.Reason != reasonNotBound {
				t.Errorf("Ready = %+v, want NotBound", ready)
			}
			if status.Volume != nil {
				t.Errorf("volume = %+v, want none", status.Volume)
			}
			if cluster.countRequests(http.MethodPost, "pods") != 0 {
				t.Error("a scanner pod was created for a library with no volume")
			}
		})
	}
}

// A bound claim reports the volume behind it, so whoever plays a title
// from this library does not have to chase the claim.
func TestReconcileReportsTheVolumeBehindTheClaim(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(library); err != nil {
		t.Fatal(err)
	}

	status := cluster.heldLibrary("films").Status
	bound := conditionOf(t, status, conditionBound)
	if bound.Status != ConditionTrue || bound.Reason != reasonBound {
		t.Errorf("Bound = %+v, want True", bound)
	}
	if bound.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want the Library's generation", bound.ObservedGeneration)
	}
	if status.Volume == nil {
		t.Fatal("the status reports no volume")
	}
	want := LibraryVolume{Name: "pv-films", Type: "nfs", Server: "syn.example", Path: "/volume1/films"}
	if *status.Volume != want {
		t.Errorf("volume = %+v, want %+v", *status.Volume, want)
	}
}

// A volume served by a driver this operator carries no type for still
// reports which driver serves it.
func TestReconcileReportsAVolumeItKnowsNothingAbout(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.volumes["pv-films"] = `{"metadata":{"name":"pv-films"},"spec":` +
		`{"csi":{"driver":"nas.example","volumeHandle":"films"}}}`

	if err := testOperator(t, cluster).reconcile(library); err != nil {
		t.Fatal(err)
	}

	want := LibraryVolume{Name: "pv-films", Type: "csi"}
	if got := *cluster.heldLibrary("films").Status.Volume; got != want {
		t.Errorf("volume = %+v, want %+v", got, want)
	}
}

// A bound Library gets its scanner pod, stamped with the hash of the
// template that built it and named in the status.
func TestReconcileCreatesTheScannerPod(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(library); err != nil {
		t.Fatal(err)
	}

	pod := cluster.heldPod("films-scanner")
	if pod == nil {
		t.Fatal("no scanner pod was created")
	}
	if len(pod.Spec.Containers) != 2 {
		t.Errorf("containers = %d, want the scanner and the catalog agent", len(pod.Spec.Containers))
	}
	if pod.Metadata.Annotations[templateHashAnnotation] == "" {
		t.Error("the pod carries no template hash")
	}
	if got := cluster.heldLibrary("films").Status.Pod; got != "films-scanner" {
		t.Errorf("status.pod = %q, want films-scanner", got)
	}
}

// A pod built from a different template is stale, so the pass deletes
// it, and the pass after that creates the replacement.
func TestReconcileReplacesAStalePod(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	stale := standingPod(t, library, podRunning, true)
	stale.Metadata.Annotations[templateHashAnnotation] = "an-older-template"
	cluster.pods["films-scanner"] = stale
	operator := testOperator(t, cluster)

	if err := operator.reconcile(library); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("films-scanner") != nil {
		t.Fatal("the stale pod still stands")
	}
	if got := conditionOf(t, cluster.heldLibrary("films").Status, conditionReady); got.Reason != reasonPodPending {
		t.Errorf("Ready = %+v, want PodPending while the pod is replaced", got)
	}

	if err := operator.reconcile(library); err != nil {
		t.Fatal(err)
	}

	replacement := cluster.heldPod("films-scanner")
	if replacement == nil {
		t.Fatal("no replacement pod was created")
	}
	if replacement.Metadata.Annotations[templateHashAnnotation] == "an-older-template" {
		t.Error("the replacement carries the stale hash")
	}
}

// A pod already on its way out is left alone, so one divergence costs
// one delete and not one delete per pass.
func TestReconcileLeavesATerminatingPodAlone(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	leaving := standingPod(t, library, podRunning, true)
	leaving.Metadata.Annotations[templateHashAnnotation] = "an-older-template"
	leaving.Metadata.DeletionTimestamp = "2026-08-29T12:00:00Z"
	cluster.pods["films-scanner"] = leaving

	if err := testOperator(t, cluster).reconcile(library); err != nil {
		t.Fatal(err)
	}

	if cluster.countRequests(http.MethodDelete, "pods") != 0 {
		t.Error("the pass deleted a pod that was already going")
	}
	if got := cluster.heldLibrary("films").Status.Pod; got != "films-scanner" {
		t.Errorf("status.pod = %q, want the pod that still stands", got)
	}
}

// A pod that matches the template is left as it stands: the pass reads
// it and writes nothing.
func TestReconcileKeepsAMatchingPod(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.pods["films-scanner"] = standingPod(t, library, podRunning, true)

	if err := testOperator(t, cluster).reconcile(library); err != nil {
		t.Fatal(err)
	}

	if cluster.countRequests(http.MethodDelete, "pods") != 0 {
		t.Error("the pass deleted a pod that matched the template")
	}
	if cluster.countRequests(http.MethodPost, "pods") != 0 {
		t.Error("the pass created a second pod")
	}
}

// A creation another writer got to first is success. The pod stands,
// and the next pass reads it.
func TestReconcileAcceptsAConflictOnCreate(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.refuseCreate = true

	if err := testOperator(t, cluster).reconcile(library); err != nil {
		t.Fatal(err)
	}

	if got := conditionOf(t, cluster.heldLibrary("films").Status, conditionReady); got.Reason != reasonPodPending {
		t.Errorf("Ready = %+v, want PodPending", got)
	}
}

// A request that fails is a failure the pass reports, because it
// cannot tell what the cluster holds.
func TestReconcileFailsWhenTheClusterCannotBeRead(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the claim", path: "/api/v1/namespaces/house/persistentvolumeclaims/films"},
		{name: "the volume", path: "/api/v1/persistentvolumes/pv-films"},
		{name: "the pod", path: "/api/v1/namespaces/house/pods/films-scanner"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			cluster.broken[one.path] = http.StatusInternalServerError

			err := testOperator(t, cluster).reconcile(library)

			if err == nil {
				t.Fatal("err = nil, want the failure the pass could not read past")
			}
		})
	}
}

// A settled Library is written once. The second pass derives the same
// status and writes nothing, so the libraries watch that wakes the
// loop is not woken by the loop itself.
func TestReconcileWritesASettledStatusOnce(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.pods["films-scanner"] = standingPod(t, library, podRunning, true)
	operator := testOperator(t, cluster)
	operator.reports.fold("house", "films", libraryReport{Titles: 12, Unidentified: 2})

	if err := operator.reconcile(library); err != nil {
		t.Fatal(err)
	}
	written := cluster.heldLibrary("films")
	if err := operator.reconcile(written); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPut, "libraries"); got != 1 {
		t.Errorf("status writes = %d, want one", got)
	}
	ready := conditionOf(t, cluster.heldLibrary("films").Status, conditionReady)
	if ready.Status != ConditionTrue {
		t.Errorf("Ready = %+v, want True", ready)
	}
}

// The hash reads the spec alone, so stamping the annotation does not
// change it, and a second build of the same Library stamps the same
// value.
func TestTemplateHashIgnoresTheAnnotationItStamps(t *testing.T) {
	library := houseFilms()
	pod := testScannerPod(library)

	before, err := templateHash(pod.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		t.Fatal(err)
	}

	after, err := templateHash(pod.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("hash = %q, want %q after the stamp", after, before)
	}
	if pod.Metadata.Annotations[templateHashAnnotation] != before {
		t.Errorf("stamp = %q, want %q", pod.Metadata.Annotations[templateHashAnnotation], before)
	}
}

// A release that changes an image, and a person who changes the root
// or the scanner image, each change the pod's hash, which is what
// rolls the scanner pod.
func TestTemplateHashFollowsThePodSpec(t *testing.T) {
	base, err := templateHash(testScannerPod(houseFilms()).Spec)
	if err != nil {
		t.Fatal(err)
	}

	newImage := houseFilms()
	newRoot := houseFilms()
	newRoot.Spec.Storage.Root = "/films"
	ownScanner := houseFilms()
	ownScanner.Spec.Films.Image = "registry.example/my-scanner:1"
	cases := []struct {
		name string
		pod  *Pod
	}{
		{"the scanner image", buildScannerPod(newImage, testScannerImage+"-next",
			testCorrosionImage, testBusAddress, defaultTopicBase)},
		{"the catalog image", buildScannerPod(newImage, testScannerImage,
			testCorrosionImage+"-next", testBusAddress, defaultTopicBase)},
		{"the root", testScannerPod(newRoot)},
		{"a scanner of one's own", testScannerPod(ownScanner)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			hash, err := templateHash(one.pod.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if hash == base {
				t.Errorf("%s changed and the hash stayed %q", one.name, hash)
			}
		})
	}
}
