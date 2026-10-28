package kubernetes

// This test reads a claim's allocation back from the API server.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetResourceClaimReadsTheAllocation(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/apis/resource.k8s.io/v1/namespaces/media/resourceclaims/transcode"; r.URL.Path != want {
			t.Errorf("got %s, want %s", r.URL.Path, want)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "transcode", "namespace": "media", "uid": "claim-uid-1"},
			"status": map[string]any{
				"allocation": map[string]any{
					"devices": map[string]any{
						"results": []map[string]any{{
							"request": "gpu",
							"driver":  "liken.sh",
							"pool":    "node-1",
							"device":  "pci-0000-00-09-0",
						}},
					},
				},
			},
		})
	}))

	claim, err := GetResourceClaim(client, "media", "transcode")
	if err != nil {
		t.Fatal(err)
	}
	if claim.Metadata.UID != "claim-uid-1" {
		t.Errorf("uid = %q", claim.Metadata.UID)
	}
	results := claim.Status.Allocation.Devices.Results
	if len(results) != 1 || results[0].Device != "pci-0000-00-09-0" || results[0].Driver != "liken.sh" {
		t.Errorf("results = %+v", results)
	}
}

func TestGetResourceClaimToleratesAnUnallocatedClaim(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "pending", "namespace": "media", "uid": "claim-uid-2"},
			"status":   map[string]any{},
		})
	}))

	claim, err := GetResourceClaim(client, "media", "pending")
	if err != nil {
		t.Fatal(err)
	}
	if claim.Status.Allocation != nil {
		t.Errorf("allocation = %+v, want nil before the scheduler allocates", claim.Status.Allocation)
	}
}

func TestAClaimIsPodOwnedWhenAPodOwnsIt(t *testing.T) {
	cases := []struct {
		name   string
		owners []OwnerReference
		want   bool
	}{
		{"a named claim", nil, false},
		{"a template's claim", []OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "zigbee-0"}}, true},
		{"a claim another object owns", []OwnerReference{{APIVersion: "observatory.liken.sh/v1", Kind: "Device", Name: "mount"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var claim ResourceClaim
			claim.Metadata.OwnerReferences = c.owners
			if got := claim.PodOwned(); got != c.want {
				t.Errorf("PodOwned() = %v, want %v", got, c.want)
			}
		})
	}
}
