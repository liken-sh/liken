package main

// These tests cover the standing pod a Remote becomes: one claim that
// holds the controller across every sleep, one pod that runs the reader
// in its remote mode, and the reconcile that creates each once.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// One Remote with the identity the claim and the pod both copy, and a
// selector so the claim carries one.
func standingRemote() *Remote {
	return &Remote{
		Metadata: ObjectMeta{Name: "sofa", Namespace: "house", UID: "remote-uid"},
		Spec: RemoteSpec{
			Device: RemoteDevice{
				Class:    "gamepad",
				Selector: `device.attributes["bluetooth.liken.sh"].address == "04:4A"`,
			},
			Keymap: "dualsense",
		},
	}
}

// The claim holds one request for the controller, tolerates the
// disconnected taint forever, and is owned by the Remote. It does not
// tolerate the no-input-node taint, which the single toleration proves.
func TestBuildRemoteClaimHoldsTheControllerForever(t *testing.T) {
	claim := buildRemoteClaim(standingRemote())

	if claim.APIVersion != claimAPIVersion || claim.Kind != "ResourceClaim" {
		t.Errorf("apiVersion = %q, kind = %q", claim.APIVersion, claim.Kind)
	}
	if claim.Metadata.Name != "sofa-remote-devices" {
		t.Errorf("name = %q, want sofa-remote-devices", claim.Metadata.Name)
	}
	if claim.Metadata.Namespace != "house" {
		t.Errorf("namespace = %q, want house", claim.Metadata.Namespace)
	}

	requests := claim.Spec.Devices.Requests
	if len(requests) != 1 {
		t.Fatalf("requests = %+v, want one", requests)
	}
	request := requests[0]
	if request.Name != "remote-sofa" {
		t.Errorf("request name = %q, want remote-sofa", request.Name)
	}
	if request.Exactly == nil || request.Exactly.DeviceClassName != "gamepad" {
		t.Fatalf("request = %+v, want the gamepad class", request)
	}
	selectors := []DeviceSelector{{CEL: &CELDeviceSelector{
		Expression: `device.attributes["bluetooth.liken.sh"].address == "04:4A"`,
	}}}
	if !reflect.DeepEqual(request.Exactly.Selectors, selectors) {
		t.Errorf("selectors = %+v, want %+v", request.Exactly.Selectors, selectors)
	}

	forever := []DeviceToleration{{Key: "bluetooth.liken.sh/disconnected", Operator: "Exists", Effect: "NoExecute"}}
	if !reflect.DeepEqual(request.Exactly.Tolerations, forever) {
		t.Errorf("tolerations = %+v, want %+v", request.Exactly.Tolerations, forever)
	}

	owners := []OwnerReference{{
		APIVersion: mediaAPIVersion,
		Kind:       "Remote",
		Name:       "sofa",
		UID:        "remote-uid",
		Controller: true,
	}}
	if !reflect.DeepEqual(claim.Metadata.OwnerReferences, owners) {
		t.Errorf("ownerReferences = %+v, want %+v", claim.Metadata.OwnerReferences, owners)
	}
}

