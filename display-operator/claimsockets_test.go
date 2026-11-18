package main

// These tests run prepare and unprepare against a fake layout module
// on a Unix socket, and check which sockets the module was asked to
// open and close.

import (
	"strings"
	"testing"
)

func TestClaimSocketNamesGivesOneClaimOneSocket(t *testing.T) {
	for name, test := range map[string]struct {
		results []AllocatedDevice
		want    map[string]string
	}{
		"one screen and its draw companion": {
			results: []AllocatedDevice{
				{Request: "screen", Driver: DriverName, Device: "hdmi-a-1"},
				{Request: "idle", Driver: DriverName, Device: "hdmi-a-1-draw"},
			},
			want: map[string]string{"hdmi-a-1": "wayland-" + testClaimUID},
		},
		"two screens": {
			results: []AllocatedDevice{
				{Request: "left", Driver: DriverName, Device: "hdmi-a-1"},
				{Request: "right", Driver: DriverName, Device: "hdmi-a-2"},
			},
			want: map[string]string{
				"hdmi-a-1": "wayland-" + testClaimUID,
				"hdmi-a-2": "wayland-" + testClaimUID + "-hdmi-a-2",
			},
		},
		"a control channel and another driver": {
			results: []AllocatedDevice{
				{Request: "panel", Driver: DriverName, Device: "hdmi-a-1-control"},
				{Request: "speakers", Driver: "audio.liken.sh", Device: "hdmi-a-1"},
			},
			want: map[string]string{},
		},
	} {
		got := claimSocketNames(testClaimUID, test.results)
		if len(got) != len(test.want) {
			t.Errorf("%s: sockets = %v, want %v", name, got, test.want)
			continue
		}
		for device, socket := range test.want {
			if got[device] != socket {
				t.Errorf("%s: %s = %q, want %q", name, device, got[device], socket)
			}
		}
	}
}

func TestPrepareOpensTheClaimsOwnSocket(t *testing.T) {
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare returned the error %q for a connected output", claim.Error)
	}
	want := "listen wayland-" + testClaimUID + " HDMI-A-1"
	if requests := module.read(); !containsString(requests, want) {
		t.Errorf("the module read %q, want %q in it", requests, want)
	}
}

func TestPrepareOpensOneSocketForEachConnector(t *testing.T) {
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "left", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
		{Request: "right", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2"},
	}, "")

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare returned the error %q for two connected outputs", claim.Error)
	}
	requests := module.read()
	for _, want := range []string{
		"listen wayland-" + testClaimUID + " HDMI-A-1",
		"listen wayland-" + testClaimUID + "-hdmi-a-2 HDMI-A-2",
	} {
		if !containsString(requests, want) {
			t.Errorf("the module read %q, want %q in it", requests, want)
		}
	}
	spec := preparedSpec(t)
	if len(spec.Devices) != 2 {
		t.Fatalf("the CDI spec has devices %+v, want 2", spec.Devices)
	}
	for _, device := range spec.Devices {
		want := "wayland-" + testClaimUID
		if strings.HasSuffix(device.Name, "hdmi-a-2") {
			want += "-hdmi-a-2"
		}
		if got := waylandSocket(device.ContainerEdits); got != want {
			t.Errorf("the socket of %s is %q, want %q", device.Name, got, want)
		}
	}
}

func TestPrepareRefusesWhileTheLayoutModuleIsNotServing(t *testing.T) {
	// During a compositor restart, the compositor accepts connections
	// before the module serves. A CDI spec written then would name a
	// socket that nothing listens on, so prepare fails and the kubelet
	// retries.
	plugin, _, _ := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	plugin.layout = newLayoutLink(layoutSocketPath)

	claim := prepare(t, plugin)
	if !strings.Contains(claim.Error, "layout module is not serving") {
		t.Errorf("prepare returned the error %q, want one that reports the layout module is not serving", claim.Error)
	}
	if files := specFiles(t); len(files) != 0 {
		t.Errorf("prepare wrote the spec files %v while the module was not serving, want none", files)
	}
}

func TestUnprepareClosesTheClaimsSocket(t *testing.T) {
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q for a connected output", claim.Error)
	}

	unprepare(t, plugin)

	want := "close wayland-" + testClaimUID
	if requests := module.read(); !containsString(requests, want) {
		t.Errorf("the module read %q, want %q in it", requests, want)
	}
}

func TestUnprepareSucceedsWithNoModuleServing(t *testing.T) {
	plugin, _, _ := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q for a connected output", claim.Error)
	}
	plugin.layout = newLayoutLink(layoutSocketPath)

	unprepare(t, plugin)

	if files := specFiles(t); len(files) != 0 {
		t.Errorf("the spec files are %v, want none left", files)
	}
}

