package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/drivers"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func simulator(kind observatory.Kind, name, driver string) *device {
	d := &device{kind: kind}
	d.object.Metadata = observatory.ObjectMeta{Name: name, Namespace: "observatory", UID: "uid-" + name}
	d.object.Spec.Driver = observatory.Driver{Name: driver}
	return d
}

// repository drops an image's tag.
func repository(image string) string {
	name, _, _ := strings.Cut(image, ":")
	return name
}

func eastDevices() []*device {
	return []*device{
		simulator(observatory.MountKind, "east", "indi_simulator_telescope"),
		simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd"),
		simulator(observatory.FocuserKind, "east", "indi_simulator_focus"),
	}
}

var east = serverRef{observatory.TelescopeKind, "east"}

var wantSecurity = &securityContext{
	RunAsNonRoot: true, RunAsUser: 1000, RunAsGroup: 1000,
	AllowPrivilegeEscalation: false, ReadOnlyRootFilesystem: true,
	Capabilities: &capabilities{Drop: []string{"ALL"}},
}

func TestADevicePodServesItsDriverThroughSocat(t *testing.T) {
	t.Parallel()
	for _, d := range eastDevices() {
		t.Run(d.name(), func(t *testing.T) {
			t.Parallel()
			p, svc, claim, err := devicePod("observatory", east, d, false, "")
			if err != nil {
				t.Fatalf("building the pod of %s: %v", d.name(), err)
			}
			if claim != nil {
				t.Errorf("the simulator has the claim template %+v, want none", claim)
			}
			if len(p.Spec.Containers) != 1 {
				t.Fatalf("the pod has the containers %+v, want one", p.Spec.Containers)
			}
			c := p.Spec.Containers[0]
			cases := []struct {
				what      string
				got, want any
			}{
				{"command", c.Command, []string{"/usr/bin/socat"}},
				{"args", c.Args, []string{"TCP-LISTEN:7625,reuseaddr", "EXEC:" + d.object.Spec.Driver.Name + ",pipes"}},
				{"ports", c.Ports, []containerPort{{Name: "driver", ContainerPort: 7625}}},
				{"security", c.SecurityContext, wantSecurity},
				{"mounts", c.VolumeMounts, []volumeMount{{Name: "tmp", MountPath: "/tmp"}}},
				{"volumes", p.Spec.Volumes, []volume{{Name: "tmp", EmptyDir: &emptyDir{}}}},
				{"image", repository(c.Image), "ghcr.io/liken-sh/indi-simulators"},
				{"probe", c.ReadinessProbe, (*probe)(nil)},
				{"restart policy", p.Spec.RestartPolicy, "Always"},
				{"service ports", svc.Spec.Ports, []servicePort{{Name: "driver", Port: 7625, TargetPort: "driver"}}},
				{"service selector", svc.Spec.Selector, map[string]string{labelName: p.Metadata.Name}},
				{"pod label", p.Metadata.Labels[labelName], p.Metadata.Name},
			}
			for _, c := range cases {
				if !reflect.DeepEqual(c.got, c.want) {
					t.Errorf("%s = %s, want %s", c.what, mustJSON(c.got), mustJSON(c.want))
				}
			}
		})
	}
}

func TestTheServerPodListsEachDeviceForTheShim(t *testing.T) {
	t.Parallel()
	p, svc, err := serverPod("observatory", east, "uid-east", eastDevices())
	if err != nil {
		t.Fatalf("building the server pod: %v", err)
	}
	if len(p.Spec.InitContainers) != 0 || len(p.Spec.Containers) != 1 {
		t.Fatalf("the pod spec is %s, want no init container and one container", mustJSON(p.Spec))
	}
	server := p.Spec.Containers[0]
	cases := []struct {
		what      string
		got, want any
	}{
		{"drivers", p.Metadata.Annotations[annotationDrivers], "east-mount:7625\neast-main-camera:7625\neast-focuser:7625\n"},
		{"server command", server.Command, []string{
			"/indi-shim", "serve", "/etc/indi/devices/drivers", "/run/indi/drivers", "/tmp/indiserver.fifo",
			"/usr/bin/indiserver", "-v", "-r", "2147483647",
		}},
		{"server args", server.Args, []string(nil)},
		{"server image", server.Image, drivers.ServerImage()},
		{"server ports", server.Ports, []containerPort{{Name: "indi", ContainerPort: 7624}}},
		{"server security", server.SecurityContext, wantSecurity},
		{"server mounts", server.VolumeMounts, []volumeMount{
			{Name: "drivers", MountPath: "/run/indi/drivers"},
			{Name: "devices", MountPath: "/etc/indi/devices", ReadOnly: true},
			{Name: "tmp", MountPath: "/tmp"},
		}},
		{"server probe", server.ReadinessProbe, &probe{TCPSocket: &tcpSocket{Port: "indi"}, PeriodSeconds: 10}},
		{"volumes", p.Spec.Volumes, []volume{
			{Name: "drivers", EmptyDir: &emptyDir{}},
			{Name: "devices", DownwardAPI: &downwardAPISource{Items: []downwardAPIFile{{
				Path: "drivers", FieldRef: fieldSource{FieldPath: "metadata.annotations['observatory.liken.sh/drivers']"},
			}}}},
			{Name: "tmp", EmptyDir: &emptyDir{}},
		}},
		{"grace period", p.Spec.TerminationGracePeriodSeconds, int64Pointer(1)},
		{"service name", svc.Metadata.Name, "east-telescope"},
		{"service ports", svc.Spec.Ports, []servicePort{{Name: "indi", Port: 7624, TargetPort: "indi"}}},
		{"service selector", svc.Spec.Selector, map[string]string{labelName: "east-telescope"}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %s, want %s", c.what, mustJSON(c.got), mustJSON(c.want))
		}
	}
}

