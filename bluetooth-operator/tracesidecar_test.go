package main

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// initContainerNames lists the names of the pod's init containers, in the
// order the kubelet starts them.
func initContainerNames(containers []corev1.Container) []string {
	names := make([]string, 0, len(containers))
	for _, container := range containers {
		names = append(names, container.Name)
	}
	return names
}

// btmon reports only what happens after it binds the monitor channel,
// so it starts before bluetoothd: the commands that bluetoothd sends at
// adapter registration, which tell the kernel which bonded devices may
// reconnect, are in the trace. bondfetch runs to completion first, as
// the comment on the pod explains.
func TestTheTraceStartsBeforeBluetoothd(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	got := initContainerNames(daemonSet.Spec.Template.Spec.InitContainers)
	want := []string{"bondfetch", "btmon", "bluetoothd"}
	if !slices.Equal(got, want) {
		t.Errorf("the init containers are %v, want %v", got, want)
	}
}

// btmon buffers its output fully when stdout is a pipe, and the
// container log is a pipe. A terminal makes the C library flush each
// line, so a line reaches the log when btmon writes it.
func TestTheTraceWritesToATerminal(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	trace := daemonSet.Spec.Template.Spec.InitContainers[1]
	if trace.Name != "btmon" || !trace.TTY {
		t.Errorf("the second init container is %q with tty %v, want btmon with tty true", trace.Name, trace.TTY)
	}
}

// settingsMount answers the mount of the settings volume in one
// container, and fails the test when the container has none.
func settingsMount(t *testing.T, container corev1.Container) corev1.VolumeMount {
	t.Helper()
	for _, mount := range container.VolumeMounts {
		if mount.Name == "settings" {
			return mount
		}
	}
	t.Fatalf("the %s container does not mount the settings volume", container.Name)
	return corev1.VolumeMount{}
}

// start-btmon runs the trace, and reads its setting from the settings
// volume. The operator writes that setting, so its own mount is
// writable, and the trace's mount is not.
func TestTheTraceReadsItsSettingFromTheSettingsVolume(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	trace := daemonSet.Spec.Template.Spec.InitContainers[1]
	operator := daemonSet.Spec.Template.Spec.Containers[0]

	if !slices.Equal(trace.Command, []string{"/usr/local/bin/start-btmon"}) {
		t.Errorf("the btmon container runs %v, want start-btmon", trace.Command)
	}
	if mount := settingsMount(t, trace); !mount.ReadOnly {
		t.Error("the btmon container mounts the settings volume writable, want read-only")
	}
	if mount := settingsMount(t, operator); mount.ReadOnly {
		t.Error("the operator container mounts the settings volume read-only, want writable")
	}
}
