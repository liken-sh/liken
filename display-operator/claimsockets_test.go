package main

// These tests drive the prepare and unprepare paths against a real
// layout module on a real socket, and read what the module was asked
// to open and close. What they cover is the rule that names a claim's
// socket, and the replay that opens it again after the compositor
// restarts.

import (
	"strings"
	"testing"
)

func TestClaimSocketNamesGivesOneClaimOneSocket(t *testing.T) {
	for name, test := range map[string]struct {
		results []AllocatedDevice
		want    map[string]string
	}{
		// The output and the draw companion on one connector are one
		// screen, so both deliver the claim's own name.
		"one screen and its draw companion": {
			results: []AllocatedDevice{
				{Request: "screen", Driver: DriverName, Device: "hdmi-a-1"},
				{Request: "idle", Driver: DriverName, Device: "hdmi-a-1-draw"},
			},
			want: map[string]string{"hdmi-a-1": "wayland-" + testClaimUID},
		},
		// A socket belongs to one output, because the module remembers
		// which output the surfaces on it go on, so a second connector
		// takes a name of its own.
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
		// A control result draws nothing, and another driver's result
		// is that driver's own to prepare.
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
	// The socket is the claim's identity, so the module opens it for
	// the connector the claim allocated before the spec that names it
	// is written.
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused a live output: %s", claim.Error)
	}
	want := "listen wayland-" + testClaimUID + " HDMI-A-1"
	if requests := module.read(); !containsString(requests, want) {
		t.Errorf("the module read %q, want %q in it", requests, want)
	}
}

func TestPrepareOpensOneSocketForEachConnector(t *testing.T) {
	// A claim on two screens holds one socket on each of them, and the
	// second one carries the output's name so that the module can tell
	// which screen a surface arrived for.
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "left", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
		{Request: "right", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2"},
	}, "")

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused two live outputs: %s", claim.Error)
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
	// The spec is what tells each container which name is its own.
	spec := preparedSpec(t)
	if len(spec.Devices) != 2 {
		t.Fatalf("spec devices = %+v", spec.Devices)
	}
	for _, device := range spec.Devices {
		want := "wayland-" + testClaimUID
		if strings.HasSuffix(device.Name, "hdmi-a-2") {
			want += "-hdmi-a-2"
		}
		if got := waylandSocket(device.ContainerEdits); got != want {
			t.Errorf("%s delivers %q, want %q", device.Name, got, want)
		}
	}
}

func TestPrepareRefusesWhileTheLayoutModuleIsNotServing(t *testing.T) {
	// The compositor answers on its socket and its module answers
	// nothing, which is the window a compositor restart opens. A
	// delivery now would name a socket that nothing listens on, so the
	// kubelet holds the pod and retries.
	plugin, _, _ := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	plugin.layout = newLayoutLink(layoutSocketPath)

	claim := prepare(t, plugin)
	if !strings.Contains(claim.Error, "layout module is not serving") {
		t.Errorf("prepare answered %q, want the module named", claim.Error)
	}
	if files := specFiles(t); len(files) != 0 {
		t.Errorf("prepare wrote %v with no module serving", files)
	}
}

func TestUnprepareClosesTheClaimsSocket(t *testing.T) {
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare refused a live output: %s", claim.Error)
	}

	unprepare(t, plugin)

	want := "close wayland-" + testClaimUID
	if requests := module.read(); !containsString(requests, want) {
		t.Errorf("the module read %q, want %q in it", requests, want)
	}
}

func TestUnprepareSucceedsWithNoModuleServing(t *testing.T) {
	// The sockets exist only in the running compositor, so a module
	// that is gone took them already. An unprepare that failed here
	// would hold the kubelet for a socket that is gone.
	plugin, _, _ := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare refused a live output: %s", claim.Error)
	}
	plugin.layout = newLayoutLink(layoutSocketPath)

	unprepare(t, plugin)

	if files := specFiles(t); len(files) != 0 {
		t.Errorf("the spec files are %v, want none left", files)
	}
}

