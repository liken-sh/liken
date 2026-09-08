package main

// What these tests read: the standing pod that holds one namespace's
// durable progress store, the claim under it, and the progress role
// beside its agent.

import (
	"net/http"
	"strings"
	"testing"
)

func testProgressPod(catalog *NamespaceCatalog, index int) *Pod {
	return buildProgressPod(catalog, index, testScannerImage, testCorrosionImage,
		testBusAddress, defaultTopicBase, defaultMediaTopicBase)
}

// The pod is named for the Catalog, owned by it, and carries the
// progress member label alone, so it is a peer of the progress cluster
// and not of the catalog cluster.
func TestProgressPodBelongsToItsCatalogAndItsOwnCluster(t *testing.T) {
	pod := testProgressPod(housekeepingCatalog(), 0)

	if pod.Metadata.Name != "house-catalog-progress-0" || pod.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Catalog's own progress pod", pod.Metadata)
	}
	if len(pod.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the Catalog", pod.Metadata.OwnerReferences)
	}
	owner := pod.Metadata.OwnerReferences[0]
	if owner.Kind != "Catalog" || owner.Name != "house-catalog" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Catalog", owner)
	}
	if pod.Metadata.Labels[progressMemberLabelKey] != progressMemberLabelValue {
		t.Errorf("labels = %v, want the progress member label", pod.Metadata.Labels)
	}
	if _, held := pod.Metadata.Labels[memberLabelKey]; held {
		t.Errorf("labels = %v, want no catalog member label on a progress pod", pod.Metadata.Labels)
	}
	if pod.Metadata.Labels[scannerLabelKey] != progressLabelValue {
		t.Errorf("labels = %v, want the progress name label", pod.Metadata.Labels)
	}
}

// The progress pod is a standing service that holds no Kubernetes
// credential, because the operator is the only API client and every
// fact reaches this pod over the bus.
func TestProgressPodStandsWithNoCredential(t *testing.T) {
	pod := testProgressPod(housekeepingCatalog(), 0)

	if pod.Spec.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken is not false; the pod holds no credential")
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil ||
		*pod.Spec.TerminationGracePeriodSeconds != scannerGracePeriod {
		t.Errorf("terminationGracePeriodSeconds = %+v, want %d",
			pod.Spec.TerminationGracePeriodSeconds, scannerGracePeriod)
	}
	for _, container := range append(append([]Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		if container.SecurityContext == nil || container.SecurityContext.Capabilities == nil {
			t.Errorf("%s has no security context", container.Name)
		}
	}
}

// The agent is a native sidecar on the progress configuration, so the
// kubelet passes its startupProbe before the progress role starts and
// the first write never races an API that is not listening.
func TestProgressPodRunsTheAgentOnTheProgressConfiguration(t *testing.T) {
	pod := testProgressPod(housekeepingCatalog(), 0)

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("initContainers = %+v, want the progress agent alone", pod.Spec.InitContainers)
	}
	agent := pod.Spec.InitContainers[0]
	if agent.Name != progressContainer || agent.Image != testCorrosionImage {
		t.Errorf("container = %+v, want the Corrosion image as the progress agent", agent)
	}
	if agent.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always, which is what makes it a native sidecar", agent.RestartPolicy)
	}
	if want := "agent --config " + progressConfigPath; strings.Join(agent.Args, " ") != want {
		t.Errorf("args = %v, want %q", agent.Args, want)
	}
	if len(agent.VolumeMounts) != 1 || agent.VolumeMounts[0].MountPath != progressStatePath {
		t.Errorf("mounts = %+v, want the progress claim at %s", agent.VolumeMounts, progressStatePath)
	}
	if agent.StartupProbe == nil || agent.LivenessProbe == nil {
		t.Fatal("the agent carries no startup or liveness probe")
	}
	if want := strings.Join(agent.StartupProbe.Exec.Command, " "); !strings.Contains(want, progressConfigPath) {
		t.Errorf("startup probe = %q, want the progress configuration", want)
	}
}

// The agent announces the pod's own address on the progress port, which
// nothing knows until the kubelet has started the pod.
func TestProgressPodAgentAnnouncesItsOwnAddress(t *testing.T) {
	agent := testProgressPod(housekeepingCatalog(), 0).Spec.InitContainers[0]

	held := envOf(agent)
	if held[gossipAddressVariable] != progressGossipAddress {
		t.Errorf("%s = %q, want %q", gossipAddressVariable, held[gossipAddressVariable], progressGossipAddress)
	}
	if !strings.HasSuffix(progressGossipAddress, ":8788") {
		t.Errorf("the gossip address is %q, want the progress port", progressGossipAddress)
	}
	for _, variable := range agent.Env {
		if variable.Name == podIPVariable {
			if variable.ValueFrom == nil || variable.ValueFrom.FieldRef.FieldPath != podIPFieldPath {
				t.Errorf("%s = %+v, want the downward API field", podIPVariable, variable)
			}
			return
		}
	}
	t.Errorf("the agent reads no %s", podIPVariable)
}

