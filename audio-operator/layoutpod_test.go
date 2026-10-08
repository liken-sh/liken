package main

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// containerIn finds one container of the pod template by name, among
// the init containers and the regular ones.
func containerIn(t *testing.T, daemonSet *appsv1.DaemonSet, name string) corev1.Container {
	t.Helper()
	spec := daemonSet.Spec.Template.Spec
	for _, container := range append(spec.InitContainers, spec.Containers...) {
		if container.Name == name {
			return container
		}
	}
	t.Fatalf("the DaemonSet holds no container %s", name)
	return corev1.Container{}
}

// A layout change reaches PipeWire through three parts of the pod: the
// declare container names the Sinks with the machine's name, the
// operator writes the drop-in, and the PipeWire container's first
// process restarts PipeWire in place. No liveness probe restarts the
// container for it, because that restart would wait in the kubelet's
// crash backoff. WirePlumber's first process starts WirePlumber again
// after the new PipeWire.
func TestThePodCarriesALayoutChangeToPipeWire(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")

	declare := containerIn(t, daemonSet, "declare")
	named := false
	for _, variable := range declare.Env {
		if variable.Name == "NODE_NAME" && variable.ValueFrom != nil && variable.ValueFrom.FieldRef != nil &&
			variable.ValueFrom.FieldRef.FieldPath == "spec.nodeName" {
			named = true
		}
	}
	if !named {
		t.Errorf("the declare container has no NODE_NAME from spec.nodeName: %+v", declare.Env)
	}

	for _, mount := range containerIn(t, daemonSet, "operator").VolumeMounts {
		if mount.Name == "pipewire-config" && mount.ReadOnly {
			t.Errorf("the operator mounts the drop-in directory read-only")
		}
	}

	pipewire := containerIn(t, daemonSet, "pipewire")
	if pipewire.LivenessProbe != nil {
		t.Errorf("the PipeWire container has a liveness probe: %+v", pipewire.LivenessProbe)
	}
	for name, mode := range map[string]string{"pipewire": pipewireMode, "wireplumber": wireplumberMode} {
		command := containerIn(t, daemonSet, name).Command
		if len(command) < 2 || command[0] != "/usr/local/bin/audio-operator" || command[1] != mode {
			t.Errorf("the %s container runs %q, want the operator's %s mode", name, command, mode)
		}
	}
}
