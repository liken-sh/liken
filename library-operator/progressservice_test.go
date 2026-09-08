package main

// These tests read the progress Service and EndpointSlice of one
// namespace: that they stand on the progress cluster's own port, that
// they are owned by the same Catalog the catalog Service is, and that
// the pass writes both.

import (
	"encoding/json"
	"strings"
	"testing"
)

// The progress Service is headless on the progress cluster's own port,
// so a screen pod that runs both agents reaches two names and two
// ports.
func TestProgressServiceIsHeadlessOnItsOwnPort(t *testing.T) {
	service := buildProgressService(testLibraryNamespace, nil)

	if service.Metadata.Name != progressServiceName || service.Metadata.Namespace != testLibraryNamespace {
		t.Errorf("metadata = %+v, want the progress Service in the namespace", service.Metadata)
	}
	if service.Spec.ClusterIP != headlessClusterIP || !service.Spec.PublishNotReadyAddresses {
		t.Errorf("spec = %+v, want a headless Service that publishes not-ready addresses", service.Spec)
	}
	want := ServicePort{Name: catalogPortName, Protocol: catalogPortProtocol, Port: progressPort, TargetPort: "8788"}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0] != want {
		t.Errorf("ports = %+v, want %+v", service.Spec.Ports, want)
	}
	if progressPort == catalogPort {
		t.Error("the two clusters gossip on one port, so an agent joins the wrong one")
	}
}

// The progress Service states no selector, because this operator writes
// the slice behind it.
func TestProgressServiceNamesNoSelector(t *testing.T) {
	body, err := json.Marshal(buildProgressService(testLibraryNamespace, nil))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(body), "selector") {
		t.Errorf("body = %s, want no selector at all", body)
	}
}

// The pass writes the progress Service where there is none, owned by
// the namespace's one Catalog.
func TestStandProgressServiceCreatesTheService(t *testing.T) {
	cluster := newFakeCluster()
	owners := []OwnerReference{catalogOwner("house-catalog", "house-catalog-uid")}

	if err := testOperator(t, cluster).standProgressService(t.Context(), testLibraryNamespace, owners); err != nil {
		t.Fatal(err)
	}

	service := cluster.heldService(testLibraryNamespace, progressServiceName)
	if service == nil {
		t.Fatal("the pass wrote no progress Service")
	}
	if len(service.Metadata.OwnerReferences) != 1 || service.Metadata.OwnerReferences[0] != owners[0] {
		t.Errorf("owners = %+v, want the Catalog", service.Metadata.OwnerReferences)
	}
	if service.Spec.Ports[0].Port != progressPort {
		t.Errorf("port = %d, want the progress cluster's %d", service.Spec.Ports[0].Port, progressPort)
	}
}

// The slice holds one endpoint per progress pod of the namespace, and
// it names the progress port, so an agent of one namespace never joins
// another's cluster.
func TestProgressEndpointsHoldTheNamespacesProgressPods(t *testing.T) {
	members := []Pod{
		{Metadata: ObjectMeta{Name: "house-progress", Namespace: testLibraryNamespace, UID: "one"},
			Status: PodStatus{PodIP: "10.0.0.2", Phase: podRunning}},
		{Metadata: ObjectMeta{Name: "loft-progress", Namespace: "loft", UID: "two"},
			Status: PodStatus{PodIP: "10.0.0.3", Phase: podRunning}},
	}

	slice := buildProgressEndpoints(testLibraryNamespace, nil, members)

	if slice.Metadata.Name != progressServiceName {
		t.Errorf("name = %q, want %q", slice.Metadata.Name, progressServiceName)
	}
	if slice.Metadata.Labels[serviceNameLabel] != progressServiceName {
		t.Errorf("labels = %+v, want the progress Service name", slice.Metadata.Labels)
	}
	if len(slice.Endpoints) != 1 || slice.Endpoints[0].Addresses[0] != "10.0.0.2" {
		t.Errorf("endpoints = %+v, want the one pod in the namespace", slice.Endpoints)
	}
	if len(slice.Ports) != 1 || slice.Ports[0].Port != progressPort {
		t.Errorf("ports = %+v, want the progress port %d", slice.Ports, progressPort)
	}
}

// The pass writes the slice where there is none, so the agents of a
// fresh namespace find each other on the pass that stood them.
func TestStandProgressEndpointsWritesTheSlice(t *testing.T) {
	cluster := newFakeCluster()
	owners := []OwnerReference{catalogOwner("house-catalog", "house-catalog-uid")}
	members := []Pod{{
		Metadata: ObjectMeta{Name: "house-progress", Namespace: testLibraryNamespace, UID: "one"},
		Status:   PodStatus{PodIP: "10.0.0.2", Phase: podRunning},
	}}

	if err := testOperator(t, cluster).standProgressEndpoints(
		t.Context(), testLibraryNamespace, owners, members); err != nil {
		t.Fatal(err)
	}

	slice := cluster.heldEndpointSlice(testLibraryNamespace, progressServiceName)
	if slice == nil {
		t.Fatal("the pass wrote no progress EndpointSlice")
	}
	if len(slice.Endpoints) != 1 || slice.Endpoints[0].Addresses[0] != "10.0.0.2" {
		t.Errorf("endpoints = %+v, want the progress pod", slice.Endpoints)
	}
}

// The two clusters keep separate slices, so the catalog agents and the
// progress agents never read each other's peer list.
func TestTheTwoClustersKeepSeparateSlices(t *testing.T) {
	if progressServiceName == catalogServiceName {
		t.Fatal("the two Services share one name")
	}
	if progressMemberLabelKey == memberLabelKey {
		t.Error("the two member labels share one key, so one selector answers both")
	}
}

// The slice reads readiness from the agent's own container, so a
// progress agent that is up is not marked not-ready because a catalog
// agent of that name is absent.
func TestTheProgressSliceReadsItsOwnAgentsReadiness(t *testing.T) {
	members := []Pod{{
		Metadata: ObjectMeta{Name: "den-tv-media-browser", Namespace: testLibraryNamespace, UID: "one"},
		Status: PodStatus{
			PodIP: "10.0.0.2", Phase: podRunning,
			InitContainerStatuses: []ContainerStatus{{Name: progressContainer, Ready: true}},
			ContainerStatuses:     []ContainerStatus{{Name: recorderContainer, Ready: true}},
		},
	}}

	slice := buildProgressEndpoints(testLibraryNamespace, nil, members)

	if len(slice.Endpoints) != 1 || !slice.Endpoints[0].Conditions.Ready {
		t.Errorf("endpoints = %+v, want a ready progress agent", slice.Endpoints)
	}
}
