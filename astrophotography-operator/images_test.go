package main

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func TestTheWorkerImageTakesTheOperatorsTag(t *testing.T) {
	cases := []struct {
		operator, want string
	}{
		{"ghcr.io/liken-sh/astrophotography-operator:2026.10.12-001", "ghcr.io/liken-sh/astrophotography-operator-worker:2026.10.12-001"},
		{"registry.lab:5000/astrophotography-operator:dev", "registry.lab:5000/astrophotography-operator-worker:dev"},
	}
	for _, c := range cases {
		if got, err := deriveWorkerImage(c.operator); err != nil || got != c.want {
			t.Errorf("%s: %q, %v, want %q", c.operator, got, err, c.want)
		}
	}
}

// An image with no tag has no tag to share, and the error names the
// variable to set.
func TestAnImageWithNoTagNamesNoWorkerImage(t *testing.T) {
	cases := []string{
		"ghcr.io/liken-sh/astrophotography-operator",
		"registry.lab:5000/astrophotography-operator",
		"ghcr.io/liken-sh/astrophotography-operator@sha256:0123",
		"ghcr.io/liken-sh/astrophotography-operator:",
	}
	for _, reference := range cases {
		if _, err := deriveWorkerImage(reference); err == nil {
			t.Errorf("%s: no error", reference)
		}
	}
}

func TestTheVariableWinsOverTheOperatorsPod(t *testing.T) {
	t.Setenv(workerImageVariable, "registry.lab/worker@sha256:0123")
	if got, err := workerImage(nil, operatorNS, operatorPodName); err != nil || got != "registry.lab/worker@sha256:0123" {
		t.Errorf("%q, %v", got, err)
	}
}

// The operator reads its own pod once, at the start of a lead, and
// takes the image of its container.
func TestTheWorkerImageComesFromTheOperatorsPod(t *testing.T) {
	cases := []struct {
		name      string
		container string
		want      string
		fails     bool
	}{
		{"the operator's container", "operator", "ghcr.io/liken-sh/astrophotography-operator-worker:dev", false},
		{"no container named operator", "sidecar", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				own := object(operatorNS, operatorPodName, nil, map[string]any{"containers": []any{
					map[string]any{"name": c.container, "image": "ghcr.io/liken-sh/astrophotography-operator:dev"},
				}})
				api.put(podsPath, own)
				client := apiclient.New(apiservertest.Host, api.server.Client(), "")
				got, err := workerImage(client, operatorNS, operatorPodName)
				if got != c.want || (err != nil) != c.fails {
					t.Errorf("%q, %v, want %q", got, err, c.want)
				}
			})
		})
	}
}

func TestAnOperatorPodThatIsGoneNamesNoWorkerImage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		client := apiclient.New(apiservertest.Host, api.server.Client(), "")
		if _, err := workerImage(client, operatorNS, operatorPodName); err == nil {
			t.Error("no error")
		}
	})
}
