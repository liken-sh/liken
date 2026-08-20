package main

// These tests cover the manifest the operator writes so nobody has
// to: which requests a Player becomes, what each one selects and
// tolerates, and where the Player's parameters land.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// One Play, with the identity the claim and the pod both copy.
func testPlay() *Play {
	return &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", UID: "play-1"},
		Spec:     PlaySpec{Players: []string{"theater"}, URIs: []string{"https://films.example/film.mkv"}},
	}
}

// A Player that holds every role: a screen, two speakers, and a
// render node.
func testPlayer() *Player {
	return &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec: PlayerSpec{
			Zone:    "living-room",
			Display: &PlayerDevice{Class: "display-output"},
			Sinks: []PlayerDevice{
				{Class: "audio-output"},
				{Class: "audio-speaker"},
			},
			Render: &PlayerDevice{Class: "render-node"},
		},
	}
}

func TestBuildClaimTurnsEveryRoleIntoARequest(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), nil)

	requests := claim.Spec.Devices.Requests
	if len(requests) != 4 {
		t.Fatalf("requests = %+v, want four", requests)
	}
	roles := []struct {
		name  string
		class string
	}{
		{name: "screen", class: "display-output"},
		{name: "audio0", class: "audio-output"},
		{name: "audio1", class: "audio-speaker"},
		{name: "render", class: "render-node"},
	}
	for index, role := range roles {
		request := requests[index]
		if request.Name != role.name {
			t.Errorf("requests[%d].name = %q, want %q", index, request.Name, role.name)
			continue
		}
		if request.Exactly == nil {
			t.Errorf("%s carries no exactly block", role.name)
			continue
		}
		if request.Exactly.DeviceClassName != role.class {
			t.Errorf("%s deviceClassName = %q, want %q", role.name, request.Exactly.DeviceClassName, role.class)
		}
		if request.Exactly.AllocationMode != "ExactCount" {
			t.Errorf("%s allocationMode = %q, want ExactCount", role.name, request.Exactly.AllocationMode)
		}
		if request.Exactly.Count != 1 {
			t.Errorf("%s count = %d, want 1", role.name, request.Exactly.Count)
		}
	}
}

// The expression the Player wrote is the expression the claim
// carries, and a Player that wrote none leaves the selector list out
// of the object entirely.
func TestBuildClaimCarriesOnlyTheSelectorsThePlayerWrote(t *testing.T) {
	expression := `device.attributes["display.liken.sh"].connector == "HDMI-A-1"`
	player := testPlayer()
	player.Spec.Display.Selector = expression

	claim := buildClaim(testPlay(), player, nil)

	screen := claim.Spec.Devices.Requests[0]
	selectors := []DeviceSelector{{CEL: &CELDeviceSelector{Expression: expression}}}
	if !reflect.DeepEqual(screen.Exactly.Selectors, selectors) {
		t.Errorf("screen selectors = %+v, want %+v", screen.Exactly.Selectors, selectors)
	}

	render := claim.Spec.Devices.Requests[3]
	if len(render.Exactly.Selectors) != 0 {
		t.Errorf("render selectors = %+v, want none", render.Exactly.Selectors)
	}
	written, err := json.Marshal(render)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "selectors") {
		t.Errorf("render = %s, want no selectors key", written)
	}
}

// A display and a speaker come and go while a machine runs, and
// thirty seconds is the window that survives a cable being moved. A
// render node never leaves, so it takes no toleration.
func TestBuildClaimToleratesTheDisconnectTaintsForThirtySeconds(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), nil)

	seconds := int64(30)
	cases := []struct {
		request string
		index   int
		taints  []DeviceToleration
	}{
		{
			request: "screen",
			index:   0,
			taints: []DeviceToleration{{
				Key:               "display.liken.sh/disconnected",
				Operator:          "Exists",
				Effect:            "NoExecute",
				TolerationSeconds: &seconds,
			}},
		},
		{
			request: "audio0",
			index:   1,
			taints: []DeviceToleration{{
				Key:               "audio.liken.sh/disconnected",
				Operator:          "Exists",
				Effect:            "NoExecute",
				TolerationSeconds: &seconds,
			}},
		},
		{
			request: "audio1",
			index:   2,
			taints: []DeviceToleration{{
				Key:               "audio.liken.sh/disconnected",
				Operator:          "Exists",
				Effect:            "NoExecute",
				TolerationSeconds: &seconds,
			}},
		},
		{request: "render", index: 3},
	}
	for _, c := range cases {
		t.Run(c.request, func(t *testing.T) {
			request := claim.Spec.Devices.Requests[c.index]
			if request.Name != c.request {
				t.Fatalf("requests[%d] = %q, want %q", c.index, request.Name, c.request)
			}
			if !reflect.DeepEqual(request.Exactly.Tolerations, c.taints) {
				t.Errorf("tolerations = %+v, want %+v", request.Exactly.Tolerations, c.taints)
			}
		})
	}
}

