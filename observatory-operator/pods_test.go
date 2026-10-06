package main

// The operator's pods and Services hold the properties that plan 03
// measured on a test cluster with manifests written by hand: socat
// serves each driver over a pipe, every container runs as user 1000 on
// a read-only root, the server links one shim to each device, and the
// server stops in 1 second.

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

// eastDevices are three devices of the east telescope, as plan 03 ran
// them.
func eastDevices() []*device {
	return []*device{
		simulator(observatory.MountKind, "east", "indi_simulator_telescope"),
		simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd"),
		simulator(observatory.FocuserKind, "east", "indi_simulator_focus"),
	}
}

var east = serverRef{observatory.TelescopeKind, "east"}

// Every container runs as user 1000, on a read-only root, with no
// capabilities, as plan 06's contract for a driver's image states.
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
			p, svc, claim, err := devicePod("observatory", east, d, false)
			if err != nil {
				t.Fatal(err)
			}
			if claim != nil {
				t.Errorf("a simulator has a claim: %+v", claim)
			}
			if len(p.Spec.Containers) != 1 {
				t.Fatalf("containers = %+v", p.Spec.Containers)
			}
			c := p.Spec.Containers[0]
			cases := []struct {
				what      string
				got, want any
			}{
				{"command", c.Command, []string{"/usr/bin/socat"}},
				// pipes gives the driver a pipe for stdout, so it sends
				// each BLOB as base64, which socat carries.
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

func TestTheServerPodLinksAShimToEachDevice(t *testing.T) {
	t.Parallel()
	p, svc, err := serverPod("observatory", east, "uid-east", eastDevices())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Spec.InitContainers) != 1 || len(p.Spec.Containers) != 1 {
		t.Fatalf("pod spec = %s", mustJSON(p.Spec))
	}
	links, server := p.Spec.InitContainers[0], p.Spec.Containers[0]
	targets := []string{"east-mount:7625", "east-main-camera:7625", "east-focuser:7625"}
	var paths []string
	for _, target := range targets {
		paths = append(paths, "/run/indi/drivers/"+target)
	}
	drivesMount := volumeMount{Name: "drivers", MountPath: "/run/indi/drivers"}
	cases := []struct {
		what      string
		got, want any
	}{
		{"link command", links.Command, append([]string{"/usr/bin/indi-shim", "link", "/run/indi/drivers"}, targets...)},
		{"link security", links.SecurityContext, wantSecurity},
		{"link mounts", links.VolumeMounts, []volumeMount{drivesMount}},
		{"link image", links.Image, drivers.ServerImage()},
		// -r is the largest restart count that indiserver's atoi holds,
		// because the count never resets.
		{"server args", server.Args, append([]string{"-v", "-r", "2147483647"}, paths...)},
		{"server command", server.Command, []string(nil)},
		{"server image", server.Image, drivers.ServerImage()},
		{"server ports", server.Ports, []containerPort{{Name: "indi", ContainerPort: 7624}}},
		{"server security", server.SecurityContext, wantSecurity},
		{"server mounts", server.VolumeMounts, []volumeMount{drivesMount, {Name: "tmp", MountPath: "/tmp"}}},
		{"server probe", server.ReadinessProbe, &probe{TCPSocket: &tcpSocket{Port: "indi"}, PeriodSeconds: 10}},
		{"volumes", p.Spec.Volumes, []volume{{Name: "drivers", EmptyDir: &emptyDir{}}, {Name: "tmp", EmptyDir: &emptyDir{}}}},
		// indiserver ignores SIGTERM as process 1, so a longer grace
		// period left every device offline for 31 s on plan 03's cluster.
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
	devicePodBuilt, deviceService, _, err := devicePod("observatory", east, camera, false)
	if err != nil {
		t.Fatal(err)
	}
	server, serverService, err := serverPod("observatory", east, "uid-east", []*device{camera})
	if err != nil {
		t.Fatal(err)
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
				t.Errorf("labels = %v", c.object.Labels)
			}
		})
	}
}

