package main

// The machine's endpoint to the API: leaders through their own API
// server, followers through k3s's health-checked local load
// balancer, and a standalone machine through whatever the pod
// environment offers.

import (
	"testing"

	"github.com/liken-sh/liken/machine"
)

func TestLocalAPIEndpointByRole(t *testing.T) {
	cluster := &machine.Cluster{}
	cluster.Spec.Leaders = []string{"node-1", "node-2", "node-3"}
	cases := []struct {
		name string
		want string
	}{
		{"node-1", "https://127.0.0.1:6443"},
		{"node-4", "https://127.0.0.1:6444"},
	}
	for _, c := range cases {
		if got := localAPIEndpoint(cluster, c.name); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestLocalAPIEndpointWithoutACluster(t *testing.T) {
	if got := localAPIEndpoint(nil, "node-1"); got != "" {
		t.Errorf("a standalone machine uses the environment's endpoint: %q", got)
	}
}