// The parameters reach the driver as the Player wrote them, aimed at
// the one request they belong to. A device with no parameters adds
// no config block at all.
func TestBuildClaimConfiguresOnlyTheDevicesWithParameters(t *testing.T) {
	mode := json.RawMessage(`{"mode":"1920x1080@60"}`)
	player := testPlayer()
	player.Spec.Display.Parameters = &DeviceParameters{Driver: "display.liken.sh", Values: mode}

	claim := buildClaim(testPlay(), player, nil)

	config := claim.Spec.Devices.Config
	if len(config) != 1 {
		t.Fatalf("config = %+v, want one entry", config)
	}
	if !reflect.DeepEqual(config[0].Requests, []string{"screen"}) {
		t.Errorf("requests = %v, want [screen]", config[0].Requests)
	}
	if config[0].Opaque == nil {
		t.Fatal("the config entry carries no opaque block")
	}
	if config[0].Opaque.Driver != "display.liken.sh" {
		t.Errorf("driver = %q", config[0].Opaque.Driver)
	}
	if string(config[0].Opaque.Parameters) != string(mode) {
		t.Errorf("parameters = %s, want %s", config[0].Opaque.Parameters, mode)
	}
}

func TestBuildClaimConfiguresEachDeviceSeparately(t *testing.T) {
	player := testPlayer()
	player.Spec.Sinks[1].Parameters = &DeviceParameters{
		Driver: "audio.liken.sh",
		Values: json.RawMessage(`{"codec":"aptx"}`),
	}
	player.Spec.Render.Parameters = &DeviceParameters{
		Driver: "gpu.liken.sh",
		Values: json.RawMessage(`{"node":"renderD128"}`),
	}

	claim := buildClaim(testPlay(), player, nil)

	config := claim.Spec.Devices.Config
	if len(config) != 2 {
		t.Fatalf("config = %+v, want two entries", config)
	}
	if !reflect.DeepEqual(config[0].Requests, []string{"audio1"}) {
		t.Errorf("config[0] requests = %v, want [audio1]", config[0].Requests)
	}
	if !reflect.DeepEqual(config[1].Requests, []string{"render"}) {
		t.Errorf("config[1] requests = %v, want [render]", config[1].Requests)
	}
	if string(config[0].Opaque.Parameters) != `{"codec":"aptx"}` {
		t.Errorf("config[0] parameters = %s", config[0].Opaque.Parameters)
	}
	if string(config[1].Opaque.Parameters) != `{"node":"renderD128"}` {
		t.Errorf("config[1] parameters = %s", config[1].Opaque.Parameters)
	}
}

// The claim's name and its owner are what make deleting the Play the
// whole teardown.
func TestBuildClaimNamesTheClaimForThePlayThatOwnsIt(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), nil)

	if claim.APIVersion != claimAPIVersion || claim.Kind != "ResourceClaim" {
		t.Errorf("apiVersion = %q, kind = %q", claim.APIVersion, claim.Kind)
	}
	if claim.Metadata.Name != "movie-devices" {
		t.Errorf("name = %q, want movie-devices", claim.Metadata.Name)
	}
	if claim.Metadata.Namespace != "house" {
		t.Errorf("namespace = %q, want house", claim.Metadata.Namespace)
	}
	owners := []OwnerReference{{
		APIVersion: mediaAPIVersion,
		Kind:       "Play",
		Name:       "movie",
		UID:        "play-1",
		Controller: true,
	}}
	if !reflect.DeepEqual(claim.Metadata.OwnerReferences, owners) {
		t.Errorf("ownerReferences = %+v, want %+v", claim.Metadata.OwnerReferences, owners)
	}
}

