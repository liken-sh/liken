package main

// These tests read the pod the operator would send to the API server.
// The pod is the whole of what a Library becomes at run time, so what
// it carries is worth reading field by field: the mount that makes the
// volume read-only, the environment the scanner learns its Library
// from, and the address the catalog agent gossips on. The four pod
// requests the client makes are tested here too, beside the pod they
// carry.

import (
	"net/http"
	"strings"
	"testing"
)

// The images the tests stamp, named once so a change to one reads in
// one place.
const (
	testScannerImage   = "ghcr.io/liken-sh/library-operator:test"
	testCorrosionImage = "ghcr.io/liken-sh/library-operator-corrosion:test"
	testBusAddress     = "bus.liken-system.svc:1883"
)

// studioMovies is the Library every test in this package starts from: a
// movies library over a claim in the house namespace.
func studioMovies() *Library {
	return &Library{
		Metadata: ObjectMeta{
			Name:            "movies",
			Namespace:       "house",
			UID:             "library-uid",
			Generation:      3,
			ResourceVersion: "9",
		},
		Spec: LibrarySpec{
			Storage: LibraryStorage{Claim: "movies", Root: "/movies"},
			Kind:    libraryKindMovies,
			Movies:  &LibrarySettings{},
		},
	}
}

func testScannerPod(library *Library) *Pod {
	return buildScannerPod(library, testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
}

// The pod's name, namespace, owner, and marks are what tie it to the
// Library: the owner reference is the whole teardown, and the labels
// are what the pod watch and a person's kubectl select on.
func TestScannerPodBelongsToItsLibrary(t *testing.T) {
	pod := testScannerPod(studioMovies())

	if pod.Metadata.Name != "movies-scanner" {
		t.Errorf("name = %q, want movies-scanner", pod.Metadata.Name)
	}
	if pod.Metadata.Namespace != "house" {
		t.Errorf("namespace = %q, want house", pod.Metadata.Namespace)
	}
	if len(pod.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %v, want one", pod.Metadata.OwnerReferences)
	}
	owner := pod.Metadata.OwnerReferences[0]
	if owner.APIVersion != libraryAPIVersion || owner.Kind != "Library" {
		t.Errorf("owner = %+v, want the Library kind", owner)
	}
	if owner.Name != "movies" || owner.UID != "library-uid" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Library movies", owner)
	}
	if pod.Metadata.Labels[scannerLabelKey] != scannerLabelValue {
		t.Errorf("labels = %v, want the scanner name label", pod.Metadata.Labels)
	}
	if pod.Metadata.Labels[libraryLabelKey] != "movies" {
		t.Errorf("labels = %v, want the library label", pod.Metadata.Labels)
	}
}

// A scanner is a standing service, so the pod restarts in place, and
// the grace period is long enough for the catalog agent to finish its
// exit.
func TestScannerPodStandsAndStopsSlowly(t *testing.T) {
	pod := testScannerPod(studioMovies())

	if pod.Spec.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil {
		t.Fatal("terminationGracePeriodSeconds is unset")
	}
	if *pod.Spec.TerminationGracePeriodSeconds != scannerGracePeriod {
		t.Errorf("terminationGracePeriodSeconds = %d, want %d",
			*pod.Spec.TerminationGracePeriodSeconds, scannerGracePeriod)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken is not false; the scanner holds no credential")
	}
}

// The scanner runs this same image in its scan role, mounts the claim
// read-only, and learns its Library from the environment alone.
func TestScannerContainerReadsTheVolumeReadOnly(t *testing.T) {
	pod := testScannerPod(studioMovies())

	scanner := pod.Spec.Containers[0]
	if scanner.Name != scannerContainer {
		t.Fatalf("first container = %q, want %s", scanner.Name, scannerContainer)
	}
	if scanner.Image != testScannerImage {
		t.Errorf("image = %q, want %q", scanner.Image, testScannerImage)
	}
	if strings.Join(scanner.Command, " ") != "/library-operator scan" {
		t.Errorf("command = %v, want the scan role", scanner.Command)
	}
	if len(scanner.VolumeMounts) != 1 {
		t.Fatalf("volumeMounts = %v, want one", scanner.VolumeMounts)
	}
	mount := scanner.VolumeMounts[0]
	if mount.MountPath != libraryMountPath || !mount.ReadOnly {
		t.Errorf("mount = %+v, want %s read-only", mount, libraryMountPath)
	}

	source := podVolume(t, pod, mount.Name)
	if source.PersistentVolumeClaim == nil {
		t.Fatalf("volume %s = %+v, want the Library's claim", mount.Name, source)
	}
	if source.PersistentVolumeClaim.ClaimName != "movies" || !source.PersistentVolumeClaim.ReadOnly {
		t.Errorf("claim = %+v, want movies read-only", source.PersistentVolumeClaim)
	}
}

