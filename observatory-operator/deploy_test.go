package main

// Checks the manifests in deploy/ against what the operator reads and
// writes.

import (
	"bytes"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The base puts the operator in liken-system and defines no namespace
// of its own.
func TestTheDeploymentRunsInLikenSystem(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("deploy/kustomization.yaml")
	if err != nil {
		t.Fatalf("reading deploy/kustomization.yaml: %v", err)
	}
	var base struct {
		Transformers []string `json:"transformers"`
		Resources    []string `json:"resources"`
	}
	if err := yaml.Unmarshal(raw, &base); err != nil {
		t.Fatalf("parsing the manifest: %v", err)
	}
	if !setsLikenSystem(t, base.Transformers) || slices.Contains(base.Resources, "namespace.yaml") {
		t.Errorf("base = %+v, want liken-system and no namespace of its own", base)
	}
}

// deployment holds the fields of the operator's Deployment in
// deploy/operator.yaml that these tests read.
type deployment struct {
	Spec struct {
		Replicas int `json:"replicas"`
		Strategy struct {
			Type          string `json:"type"`
			RollingUpdate struct {
				MaxSurge       int `json:"maxSurge"`
				MaxUnavailable int `json:"maxUnavailable"`
			} `json:"rollingUpdate"`
		} `json:"strategy"`
		Template struct {
			Spec struct {
				Containers []struct {
					Env []struct {
						Name      string `json:"name"`
						ValueFrom struct {
							FieldRef struct {
								FieldPath string `json:"fieldPath"`
							} `json:"fieldRef"`
						} `json:"valueFrom"`
					} `json:"env"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

// manifest reads the first document of the given kind from a file in
// deploy/ into the value that into points to. It matches the kind at the
// top level of the document, so the roleRef of a RoleBinding does not
// match as a Role.
func manifest(t *testing.T, file, kind string, into any) {
	t.Helper()
	raw, err := os.ReadFile("deploy/" + file)
	if err != nil {
		t.Fatalf("reading deploy/%s: %v", file, err)
	}
	top := regexp.MustCompile(`(?m)^kind: ` + kind + `$`)
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		if top.Match(document) {
			if err := yaml.Unmarshal(document, into); err != nil {
				t.Fatalf("parsing the manifest: %v", err)
			}
			return
		}
	}
	t.Fatalf("deploy/%s holds no %s", file, kind)
}

// A rollout starts the new pod before it stops the old one. The new pod
// waits in the leader election until the old one releases the Lease.
// The operator reads its pod name and namespace from the downward API
// to identify itself in the Lease.
func TestTheDeploymentRollsOverToACopyThatWaits(t *testing.T) {
	t.Parallel()
	var d deployment
	manifest(t, "operator.yaml", "Deployment", &d)
	strategy := d.Spec.Strategy
	if d.Spec.Replicas != 1 || strategy.Type != "RollingUpdate" ||
		strategy.RollingUpdate.MaxSurge != 1 || strategy.RollingUpdate.MaxUnavailable != 0 {
		t.Errorf("spec = %+v, want one replica that rolls with a surge of 1 and none unavailable", d.Spec)
	}
	containers := d.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("containers = %+v, want one", containers)
	}
	env := map[string]string{}
	// Only the variables that come from the downward API. The metrics
	// address is a plain value, which monitoring_test.go checks.
	for _, v := range containers[0].Env {
		if v.ValueFrom.FieldRef.FieldPath != "" {
			env[v.Name] = v.ValueFrom.FieldRef.FieldPath
		}
	}
	want := map[string]string{podNameVariable: "metadata.name", podNamespaceVariable: "metadata.namespace"}
	if !maps.Equal(env, want) {
		t.Errorf("environment = %v, want %v", env, want)
	}
}

// The Role grants the requests that the leader election sends for the
// Lease: get and update on the Lease by name, and create, which RBAC
// cannot limit by name. The RoleBinding binds the Role to the
// operator's ServiceAccount.
func TestTheRoleGrantsTheLease(t *testing.T) {
	t.Parallel()
	type rule struct {
		APIGroups     []string `json:"apiGroups"`
		Resources     []string `json:"resources"`
		ResourceNames []string `json:"resourceNames"`
		Verbs         []string `json:"verbs"`
	}
	var role struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Rules []rule `json:"rules"`
	}
	manifest(t, "rbac.yaml", "Role", &role)
	var binding struct {
		RoleRef struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"roleRef"`
		Subjects []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"subjects"`
	}
	manifest(t, "rbac.yaml", "RoleBinding", &binding)
	want := []rule{
		{APIGroups: []string{"coordination.k8s.io"}, Resources: []string{"leases"}, ResourceNames: []string{leaseName}, Verbs: []string{"get", "update"}},
		{APIGroups: []string{"coordination.k8s.io"}, Resources: []string{"leases"}, Verbs: []string{"create"}},
	}
	if !reflect.DeepEqual(role.Rules, want) {
		t.Errorf("the Role's rules = %+v, want %+v", role.Rules, want)
	}
	if binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != role.Metadata.Name ||
		len(binding.Subjects) != 1 || binding.Subjects[0].Name != "observatory-operator" {
		t.Errorf("the RoleBinding = %+v, want the Role %s for the ServiceAccount observatory-operator", binding, role.Metadata.Name)
	}
}

// The ClusterRole grants every verb that the operator sends, for every
// kind.
func TestTheClusterRoleGrantsWhatTheOperatorSends(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("deploy/rbac.yaml")
	if err != nil {
		t.Fatalf("reading deploy/rbac.yaml: %v", err)
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
		if bytes.Contains(document, []byte("kind: ClusterRole\n")) {
			if err := yaml.Unmarshal(document, &role); err != nil {
				t.Fatalf("parsing the manifest: %v", err)
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
		if kind.HasStatus() {
			needs = append(needs, need{observatory.Group, kind.Plural + "/status", "update"})
		}
	}
	// A merge patch removes the retry annotation from any resource that
	// has procedures, and adds and removes the finalizer on a device, a
	// Telescope, or an Observatory.
	for _, kind := range append([]observatory.Kind{observatory.ObservatoryKind, observatory.TelescopeKind}, observatory.DeviceKinds...) {
		needs = append(needs, need{observatory.Group, kind.Plural, "patch"})
	}
	needs = append(needs,
		need{observatory.Group, "reservations", "patch"},
		need{"", "pods", "patch"},
		need{"", "events", "create"},
		need{"", "events", "patch"},
		need{"resource.k8s.io", "resourceclaimtemplates", "create"},
		need{"resource.k8s.io", "resourceclaimtemplates", "delete"},
		need{"batch", "jobs", "list"},
		need{"batch", "jobs", "watch"},
		need{"batch", "jobs", "create"},
		need{"batch", "jobs", "delete"},
		need{"", "persistentvolumeclaims", "get"},
	)
	for _, resource := range []string{"pods", "services"} {
		for _, verb := range []string{"get", "list", "watch", "create", "delete"} {
			needs = append(needs, need{"", resource, verb})
		}
	}
	for _, n := range needs {
		if !grants(n.group, n.resource, n.verb) {
			t.Errorf("the ClusterRole does not grant %s on %s in the group %q", n.verb, n.resource, n.group)
		}
	}
}

// setsLikenSystem reports whether one of a base's transformers sets the
// namespace liken-system on every object that has no namespace.
func setsLikenSystem(t *testing.T, transformers []string) bool {
	t.Helper()
	for _, raw := range transformers {
		var transformer struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			UnsetOnly bool `json:"unsetOnly"`
		}
		if err := yaml.Unmarshal([]byte(raw), &transformer); err != nil {
			t.Fatalf("parsing the manifest: %v", err)
		}
		if transformer.Kind == "NamespaceTransformer" && transformer.Metadata.Namespace == "liken-system" && transformer.UnsetOnly {
			return true
		}
	}
	return false
}
