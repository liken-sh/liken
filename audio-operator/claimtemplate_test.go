package main

import (
	"os"
	"strings"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	"sigs.k8s.io/yaml"
)

// machine-operator taints the device of an unplugged USB card with
// liken.sh/unplugged and the NoExecute effect. The operator's pod claims
// every sound card of the node, so without a toleration the taint evicts
// the pod and stops the sound of every other card. The pod can stay,
// because PipeWire follows the sound cards of the node by itself.
func TestTheSoundCardClaimToleratesAnUnpluggedCard(t *testing.T) {
	raw, err := os.ReadFile("deploy/operator.yaml")
	if err != nil {
		t.Fatalf("reading the operator manifest: %v", err)
	}
	var template resourcev1.ResourceClaimTemplate
	for _, document := range strings.Split(string(raw), "\n---\n") {
		if strings.Contains(document, "kind: ResourceClaimTemplate") {
			if err := yaml.UnmarshalStrict([]byte(document), &template); err != nil {
				t.Fatalf("parsing the ResourceClaimTemplate: %v", err)
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
