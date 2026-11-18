package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func remoteWithController() *Remote {
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

// The claim has one request for the controller, tolerates the
// disconnected taint with no time limit, and is owned by the Remote. The
// request has no toleration for the no-input-node taint, so the pod
// stays Pending until the controller connects.
func TestBuildRemoteClaimToleratesTheDisconnectedTaintWithoutLimit(t *testing.T) {
	claim := buildRemoteClaim(remoteWithController())

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

func TestBuildRemotePodRunsTheReaderInTheRemoteMode(t *testing.T) {
	remote := remoteWithController()
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
	command := []string{"/media-operator-pod", remoteMode}
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

func TestBuildRemotePodMountsNoServiceAccountToken(t *testing.T) {
	remote := remoteWithController()
	pod := buildRemotePod(remote, buildRemoteClaim(remote),
		testSidecarImage, testBusAddress, testTopicBase)

	mustMatch(t, pod.Spec.AutomountServiceAccountToken != nil, true)
	mustMatch(t, *pod.Spec.AutomountServiceAccountToken, false)
}

// The reader pod tolerates the player-node taint only with the
// NoSchedule effect, so a NoExecute taint still evicts it.
func TestBuildRemotePodToleratesOnlyThePlayerNodeTaint(t *testing.T) {
	remote := remoteWithController()
	pod := buildRemotePod(remote, buildRemoteClaim(remote), testSidecarImage, testBusAddress, testTopicBase)

	want := []Toleration{{Key: "media.liken.sh/player", Operator: "Exists", Effect: "NoSchedule"}}
	if !reflect.DeepEqual(pod.Spec.Tolerations, want) {
		t.Errorf("tolerations = %+v, want %+v", pod.Spec.Tolerations, want)
	}
}

// A Remote with discovery enabled passes the mode to its reader in one
// environment variable. A Remote without discovery has no such
// variable, so its pod spec is unchanged.
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
			remote := remoteWithController()
			remote.Spec.Discovery = each.discovery
			pod := buildRemotePod(remote, buildRemoteClaim(remote),
				testSidecarImage, testBusAddress, testTopicBase)

			container := pod.Spec.Containers[0]
			found := false
			for _, entry := range container.Env {
				if entry.Name == remoteDiscoveryVariable {
					found = true
					mustMatch(t, entry.Value, each.want)
				}
			}
			mustMatch(t, found, each.set)
		})
	}
}

// Enabling discovery changes the pod's template hash and not the
// claim's, so the pass replaces only the pod and the controller keeps
// its allocation.
func TestEnablingDiscoveryChangesThePodHashAndNotTheClaimHash(t *testing.T) {
	ordinary := remoteWithController()
	discovering := remoteWithController()
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
		t.Errorf("the pod template hash is %s with and without discovery, want a different hash so that the pod is replaced", podHash)
	}

	claimHash, err := templateHash(buildRemoteClaim(ordinary).Spec)
	mustSucceed(t, err)
	discoveringClaimHash, err := templateHash(buildRemoteClaim(discovering).Spec)
	mustSucceed(t, err)
	mustMatch(t, discoveringClaimHash, claimHash)
}

// The first reconcile creates the claim and the pod. A second reconcile
// creates nothing, because both already exist.
func TestReconcileRemoteCreatesTheClaimAndThePodOnce(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := remoteWithController()

	if err := media.reconcileRemote(remote, claimRead{}); err != nil {
		t.Fatalf("reconciling the Remote the first time: %v", err)
	}

	claim, found := cluster.claims["sofa-remote-devices"]
	if !found {
		t.Fatalf("the requests are %v, want one that creates the claim", cluster.requests)
	}
	if got := claim.Spec.Devices.Requests[0].Name; got != "remote-sofa" {
		t.Errorf("request = %q, want remote-sofa", got)
	}
	if _, found := cluster.pods["sofa-remote"]; !found {
		t.Fatalf("the requests are %v, want one that creates the reader pod", cluster.requests)
	}

	created := len(cluster.requests)
	if err := media.reconcileRemote(remote, claimRead{}); err != nil {
		t.Fatalf("reconciling the Remote the second time: %v", err)
	}
	posts := 0
	for _, request := range cluster.requests[created:] {
		if strings.HasPrefix(request, http.MethodPost) {
			posts++
		}
	}
	if posts != 0 {
		t.Errorf("the second reconcile sent %v, want no request that creates an object", cluster.requests[created:])
	}
}

// The Remote's parameters are copied unchanged into one opaque config
// block for the controller's request, because this operator does not
// read the values.
func TestTheDeviceParametersReachTheRemoteClaim(t *testing.T) {
	remote := remoteWithController()
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

func TestARemoteWithNoParametersGetsNoConfigBlock(t *testing.T) {
	mustMatch(t, len(buildRemoteClaim(remoteWithController()).Spec.Devices.Config), 0)
}

// A claim is immutable, so changed parameters require a new claim. The
// pass deletes the claim and the pod that uses it, and the next pass
// creates both with the new parameters.
func TestChangingTheParametersReplacesTheClaim(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	remote := remoteWithController()
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	remote.Spec.Device.Parameters = &DeviceParameters{
		Driver: "bluetooth.liken.sh",
		Values: json.RawMessage(`{"inputs":["joystick","touchpad"]}`),
	}
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	if _, found := cluster.claims["sofa-remote-devices"]; found {
		t.Fatalf("after the parameters changed, the claim remains (requests %v), want it deleted", cluster.requests)
	}
	if _, found := cluster.pods["sofa-remote"]; found {
		t.Fatalf("after the parameters changed, the pod remains (requests %v), want it deleted", cluster.requests)
	}

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	claim, found := cluster.claims["sofa-remote-devices"]
	if !found {
		t.Fatalf("after the next reconcile, the claim is missing (requests %v), want it created again", cluster.requests)
	}
	config := claim.Spec.Devices.Config
	if len(config) != 1 || config[0].Opaque == nil {
		t.Fatalf("config = %+v, want the new block", config)
	}
	mustMatch(t, string(config[0].Opaque.Parameters), `{"inputs":["joystick","touchpad"]}`)
}