// A Player that plays sound alone is a whole Player, and its claim
// asks for nothing else.
func TestBuildClaimAsksForNothingAPlayerDoesNotHold(t *testing.T) {
	player := testPlayer()
	player.Spec.Display = nil
	player.Spec.Render = nil

	claim := buildClaim(testPlay(), player, nil)

	want := []string{"audio0", "audio1"}
	if got := claimRequests(claim); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestClaimRequestsReadsTheNamesInClaimOrder(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), nil)

	want := []string{"screen", "audio0", "audio1", "render"}
	if got := claimRequests(claim); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// Two remotes bound to the player, as the gather hands them to the
// builders.
func testBoundRemotes() []boundRemote {
	return []boundRemote{{
		Name: "armchair",
		Device: RemoteDevice{
			Class:    "gamepad",
			Selector: `device.attributes["bluetooth.liken.sh"].address == "04:4A"`,
		},
		Bindings: []compiledBinding{{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause}},
	}, {
		Name:     "sofa",
		Device:   RemoteDevice{Class: "gamepad"},
		Bindings: []compiledBinding{{EventType: evAbs, Code: 0x11, Value: -1, Action: actionVolume, Amount: 5}},
	}}
}

// The remotes follow the player's own roles, one request each, named
// for the Remote they claim.
func TestBuildClaimAsksForOneRequestPerBoundRemote(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), testBoundRemotes())

	want := []string{"screen", "audio0", "audio1", "render", "remote-armchair", "remote-sofa"}
	if got := claimRequests(claim); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	armchair := claim.Spec.Devices.Requests[4]
	if armchair.Exactly.DeviceClassName != "gamepad" {
		t.Errorf("deviceClassName = %q, want gamepad", armchair.Exactly.DeviceClassName)
	}
	selectors := []DeviceSelector{{CEL: &CELDeviceSelector{
		Expression: `device.attributes["bluetooth.liken.sh"].address == "04:4A"`,
	}}}
	if !reflect.DeepEqual(armchair.Exactly.Selectors, selectors) {
		t.Errorf("selectors = %+v, want %+v", armchair.Exactly.Selectors, selectors)
	}
	if len(claim.Spec.Devices.Requests[5].Exactly.Selectors) != 0 {
		t.Errorf("a remote with no selector carried one: %+v", claim.Spec.Devices.Requests[5])
	}
}

// A toleration with no tolerationSeconds never expires, and one with
// no effect matches every effect. A controller asleep at create time
// must not park the claim, and one that sleeps for an hour must not
// evict the film.
func TestBuildClaimToleratesASleepingRemoteWithNoTimeLimit(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), testBoundRemotes())

	armchair := claim.Spec.Devices.Requests[4]
	want := []DeviceToleration{{
		Key:      "bluetooth.liken.sh/disconnected",
		Operator: "Exists",
	}}
	if !reflect.DeepEqual(armchair.Exactly.Tolerations, want) {
		t.Fatalf("tolerations = %+v, want %+v", armchair.Exactly.Tolerations, want)
	}
	written, err := json.Marshal(armchair)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"tolerationSeconds", "effect"} {
		if strings.Contains(string(written), absent) {
			t.Errorf("request = %s, want no %s key", written, absent)
		}
	}
}

// Nothing prepares an input device, so a remote's request carries no
// opaque config at all.
func TestBuildClaimConfiguresNothingForARemote(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), testBoundRemotes())

	if len(claim.Spec.Devices.Config) != 0 {
		t.Errorf("config = %+v, want none", claim.Spec.Devices.Config)
	}
}

// The player container holds the player's devices; each remote's
// request belongs to its own sidecar.
func TestPlayerRequestsLeavesTheRemoteRequestsOut(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer(), testBoundRemotes())

	want := []string{"screen", "audio0", "audio1", "render"}
	if got := playerRequests(claim); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}