// The progress role runs this operator's own image, and it learns the
// namespace, the two topic trees, the broker, and its agent's address
// from its environment alone.
func TestProgressPodRunsTheProgressRole(t *testing.T) {
	pod := testProgressPod(housekeepingCatalog(), 0)

	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want the progress role alone", pod.Spec.Containers)
	}
	role := pod.Spec.Containers[0]
	if role.Name != recorderContainer || role.Image != testScannerImage {
		t.Errorf("container = %+v, want the operator image in its progress role", role)
	}
	if want := "/library-operator " + progressMode; strings.Join(role.Command, " ") != want {
		t.Errorf("command = %v, want %q", role.Command, want)
	}
	held := envOf(role)
	want := map[string]string{
		libraryNamespaceVariable: "house",
		busAddressVariable:       testBusAddress,
		topicBaseVariable:        defaultTopicBase,
		mediaTopicBaseVariable:   defaultMediaTopicBase,
		progressAPIVariable:      defaultProgressAPI,
	}
	for name, value := range want {
		if held[name] != value {
			t.Errorf("%s = %q, want %q", name, held[name], value)
		}
	}
}

// envOf reads a container's literal environment as a map, so a test
// names the variable it reads rather than its place in the list.
func envOf(container Container) map[string]string {
	held := map[string]string{}
	for _, variable := range container.Env {
		held[variable.Name] = variable.Value
	}
	return held
}

// The progress store keeps a claim of its own, sized and classed by the
// namespace's Catalog, so a rebuilt cluster starts from it and no
// rescan touches it.
// The progress claim takes spec.progress's own size and class, so a
// namespace puts its central stores on a durable class at a size of
// their own while every working copy stays on the local class.
func TestProgressClaimTakesItsOwnSizeAndClass(t *testing.T) {
	catalog := housekeepingCatalog()
	catalog.Spec.Storage.Size = "4Gi"
	catalog.Spec.Storage.StorageClassName = "local-path"
	catalog.Spec.Progress.Size = "256Mi"
	catalog.Spec.Progress.StorageClassName = "synology-iscsi"

	claim := buildProgressClaim(catalog)

	if claim.Metadata.Name != "house-catalog-progress" || claim.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the progress claim in the Catalog's namespace", claim.Metadata)
	}
	if claim.Spec.StorageClassName != "synology-iscsi" {
		t.Errorf("storageClassName = %q, want spec.progress's own", claim.Spec.StorageClassName)
	}
	if got := claim.Spec.Resources.Requests["storage"]; got != "256Mi" {
		t.Errorf("storage = %q, want spec.progress's own size", got)
	}
	if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteOnce {
		t.Errorf("accessModes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
	}
	if len(claim.Metadata.OwnerReferences) != 1 || claim.Metadata.OwnerReferences[0].Kind != "Catalog" {
		t.Errorf("owners = %+v, want the Catalog", claim.Metadata.OwnerReferences)
	}
}

// A Catalog with no spec.progress block keeps today's shape: the
// progress claim takes the catalog's size and class.
func TestProgressClaimDefaultsToTheCatalogsSizeAndClass(t *testing.T) {
	catalog := housekeepingCatalog()
	catalog.Spec.Storage.StorageClassName = "local-path"

	claim := buildProgressClaim(catalog)

	if claim.Spec.StorageClassName != "local-path" {
		t.Errorf("storageClassName = %q, want the catalog's class", claim.Spec.StorageClassName)
	}
	if got := claim.Spec.Resources.Requests["storage"]; got != catalogStorageSize(catalog) {
		t.Errorf("storage = %q, want the catalog's size %q", got, catalogStorageSize(catalog))
	}
}