func TestReplayOpensEverySocketThePreparedClaimsHold(t *testing.T) {
	// The compositor restarts on every mode change, and every socket
	// the module opened goes with it. The specs on disk are what says
	// which claims still hold one.
	plugin, _, module := labPluginWithModule(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	}, "")
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare refused a live output: %s", claim.Error)
	}
	// A pod prepared before the per-claim sockets existed holds
	// wayland-0, which weston opens itself, and the draw companion of
	// another claim holds that claim's own socket.
	if err := writeCDISpec("idle", []cdiDevice{
		{Name: "idle-hdmi-a-2-draw", ContainerEdits: outputEdits(plugin.socketDir, "wayland-idle", "hdmi-a-2")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeCDISpec("old", []cdiDevice{
		{Name: "old-hdmi-a-2", ContainerEdits: outputEdits(plugin.socketDir, socketName, "hdmi-a-2")},
	}); err != nil {
		t.Fatal(err)
	}

	if err := plugin.replaySockets(); err != nil {
		t.Fatal(err)
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
		t.Errorf("the replay re-opened the compositor's own socket: %q", requests)
	}
}

func TestReplayReportsASocketWhoseOutputTheCardDoesNotCarry(t *testing.T) {
	// A monitor that left between the prepare and the restart has no
	// connector to open a socket on. One such claim must not cost
	// every other claim its socket, so the failure names it and the
	// rest are opened.
	plugin, _, module := labPluginWithModule(t, nil, "")
	if err := writeCDISpec("gone", []cdiDevice{
		{Name: "gone-hdmi-a-9", ContainerEdits: outputEdits(plugin.socketDir, "wayland-gone", "hdmi-a-9")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeCDISpec("live", []cdiDevice{
		{Name: "live-hdmi-a-1", ContainerEdits: outputEdits(plugin.socketDir, "wayland-live", "hdmi-a-1")},
	}); err != nil {
		t.Fatal(err)
	}

	err := plugin.replaySockets()
	if err == nil {
		t.Fatal("the replay answered no error for an output the card does not carry")
	}
	if !strings.Contains(err.Error(), "hdmi-a-9") {
		t.Errorf("the error is %q, and it does not name the output", err)
	}
	if want := "listen wayland-live HDMI-A-1"; !containsString(module.read(), want) {
		t.Errorf("the module read %q, want %q in it", module.read(), want)
	}
}

func TestPreparedSocketsReadsTheSpecsTheDriverWrote(t *testing.T) {
	cdiDir = t.TempDir()
	// One claim on two screens, and a control device that delivers no
	// socket at all.
	if err := writeCDISpec("two", []cdiDevice{
		{Name: "two-hdmi-a-1", ContainerEdits: outputEdits("/run/display", "wayland-two", "hdmi-a-1")},
		{Name: "two-hdmi-a-1-draw", ContainerEdits: outputEdits("/run/display", "wayland-two", "hdmi-a-1")},
		{Name: "two-hdmi-a-2", ContainerEdits: outputEdits("/run/display", "wayland-two-hdmi-a-2", "hdmi-a-2")},
		{Name: "two-hdmi-a-2-control", ContainerEdits: controlEdits("/dev/i2c-4")},
	}); err != nil {
		t.Fatal(err)
	}

	// The draw device shares the output's socket, so the claim holds
	// two names and not three.
	sockets, err := preparedSockets("two")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"wayland-two", "wayland-two-hdmi-a-2"}
	if len(sockets) != len(want) {
		t.Fatalf("the claim holds %v, want %v", sockets, want)
	}
	for i, socket := range want {
		if sockets[i] != socket {
			t.Errorf("socket %d = %q, want %q", i, sockets[i], socket)
		}
	}

	outputs, err := preparedSocketOutputs()
	if err != nil {
		t.Fatal(err)
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
	// The kubelet repeats an unprepare whenever it has no record that
	// the call succeeded, so the second call finds the file gone and
	// has nothing left to close.
	cdiDir = t.TempDir()

	sockets, err := preparedSockets("never-prepared")
	if err != nil {
		t.Fatal(err)
	}
	if len(sockets) != 0 {
		t.Errorf("a claim with no spec holds %v", sockets)
	}
}
