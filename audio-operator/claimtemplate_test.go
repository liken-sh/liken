package main

import (
	"os"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"sigs.k8s.io/yaml"
)

// PipeWire follows every sound card of the node by itself, so the pod
// must keep running when one USB card is unplugged. machine-operator
// taints such a device liken.sh/unplugged with the effect NoExecute,
// and the pod's claim tolerates it, so the eviction does not restart
// the sound of every other card on the node.
func TestTheSoundCardClaimToleratesAnUnpluggedCard(t *testing.T) {
	raw, err := os.ReadFile("deploy/operator.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var template resourcev1.ResourceClaimTemplate
	for _, document := range strings.Split(string(raw), "\n---\n") {
		if strings.Contains(document, "kind: ResourceClaimTemplate") {
			if err := yaml.UnmarshalStrict([]byte(document), &template); err != nil {
				t.Fatal(err)
			}
		}
	}
	requests := template.Spec.Spec.Devices.Requests
	if len(requests) != 1 || requests[0].Exactly == nil {
		t.Fatalf("requests = %+v, want one exact request", requests)
	}
	want := resourcev1.DeviceToleration{
		Key:      "liken.sh/unplugged",
		Operator: resourcev1.DeviceTolerationOpExists,
		Effect:   resourcev1.DeviceTaintEffectNoExecute,
	}
	tolerations := requests[0].Exactly.Tolerations
	if len(tolerations) != 1 || tolerations[0] != want {
		t.Errorf("tolerations = %+v, want only %+v", tolerations, want)
	}
}
