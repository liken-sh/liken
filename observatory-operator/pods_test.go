package main

// The operator's pods and Services against the manifests of plan 03 in
// topology/, which ran the simulators on a test cluster by hand. A
// difference between the two is either a bug or one of the changes
// listed in each test, on purpose.

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/observatory-operator/drivers"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// handWritten reads the objects of one file in topology/, by kind and
// name.
func handWritten(t *testing.T, file string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("topology/" + file)
	if err != nil {
		t.Fatal(err)
	}
	objects := map[string]json.RawMessage{}
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		body, err := yaml.YAMLToJSON(document)
		if err != nil {
			t.Fatal(err)
		}
		var head struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(body, &head); err != nil {
			t.Fatal(err)
		}
		objects[head.Kind+"/"+head.Metadata.Name] = body
	}
	return objects
}

// templateSpec reads a Deployment's pod spec into the operator's own
// type. The Deployment's anti-affinity has no field there, and drops
// out: plan 03 put the devices on another node than the server to
// measure the hops, and the operator places no pod.
func templateSpec(t *testing.T, deployment json.RawMessage) podSpec {
	t.Helper()
	var d struct {
		Spec struct {
			Template struct {
				Spec podSpec `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(deployment, &d); err != nil {
		t.Fatal(err)
	}
	return d.Spec.Template.Spec
}

func simulator(kind observatory.Kind, name, driver string) *device {
	d := &device{kind: kind}
	d.object.Metadata = observatory.ObjectMeta{Name: name, Namespace: "observatory", UID: "uid-" + name}
	d.object.Spec.Driver = observatory.Driver{Name: driver}
	return d
}

// repository drops an image's tag. The manifests name the tag of their
// day, and the operator names the tag that indi/package.toml pins.
func repository(image string) string {
	name, _, _ := strings.Cut(image, ":")
	return name
}

func TestADevicePodHasTheShapeOfPlan03(t *testing.T) {
	topology := handWritten(t, "devices.yaml")
	east := serverRef{observatory.TelescopeKind, "east"}
	cases := []struct {
		hand   string
		device *device
	}{
		{"mount", simulator(observatory.MountKind, "east-mount", "indi_simulator_telescope")},
		{"ccd", simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd")},
		{"focuser", simulator(observatory.FocuserKind, "east-focuser", "indi_simulator_focus")},
	}
	for _, c := range cases {
		t.Run(c.hand, func(t *testing.T) {
			built, svc, claim, err := devicePod("observatory", east, c.device)
			if err != nil {
				t.Fatal(err)
			}
			if claim != nil {
				t.Errorf("a simulator has a claim: %+v", claim)
			}
			want := templateSpec(t, topology["Deployment/"+c.hand])
			got := built.Spec
			// Each simulator runs from indi-simulators, which holds the
			// star catalog. Plan 03 ran the mount and the focuser from
			// indi, which holds the same drivers.
			if repository(got.Containers[0].Image) != "ghcr.io/liken-sh/indi-simulators" {
				t.Errorf("image = %s", got.Containers[0].Image)
			}
			want.Containers[0].Image, got.Containers[0].Image = "", ""
			// The operator adds these on purpose: a bare pod states its
			// restart policy, and a driver reads no Service variables
			// and no API token.
			want.RestartPolicy = "Always"
			want.EnableServiceLinks = boolPointer(false)
			want.AutomountServiceAccountToken = boolPointer(false)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("pod spec:\n%s\nwant plan 03's:\n%s", mustJSON(got), mustJSON(want))
			}

			var wantService service
			if err := json.Unmarshal(topology["Service/"+c.hand], &wantService); err != nil {
				t.Fatal(err)
			}
			name := built.Metadata.Name
			wantService.Spec.Selector = map[string]string{labelName: name}
			if !reflect.DeepEqual(svc.Spec, wantService.Spec) {
				t.Errorf("Service spec = %+v, want %+v", svc.Spec, wantService.Spec)
			}
			if svc.Metadata.Name != name || built.Metadata.Labels[labelName] != name {
				t.Errorf("the Service %s does not select the pod %s", svc.Metadata.Name, name)
			}
		})
	}
}

func TestTheServerPodHasTheShapeOfPlan03(t *testing.T) {
	topology := handWritten(t, "server.yaml")
	east := serverRef{observatory.TelescopeKind, "east"}
	devices := []*device{
		simulator(observatory.MountKind, "east-mount", "indi_simulator_telescope"),
		simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd"),
		simulator(observatory.FocuserKind, "east-focuser", "indi_simulator_focus"),
	}
	built, svc, err := serverPod("observatory", east, "uid-east", devices)
	if err != nil {
		t.Fatal(err)
	}
	// The hand-written names become the operator's names.
	hand := string(topology["Deployment/indiserver"])
	for from, to := range map[string]string{"mount:7625": "mount-east-mount:7625", "ccd:7625": "camera-east-main:7625", "focuser:7625": "focuser-east-focuser:7625"} {
		hand = strings.ReplaceAll(hand, `/`+from, `/`+to)
		hand = strings.ReplaceAll(hand, `"`+from, `"`+to)
	}
	want := templateSpec(t, json.RawMessage(hand))
	got := built.Spec
	for i := range got.InitContainers {
		if got.InitContainers[i].Image != drivers.ServerImage() {
			t.Errorf("init image = %s", got.InitContainers[i].Image)
		}
		want.InitContainers[i].Image, got.InitContainers[i].Image = "", ""
	}
	if got.Containers[0].Image != drivers.ServerImage() {
		t.Errorf("image = %s", got.Containers[0].Image)
	}
	want.Containers[0].Image, got.Containers[0].Image = "", ""
	// The operator adds these on purpose: the restart policy of a bare
	// pod, no Service variables and no API token, and the probe that
	// tells the operator when to open its INDI connection.
	want.RestartPolicy = "Always"
	want.EnableServiceLinks = boolPointer(false)
	want.AutomountServiceAccountToken = boolPointer(false)
	want.Containers[0].ReadinessProbe = &probe{TCPSocket: &tcpSocket{Port: "indi"}, PeriodSeconds: 10}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pod spec:\n%s\nwant plan 03's:\n%s", mustJSON(got), mustJSON(want))
	}

	var wantService service
	if err := json.Unmarshal(topology["Service/indiserver"], &wantService); err != nil {
		t.Fatal(err)
	}
	wantService.Spec.Selector = map[string]string{labelName: "telescope-east"}
	if svc.Metadata.Name != "telescope-east" || !reflect.DeepEqual(svc.Spec, wantService.Spec) {
		t.Errorf("Service %s = %+v, want telescope-east with %+v", svc.Metadata.Name, svc.Spec, wantService.Spec)
	}
}

func TestEveryObjectHasItsOwnerAndTheOperatorsLabels(t *testing.T) {
	east := serverRef{observatory.TelescopeKind, "east"}
	camera := simulator(observatory.CameraKind, "east-main", "indi_simulator_ccd")
	devicePodBuilt, deviceService, _, err := devicePod("observatory", east, camera)
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
			refs := c.object.OwnerReferences
			if len(refs) != 1 || refs[0].Kind+"/"+refs[0].Name+"/"+refs[0].UID != c.owner || !refs[0].Controller || refs[0].APIVersion != observatory.APIVersion {
				t.Errorf("owner references = %+v, want %s", refs, c.owner)
			}
			if c.object.Labels[labelManagedBy] != managedBy || c.object.Labels[labelRole] != c.role || c.object.Labels[labelServer] != "telescope-east" {
				t.Errorf("labels = %v", c.object.Labels)
			}
		})
	}
}

// A device on real hardware names a claim, and its pod uses a
// ResourceClaim made from it.
func TestADeviceWithAClaimGetsAResourceClaim(t *testing.T) {
	camera := simulator(observatory.CameraKind, "east-main", "indi_asi_ccd")
	camera.object.Spec.Claim = json.RawMessage(`{"devices":{"requests":[{"name":"camera","exactly":{"deviceClassName":"usb.liken.sh"}}]}}`)
	p, _, claim, err := devicePod("observatory", serverRef{observatory.TelescopeKind, "east"}, camera)
	if err != nil {
		t.Fatal(err)
	}
	if claim == nil || claim.Metadata.Name != "camera-east-main" || claim.Kind != "ResourceClaim" {
		t.Fatalf("claim = %+v", claim)
	}
	if mustJSON(claim.Spec) != `{"devices":{"requests":[{"exactly":{"deviceClassName":"usb.liken.sh"},"name":"camera"}]}}` {
		t.Errorf("claim spec = %s", mustJSON(claim.Spec))
	}
	if len(p.Spec.ResourceClaims) != 1 || p.Spec.ResourceClaims[0].ResourceClaimName != "camera-east-main" ||
		p.Spec.Containers[0].Resources == nil || p.Spec.Containers[0].Resources.Claims[0].Name != p.Spec.ResourceClaims[0].Name {
		t.Errorf("the pod does not use the claim: %s", mustJSON(p.Spec))
	}
	if repository(p.Spec.Containers[0].Image) != "ghcr.io/liken-sh/indi-zwo" {
		t.Errorf("image = %s", p.Spec.Containers[0].Image)
	}
}

func TestANameThatIsNoServiceNameIsRefused(t *testing.T) {
	cases := []string{"east.main", strings.Repeat("a", 60)}
	for _, name := range cases {
		camera := simulator(observatory.CameraKind, name, "indi_simulator_ccd")
		if _, _, _, err := devicePod("observatory", serverRef{observatory.TelescopeKind, "east"}, camera); err == nil {
			t.Errorf("devicePod accepted the name %q", name)
		}
	}
}

// A pod's digest changes with its spec, so the operator replaces a
// server whose devices changed.
func TestTheDigestFollowsTheSpec(t *testing.T) {
	east := serverRef{observatory.TelescopeKind, "east"}
	mount := simulator(observatory.MountKind, "east-mount", "indi_simulator_telescope")
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
