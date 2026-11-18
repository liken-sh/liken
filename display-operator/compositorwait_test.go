package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// compositorGone makes the compositor look as it does while its
// container restarts: the Wayland socket file exists with nothing
// listening on it, and the plugin has no connection to the layout
// module.
func compositorGone(t *testing.T, plugin *draPlugin) {
	t.Helper()
	plugin.socketDir = t.TempDir()
	staleSocket(t, plugin.socketDir)
	plugin.layout = newLayoutLink(filepath.Join(t.TempDir(), "layout.sock"))
}

func joined(parts ...[]AllocatedDevice) []AllocatedDevice {
	return slices.Concat(parts...)
}

// A control device gives the pod an i2c node, which reaches the panel
// whether or not a compositor is running. A claim with an output or
// draw device needs the Wayland socket, so prepare refuses the whole
// claim, control device included, while the compositor is down.
func TestPrepareWaitsForTheCompositorOnlyForAWaylandResult(t *testing.T) {
	cases := []struct {
		name      string
		results   []AllocatedDevice
		served    bool
		delivered []string
	}{
		{"control only, compositor up", controlRequest(), true, []string{"hdmi-a-2-control"}},
		{"control only, compositor down", controlRequest(), false, []string{"hdmi-a-2-control"}},
		{"output only, compositor up", screenRequest(), true, []string{"hdmi-a-2"}},
		{"output only, compositor down", screenRequest(), false, nil},
		{"draw only, compositor up", drawRequest(), true, []string{"hdmi-a-2-draw"}},
		{"draw only, compositor down", drawRequest(), false, nil},
		{"output and control, compositor up", joined(screenRequest(), controlRequest()), true,
			[]string{"hdmi-a-2", "hdmi-a-2-control"}},
		{"output and control, compositor down", joined(screenRequest(), controlRequest()), false, nil},
		{"control before output, compositor down", joined(controlRequest(), screenRequest()), false, nil},
		{"draw and control, compositor down", joined(drawRequest(), controlRequest()), false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plugin, _ := labPluginWithClaim(t, c.results, "", claimedPanel(newFakeMonitor()))
			if !c.served {
				compositorGone(t, plugin)
			}

			claim := prepare(t, plugin)

			var got []string
			for _, device := range claim.Devices {
				got = append(got, device.DeviceName)
			}
			if !slices.Equal(got, c.delivered) {
				t.Fatalf("delivered %v, want %v (error %q)", got, c.delivered, claim.Error)
			}
			if c.delivered == nil && !strings.Contains(claim.Error, "compositor") {
				t.Errorf("error = %q, want it to say %q", claim.Error, "compositor")
			}
			if c.delivered == nil && len(specFiles(t)) != 0 {
				t.Errorf("a refused claim left %v behind", specFiles(t))
			}
		})
	}
}

// A claim with only a control device gets no Wayland socket, so its
// prepared spec sets no socket and mounts no runtime directory, even
// with the compositor down.
func TestAControlClaimDeliversNoSocketWithTheCompositorDown(t *testing.T) {
	plugin, _ := labPluginWithClaim(t, controlRequest(), "", claimedPanel(newFakeMonitor()))
	compositorGone(t, plugin)

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare refused a control claim: %s", claim.Error)
	}

	edits := preparedSpec(t).Devices[0].ContainerEdits
	if socket := waylandSocket(edits); socket != "" {
		t.Errorf("the control device mounts the socket %s, want none", socket)
	}
	if len(edits.Mounts) != 0 {
		t.Errorf("mounts are %+v, want none", edits.Mounts)
	}
}

// replacedPanel returns a panel with DDC controls whose connector now
// has a different monitor (Output.Replaced).
func replacedPanel() Output {
	output := controlledPanel(supportedControls{Brightness: true})
	output.Replaced = true
	return output
}

// compositorDown taints the output and draw devices, because their
// pods lose their Wayland connections when the compositor stops. A
// control device has no Wayland connection, so compositorDown adds no
// taint to it, and it keeps only the taint of a replaced monitor.
func TestCompositorDownTaintsOnlyTheDevicesThatNeedIt(t *testing.T) {
	cases := []struct {
		name    string
		output  Output
		device  string
		serving bool
		tainted bool
	}{
		{"output, compositor up", controlledPanel(supportedControls{Brightness: true}), "hdmi-a-1", true, false},
		{"output, compositor down", controlledPanel(supportedControls{Brightness: true}), "hdmi-a-1", false, true},
		{"draw, compositor up", controlledPanel(supportedControls{Brightness: true}), "hdmi-a-1-draw", true, false},
		{"draw, compositor down", controlledPanel(supportedControls{Brightness: true}), "hdmi-a-1-draw", false, true},
		{"control, compositor up", controlledPanel(supportedControls{Brightness: true}), "hdmi-a-1-control", true, false},
		{"control, compositor down", controlledPanel(supportedControls{Brightness: true}), "hdmi-a-1-control", false, false},
		{"control on a replaced monitor, compositor up", replacedPanel(), "hdmi-a-1-control", true, true},
		{"control on a replaced monitor, compositor down", replacedPanel(), "hdmi-a-1-control", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			devices := sliceDevices([]Output{c.output})
			if !c.serving {
				devices = compositorDown(devices, nil)
			}

			index := slices.IndexFunc(devices, func(d SliceDevice) bool { return d.Name == c.device })
			if index < 0 {
				t.Fatalf("no device %s in %v", c.device, deviceNames(devices))
			}
			taints := devices[index].Taints
			if !c.tainted && len(taints) != 0 {
				t.Fatalf("taints = %+v, want none", taints)
			}
			if c.tainted && !slices.Equal(taints, disconnectedTaints()) {
				t.Fatalf("taints = %+v, want %+v", taints, disconnectedTaints())
			}
		})
	}
}

// A claim with a control request and a screen request, refused because
// the compositor is down, also writes no brightness to the panel,
// because prepare checks the compositor before it acts on any result.
func TestAWaitingMixedClaimWritesNothingToThePanel(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithClaim(t, joined(controlRequest(), screenRequest()),
		configEntry(configFromClaim, `"screen"`, `{"brightness": 40}`), claimedPanel(panel))
	compositorGone(t, plugin)

	if claim := prepare(t, plugin); claim.Error == "" {
		t.Fatal("prepare delivered a screen that no compositor serves")
	}
	if len(panel.sets) != 0 {
		t.Errorf("a waiting claim wrote %+v to the panel", panel.sets)
	}
}

// While prepare waits for a compositor restart that it caused, the
// claim's pod has not started and has no connection to lose.
// compositorDown leaves that output untainted, so the taint does not
// evict the pod that requested the restart. The draw device of the
// same output is shared with clients that did lose their connections,
// so it is tainted.
func TestCompositorDownSparesTheOutputAPrepareWaitsOn(t *testing.T) {
	devices := compositorDown(sliceDevices([]Output{controlledPanel(supportedControls{Brightness: true})}),
		map[string]bool{"hdmi-a-1": true})

	for _, device := range devices {
		tainted := len(device.Taints) > 0
		if want := device.Name == "hdmi-a-1-draw"; tainted != want {
			t.Errorf("%s: taints = %+v, want tainted: %v", device.Name, device.Taints, want)
		}
	}
}
