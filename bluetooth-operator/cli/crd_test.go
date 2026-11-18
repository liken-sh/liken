package main

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNewPairingRequest(t *testing.T) {
	request := newPairingRequest("liken-system", "pair-abcd", "04-4a-69-66-92-27", 180)
	if request.GetKind() != pairingRequestKind {
		t.Fatalf("kind = %q, want %q", request.GetKind(), pairingRequestKind)
	}
	if request.GetAPIVersion() != driverGroup+"/"+driverVersion {
		t.Fatalf("apiVersion = %q, want %q", request.GetAPIVersion(), driverGroup+"/"+driverVersion)
	}
	if request.GetName() != "pair-abcd" || request.GetNamespace() != "liken-system" {
		t.Fatalf("name and namespace are %q and %q, want pair-abcd and liken-system", request.GetName(), request.GetNamespace())
	}
	adapter, _, _ := unstructured.NestedString(request.Object, "spec", "adapter")
	if adapter != "04-4a-69-66-92-27" {
		t.Fatalf("spec.adapter = %q, want 04-4a-69-66-92-27", adapter)
	}
	window, found, _ := unstructured.NestedInt64(request.Object, "spec", "windowSeconds")
	if !found || window != 180 {
		t.Fatalf("spec.windowSeconds = %d (found: %v), want 180", window, found)
	}
}

func TestNewPairingRequestOmitsAnUnsetWindow(t *testing.T) {
	request := newPairingRequest("liken-system", "pair-abcd", "04-4a-69-66-92-27", 0)
	if _, found, _ := unstructured.NestedInt64(request.Object, "spec", "windowSeconds"); found {
		t.Fatal("spec.windowSeconds is set for a window of 0, want it omitted")
	}
}

func TestApprovalPatch(t *testing.T) {
	patch, err := approvalPatch("A0:AB:51:33:B7:12")
	if err != nil {
		t.Fatalf("building the approval patch: %v", err)
	}
	var decoded map[string]map[string]string
	if err := json.Unmarshal(patch, &decoded); err != nil {
		t.Fatalf("decoding the approval patch: %v", err)
	}
	if decoded["spec"]["device"] != "A0:AB:51:33:B7:12" {
		t.Fatalf("the patch is %s, want spec.device A0:AB:51:33:B7:12", patch)
	}
}
