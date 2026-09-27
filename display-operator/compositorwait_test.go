package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// compositorGone takes the compositor away from a lab plugin: its
// socket file is left behind with nothing answering on it, and the
// layout module it ran has no connection. A restart of the
// compositor's container looks like this until the new one listens.
func compositorGone(t *testing.T, plugin *draPlugin) {
	t.Helper()
	plugin.socketDir = t.TempDir()
	staleSocket(t, plugin.socketDir)
	plugin.layout = newLayoutLink(filepath.Join(t.TempDir(), "layout.sock"))
}

func joined(parts ...[]AllocatedDevice) []AllocatedDevice {
	return slices.Concat(parts...)
}

// A control device delivers an i2c node, and the i2c bus reaches the
// panel whether or not a compositor draws on it. Every result that
// delivers a Wayland socket still waits for the compositor, so a claim
// that holds one waits whole.
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

// A control-only claim receives no Wayland socket, so its delivery
// names no socket, mounts no runtime directory, and asks the layout
// module for nothing, with or without a compositor.
func TestAControlClaimDeliversNoSocketWithTheCompositorDown(t *testing.T) {
	plugin, _ := labPluginWithClaim(t, controlRequest(), "", claimedPanel(newFakeMonitor()))
	compositorGone(t, plugin)

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare refused a control claim: %s", claim.Error)
	}

	edits := preparedSpec(t).Devices[0].ContainerEdits
	if socket := waylandSocket(edits); socket != "" {
		t.Errorf("the control device delivers the socket %s", socket)
	}
	if len(edits.Mounts) != 0 {
		t.Errorf("mounts = %+v", edits.Mounts)
	}
}

// replacedPanel is a controlled panel whose connector came back
// carrying a different monitor, so its output serves nobody.
func replacedPanel() Output {
	output := controlledPanel(supportedControls{Brightness: true})
	output.Replaced = true
	return output
}

// The taint the compositor's absence adds is for the clients whose
// Wayland connections died with it. A control device's holder has no
// such connection, so the control device keeps its own taints: those
// of a screen that serves nobody, and nothing more.
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
				devices = compositorDown(devices)
			}

			index := slices.IndexFunc(devices, func(d SliceDevice) bool { return d.Name == c.device })
			if index < 0 {
				t.Fatalf("no device %s in %v", c.device, deviceNames(devices))
			}
			taints := devices[index].Taints
			if !c.tainted && len(taints) != 0 {
				t.Fatalf("taints = %+v, want none", taints)
			}
			if c.tainted && !slices.Equal(taints, unservableTaints()) {
				t.Fatalf("taints = %+v, want %+v", taints, unservableTaints())
			}
		})
	}
}

// A mixed claim that waits for the compositor writes nothing to the
// panel on that pass, even when its screen request states a
// brightness, because the check runs before any result acts.
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
