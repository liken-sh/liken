package main

// What these tests read: the pod and the Service a Catalog that names a
// Jellyfin server stands, and the pair a Catalog that names none leaves
// behind.

import (
	"net/http"
	"strings"
	"testing"
)

// The Catalog every test here starts from: the housekeeping Catalog with a
// Jellyfin server on it.
func jellyfinCatalog() *NamespaceCatalog {
	catalog := housekeepingCatalog()
	catalog.Spec.Jellyfin = &CatalogJellyfin{
		URL:       "http://jellyfin.jellyfin.svc:8096",
		SecretRef: SecretKeyRef{Name: "jellyfin-api-key"},
	}
	return catalog
}

func testJellyfinPod(catalog *NamespaceCatalog) *Pod {
	return buildJellyfinPod(catalog, testScannerImage, testBusAddress,
		defaultTopicBase, defaultMediaTopicBase)
}

// The pod is named for the Catalog, owned by it, and carries a name label of
// its own, so neither gossip cluster's peer list reaches it.
func TestJellyfinPodBelongsToItsCatalog(t *testing.T) {
	pod := testJellyfinPod(jellyfinCatalog())

	if pod.Metadata.Name != "house-catalog-jellyfin" || pod.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Catalog's own jellyfin pod", pod.Metadata)
	}
	if len(pod.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the Catalog", pod.Metadata.OwnerReferences)
	}
	owner := pod.Metadata.OwnerReferences[0]
	if owner.Kind != "Catalog" || owner.Name != "house-catalog" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Catalog", owner)
	}
	if pod.Metadata.Labels[scannerLabelKey] != jellyfinLabelValue {
		t.Errorf("labels = %v, want the jellyfin name label", pod.Metadata.Labels)
	}
	for _, key := range []string{memberLabelKey, progressMemberLabelKey} {
		if _, held := pod.Metadata.Labels[key]; held {
			t.Errorf("labels = %v, want no %s on a jellyfin pod", pod.Metadata.Labels, key)
		}
	}
}

// The pod is a standing service that holds no Kubernetes credential, because
// everything it reads reaches it over the bus and from the Jellyfin server.
func TestJellyfinPodStandsWithNoCredential(t *testing.T) {
	pod := testJellyfinPod(jellyfinCatalog())

	if pod.Spec.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken is not false; the pod holds no credential")
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want the jellyfin role alone", pod.Spec.Containers)
	}
	role := pod.Spec.Containers[0]
	if role.SecurityContext == nil || role.SecurityContext.Capabilities == nil {
		t.Error("the role has no security context")
	}
	if role.Resources.Requests["cpu"] != scannerCPURequest ||
		role.Resources.Requests["memory"] != scannerMemoryRequest {
		t.Errorf("requests = %v, want the scanner's", role.Resources.Requests)
	}
}

// The role runs this operator's own image in its jellyfin mode.
func TestJellyfinPodRunsTheJellyfinRole(t *testing.T) {
	role := testJellyfinPod(jellyfinCatalog()).Spec.Containers[0]

	if role.Name != jellyfinContainer || role.Image != testScannerImage {
		t.Errorf("container = %+v, want the operator image in its jellyfin role", role)
	}
	if want := "/library-operator " + jellyfinMode; strings.Join(role.Command, " ") != want {
		t.Errorf("command = %v, want %q", role.Command, want)
	}
}

// The role learns the namespace, both topic trees, the broker, the Jellyfin
// server, and the address it listens on from its environment alone.
func TestJellyfinPodReadsItsSettingsFromTheEnvironment(t *testing.T) {
	held := envOf(testJellyfinPod(jellyfinCatalog()).Spec.Containers[0])

	cases := []struct {
		variable string
		want     string
	}{
		{libraryNamespaceVariable, "house"},
		{busAddressVariable, testBusAddress},
		{topicBaseVariable, defaultTopicBase},
		{mediaTopicBaseVariable, defaultMediaTopicBase},
		{jellyfinURLVariable, "http://jellyfin.jellyfin.svc:8096"},
		{jellyfinListenVariable, defaultJellyfinListen},
	}
	for _, one := range cases {
		t.Run(one.variable, func(t *testing.T) {
			if held[one.variable] != one.want {
				t.Errorf("%s = %q, want %q", one.variable, held[one.variable], one.want)
			}
		})
	}
}

