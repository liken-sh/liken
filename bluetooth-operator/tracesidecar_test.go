package main

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// initContainerNames returns the names of the init containers, in the
// order the kubelet starts them.
func initContainerNames(containers []corev1.Container) []string {
	names := make([]string, 0, len(containers))
	for _, container := range containers {
		names = append(names, container.Name)
	}
	return names
}

// When bluetoothd registers the adapter, it sends the kernel the list
// of bonded devices that may reconnect, and rescan reads those commands
// from the monitor channel to learn which devices to add again. btmon
// and rescan receive only the traffic after they bind the channel, so
// both must start before bluetoothd. bondfetch runs to completion first,
// for the reason given in deploy/operator.yaml.
func TestTheMonitorReadersStartBeforeBluetoothd(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	got := initContainerNames(daemonSet.Spec.Template.Spec.InitContainers)
	want := []string{"bondfetch", "btmon", "rescan", "bluetoothd"}
	if !slices.Equal(got, want) {
		t.Errorf("the init containers are %v, want %v", got, want)
	}
}

// btmon buffers its output in full when stdout is a pipe, and the
// container log is a pipe. With a TTY, the C library flushes each line,
// so every line reaches the log as btmon writes it.
func TestTheTraceWritesToATerminal(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	trace := daemonSet.Spec.Template.Spec.InitContainers[1]
	if trace.Name != "btmon" || !trace.TTY {
		t.Errorf("the second init container is %q with tty %v, want btmon with tty true", trace.Name, trace.TTY)
	}
}

// settingsMount returns the container's mount of the settings volume,
// and fails the test when the container has none.
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

// start-btmon reads the trace setting from the settings volume. The
// operator writes that setting, so it mounts the volume writable, and
// the btmon container mounts it read-only.
func TestTheTraceReadsItsSettingFromTheSettingsVolume(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	trace := daemonSet.Spec.Template.Spec.InitContainers[1]
	operator := daemonSet.Spec.Template.Spec.Containers[0]

	if !slices.Equal(trace.Command, []string{"/start-btmon"}) {
		t.Errorf("the btmon container runs %v, want start-btmon", trace.Command)
	}
	if mount := settingsMount(t, trace); !mount.ReadOnly {
		t.Error("the btmon container mounts the settings volume writable, want read-only")
	}
	if mount := settingsMount(t, operator); mount.ReadOnly {
		t.Error("the operator container mounts the settings volume read-only, want writable")
	}
}
