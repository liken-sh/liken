package main

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func TestTheWorkerImageUsesTheOperatorsTag(t *testing.T) {
	cases := []struct {
		operator, want string
	}{
		{"ghcr.io/liken-sh/astrophotography-operator:2026.10.12-001", "ghcr.io/liken-sh/astrophotography-operator-worker:2026.10.12-001"},
		{"registry.lab:5000/astrophotography-operator:dev", "registry.lab:5000/astrophotography-operator-worker:dev"},
	}
	for _, c := range cases {
		if got, err := deriveWorkerImage(c.operator); err != nil || got != c.want {
			t.Errorf("deriveWorkerImage(%q) = %q, %v, want %q", c.operator, got, err, c.want)
		}
	}
}

func TestAnOperatorImageWithNoTagIsAnError(t *testing.T) {
	cases := []string{
		"ghcr.io/liken-sh/astrophotography-operator",
		"registry.lab:5000/astrophotography-operator",
		"ghcr.io/liken-sh/astrophotography-operator@sha256:0123",
		"ghcr.io/liken-sh/astrophotography-operator:",
	}
	for _, reference := range cases {
		if _, err := deriveWorkerImage(reference); err == nil {
			t.Errorf("deriveWorkerImage(%q) returned no error, want one because the reference has no tag", reference)
		}
	}
}

func TestTheVariableOverridesTheOperatorsPod(t *testing.T) {
	t.Setenv(workerImageVariable, "registry.lab/worker@sha256:0123")
	if got, err := workerImage(nil, operatorNS, operatorPodName); err != nil || got != "registry.lab/worker@sha256:0123" {
		t.Errorf("workerImage = %q, %v, want the image that the variable sets", got, err)
	}
}

// The worker image comes from the image of the container named operator.
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
					t.Errorf("workerImage = %q, %v, want %q error %v", got, err, c.want, c.fails)
				}
			})
		})
	}
}

func TestAnOperatorPodThatIsGoneIsAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		client := apiclient.New(apiservertest.Host, api.server.Client(), "")
		if _, err := workerImage(client, operatorNS, operatorPodName); err == nil {
			t.Error("workerImage returned no error, want one because the operator pod does not exist")
		}
	})
}