// A device on real hardware names a claim, and its pod uses a
// ResourceClaim made from it.
func TestADeviceWithAClaimGetsAResourceClaim(t *testing.T) {
	t.Parallel()
	camera := simulator(observatory.CameraKind, "east-main", "indi_asi_ccd")
	camera.object.Spec.Claim = json.RawMessage(`{"devices":{"requests":[{"name":"camera","exactly":{"deviceClassName":"usb.liken.sh"}}]}}`)
	p, _, claim, err := devicePod("observatory", serverRef{observatory.TelescopeKind, "east"}, camera, false)
	if err != nil {
		t.Fatal(err)
	}
	if claim == nil || claim.Metadata.Name != "east-main-camera" || claim.Kind != "ResourceClaim" {
		t.Fatalf("claim = %+v", claim)
	}
	if mustJSON(claim.Spec) != `{"devices":{"requests":[{"exactly":{"deviceClassName":"usb.liken.sh"},"name":"camera"}]}}` {
		t.Errorf("claim spec = %s", mustJSON(claim.Spec))
	}
	if len(p.Spec.ResourceClaims) != 1 || p.Spec.ResourceClaims[0].ResourceClaimName != "east-main-camera" ||
		p.Spec.Containers[0].Resources == nil || p.Spec.Containers[0].Resources.Claims[0].Name != p.Spec.ResourceClaims[0].Name {
		t.Errorf("the pod does not use the claim: %s", mustJSON(p.Spec))
	}
	if repository(p.Spec.Containers[0].Image) != "ghcr.io/liken-sh/indi-zwo" {
		t.Errorf("image = %s", p.Spec.Containers[0].Image)
	}
}

// Each object takes the name <resource-name>-<kind>, so kubectl get pods
// lists one telescope's objects together.
func TestEachObjectIsNamedForItsResourceThenItsKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind observatory.Kind
		name string
		want string
	}{
		{observatory.MountKind, "east", "east-mount"},
		{observatory.CameraKind, "east-guide", "east-guide-camera"},
		{observatory.SkyQualityMeterKind, "lab", "lab-skyqualitymeter"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			p, svc, _, err := devicePod("observatory", east, simulator(c.kind, c.name, "indi_simulator_ccd"), false)
			if err != nil {
				t.Fatal(err)
			}
			if p.Metadata.Name != c.want || svc.Metadata.Name != c.want {
				t.Errorf("pod %s and Service %s, want %s", p.Metadata.Name, svc.Metadata.Name, c.want)
			}
		})
	}
}

// A Service name is a DNS label of 63 characters or fewer, and the
// shims dial a device by its Service name. A resource whose generated
// name breaks that rule is refused with a message that says how to fix
// the name.
func TestANameThatIsNoServiceNameIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		what    string
		build   func() error
		message string
	}{
		{"a dot", func() error {
			_, _, _, err := devicePod("observatory", east, simulator(observatory.CameraKind, "east.main", "indi_simulator_ccd"), false)
			return err
		}, `the pod and Service name "east.main-camera" is not a DNS label: use only lowercase letters, digits, and "-", and start with a letter`},
		{"a camera of 57 characters", func() error {
			_, _, _, err := devicePod("observatory", east, simulator(observatory.CameraKind, strings.Repeat("a", 57), "indi_simulator_ccd"), false)
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
				t.Errorf("error = %v\nwant %s", err, c.message)
			}
		})
	}
}

// The longest names that fit are accepted.
func TestTheLongestNamesThatFitAreAccepted(t *testing.T) {
	t.Parallel()
	if _, _, _, err := devicePod("observatory", east, simulator(observatory.CameraKind, strings.Repeat("a", 56), "indi_simulator_ccd"), false); err != nil {
		t.Error(err)
	}
	if _, _, err := serverPod("observatory", serverRef{observatory.TelescopeKind, strings.Repeat("a", 53)}, "uid", nil); err != nil {
		t.Error(err)
	}
}

// A pod's digest changes with its spec, so the operator replaces a
// server whose devices changed.
func TestTheDigestFollowsTheSpec(t *testing.T) {
	t.Parallel()
	east := serverRef{observatory.TelescopeKind, "east"}
	mount := simulator(observatory.MountKind, "east", "indi_simulator_telescope")
	camera := simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd")
	one, _, _ := serverPod("observatory", east, "uid", []*device{mount})
	same, _, _ := serverPod("observatory", east, "uid", []*device{mount})
	two, _, _ := serverPod("observatory", east, "uid", []*device{mount, camera})
	if !current(one, same) || current(one, two) {
		t.Errorf("digests: %v %v %v", one.Metadata.Annotations, same.Metadata.Annotations, two.Metadata.Annotations)
	}
}

func mustJSON(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(body)
}