// A Catalog that names a claim of its own names the catalog's claim,
// never the progress store's, so the progress claim is always the one
// the operator provisions.
func TestProgressClaimStandsBesideACatalogThatNamesItsOwn(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	catalog.Spec.Storage.ClaimName = "a-claim-of-my-own"

	if err := testOperator(t, cluster).standProgressClaim(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	if cluster.heldClaim("house-catalog-progress") == nil {
		t.Error("the pass provisioned no progress claim")
	}
}

// The pod mounts the progress claim and nothing else, because the
// progress store reads no volume and no media.
func TestProgressPodMountsItsClaimAlone(t *testing.T) {
	pod := testProgressPod(housekeepingCatalog(), 0)

	if len(pod.Spec.Volumes) != 1 {
		t.Fatalf("volumes = %+v, want the progress claim alone", pod.Spec.Volumes)
	}
	volume := pod.Spec.Volumes[0]
	if volume.Name != progressVolumeName || volume.PersistentVolumeClaim == nil ||
		volume.PersistentVolumeClaim.ClaimName != "house-catalog-progress" {
		t.Errorf("volume = %+v, want the progress claim", volume)
	}
}

// The pass stands the pods and the store's one claim together, so every
// pod the kubelet schedules has a volume to bind.
func TestStandProgressPodsCreateThePodsAndTheStoresClaim(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")

	if _, err := testOperator(t, cluster).standProgressPods(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("house-catalog-progress-0") == nil {
		t.Error("the pass stood no progress pod")
	}
	if cluster.heldClaim("house-catalog-progress") == nil {
		t.Error("the pass provisioned no progress claim")
	}
}

// The list the pass writes the progress slice from reads the progress
// member label alone, so a catalog pod never reaches the progress
// cluster's peer list.
func TestListProgressMemberPodsReadsTheProgressLabelAlone(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	operator := testOperator(t, cluster)
	if _, err := operator.standProgressPod(t.Context(), catalog, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.standCatalogPod(t.Context(), catalog, 0); err != nil {
		t.Fatal(err)
	}

	list, err := ListProgressMemberPods(t.Context(), operator.client)
	if err != nil {
		t.Fatal(err)
	}

	if len(list.Items) != 1 || list.Items[0].Metadata.Name != "house-catalog-progress-0" {
		t.Errorf("pods = %+v, want the progress pod alone", list.Items)
	}
}

// The pass stands the progress cluster beside the catalog cluster, so
// one Catalog brings up both, and each cluster keeps its own Service
// and slice.
func TestReconcileCatalogsStandsTheProgressClusterToo(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	// The pods the pass reads are the ones that already stand, so the
	// slice this pass writes names a screen's progress agent and not
	// the pod this pass creates, the way the catalog slice does.
	cluster.pods["den-tv-media-browser"] = progressPodAt("den-tv-media-browser", "house", "10.42.1.9")

	testOperator(t, cluster).reconcileCatalogs(t.Context(), oneNamespace("house", catalog), nil, nil, testNow)

	if cluster.heldPod("house-catalog-progress-0") == nil {
		t.Fatal("the pass stood no progress pod")
	}
	if cluster.heldClaim("house-catalog-progress") == nil {
		t.Fatal("the pass provisioned no claim for the progress pod")
	}
	service := cluster.heldService("house", progressServiceName)
	if service == nil || len(service.Metadata.OwnerReferences) != 1 ||
		service.Metadata.OwnerReferences[0].Name != "house-catalog" {
		t.Fatalf("service = %+v, want one owned by the Catalog", service)
	}
	slice := cluster.heldEndpointSlice("house", progressServiceName)
	if slice == nil {
		t.Fatal("the pass wrote no progress EndpointSlice")
	}
	if len(slice.Endpoints) != 1 || slice.Endpoints[0].Addresses[0] != "10.42.1.9" {
		t.Errorf("endpoints = %+v, want the standing progress pod", slice.Endpoints)
	}
	if cluster.heldEndpointSlice("house", catalogServiceName) == nil {
		t.Error("the pass left the catalog slice unwritten")
	}
}

// progressPodAt is one standing progress agent, so a test hands the
// pass a peer the progress slice is written over.
func progressPodAt(name, namespace, address string) *Pod {
	return &Pod{
		Metadata: ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       name + "-uid",
			Labels:    progressPodLabels(),
		},
		Status: PodStatus{
			Phase:                 podRunning,
			PodIP:                 address,
			InitContainerStatuses: []ContainerStatus{{Name: progressContainer, Ready: true}},
			ContainerStatuses:     []ContainerStatus{{Name: recorderContainer, Ready: true}},
		},
	}
}

// A namespace with more than one Catalog stands nothing new, the
// progress cluster with the rest, because the pass cannot tell which
// Catalog owns the objects.
func TestReconcileCatalogsStandsNoProgressClusterForManyCatalogs(t *testing.T) {
	cluster := newFakeCluster()
	first := seedCatalog(cluster, "house-catalog", "house")
	second := seedCatalog(cluster, "another-catalog", "house")

	testOperator(t, cluster).reconcileCatalogs(t.Context(), oneNamespace("house", first, second), nil, nil, testNow)

	if cluster.heldPod("house-catalog-progress-0") != nil {
		t.Error("the pass stood a progress pod for a namespace with two Catalogs")
	}
}

// A pod's container names are one set, so the agent and the role beside
// it carry different names, and a pod that named both the same is
// refused at create.
func TestTheProgressPodNamesEveryContainerOnce(t *testing.T) {
	pod := testProgressPod(housekeepingCatalog(), 0)

	held := map[string]int{}
	for _, container := range append(append([]Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		held[container.Name]++
	}

	for name, count := range held {
		if count != 1 {
			t.Errorf("the pod names the container %s %d times", name, count)
		}
	}
	if len(held) != 2 {
		t.Errorf("the pod holds %d named containers, want the agent and the role", len(held))
	}
}

// A failure standing one copy ends the stand, and the pass reports it
// with the copies that already stood.
func TestStandProgressPodsReportsAFailedCopy(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	catalog.Spec.Progress.Replicas = 2
	cluster.broken["/api/v1/namespaces/house/pods/house-catalog-progress-0"] = http.StatusInternalServerError

	_, err := testOperator(t, cluster).standProgressPods(t.Context(), catalog)

	if err == nil {
		t.Fatal("err = nil, want the failure the stand could not read past")
	}
}
