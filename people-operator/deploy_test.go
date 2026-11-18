package main

import (
	"os"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

// The operator finds its image in its own pod by container name, and
// reads the pod's name and namespace from environment variables. The
// Deployment must define all of them.
func TestDeploymentDefinesWhatTheOperatorReads(t *testing.T) {
	raw, err := os.ReadFile("deploy/operator.yaml")
	if err != nil {
		t.Fatalf("reading deploy/operator.yaml: %v", err)
	}
	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name string `json:"name"`
						Env  []struct {
							Name string `json:"name"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &deployment); err != nil {
		t.Fatalf("decoding deploy/operator.yaml: %v", err)
	}
	containers := deployment.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Name != operatorContainer {
		t.Fatalf("containers = %+v, want one named %s", containers, operatorContainer)
	}
	var names []string
	for _, variable := range containers[0].Env {
		names = append(names, variable.Name)
	}
	for _, want := range []string{podNameVariable, podNamespaceVariable} {
		if !slices.Contains(names, want) {
			t.Errorf("the operator's environment %v has no %s", names, want)
		}
	}
}
