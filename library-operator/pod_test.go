package main

// These tests read the pod the operator would send to the API server.
// The pod is the whole of what a Library becomes at run time, so what
// it carries is worth reading field by field: the mount that makes the
// volume read-only, the environment the scanner learns its Library
// from, and the address the catalog agent gossips on.

import (
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

// houseFilms is the Library every test in this package starts from: a
// films library over a claim in the house namespace.
func houseFilms() *Library {
	return &Library{
		Metadata: ObjectMeta{
			Name:            "films",
			Namespace:       "house",
			UID:             "library-uid",
			Generation:      3,
			ResourceVersion: "9",
		},
		Spec: LibrarySpec{
			Storage: LibraryStorage{Claim: "films", Root: "/movies"},
			Kind:    libraryKindFilms,
			Films:   &LibrarySettings{},
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
	pod := testScannerPod(houseFilms())

	if pod.Metadata.Name != "films-scanner" {
		t.Errorf("name = %q, want films-scanner", pod.Metadata.Name)
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
	if owner.Name != "films" || owner.UID != "library-uid" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Library films", owner)
	}
	if pod.Metadata.Labels[scannerLabelKey] != scannerLabelValue {
		t.Errorf("labels = %v, want the scanner name label", pod.Metadata.Labels)
	}
	if pod.Metadata.Labels[libraryLabelKey] != "films" {
		t.Errorf("labels = %v, want the library label", pod.Metadata.Labels)
	}
}

// A scanner is a standing service, so the pod restarts in place, and
// the grace period is long enough for the catalog agent to finish its
// exit.
func TestScannerPodStandsAndStopsSlowly(t *testing.T) {
	pod := testScannerPod(houseFilms())

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
	pod := testScannerPod(houseFilms())

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
	if source.PersistentVolumeClaim.ClaimName != "films" || !source.PersistentVolumeClaim.ReadOnly {
		t.Errorf("claim = %+v, want films read-only", source.PersistentVolumeClaim)
	}
}

func TestScannerContainerCarriesTheLibrarysEnvironment(t *testing.T) {
	pod := testScannerPod(houseFilms())

	want := map[string]string{
		libraryNamespaceVariable: "house",
		libraryNameVariable:      "films",
		libraryKindVariable:      libraryKindFilms,
		libraryRootVariable:      "/movies",
		busAddressVariable:       testBusAddress,
		topicBaseVariable:        defaultTopicBase,
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
	library := houseFilms()
	library.Spec.Films.Image = "registry.example/my-scanner:1"

	pod := testScannerPod(library)

	if pod.Spec.Containers[0].Image != "registry.example/my-scanner:1" {
		t.Errorf("image = %q, want the settings block's own", pod.Spec.Containers[0].Image)
	}
}

// The catalog agent announces the pod's own address to its peers, and
// nothing knows that address until the kubelet assigns it, so the
// downward API reads it into POD_IP and the gossip address is built
// from that variable.
func TestCatalogContainerGossipsOnThePodsAddress(t *testing.T) {
	pod := testScannerPod(houseFilms())

	catalog := pod.Spec.Containers[1]
	if catalog.Name != catalogContainer {
		t.Fatalf("second container = %q, want %s", catalog.Name, catalogContainer)
	}
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

// The catalog is derived from a walk, so its database lives in a
// directory the kubelet creates with the pod and deletes with it.
func TestCatalogContainerWritesToAnEmptyDirectory(t *testing.T) {
	pod := testScannerPod(houseFilms())

	catalog := pod.Spec.Containers[1]
	if len(catalog.VolumeMounts) != 1 {
		t.Fatalf("volumeMounts = %v, want one", catalog.VolumeMounts)
	}
	mount := catalog.VolumeMounts[0]
	if mount.MountPath != catalogStatePath || mount.ReadOnly {
		t.Errorf("mount = %+v, want %s writable", mount, catalogStatePath)
	}
	if podVolume(t, pod, mount.Name).EmptyDir == nil {
		t.Errorf("volume %s is not an emptyDir", mount.Name)
	}
}

// Neither container needs a capability, and neither may gain one.
func TestBothContainersDropEveryCapability(t *testing.T) {
	for _, container := range testScannerPod(houseFilms()).Spec.Containers {
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
	pod := testScannerPod(houseFilms())
	for index, one := range cases {
		t.Run(one.container, func(t *testing.T) {
			resources := pod.Spec.Containers[index].Resources
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
