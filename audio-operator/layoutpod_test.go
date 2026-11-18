package main

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// containerIn searches the init containers and the containers of the
// pod template.
func containerIn(t *testing.T, daemonSet *appsv1.DaemonSet, name string) corev1.Container {
	t.Helper()
	spec := daemonSet.Spec.Template.Spec
	for _, container := range append(spec.InitContainers, spec.Containers...) {
		if container.Name == name {
			return container
		}
	}
	t.Fatalf("the DaemonSet has no container %s", name)
	return corev1.Container{}
}

// A layout change reaches PipeWire only when the DaemonSet has these
// settings. The declare container gets NODE_NAME, so it can name the
// Sinks after the machine. The operator mounts the PipeWire config
// directory read-write, because it writes the drop-in there. The pipewire
// and wireplumber containers run the operator binary in pipewireMode and
// wireplumberMode (restarts.go). The first restarts PipeWire in place
// when the drop-in changes, and the second restarts WirePlumber when a
// new PipeWire replaces the one it served. The pipewire container has no
// liveness probe, because a probe would restart the whole container and
// the kubelet's crash backoff would delay the restart.
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
		t.Errorf("the declare container's env is %+v, want NODE_NAME from spec.nodeName", declare.Env)
	}

	for _, mount := range containerIn(t, daemonSet, "operator").VolumeMounts {
		if mount.Name == "pipewire-config" && mount.ReadOnly {
			t.Errorf("the operator mounts the drop-in directory read-only, want read-write so it can write a layout")
		}
	}

	pipewire := containerIn(t, daemonSet, "pipewire")
	if pipewire.LivenessProbe != nil {
		t.Errorf("the PipeWire container has the liveness probe %+v, want none", pipewire.LivenessProbe)
	}
	for name, mode := range map[string]string{"pipewire": pipewireMode, "wireplumber": wireplumberMode} {
		command := containerIn(t, daemonSet, name).Command
		if len(command) < 2 || command[0] != "/audio-operator" || command[1] != mode {
			t.Errorf("the %s container runs %q, want the operator's %s mode", name, command, mode)
		}
	}
}
