package main

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// start-rescan binds the HCI monitor channel, which needs CAP_NET_RAW,
// and sends the Add Device command, which needs CAP_NET_ADMIN. It uses
// only kernel sockets, so it mounts no volumes.
func TestTheRescanContainerAddsOnlyNetRawAndNetAdmin(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	rescan := daemonSet.Spec.Template.Spec.InitContainers[2]
	if rescan.Name != "rescan" {
		t.Fatalf("the third init container is %q, want rescan", rescan.Name)
	}
	if !slices.Equal(rescan.Command, []string{"/start-rescan"}) {
		t.Errorf("the rescan container runs %v, want start-rescan", rescan.Command)
	}
	capabilities := rescan.SecurityContext.Capabilities
	if !slices.Equal(capabilities.Drop, []corev1.Capability{"ALL"}) || !slices.Equal(capabilities.Add, []corev1.Capability{"NET_RAW", "NET_ADMIN"}) {
		t.Errorf("the rescan container drops %v and adds %v, want ALL and [NET_RAW NET_ADMIN]", capabilities.Drop, capabilities.Add)
	}
	if len(rescan.VolumeMounts) != 0 {
		t.Errorf("the rescan container mounts %v, want nothing", rescan.VolumeMounts)
	}
}
