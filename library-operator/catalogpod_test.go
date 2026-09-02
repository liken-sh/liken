package main

// what these tests read: the standing pod one Catalog becomes, the
// claim under it, and the reporter that publishes what it holds.

import (
	"net/http"
	"strings"
	"testing"
)

// the Catalog every test here starts from.
func housekeepingCatalog() *NamespaceCatalog {
	return &NamespaceCatalog{
		Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house", UID: "house-catalog-uid"},
	}
}

func testCatalogPod(catalog *NamespaceCatalog) *Pod {
	return buildCatalogPod(catalog, testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
}

// the pod is named for the Catalog, owned by it, and carries the member
// label, so it is a peer of the namespace's gossip cluster like every
// other agent.
func TestCatalogPodBelongsToItsCatalog(t *testing.T) {
	pod := testCatalogPod(housekeepingCatalog())

	if pod.Metadata.Name != "house-catalog-catalog" || pod.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Catalog's own pod", pod.Metadata)
	}
	if len(pod.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the Catalog", pod.Metadata.OwnerReferences)
	}
	owner := pod.Metadata.OwnerReferences[0]
	if owner.Kind != "Catalog" || owner.Name != "house-catalog" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Catalog", owner)
	}
	if pod.Metadata.Labels[memberLabelKey] != memberLabelValue {
		t.Errorf("labels = %v, want the member label", pod.Metadata.Labels)
	}
	if pod.Metadata.Labels[scannerLabelKey] != catalogLabelValue {
		t.Errorf("labels = %v, want the catalog name label", pod.Metadata.Labels)
	}
}

// the catalog pod is a standing service: it restarts in place, it holds
// no Kubernetes credential, and it answers on no port.
func TestCatalogPodStandsAndAnswersOnNoPort(t *testing.T) {
	pod := testCatalogPod(housekeepingCatalog())

	if pod.Spec.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil ||
		*pod.Spec.TerminationGracePeriodSeconds != scannerGracePeriod {
		t.Errorf("terminationGracePeriodSeconds = %+v, want %d",
			pod.Spec.TerminationGracePeriodSeconds, scannerGracePeriod)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken is not false; the pod holds no credential")
	}
	for _, container := range append(append([]Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		if len(container.Ports) != 0 {
			t.Errorf("%s declares %+v, want no port", container.Name, container.Ports)
		}
		if container.SecurityContext == nil || container.SecurityContext.Capabilities == nil {
			t.Errorf("%s has no security context", container.Name)
		}
	}
}

// the reporter runs this operator's own image in its report role, and
// it learns the namespace, the broker, and the catalog API from its
// environment alone.
func TestCatalogPodRunsTheReporterBesideTheAgent(t *testing.T) {
	pod := testCatalogPod(housekeepingCatalog())

	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Name != catalogContainer {
		t.Fatalf("initContainers = %+v, want the catalog agent alone", pod.Spec.InitContainers)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want the reporter alone", pod.Spec.Containers)
	}
	reporter := pod.Spec.Containers[0]
	if reporter.Name != reporterContainer || reporter.Image != testScannerImage {
		t.Errorf("container = %+v, want the operator image as the reporter", reporter)
	}
	if strings.Join(reporter.Command, " ") != "/library-operator report" {
		t.Errorf("command = %v, want the report role", reporter.Command)
	}
	want := map[string]string{
		libraryNamespaceVariable: "house",
		busAddressVariable:       testBusAddress,
		topicBaseVariable:        defaultTopicBase,
		catalogAPIVariable:       defaultCatalogAPI,
	}
	got := containerEnvironment(reporter)
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("environment = %v, want exactly %d variables", got, len(want))
	}
}

// the agent holds the namespace's durable catalog: the claim the
// operator provisions, or the one the Catalog names.
func TestCatalogPodMountsTheNamespacesClaim(t *testing.T) {
	named := housekeepingCatalog()
	named.Spec.Storage.ClaimName = "catalog-of-my-own"

	cases := []struct {
		name    string
		catalog *NamespaceCatalog
		want    string
	}{
		{name: "the operator's own claim", catalog: housekeepingCatalog(), want: "house-catalog-catalog"},
		{name: "a claim the Catalog names", catalog: named, want: "catalog-of-my-own"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			pod := testCatalogPod(one.catalog)

			source := podVolume(t, pod, catalogVolumeName).PersistentVolumeClaim
			if source == nil || source.ClaimName != one.want {
				t.Errorf("claim = %+v, want %q", source, one.want)
			}
		})
	}
}

// the claim is ReadWriteOnce, sized from the Catalog, and owned by it,
// so the standing catalog survives every roll of the pod.
func TestCatalogPodClaimIsOwnedByItsCatalog(t *testing.T) {
	catalog := housekeepingCatalog()
	catalog.Spec.Storage = CatalogStorage{Size: "8Gi", StorageClassName: "fast"}

	claim := buildCatalogPodClaim(catalog)

	if claim.Metadata.Name != "house-catalog-catalog" || claim.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Catalog's own claim", claim.Metadata)
	}
	if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteOnce {
		t.Errorf("accessModes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
	}
	if claim.Spec.Resources.Requests["storage"] != "8Gi" {
		t.Errorf("storage = %q, want the Catalog's size", claim.Spec.Resources.Requests["storage"])
	}
	if claim.Spec.StorageClassName != "fast" {
		t.Errorf("storageClassName = %q, want the Catalog's class", claim.Spec.StorageClassName)
	}
	if len(claim.Metadata.OwnerReferences) != 1 ||
		claim.Metadata.OwnerReferences[0].Kind != "Catalog" {
		t.Errorf("ownerReferences = %+v, want the Catalog", claim.Metadata.OwnerReferences)
	}
}