func TestEveryObjectHasItsOwnerAndTheOperatorsLabels(t *testing.T) {
	t.Parallel()
	east := serverRef{observatory.TelescopeKind, "east"}
	camera := simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd")
	devicePodBuilt, deviceService, _, err := devicePod("observatory", east, camera, false, "")
	if err != nil {
		t.Fatalf("building the device pod: %v", err)
	}
	server, serverService, err := serverPod("observatory", east, "uid-east", []*device{camera})
	if err != nil {
		t.Fatalf("building the server pod: %v", err)
	}
	cases := []struct {
		object meta
		owner  string
		role   string
	}{
		{devicePodBuilt.Metadata, "Camera/east-main/uid-east-main", roleDevice},
		{deviceService.Metadata, "Camera/east-main/uid-east-main", roleDevice},
		{server.Metadata, "Telescope/east/uid-east", roleServer},
		{serverService.Metadata, "Telescope/east/uid-east", roleServer},
	}
	for _, c := range cases {
		t.Run(c.object.Name, func(t *testing.T) {
			t.Parallel()
			refs := c.object.OwnerReferences
			if len(refs) != 1 || refs[0].Kind+"/"+refs[0].Name+"/"+refs[0].UID != c.owner || !refs[0].Controller || refs[0].APIVersion != observatory.APIVersion {
				t.Errorf("owner references = %+v, want %s", refs, c.owner)
			}
			if c.object.Labels[labelManagedBy] != managedBy || c.object.Labels[labelRole] != c.role || c.object.Labels[labelServer] != "east-telescope" {
				t.Errorf("labels are %v, want %s=%s, %s=%s, and %s=east-telescope", c.object.Labels, labelManagedBy, managedBy, labelRole, c.role, labelServer)
			}
		})
	}
}

// A device with spec.claim gets a ResourceClaimTemplate, and its pod
// creates its own ResourceClaim from it, so the claim is deleted with
// the pod.
func TestADeviceWithAClaimGetsAResourceClaimTemplate(t *testing.T) {
	t.Parallel()
	camera := simulator(observatory.CameraKind, "east-main", "indi_asi_ccd")
	camera.object.Spec.Claim = json.RawMessage(`{"devices":{"requests":[{"name":"camera","exactly":{"deviceClassName":"usb.liken.sh"}}]}}`)
	p, _, template, err := devicePod("observatory", serverRef{observatory.TelescopeKind, "east"}, camera, false, "")
	if err != nil {
		t.Fatalf("building the device pod: %v", err)
	}
	if template == nil || template.Metadata.Name != "east-main-camera" || template.Kind != "ResourceClaimTemplate" {
		t.Fatalf("the template is %+v, want a ResourceClaimTemplate named east-main-camera", template)
	}
	if mustJSON(template.Spec) != `{"spec":{"devices":{"requests":[{"exactly":{"deviceClassName":"usb.liken.sh"},"name":"camera"}]}}}` {
		t.Errorf("the template spec is %s, want the spec.claim of the Camera", mustJSON(template.Spec))
	}
	if len(p.Spec.ResourceClaims) != 1 || p.Spec.ResourceClaims[0].ResourceClaimTemplateName != "east-main-camera" ||
		p.Spec.Containers[0].Resources == nil || p.Spec.Containers[0].Resources.Claims[0].Name != p.Spec.ResourceClaims[0].Name {
		t.Errorf("the pod spec is %s, want one resource claim from the template that the container uses", mustJSON(p.Spec))
	}
	if repository(p.Spec.Containers[0].Image) != "ghcr.io/liken-sh/indi-zwo" {
		t.Errorf("the image is %s, want a ghcr.io/liken-sh/indi-zwo image", p.Spec.Containers[0].Image)
	}
}

