package main

import (
	"slices"
	"testing"
)

// The upload makes the bare chip enumerate again with its loaded IDs,
// and machine-operator taints a device that left while a claim held it.
// The upload pod is a bare pod that nothing runs again, so an eviction
// would end an upload that worked. Its claim tolerates the taint.
func TestTheUploadClaimToleratesTheChipLeaving(t *testing.T) {
	claim := buildUploadClaim(pod{})

	want := []deviceToleration{{Key: "liken.sh/unplugged", Operator: "Exists", Effect: "NoExecute"}}
	if got := claim.Spec.Devices.Requests[0].Exactly.Tolerations; !slices.Equal(got, want) {
		t.Errorf("tolerations = %+v, want %+v", got, want)
	}
}