// The scanner container declares the webhook port, so a person who reads the
// pod finds the port the Service sends to.
func TestScannerContainerDeclaresTheWebhookPort(t *testing.T) {
	pod := testScannerPod(studioMovies())

	scanner := pod.Spec.Containers[0]
	want := ContainerPort{Name: webhookPortName, ContainerPort: webhookPort, Protocol: webhookPortProtocol}
	if len(scanner.Ports) != 1 || scanner.Ports[0] != want {
		t.Errorf("ports = %+v, want %+v", scanner.Ports, want)
	}
	if catalog := pod.Spec.InitContainers[0]; len(catalog.Ports) != 0 {
		t.Errorf("the catalog agent declares %+v, want no port", catalog.Ports)
	}
}

func TestScannerContainerCarriesTheLibrarysEnvironment(t *testing.T) {
	pod := testScannerPod(studioMovies())

	want := map[string]string{
		libraryNamespaceVariable: "house",
		libraryNameVariable:      "movies",
		libraryKindVariable:      libraryKindMovies,
		libraryRootVariable:      "/movies",
		busAddressVariable:       testBusAddress,
		topicBaseVariable:        defaultTopicBase,
		catalogAPIVariable:       defaultCatalogAPI,
		libraryIgnoreVariable:    "null",
	}
	got := containerEnvironment(pod.Spec.Containers[0])
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("environment = %v, want exactly %d variables", got, len(want))
	}
}

// A settings block that names an image runs that image, which is how a
// person supplies a scanner of their own.
func TestScannerContainerPrefersTheSettingsImage(t *testing.T) {
	library := studioMovies()
	library.Spec.Movies.Image = "registry.example/my-scanner:1"

	pod := testScannerPod(library)

	if pod.Spec.Containers[0].Image != "registry.example/my-scanner:1" {
		t.Errorf("image = %q, want the settings block's own", pod.Spec.Containers[0].Image)
	}
}

// The ignore list travels as one JSON value, so the scanner reads the
// folders to skip whole, whatever characters a folder name holds.
func TestScannerContainerCarriesTheIgnoreList(t *testing.T) {
	library := studioMovies()
	library.Spec.Ignore = []string{"#recycle", ".incoming"}

	pod := testScannerPod(library)

	got := containerEnvironment(pod.Spec.Containers[0])
	if got[libraryIgnoreVariable] != `["#recycle",".incoming"]` {
		t.Errorf("%s = %q, want the JSON-encoded list", libraryIgnoreVariable, got[libraryIgnoreVariable])
	}
}

// The catalog agent announces the pod's own address to its peers, and
// nothing knows that address until the kubelet assigns it, so the
// downward API reads it into POD_IP and the gossip address is built
// from that variable.
func TestCatalogContainerGossipsOnThePodsAddress(t *testing.T) {
	pod := testScannerPod(studioMovies())

	catalog := catalogSidecarOf(t, pod)
	if catalog.Image != testCorrosionImage {
		t.Errorf("image = %q, want %q", catalog.Image, testCorrosionImage)
	}
	if len(catalog.Env) != 2 {
		t.Fatalf("env = %v, want the address and the gossip variable", catalog.Env)
	}
	if catalog.Env[0].Name != podIPVariable {
		t.Errorf("env[0] = %+v, want %s first", catalog.Env[0], podIPVariable)
	}
	if catalog.Env[0].ValueFrom == nil || catalog.Env[0].ValueFrom.FieldRef == nil {
		t.Fatalf("env[0] = %+v, want a field reference", catalog.Env[0])
	}
	if catalog.Env[0].ValueFrom.FieldRef.FieldPath != "status.podIP" {
		t.Errorf("fieldPath = %q, want status.podIP", catalog.Env[0].ValueFrom.FieldRef.FieldPath)
	}
	if catalog.Env[1].Name != gossipAddressVariable {
		t.Errorf("env[1] = %+v, want %s second", catalog.Env[1], gossipAddressVariable)
	}
	if catalog.Env[1].Value != "$(POD_IP):8787" {
		t.Errorf("gossip address = %q, want $(POD_IP):8787", catalog.Env[1].Value)
	}
}