// A pod and its Service are named <resource-name>-<kind in lower case>.
func TestEachObjectIsNamedForItsResourceThenItsKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind observatory.Kind
		name string
		want string
	}{
		{observatory.MountKind, "east", "east-mount"},
		{observatory.CameraKind, "east-guide", "east-guide-camera"},
		{observatory.WeatherStationKind, "lab", "lab-weatherstation"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			p, svc, _, err := devicePod("observatory", east, simulator(c.kind, c.name, "indi_simulator_ccd"), false, "")
			if err != nil {
				t.Fatalf("building the pod for the %s %s: %v", c.kind.Name, c.name, err)
			}
			if p.Metadata.Name != c.want || svc.Metadata.Name != c.want {
				t.Errorf("the pod is named %s and the Service %s, want both %s", p.Metadata.Name, svc.Metadata.Name, c.want)
			}
		})
	}
}

// A Service name must be a DNS label of at most 63 characters, and
// each shim connects to a device by its Service name. The operator
// refuses a resource whose generated name breaks the rule, and the
// error says how to fix the name of the resource.
func TestANameThatIsNoServiceNameIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		what    string
		build   func() error
		message string
	}{
		{"a dot", func() error {
			_, _, _, err := devicePod("observatory", east, simulator(observatory.CameraKind, "east.main", "indi_simulator_ccd"), false, "")
			return err
		}, `the pod and Service name "east.main-camera" is not a DNS label: use only lowercase letters, digits, and "-", and start with a letter`},
		{"a camera of 57 characters", func() error {
			_, _, _, err := devicePod("observatory", east, simulator(observatory.CameraKind, strings.Repeat("a", 57), "indi_simulator_ccd"), false, "")
			return err
		}, `the pod and Service name "` + strings.Repeat("a", 57) + `-camera" has 64 characters, and Kubernetes allows 63: shorten the name of the Camera to 56 characters or fewer`},
		{"a telescope of 54 characters", func() error {
			_, _, err := serverPod("observatory", serverRef{observatory.TelescopeKind, strings.Repeat("a", 54)}, "uid", nil)
			return err
		}, `the pod and Service name "` + strings.Repeat("a", 54) + `-telescope" has 64 characters, and Kubernetes allows 63: shorten the name of the Telescope to 53 characters or fewer`},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			if err := c.build(); err == nil || err.Error() != c.message {
				t.Errorf("the error is %v, want %s", err, c.message)
			}
		})
	}
}

func TestTheLongestNamesThatFitAreAccepted(t *testing.T) {
	t.Parallel()
	if _, _, _, err := devicePod("observatory", east, simulator(observatory.CameraKind, strings.Repeat("a", 56), "indi_simulator_ccd"), false, ""); err != nil {
		t.Errorf("refusing a Camera name of 56 characters: %v", err)
	}
	if _, _, err := serverPod("observatory", serverRef{observatory.TelescopeKind, strings.Repeat("a", 53)}, "uid", nil); err != nil {
		t.Errorf("refusing a Telescope name of 53 characters: %v", err)
	}
}

// A server's device list is an annotation outside the spec, so a
// change of devices does not replace the server pod.
func TestTheDigestOfAServerLeavesOutItsDevices(t *testing.T) {
	t.Parallel()
	east := serverRef{observatory.TelescopeKind, "east"}
	mount := simulator(observatory.MountKind, "east", "indi_simulator_telescope")
	camera := simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd")
	one, _, _ := serverPod("observatory", east, "uid", []*device{mount})
	two, _, _ := serverPod("observatory", east, "uid", []*device{mount, camera})
	if !current(one, two) {
		t.Errorf("the annotations are %v and %v, want the same digest", one.Metadata.Annotations, two.Metadata.Annotations)
	}
}

func TestTheDigestOfADevicePodFollowsItsDriver(t *testing.T) {
	t.Parallel()
	east := serverRef{observatory.TelescopeKind, "east"}
	one, _, _, _ := devicePod("observatory", east, simulator(observatory.MountKind, "east", "indi_simulator_telescope"), false, "")
	same, _, _, _ := devicePod("observatory", east, simulator(observatory.MountKind, "east", "indi_simulator_telescope"), false, "")
	other, _, _, _ := devicePod("observatory", east, simulator(observatory.MountKind, "east", "indi_lx200generic"), false, "")
	if !current(one, same) || current(one, other) {
		t.Errorf("the annotations are %v, %v, and %v, want the first two to share a digest and the third to differ", one.Metadata.Annotations, same.Metadata.Annotations, other.Metadata.Annotations)
	}
}

func mustJSON(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(body)
}