// The API key reaches the container through a secretKeyRef, so the key never
// passes through the operator. A Catalog that names no key inside the Secret
// reads token, the key the schema defaults.
func TestJellyfinPodTakesItsKeyThroughASecretKeyRef(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{name: "the key the Catalog names", key: "api-key", want: "api-key"},
		{name: "the default key", key: "", want: defaultProviderSecretKey},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog := jellyfinCatalog()
			catalog.Spec.Jellyfin.SecretRef.Key = one.key

			role := testJellyfinPod(catalog).Spec.Containers[0]

			held := valueFromOf(role)[jellyfinAPIKeyVariable]
			if held == nil || held.SecretKeyRef == nil {
				t.Fatalf("%s = %+v, want a secretKeyRef", jellyfinAPIKeyVariable, held)
			}
			if held.SecretKeyRef.Name != "jellyfin-api-key" || held.SecretKeyRef.Key != one.want {
				t.Errorf("secretKeyRef = %+v, want the Secret's %q", held.SecretKeyRef, one.want)
			}
		})
	}
}

// valueFromOf reads a container's referenced environment as a map, so a test
// names the variable it reads rather than its place in the list.
func valueFromOf(container Container) map[string]*EnvVarSource {
	held := map[string]*EnvVarSource{}
	for _, variable := range container.Env {
		if variable.ValueFrom != nil {
			held[variable.Name] = variable.ValueFrom
		}
	}
	return held
}

// The role names the port Jellyfin posts to, and the kubelet reads its health
// on that same port, so the pod reaches the Service's endpoints only once the
// role listens.
func TestJellyfinPodNamesItsPortAndItsHealth(t *testing.T) {
	role := testJellyfinPod(jellyfinCatalog()).Spec.Containers[0]

	if len(role.Ports) != 1 {
		t.Fatalf("ports = %+v, want the webhook port alone", role.Ports)
	}
	if role.Ports[0].Name != jellyfinPortName || role.Ports[0].ContainerPort != jellyfinPort {
		t.Errorf("port = %+v, want %s on %d", role.Ports[0], jellyfinPortName, jellyfinPort)
	}
	if role.ReadinessProbe == nil || role.ReadinessProbe.HTTPGet == nil {
		t.Fatalf("readinessProbe = %+v, want an httpGet", role.ReadinessProbe)
	}
	probe := role.ReadinessProbe.HTTPGet
	if probe.Path != jellyfinProbePath || probe.Port != jellyfinPort {
		t.Errorf("readinessProbe = %+v, want %s on %d", probe, jellyfinProbePath, jellyfinPort)
	}
}

// The Service selects the pod's own labels, so the API server writes its
// endpoints from the pod's readiness, and it carries the post to the port the
// container named.
func TestJellyfinServiceReachesThePod(t *testing.T) {
	service := buildJellyfinService(jellyfinCatalog())

	if service.Metadata.Name != "house-catalog-jellyfin" || service.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Catalog's own jellyfin Service", service.Metadata)
	}
	if len(service.Metadata.OwnerReferences) != 1 ||
		service.Metadata.OwnerReferences[0].Name != "house-catalog" {
		t.Errorf("owners = %+v, want the Catalog", service.Metadata.OwnerReferences)
	}
	if service.Spec.ClusterIP != "" {
		t.Errorf("clusterIP = %q, want the address the API server assigns", service.Spec.ClusterIP)
	}
	if service.Spec.Selector[scannerLabelKey] != jellyfinLabelValue {
		t.Errorf("selector = %v, want the jellyfin pod's labels", service.Spec.Selector)
	}
	if len(service.Spec.Ports) != 1 {
		t.Fatalf("ports = %+v, want the webhook port alone", service.Spec.Ports)
	}
	port := service.Spec.Ports[0]
	if port.Port != jellyfinPort || port.TargetPort != jellyfinPortName || port.Protocol != "TCP" {
		t.Errorf("port = %+v, want %d to %s over TCP", port, jellyfinPort, jellyfinPortName)
	}
}

