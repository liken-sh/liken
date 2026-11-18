package main

import (
	"slices"
	"testing"
)

func TestTheUploadClaimToleratesTheChipLeaving(t *testing.T) {
	claim := buildUploadClaim(pod{})

	want := []deviceToleration{{Key: "liken.sh/unplugged", Operator: "Exists", Effect: "NoExecute"}}
	if got := claim.Spec.Devices.Requests[0].Exactly.Tolerations; !slices.Equal(got, want) {
		t.Errorf("tolerations = %+v, want %+v", got, want)
	}
}
