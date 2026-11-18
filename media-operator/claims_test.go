package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testPlay() *Play {
	return &Play{
		Metadata: ObjectMeta{Name: "movie", Namespace: "house", UID: "play-1"},
		Spec:     PlaySpec{Players: []string{"theater"}, Items: []PlayItem{{URI: "https://films.example/film.mkv"}}},
	}
}

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
	claim := buildClaim(testPlay(), testPlayer())

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
			t.Errorf("request %s has no exactly block, want one", role.name)
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

// A device with no selector gets no selectors key, because the API
// server rejects an empty CEL expression.
func TestBuildClaimCopiesOnlyTheSelectorsThePlayerSets(t *testing.T) {
	expression := `device.attributes["display.liken.sh"].connector == "HDMI-A-1"`
	player := testPlayer()
	player.Spec.Display.Selector = expression

	claim := buildClaim(testPlay(), player)

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
		t.Fatalf("marshaling the render request: %v", err)
	}
	if strings.Contains(string(written), "selectors") {
		t.Errorf("render = %s, want no selectors key", written)
	}
}

func TestBuildClaimHasNoTolerations(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer())

	for _, request := range claim.Spec.Devices.Requests {
		if len(request.Exactly.Tolerations) != 0 {
			t.Errorf("request %q has tolerations %+v, want none",
				request.Name, request.Exactly.Tolerations)
		}
	}
}

func TestBuildClaimConfiguresOnlyTheDevicesWithParameters(t *testing.T) {
	mode := json.RawMessage(`{"mode":"1920x1080@60"}`)
	player := testPlayer()
	player.Spec.Display.Parameters = &DeviceParameters{Driver: "display.liken.sh", Values: mode}

	claim := buildClaim(testPlay(), player)

	config := claim.Spec.Devices.Config
	if len(config) != 1 {
		t.Fatalf("config = %+v, want one entry", config)
	}
	if !reflect.DeepEqual(config[0].Requests, []string{"screen"}) {
		t.Errorf("requests = %v, want [screen]", config[0].Requests)
	}
	if config[0].Opaque == nil {
		t.Fatal("the config entry has no opaque block, want one")
	}
	if config[0].Opaque.Driver != "display.liken.sh" {
		t.Errorf("driver = %q, want display.liken.sh", config[0].Opaque.Driver)
	}
	if string(config[0].Opaque.Parameters) != string(mode) {
		t.Errorf("parameters = %s, want %s", config[0].Opaque.Parameters, mode)
	}
}

func TestBuildClaimAddsOneConfigBlockForEachDeviceWithParameters(t *testing.T) {
	player := testPlayer()
	player.Spec.Sinks[1].Parameters = &DeviceParameters{
		Driver: "audio.liken.sh",
		Values: json.RawMessage(`{"codec":"aptx"}`),
	}
	player.Spec.Render.Parameters = &DeviceParameters{
		Driver: "gpu.liken.sh",
		Values: json.RawMessage(`{"node":"renderD128"}`),
	}

	claim := buildClaim(testPlay(), player)

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
		t.Errorf("config[0] parameters = %s, want {\"codec\":\"aptx\"}", config[0].Opaque.Parameters)
	}
	if string(config[1].Opaque.Parameters) != `{"node":"renderD128"}` {
		t.Errorf("config[1] parameters = %s, want {\"node\":\"renderD128\"}", config[1].Opaque.Parameters)
	}
}

func TestBuildClaimNamesTheClaimForThePlayThatOwnsIt(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer())

	if claim.APIVersion != claimAPIVersion || claim.Kind != "ResourceClaim" {
		t.Errorf("apiVersion = %q and kind = %q, want %q and ResourceClaim", claim.APIVersion, claim.Kind, claimAPIVersion)
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

func TestBuildClaimLeavesOutRequestsForDevicesThePlayerLacks(t *testing.T) {
	player := testPlayer()
	player.Spec.Display = nil
	player.Spec.Render = nil

	claim := buildClaim(testPlay(), player)

	want := []string{"audio0", "audio1"}
	if got := claimRequests(claim); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestClaimRequestsReadsTheNamesInClaimOrder(t *testing.T) {
	claim := buildClaim(testPlay(), testPlayer())

	want := []string{"screen", "audio0", "audio1", "render"}
	if got := claimRequests(claim); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// testBoundRemotes returns Remotes with the topics that the operator
// fills in before it builds the command sidecar.
func testBoundRemotes() []boundRemote {
	return []boundRemote{{
		Name:        "armchair",
		EventsTopic: "liken/media/remotes/house/armchair/events",
		FocusTopic:  "liken/media/remotes/house/armchair/focus",
	}, {
		Name:        "sofa",
		EventsTopic: "liken/media/remotes/house/sofa/events",
		FocusTopic:  "liken/media/remotes/house/sofa/focus",
	}}
}
