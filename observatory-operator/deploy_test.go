package main

// The manifests in deploy/ against what the operator reads and writes.

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The operator reads its namespace from the environment that the
// Deployment sets.
func TestTheDeploymentGivesTheOperatorItsNamespace(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("deploy/operator.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var deployment struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Replicas int `json:"replicas"`
			Template struct {
				Spec struct {
					Containers []struct {
						Env []struct {
							Name string `json:"name"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		if bytes.Contains(document, []byte("kind: Deployment")) {
			if err := yaml.Unmarshal(document, &deployment); err != nil {
				t.Fatal(err)
			}
		}
	}
	containers := deployment.Spec.Template.Spec.Containers
	if deployment.Metadata.Namespace != "observatory" || deployment.Spec.Replicas != 1 || len(containers) != 1 {
		t.Fatalf("deployment = %+v", deployment)
	}
	if !slices.ContainsFunc(containers[0].Env, func(e struct {
		Name string `json:"name"`
	}) bool {
		return e.Name == podNamespaceVariable
	}) {
		t.Errorf("the operator's environment has no %s", podNamespaceVariable)
	}
}

// The Role grants every verb the operator sends, for every kind.
func TestTheRoleGrantsWhatTheOperatorSends(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("deploy/rbac.yaml")
	if err != nil {
		t.Fatal(err)
	}
	type rule struct {
		APIGroups []string `json:"apiGroups"`
		Resources []string `json:"resources"`
		Verbs     []string `json:"verbs"`
	}
	var role struct {
		Kind  string `json:"kind"`
		Rules []rule `json:"rules"`
	}
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		if bytes.Contains(document, []byte("kind: Role\n")) {
			if err := yaml.Unmarshal(document, &role); err != nil {
				t.Fatal(err)
			}
		}
	}
	grants := func(group, resource, verb string) bool {
		for _, r := range role.Rules {
			if slices.Contains(r.APIGroups, group) && slices.Contains(r.Resources, resource) && slices.Contains(r.Verbs, verb) {
				return true
			}
		}
		return false
	}
	type need struct{ group, resource, verb string }
	var needs []need
	for _, kind := range observatory.Kinds {
		for _, verb := range []string{"get", "list", "watch"} {
			needs = append(needs, need{observatory.Group, kind.Plural, verb})
		}
		needs = append(needs, need{observatory.Group, kind.Plural + "/status", "update"})
	}
	// A merge patch removes the retry annotation of any resource with
	// procedures, and adds and removes the finalizer of a device, a
	// Telescope, or an Observatory.
	for _, kind := range append([]observatory.Kind{observatory.ObservatoryKind, observatory.TelescopeKind}, observatory.DeviceKinds...) {
		needs = append(needs, need{observatory.Group, kind.Plural, "patch"})
	}
	needs = append(needs,
		need{observatory.Group, "reservations", "patch"},
		need{"", "pods", "patch"},
		need{"", "events", "create"},
		need{"", "events", "patch"},
		need{"resource.k8s.io", "resourceclaims", "create"},
		need{"resource.k8s.io", "resourceclaims", "delete"},
		need{"batch", "jobs", "list"},
		need{"batch", "jobs", "watch"},
		need{"batch", "jobs", "create"},
		need{"batch", "jobs", "delete"},
	)
	for _, resource := range []string{"pods", "services"} {
		for _, verb := range []string{"get", "list", "watch", "create", "delete"} {
			needs = append(needs, need{"", resource, verb})
		}
	}
	for _, n := range needs {
		if !grants(n.group, n.resource, n.verb) {
			t.Errorf("the Role does not grant %s on %s in the group %q", n.verb, n.resource, n.group)
		}
	}
}
