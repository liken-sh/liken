package main

import (
	"testing"

	"github.com/liken-sh/liken/liken/cluster"
)

func TestLocalAPIEndpointByRole(t *testing.T) {
	clusterDoc := &cluster.Cluster{}
	clusterDoc.Spec.Leaders = []string{"node-1", "node-2", "node-3"}
	cases := []struct {
		name string
		want string
	}{
		{"node-1", "https://127.0.0.1:6443"},
		{"node-4", "https://127.0.0.1:6444"},
	}
	for _, c := range cases {
		if got := localAPIEndpoint(clusterDoc, c.name); got != c.want {
			t.Errorf("localAPIEndpoint(%s) = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestLocalAPIEndpointWithoutACluster(t *testing.T) {
	if got := localAPIEndpoint(nil, "node-1"); got != "" {
		t.Errorf("localAPIEndpoint with no Cluster = %q, want empty so that a standalone machine uses the environment's endpoint", got)
	}
}