// The pod runs the reader in the remote mode, carries the Remote's
// identity in the environment, holds the claim's one request, restarts
// on a crash, and is owned by the Remote. It carries no IPC volume.
func TestBuildRemotePodRunsTheReaderInTheRemoteMode(t *testing.T) {
	remote := standingRemote()
	claim := buildRemoteClaim(remote)
	pod := buildRemotePod(remote, claim, testSidecarImage, testBusAddress, testTopicBase)

	if pod.Metadata.Name != "sofa-remote" {
		t.Errorf("name = %q, want sofa-remote", pod.Metadata.Name)
	}
	if pod.Spec.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if len(pod.Spec.Volumes) != 0 {
		t.Errorf("volumes = %+v, want none", pod.Spec.Volumes)
	}
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("initContainers = %+v, want none", pod.Spec.InitContainers)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want one", pod.Spec.Containers)
	}

	container := pod.Spec.Containers[0]
	command := []string{"/media-operator", remoteMode}
	if !reflect.DeepEqual(container.Command, command) {
		t.Errorf("command = %v, want %v", container.Command, command)
	}
	env := map[string]string{}
	for _, entry := range container.Env {
		env[entry.Name] = entry.Value
	}
	wantEnv := map[string]string{
		remoteNamespaceVariable: "house",
		remoteNameVariable:      "sofa",
		busAddressVariable:      testBusAddress,
		topicBaseVariable:       testTopicBase,
	}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %+v, want %+v", env, wantEnv)
	}

	claims := container.Resources.Claims
	wantClaims := []ContainerClaim{{Name: podClaimName, Request: "remote-sofa"}}
	if !reflect.DeepEqual(claims, wantClaims) {
		t.Errorf("resources.claims = %+v, want %+v", claims, wantClaims)
	}

	owners := []OwnerReference{{
		APIVersion: mediaAPIVersion,
		Kind:       "Remote",
		Name:       "sofa",
		UID:        "remote-uid",
		Controller: true,
	}}
	if !reflect.DeepEqual(pod.Metadata.OwnerReferences, owners) {
		t.Errorf("ownerReferences = %+v, want %+v", pod.Metadata.OwnerReferences, owners)
	}
}

// The reader pod holds a controller that is paired to one machine, so
// it tolerates the taint that keeps unrelated work off that machine. It
// tolerates that key alone, and for scheduling alone, so a NoExecute
// taint still moves it away.
func TestBuildRemotePodToleratesThePlayerNodeTaint(t *testing.T) {
	remote := standingRemote()
	pod := buildRemotePod(remote, buildRemoteClaim(remote), testSidecarImage, testBusAddress, testTopicBase)

	want := []Toleration{{Key: "media.liken.sh/player", Operator: "Exists", Effect: "NoSchedule"}}
	if !reflect.DeepEqual(pod.Spec.Tolerations, want) {
		t.Errorf("tolerations = %+v, want %+v", pod.Spec.Tolerations, want)
	}
}

// A Remote that asks for discovery carries the mode to its reader as
// one variable, and a Remote that does not carries none, so an
// ordinary Remote's pod spec is what it always was.
func TestTheDiscoveryModeReachesTheReaderAsOneVariable(t *testing.T) {
	cases := []struct {
		name      string
		discovery bool
		want      string
		set       bool
	}{
		{name: "a Remote in discovery", discovery: true, want: discoveryOn, set: true},
		{name: "an ordinary Remote", discovery: false, set: false},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			remote := standingRemote()
			remote.Spec.Discovery = each.discovery
			pod := buildRemotePod(remote, buildRemoteClaim(remote),
				testSidecarImage, testBusAddress, testTopicBase)

			container := pod.Spec.Containers[0]
			held := false
			for _, entry := range container.Env {
				if entry.Name == remoteDiscoveryVariable {
					held = true
					mustMatch(t, entry.Value, each.want)
				}
			}
			mustMatch(t, held, each.set)
		})
	}
}

// Turning discovery on changes the pod's template and not the claim's,
// so the pass replaces the reader pod alone and the controller keeps
// its allocation.
func TestTurningDiscoveryOnRollsThePodAndNotTheClaim(t *testing.T) {
	ordinary := standingRemote()
	discovering := standingRemote()
	discovering.Spec.Discovery = true

	ordinaryPod := buildRemotePod(ordinary, buildRemoteClaim(ordinary),
		testSidecarImage, testBusAddress, testTopicBase)
	discoveringPod := buildRemotePod(discovering, buildRemoteClaim(discovering),
		testSidecarImage, testBusAddress, testTopicBase)

	podHash, err := templateHash(ordinaryPod.Spec)
	mustSucceed(t, err)
	discoveringHash, err := templateHash(discoveringPod.Spec)
	mustSucceed(t, err)
	if podHash == discoveringHash {
		t.Errorf("the pod template hash did not change, so the reader would not roll")
	}

	claimHash, err := templateHash(buildRemoteClaim(ordinary).Spec)
	mustSucceed(t, err)
	discoveringClaimHash, err := templateHash(buildRemoteClaim(discovering).Spec)
	mustSucceed(t, err)
	mustMatch(t, discoveringClaimHash, claimHash)
}

