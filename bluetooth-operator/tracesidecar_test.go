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
