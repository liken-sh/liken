package main

// The scan image is the operator's repository with -scan added, at the
// operator's tag, unless --scan-image names one.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func TestScanImageFor(t *testing.T) {
	cases := []struct {
		name, operator, want string
	}{
		{"a release", "ghcr.io/liken-sh/firmware-operator:2026.10.20-001", "ghcr.io/liken-sh/firmware-operator-scan:2026.10.20-001"},
		{"a registry with a port", "registry.local:5000/firmware-operator:dev", "registry.local:5000/firmware-operator-scan:dev"},
		{"no tag", "ghcr.io/liken-sh/firmware-operator", ""},
		{"a port and no tag", "registry.local:5000/firmware-operator", ""},
		{"a digest", "ghcr.io/liken-sh/firmware-operator@sha256:abcd", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := scanImageFor(c.operator)
			if got != c.want || (err == nil) != (c.want != "") {
				t.Errorf("scanImageFor = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

// The operator reads its own pod for its image, and the flag wins over
// it.
func TestResolveScanImage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		own := pod{Metadata: objectMeta{Name: operatorPodName, Namespace: operatorNS},
			Spec: podSpec{Containers: []container{{Name: operatorContainer, Image: "ghcr.io/liken-sh/firmware-operator:v1"}}}}
		api.putUpload(own)
		client := apiclient.New(apiservertest.Host, api.server.Client(), "").WithContext(t.Context())

		cases := []struct {
			name, flag, pod, want string
		}{
			{"the flag", "example/scan:1", operatorPodName, "example/scan:1"},
			{"the operator's own image", "", operatorPodName, "ghcr.io/liken-sh/firmware-operator-scan:v1"},
			{"no pod", "", "missing", ""},
		}
		for _, c := range cases {
			got, err := resolveScanImage(client, c.flag, operatorNS, c.pod)
			if got != c.want || (err == nil) != (c.want != "") {
				t.Errorf("%s: resolveScanImage = %q, %v; want %q", c.name, got, err, c.want)
			}
		}
	})
}