// a claim that already stands is left alone, and a Catalog that names
// one of its own has none provisioned for it.
func TestStandCatalogPodClaimProvisionsOnlyWhatItOwns(t *testing.T) {
	named := housekeepingCatalog()
	named.Spec.Storage.ClaimName = "catalog-of-my-own"

	cases := []struct {
		name     string
		catalog  *NamespaceCatalog
		standing bool
		creates  int
	}{
		{name: "no claim yet", catalog: housekeepingCatalog(), creates: 1},
		{name: "a claim already bound", catalog: housekeepingCatalog(), standing: true},
		{name: "a claim the Catalog names", catalog: named},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			if one.standing {
				cluster.claims["house-catalog-catalog"] = &PersistentVolumeClaim{
					Metadata: ObjectMeta{Name: "house-catalog-catalog", Namespace: "house"},
					Status:   PersistentVolumeClaimStatus{Phase: claimBound},
				}
			}

			if err := testOperator(t, cluster).standCatalogPodClaim(t.Context(), one.catalog); err != nil {
				t.Fatal(err)
			}

			if got := cluster.countRequests(http.MethodPost, "persistentvolumeclaims"); got != one.creates {
				t.Errorf("creates = %d, want %d", got, one.creates)
			}
		})
	}
}

// a create another writer got to first is success, and a read that
// fails for any other reason is a failure the pass reports.
func TestStandCatalogPodClaimAnswersTheServer(t *testing.T) {
	conflicted := newFakeCluster()
	conflicted.refuseCreate = true
	if err := testOperator(t, conflicted).standCatalogPodClaim(t.Context(), housekeepingCatalog()); err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}

	broken := newFakeCluster()
	broken.broken["/api/v1/namespaces/house/persistentvolumeclaims/house-catalog-catalog"] =
		http.StatusInternalServerError
	err := testOperator(t, broken).standCatalogPodClaim(t.Context(), housekeepingCatalog())
	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// the pass finds the namespace's catalog pod among the member pods it
// read, and answers none for a namespace with no Catalog or no pod.
func TestCatalogPodOfFindsTheNamespacesOwn(t *testing.T) {
	house := readyCatalogPod("house-catalog", "house")
	elsewhere := readyCatalogPod("house-catalog", "studio")

	cases := []struct {
		name    string
		catalog *NamespaceCatalog
		pods    []Pod
		found   bool
	}{
		{name: "no Catalog at all", pods: []Pod{*house}},
		{name: "no pod yet", catalog: housekeepingCatalog()},
		{name: "a pod of another namespace", catalog: housekeepingCatalog(), pods: []Pod{*elsewhere}},
		{name: "its own pod", catalog: housekeepingCatalog(), pods: []Pod{*elsewhere, *house}, found: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := catalogPodOf(one.catalog, one.pods)

			if (got != nil) != one.found {
				t.Fatalf("pod = %+v, want it found: %v", got, one.found)
			}
			if one.found && got.Metadata.Namespace != "house" {
				t.Errorf("pod = %+v, want the one in the Catalog's namespace", got.Metadata)
			}
		})
	}
}

// the catalog pod is replaced like every other pod this operator
// stands: a pod built from a different template is stale, so the pass
// deletes it and the pass after that creates the replacement. A pod
// already on its way out is left alone.
func TestStandCatalogPodReplacesAStalePod(t *testing.T) {
	stale := readyCatalogPod("house-catalog", "house")
	stale.Metadata.Annotations[templateHashAnnotation] = "an-older-template"
	leaving := readyCatalogPod("house-catalog", "house")
	leaving.Metadata.Annotations[templateHashAnnotation] = "an-older-template"
	leaving.Metadata.DeletionTimestamp = "2026-08-29T12:00:00Z"

	cases := []struct {
		name    string
		live    *Pod
		deletes int
		stands  bool
	}{
		{name: "a stale pod", live: stale, deletes: 1},
		{name: "a pod already going", live: leaving, stands: true},
		{name: "a pod that matches", live: readyCatalogPod("house-catalog", "house"), stands: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.pods["house-catalog-catalog"] = one.live

			pod, err := testOperator(t, cluster).standCatalogPod(t.Context(), housekeepingCatalog())
			if err != nil {
				t.Fatal(err)
			}

			if (pod != nil) != one.stands {
				t.Errorf("pod = %+v, want it standing: %v", pod, one.stands)
			}
			if got := cluster.countRequests(http.MethodDelete, "pods"); got != one.deletes {
				t.Errorf("deletes = %d, want %d", got, one.deletes)
			}
		})
	}
}

// a failure to provision the claim ends the stand, because the pod
// would have nothing to mount.
func TestStandCatalogPodReportsAFailedClaim(t *testing.T) {
	cluster := newFakeCluster()
	cluster.broken["/api/v1/namespaces/house/persistentvolumeclaims/house-catalog-catalog"] =
		http.StatusInternalServerError

	_, err := testOperator(t, cluster).standCatalogPod(t.Context(), housekeepingCatalog())

	if err == nil {
		t.Fatal("err = nil, want the failure the stand could not read past")
	}
}