// The first reconcile creates the claim and the standing pod. A second
// reconcile creates nothing, because both are already there.
func TestReconcileRemoteCreatesTheClaimAndThePodOnce(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := standingRemote()

	if err := media.reconcileRemote(remote, claimRead{}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	claim, held := cluster.claims["sofa-remote-devices"]
	if !held {
		t.Fatalf("no claim was created: %v", cluster.requests)
	}
	if got := claim.Spec.Devices.Requests[0].Name; got != "remote-sofa" {
		t.Errorf("request = %q, want remote-sofa", got)
	}
	if _, held := cluster.pods["sofa-remote"]; !held {
		t.Fatalf("no standing pod was created: %v", cluster.requests)
	}

	created := len(cluster.requests)
	if err := media.reconcileRemote(remote, claimRead{}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	posts := 0
	for _, request := range cluster.requests[created:] {
		if strings.HasPrefix(request, http.MethodPost) {
			posts++
		}
	}
	if posts != 0 {
		t.Errorf("the second reconcile created objects: %v", cluster.requests[created:])
	}
}

// The Remote's parameters reach its claim as one opaque block for the
// controller's request, byte for byte, because this operator reads
// none of the values.
func TestTheDeviceParametersReachTheRemoteClaim(t *testing.T) {
	remote := standingRemote()
	remote.Spec.Device.Parameters = &DeviceParameters{
		Driver: "bluetooth.liken.sh",
		Values: json.RawMessage(`{"inputs":["joystick"]}`),
	}

	config := buildRemoteClaim(remote).Spec.Devices.Config
	if len(config) != 1 {
		t.Fatalf("config = %+v, want one block", config)
	}
	mustMatchAll(t, config[0].Requests, []string{"remote-sofa"})
	if config[0].Opaque == nil {
		t.Fatalf("config = %+v, want an opaque block", config[0])
	}
	mustMatch(t, config[0].Opaque.Driver, "bluetooth.liken.sh")
	mustMatch(t, string(config[0].Opaque.Parameters), `{"inputs":["joystick"]}`)
}

// A Remote without parameters carries no configuration block, so a
// claim that needs none is the same claim as before the field existed.
func TestARemoteWithNoParametersCarriesNoConfiguration(t *testing.T) {
	mustMatch(t, len(buildRemoteClaim(standingRemote()).Spec.Devices.Config), 0)
}

// A claim is immutable, so a changed parameters block is a
// replacement: the pass deletes the claim and the pod that holds it,
// and the next pass creates both with the new block.
func TestChangingTheParametersReplacesTheClaim(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := standingRemote()
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	remote.Spec.Device.Parameters = &DeviceParameters{
		Driver: "bluetooth.liken.sh",
		Values: json.RawMessage(`{"inputs":["joystick","touchpad"]}`),
	}
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	if _, held := cluster.claims["sofa-remote-devices"]; held {
		t.Fatalf("the claim was not deleted: %v", cluster.requests)
	}
	if _, held := cluster.pods["sofa-remote"]; held {
		t.Fatalf("the pod that holds the claim was not deleted: %v", cluster.requests)
	}

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	claim, held := cluster.claims["sofa-remote-devices"]
	if !held {
		t.Fatalf("the claim did not come back: %v", cluster.requests)
	}
	config := claim.Spec.Devices.Config
	if len(config) != 1 || config[0].Opaque == nil {
		t.Fatalf("config = %+v, want the new block", config)
	}
	mustMatch(t, string(config[0].Opaque.Parameters), `{"inputs":["joystick","touchpad"]}`)
}
