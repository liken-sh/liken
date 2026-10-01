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
// operator writes the drop-in, and the PipeWire container's liveness
// probe asks the kubelet for the restart.
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

	probe := containerIn(t, daemonSet, "pipewire").LivenessProbe
	if probe == nil || probe.Exec == nil || len(probe.Exec.Command) != 2 || probe.Exec.Command[1] != declarationMode {
		t.Errorf("the PipeWire container's liveness probe is %+v, want the %s check", probe, declarationMode)
	}
}