// The catalog agent writes its database on a durable claim, so a pod
// that rolls starts from the catalog its claim holds rather than
// re-syncing the whole namespace over gossip. The claim is mounted
// writable, and it is the Library's own catalog claim.
func TestCatalogContainerWritesToItsDurableClaim(t *testing.T) {
	pod := testScannerPod(studioMovies())

	catalog := catalogSidecarOf(t, pod)
	if len(catalog.VolumeMounts) != 1 {
		t.Fatalf("volumeMounts = %v, want one", catalog.VolumeMounts)
	}
	mount := catalog.VolumeMounts[0]
	if mount.MountPath != catalogStatePath || mount.ReadOnly {
		t.Errorf("mount = %+v, want %s writable", mount, catalogStatePath)
	}
	source := podVolume(t, pod, mount.Name).PersistentVolumeClaim
	if source == nil {
		t.Fatalf("volume %s is not a claim", mount.Name)
	}
	if source.ClaimName != "movies-catalog-v2" {
		t.Errorf("claimName = %q, want the Library's catalog claim", source.ClaimName)
	}
	if source.ReadOnly {
		t.Error("the catalog claim is mounted read-only, but the agent writes it")
	}
}

// The catalog agent is a native sidecar: an initContainer with
// restartPolicy Always, so the kubelet starts it and passes its
// startupProbe before it starts the scanner. The scanner is the only
// ordinary container, so its first walk cannot race a catalog API that
// is not listening.
func TestCatalogContainerIsANativeSidecar(t *testing.T) {
	pod := testScannerPod(studioMovies())

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("initContainers = %v, want the catalog agent alone", pod.Spec.InitContainers)
	}
	catalog := pod.Spec.InitContainers[0]
	if catalog.Name != catalogContainer {
		t.Errorf("initContainer = %q, want %s", catalog.Name, catalogContainer)
	}
	if catalog.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", catalog.RestartPolicy)
	}
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Name != scannerContainer {
		t.Errorf("containers = %v, want the scanner alone", pod.Spec.Containers)
	}
}

// The startupProbe and the livenessProbe run a query inside the
// container, because the catalog agent's API binds loopback alone and
// nothing the kubelet dials over the pod network reaches it. The
// startupProbe gates the scanner's start, and the livenessProbe covers
// the agent's running life.
func TestCatalogContainerProbesWithAnExecQuery(t *testing.T) {
	catalog := catalogSidecarOf(t, testScannerPod(studioMovies()))

	cases := []struct {
		name             string
		probe            *Probe
		period           int
		failureThreshold int
	}{
		{"startup", catalog.StartupProbe, 3, 30},
		{"liveness", catalog.LivenessProbe, 30, 3},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if one.probe == nil {
				t.Fatal("the probe is unset")
			}
			if one.probe.Exec == nil {
				t.Fatalf("probe = %+v, want an exec query", one.probe)
			}
			if strings.Join(one.probe.Exec.Command, " ") != "/corrosion query SELECT 1" {
				t.Errorf("command = %v, want the catalog query", one.probe.Exec.Command)
			}
			if one.probe.PeriodSeconds != one.period {
				t.Errorf("periodSeconds = %d, want %d", one.probe.PeriodSeconds, one.period)
			}
			if one.probe.FailureThreshold != one.failureThreshold {
				t.Errorf("failureThreshold = %d, want %d", one.probe.FailureThreshold, one.failureThreshold)
			}
		})
	}
}