// A Catalog that names a Jellyfin server stands the pod and the Service, and
// one that names none stands neither.
func TestReconcileCatalogsStandsTheJellyfinPairTheCatalogAsksFor(t *testing.T) {
	cases := []struct {
		name   string
		block  *CatalogJellyfin
		stands bool
	}{
		{name: "a Catalog with a Jellyfin server", block: jellyfinCatalog().Spec.Jellyfin, stands: true},
		{name: "a Catalog with none"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := seedCatalog(cluster, "house-catalog", "house")
			catalog.Spec.Jellyfin = one.block

			testOperator(t, cluster).reconcileCatalogs(t.Context(), oneNamespace("house", catalog), nil, nil, testNow)

			if held := cluster.heldPod("house-catalog-jellyfin") != nil; held != one.stands {
				t.Errorf("the jellyfin pod stands = %v, want %v", held, one.stands)
			}
			if held := cluster.heldService("house", "house-catalog-jellyfin") != nil; held != one.stands {
				t.Errorf("the jellyfin Service stands = %v, want %v", held, one.stands)
			}
		})
	}
}

// A Catalog that drops the block loses the pair the operator stood for it.
func TestStandJellyfinTakesDownThePairACatalogNoLongerAsksFor(t *testing.T) {
	cluster := newFakeCluster()
	catalog := jellyfinCatalog()
	operator := testOperator(t, cluster)
	if err := operator.standJellyfin(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	catalog.Spec.Jellyfin = nil
	if err := operator.standJellyfin(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("house-catalog-jellyfin") != nil {
		t.Error("the jellyfin pod still stands")
	}
	if cluster.heldService("house", "house-catalog-jellyfin") != nil {
		t.Error("the jellyfin Service still stands")
	}
}

// A pod or a Service another writer gave this name is left where it is,
// because the name label is what says the operator stood it.
func TestStandJellyfinLeavesObjectsItDidNotStand(t *testing.T) {
	cluster := newFakeCluster()
	cluster.pods["house-catalog-jellyfin"] = &Pod{
		Metadata: ObjectMeta{Name: "house-catalog-jellyfin", Namespace: "house"},
	}
	cluster.holdService(&Service{
		Metadata: ObjectMeta{Name: "house-catalog-jellyfin", Namespace: "house"},
	})
	catalog := housekeepingCatalog()

	if err := testOperator(t, cluster).standJellyfin(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("house-catalog-jellyfin") == nil {
		t.Error("the pass deleted a pod it did not stand")
	}
	if cluster.heldService("house", "house-catalog-jellyfin") == nil {
		t.Error("the pass deleted a Service it did not stand")
	}
}

// A pod built from a different template is stale, so the pass deletes it, and
// the pass after that creates the replacement.
func TestStandJellyfinReplacesAStalePod(t *testing.T) {
	cluster := newFakeCluster()
	catalog := jellyfinCatalog()
	stale := testJellyfinPod(catalog)
	stale.Metadata.Annotations = map[string]string{templateHashAnnotation: "an-older-template"}
	cluster.pods["house-catalog-jellyfin"] = stale
	operator := testOperator(t, cluster)

	if err := operator.standJellyfin(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
	if cluster.heldPod("house-catalog-jellyfin") != nil {
		t.Fatal("the stale pod still stands")
	}

	if err := operator.standJellyfin(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	replacement := cluster.heldPod("house-catalog-jellyfin")
	if replacement == nil {
		t.Fatal("no replacement pod was created")
	}
	if replacement.Metadata.Annotations[templateHashAnnotation] == "an-older-template" {
		t.Error("the replacement carries the stale hash")
	}
}

// The Service is written on divergence alone, so a second pass over an
// unchanged Catalog writes nothing, and a selector changed by hand is written
// back.
func TestStandJellyfinServiceWritesOnDivergenceAlone(t *testing.T) {
	cluster := newFakeCluster()
	catalog := jellyfinCatalog()
	operator := testOperator(t, cluster)
	if err := operator.standJellyfinService(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	if err := operator.standJellyfinService(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests(http.MethodPut, "services"); got != 0 {
		t.Errorf("the pass sent %d updates for an unchanged Service", got)
	}

	cluster.heldService("house", "house-catalog-jellyfin").Spec.Selector = map[string]string{"app": "mine"}
	if err := operator.standJellyfinService(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}

	written := cluster.heldService("house", "house-catalog-jellyfin")
	if written.Spec.Selector[scannerLabelKey] != jellyfinLabelValue {
		t.Errorf("selector = %v, want the jellyfin pod's labels back", written.Spec.Selector)
	}
}

// The paths of the pair, so a test breaks one of them and reads the failure
// the pass reports.
const (
	jellyfinPodPath     = "/api/v1/namespaces/house/pods/house-catalog-jellyfin"
	jellyfinServicePath = "/api/v1/namespaces/house/services/house-catalog-jellyfin"
)

// A failure standing either half ends the stand, and the pass reports it
// rather than carrying on with half a pair.
func TestStandJellyfinReportsAFailure(t *testing.T) {
	cases := []struct {
		name   string
		broken string
	}{
		{name: "the pod", broken: jellyfinPodPath},
		{name: "the Service", broken: jellyfinServicePath},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.broken[one.broken] = http.StatusInternalServerError

			err := testOperator(t, cluster).standJellyfin(t.Context(), jellyfinCatalog())

			if err == nil {
				t.Fatal("err = nil, want the failure the stand could not read past")
			}
		})
	}
}

// A failure reading or deleting either half ends the retire, and the pass
// reports it.
func TestRetireJellyfinReportsAFailure(t *testing.T) {
	cases := []struct {
		name   string
		broken string
	}{
		{name: "reading the pod", broken: http.MethodGet + " " + jellyfinPodPath},
		{name: "deleting the pod", broken: http.MethodDelete + " " + jellyfinPodPath},
		{name: "reading the Service", broken: http.MethodGet + " " + jellyfinServicePath},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			operator := testOperator(t, cluster)
			if err := operator.standJellyfin(t.Context(), jellyfinCatalog()); err != nil {
				t.Fatal(err)
			}
			cluster.broken[one.broken] = http.StatusInternalServerError

			err := operator.standJellyfin(t.Context(), housekeepingCatalog())

			if err == nil {
				t.Fatal("err = nil, want the failure the retire could not read past")
			}
		})
	}
}

// A create another writer got to first is a conflict, which is success: the
// next pass reads the Service that writer left.
func TestStandJellyfinServiceTakesAConflictAsSuccess(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true

	if err := testOperator(t, cluster).standJellyfinService(t.Context(), jellyfinCatalog()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldService("house", "house-catalog-jellyfin") != nil {
		t.Error("the pass stood a Service the API server refused")
	}
}

// The comparison reads the owners, the labels, the selector, and the ports,
// so a Service that differs in any of them is written again.
func TestSameJellyfinServiceReadsWhatTheOperatorStates(t *testing.T) {
	desired := buildJellyfinService(jellyfinCatalog())

	cases := []struct {
		name string
		live func(*Service)
		same bool
	}{
		{name: "the Service the pass built", live: func(*Service) {}, same: true},
		{name: "an address the API server assigned", same: true, live: func(s *Service) {
			s.Spec.ClusterIP = "10.43.0.7"
		}},
		{name: "another owner", live: func(s *Service) { s.Metadata.OwnerReferences = nil }},
		{name: "another label set", live: func(s *Service) { s.Metadata.Labels = nil }},
		{name: "another selector", live: func(s *Service) { s.Spec.Selector = nil }},
		{name: "another port", live: func(s *Service) { s.Spec.Ports[0].Port = 9090 }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			live := buildJellyfinService(jellyfinCatalog())
			one.live(live)

			if got := sameJellyfinService(live, desired); got != one.same {
				t.Errorf("same = %v, want %v", got, one.same)
			}
		})
	}
}