func TestReplayOpensEverySocketThePreparedClaimsHold(t *testing.T) {
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q for a connected output", claim.Error)
	}
	// A draw device of another claim sets that claim's socket. A spec
	// that sets wayland-0, the compositor's own socket, must not be
	// replayed.
	if err := writeCDISpec("idle", []cdiDevice{
		{Name: "idle-hdmi-a-2-draw", ContainerEdits: outputEdits(plugin.socketDir, "wayland-idle", "hdmi-a-2")},
	}); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}
	if err := writeCDISpec("old", []cdiDevice{
		{Name: "old-hdmi-a-2", ContainerEdits: outputEdits(plugin.socketDir, socketName, "hdmi-a-2")},
	}); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	if err := plugin.replaySockets(); err != nil {
		t.Fatalf("replaying the sockets: %v", err)
	}

	requests := module.read()
	for _, want := range []string{
		"listen wayland-" + testClaimUID + " HDMI-A-1",
		"listen wayland-idle HDMI-A-2",
	} {
		if !containsString(requests, want) {
			t.Errorf("the module read %q, want %q in it", requests, want)
		}
	}
	if containsString(requests, "listen wayland-0 HDMI-A-2") {
		t.Errorf("the replay opened the compositor's own socket again: %q", requests)
	}
}

func TestReplayReportsASocketWhoseOutputTheCardDoesNotCarry(t *testing.T) {
	// A monitor unplugged between prepare and the restart has no
	// connector to open a socket on.
	plugin, _, module := labPluginWithModule(t, nil, "")
	if err := writeCDISpec("gone", []cdiDevice{
		{Name: "gone-hdmi-a-9", ContainerEdits: outputEdits(plugin.socketDir, "wayland-gone", "hdmi-a-9")},
	}); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}
	if err := writeCDISpec("live", []cdiDevice{
		{Name: "live-hdmi-a-1", ContainerEdits: outputEdits(plugin.socketDir, "wayland-live", "hdmi-a-1")},
	}); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	err := plugin.replaySockets()
	if err == nil {
		t.Fatal("replaySockets returned no error for an output the card does not have, want an error")
	}
	if !strings.Contains(err.Error(), "hdmi-a-9") {
		t.Errorf("the error is %q, want it to name hdmi-a-9", err)
	}
	if want := "listen wayland-live HDMI-A-1"; !containsString(module.read(), want) {
		t.Errorf("the module read %q, want %q in it", module.read(), want)
	}
}

func TestPreparedSocketsReadsTheSpecsTheDriverWrote(t *testing.T) {
	cdiDir = t.TempDir()
	if err := writeCDISpec("two", []cdiDevice{
		{Name: "two-hdmi-a-1", ContainerEdits: outputEdits("/run/display", "wayland-two", "hdmi-a-1")},
		{Name: "two-hdmi-a-1-draw", ContainerEdits: outputEdits("/run/display", "wayland-two", "hdmi-a-1")},
		{Name: "two-hdmi-a-2", ContainerEdits: outputEdits("/run/display", "wayland-two-hdmi-a-2", "hdmi-a-2")},
		{Name: "two-hdmi-a-2-control", ContainerEdits: controlEdits("/dev/i2c-4")},
	}); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	sockets, err := preparedSockets("two")
	if err != nil {
		t.Fatalf("reading the sockets of claim two: %v", err)
	}
	want := []string{"wayland-two", "wayland-two-hdmi-a-2"}
	if len(sockets) != len(want) {
		t.Fatalf("the claim has the sockets %v, want %v", sockets, want)
	}
	for i, socket := range want {
		if sockets[i] != socket {
			t.Errorf("socket %d = %q, want %q", i, sockets[i], socket)
		}
	}

	outputs, err := preparedSocketOutputs()
	if err != nil {
		t.Fatalf("reading the prepared socket outputs: %v", err)
	}
	for socket, output := range map[string]string{
		"wayland-two":          "hdmi-a-1",
		"wayland-two-hdmi-a-2": "hdmi-a-2",
	} {
		if outputs[socket] != output {
			t.Errorf("%s = %q, want %q", socket, outputs[socket], output)
		}
	}
	if len(outputs) != 2 {
		t.Errorf("the prepared sockets are %v, want two of them", outputs)
	}
}

func TestPreparedSocketsReadsAClaimThatIsAlreadyGone(t *testing.T) {
	// The kubelet repeats an unprepare when it has no record that the
	// first one succeeded, and the spec file is already deleted.
	cdiDir = t.TempDir()

	sockets, err := preparedSockets("never-prepared")
	if err != nil {
		t.Fatalf("reading the sockets of a claim that was never prepared: %v", err)
	}
	if len(sockets) != 0 {
		t.Errorf("a claim with no spec has the sockets %v, want none", sockets)
	}
}