// Neither container needs a capability, and neither may gain one.
func TestBothContainersDropEveryCapability(t *testing.T) {
	pod := testScannerPod(studioMovies())
	both := append(append([]Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, container := range both {
		t.Run(container.Name, func(t *testing.T) {
			security := container.SecurityContext
			if security == nil || security.Capabilities == nil {
				t.Fatalf("securityContext = %+v, want the capabilities dropped", security)
			}
			if strings.Join(security.Capabilities.Drop, ",") != "ALL" {
				t.Errorf("drop = %v, want ALL", security.Capabilities.Drop)
			}
			if security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
				t.Errorf("allowPrivilegeEscalation = %+v, want false", security.AllowPrivilegeEscalation)
			}
		})
	}
}

// The scanner walks a volume and the catalog agent holds a database,
// so the two ask for different room. The requests are what the
// scheduler places the pod by, and the limits are what the kubelet
// holds each container to.
func TestContainersAskForTheirOwnRoom(t *testing.T) {
	cases := []struct {
		container   string
		cpuRequest  string
		memoryLimit string
	}{
		{container: scannerContainer, cpuRequest: "10m", memoryLimit: "64Mi"},
		{container: catalogContainer, cpuRequest: "10m", memoryLimit: "512Mi"},
	}
	pod := testScannerPod(studioMovies())
	for _, one := range cases {
		t.Run(one.container, func(t *testing.T) {
			resources := podContainer(t, pod, one.container).Resources
			if resources.Requests["cpu"] != one.cpuRequest {
				t.Errorf("cpu request = %q, want %q", resources.Requests["cpu"], one.cpuRequest)
			}
			if resources.Limits["memory"] != one.memoryLimit {
				t.Errorf("memory limit = %q, want %q", resources.Limits["memory"], one.memoryLimit)
			}
			if resources.Requests["memory"] == "" {
				t.Error("memory request is unset")
			}
			if _, capped := resources.Limits["cpu"]; capped {
				t.Errorf("limits = %v, want no cpu limit", resources.Limits)
			}
		})
	}
}

// podContainer reads one container the pod carries by name, across the
// scanner in containers and the catalog agent in initContainers, so a
// test names a container and never an index into either list.
func podContainer(t *testing.T, pod *Pod, name string) Container {
	t.Helper()
	both := append(append([]Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, container := range both {
		if container.Name == name {
			return container
		}
	}
	t.Fatalf("the pod carries no container named %s", name)
	return Container{}
}

// catalogSidecarOf reads the catalog agent, which the pod carries as a
// native sidecar in initContainers.
func catalogSidecarOf(t *testing.T, pod *Pod) Container {
	t.Helper()
	return podContainer(t, pod, catalogContainer)
}

// podVolume reads the pod volume one mount names, so a test proves the
// mount and the source it points at together.
func podVolume(t *testing.T, pod *Pod, name string) Volume {
	t.Helper()
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == name {
			return volume
		}
	}
	t.Fatalf("the pod carries no volume named %s", name)
	return Volume{}
}

// containerEnvironment reads a container's literal environment, the
// variables with a value the operator wrote itself.
func containerEnvironment(container Container) map[string]string {
	environment := map[string]string{}
	for _, variable := range container.Env {
		environment[variable.Name] = variable.Value
	}
	return environment
}

// One list answers every namespace, and the label selector is what
// keeps the answer to this operator's own pods.
func TestListScannerPodsSelectsTheOperatorsOwnPods(t *testing.T) {
	client, recorded := recordingAPI(t, PodList{
		Metadata: ListMeta{ResourceVersion: "88"},
		Items:    []Pod{{Metadata: ObjectMeta{Name: "movies-scanner", Namespace: "house"}}},
	})

	list, err := ListScannerPods(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/pods")
	if got := recorded.query.Get("labelSelector"); got != "app.kubernetes.io/name=library-scanner" {
		t.Errorf("labelSelector = %q, want the scanner selector", got)
	}
	if len(list.Items) != 1 || list.Items[0].Metadata.Name != "movies-scanner" {
		t.Errorf("items = %+v, want the one scanner pod the server answered", list.Items)
	}
}

func TestGetPodReadsOnePodByName(t *testing.T) {
	client, recorded := recordingAPI(t, Pod{
		Metadata: ObjectMeta{Name: "movies-scanner", Namespace: "house"},
		Status: PodStatus{
			Phase:             podRunning,
			ContainerStatuses: []ContainerStatus{{Name: "scanner", Ready: true}},
		},
	})

	pod, err := GetPod(t.Context(), client, "house", "movies-scanner")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/namespaces/house/pods/movies-scanner")
	if pod.Status.Phase != podRunning || !pod.Status.ContainerStatuses[0].Ready {
		t.Errorf("status = %+v, want the running pod with its ready container", pod.Status)
	}
}

func TestCreatePodPostsIntoTheLibrarysNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, Pod{Metadata: ObjectMeta{Name: "movies-scanner", Namespace: "house"}})

	created, err := CreatePod(t.Context(), client, &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:      "movies-scanner",
			Namespace: "house",
			Labels:    map[string]string{scannerLabelKey: scannerLabelValue},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPost, "/api/v1/namespaces/house/pods")
	if !strings.Contains(recorded.body, `"name":"movies-scanner"`) {
		t.Errorf("body = %s, want the pod the operator built", recorded.body)
	}
	if created.Metadata.Name != "movies-scanner" {
		t.Errorf("name = %q, want the pod the server wrote back", created.Metadata.Name)
	}
}

// A pod the operator already removed, or one Kubernetes removed first,
// leaves nothing to do, so an absent pod is success. Any other failure
// is reported.
func TestDeletePodTreatsAnAbsentPodAsDone(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "the pod was there", status: http.StatusOK},
		{name: "the pod was already gone", status: http.StatusNotFound},
		{name: "the server refused", status: http.StatusForbidden, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var path string
			client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.WriteHeader(testCase.status)
			}))

			err := DeletePod(t.Context(), client, "house", "movies-scanner")

			if (err != nil) != testCase.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, testCase.wantErr)
			}
			if path != "/api/v1/namespaces/house/pods/movies-scanner" {
				t.Errorf("path = %q, want the pod's own path", path)
			}
		})
	}
}
