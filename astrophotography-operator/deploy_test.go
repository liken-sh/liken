package main

// The manifests in deploy/ against what the operator reads and writes.

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The base runs the operator in liken-system and creates no namespace
// of its own, and it applies every manifest in deploy/, so a file that
// a person downloads by URL is the file that kustomize applies.
func TestTheBaseAppliesEveryManifestInLikenSystem(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("deploy/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var base struct {
		Transformers []string `json:"transformers"`
		Resources    []string `json:"resources"`
	}
	if err := yaml.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("deploy/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, file := range files {
		if name := filepath.Base(file); name != "kustomization.yaml" {
			want = append(want, name)
		}
	}
	got := slices.Sorted(slices.Values(base.Resources))
	if !setsLikenSystem(t, base.Transformers) || !slices.Equal(got, want) {
		t.Errorf("base = %+v, want the namespace liken-system and the resources %v", base, want)
	}
}

// operatorDeployment reads the operator's Deployment from deploy/operator.yaml.
type operatorDeployment struct {
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
				ServiceAccountName string `json:"serviceAccountName"`
				Containers         []struct {
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

// manifest reads the one document of a kind from a file in deploy/.
// The kind is the one at the top of the document, so a RoleBinding's
// roleRef does not read as a Role.
func manifest(t *testing.T, file, kind string, into any) {
	t.Helper()
	raw, err := os.ReadFile("deploy/" + file)
	if err != nil {
		t.Fatal(err)
	}
	top := regexp.MustCompile(`(?m)^kind: ` + kind + `$`)
	for _, document := range bytes.Split(raw, []byte("\n---\n")) {
		if top.Match(document) {
			if err := yaml.Unmarshal(document, into); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("deploy/%s holds no %s", file, kind)
}

// A rollout starts the new copy before it stops the old one, because
// the election keeps the new copy waiting until the old one releases
// the Lease. The downward API gives the pod's name and namespace, which
// name the Lease and its holder.
func TestTheDeploymentRollsOverToACopyThatWaits(t *testing.T) {
	t.Parallel()
	var d operatorDeployment
	manifest(t, "operator.yaml", "Deployment", &d)
	strategy := d.Spec.Strategy
	if d.Spec.Replicas != 1 || strategy.Type != "RollingUpdate" ||
		strategy.RollingUpdate.MaxSurge != 1 || strategy.RollingUpdate.MaxUnavailable != 0 {
		t.Errorf("spec = %+v, want one replica that rolls with a surge of 1 and none unavailable", d.Spec)
	}
	if d.Spec.Template.Spec.ServiceAccountName != managedBy {
		t.Errorf("the pod runs as %q, want %q", d.Spec.Template.Spec.ServiceAccountName, managedBy)
	}
	containers := d.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("containers = %+v, want one", containers)
	}
	env := map[string]string{}
	// The downward API's variables. The metrics address is a plain
	// value, which monitoring_test.go holds.
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

type rule struct {
	APIGroups     []string `json:"apiGroups"`
	Resources     []string `json:"resources"`
	ResourceNames []string `json:"resourceNames"`
	Verbs         []string `json:"verbs"`
}

// The Role grants the election every request it sends of the Lease:
// get and update of the Lease by its name, and create, which RBAC
// cannot limit by name. The RoleBinding gives the Role to the
// operator's ServiceAccount.
func TestTheRoleGrantsTheLease(t *testing.T) {
	t.Parallel()
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
		len(binding.Subjects) != 1 || binding.Subjects[0].Name != managedBy {
		t.Errorf("the RoleBinding = %+v, want the Role %s for the ServiceAccount %s", binding, role.Metadata.Name, managedBy)
	}
}

// clusterRole reads the ClusterRole's rules from deploy/rbac.yaml.
func clusterRole(t *testing.T) []rule {
	t.Helper()
	var role struct {
		Rules []rule `json:"rules"`
	}
	manifest(t, "rbac.yaml", "ClusterRole", &role)
	return role.Rules
}

func grants(rules []rule, group, resource, verb string) bool {
	return slices.ContainsFunc(rules, func(r rule) bool {
		return slices.Contains(r.APIGroups, group) && slices.Contains(r.Resources, resource) && slices.Contains(r.Verbs, verb)
	})
}

// Each watch lists and then watches its collection, and the operator
// writes each status of its own group.
func TestTheClusterRoleGrantsEachWatchAndStatus(t *testing.T) {
	t.Parallel()
	rules := clusterRole(t)
	for _, w := range watches() {
		for _, verb := range []string{"get", "list", "watch"} {
			if r := w.source.Resource; !grants(rules, r.Group, r.Resource, verb) {
				t.Errorf("the ClusterRole does not grant %s on %s in the group %q", verb, r.Resource, r.Group)
			}
		}
	}
	for _, kind := range astrophotography.Kinds {
		if !grants(rules, astrophotography.Group, kind.Plural+"/status", "update") {
			t.Errorf("the ClusterRole does not grant update on %s/status", kind.Plural)
		}
	}
}

// A merge patch adds and removes the finalizer on each Target.
func TestTheClusterRoleGrantsTheTargetFinalizer(t *testing.T) {
	t.Parallel()
	if !grants(clusterRole(t), astrophotography.Group, astrophotography.TargetKind.Plural, "patch") {
		t.Error("the ClusterRole does not grant patch on targets")
	}
}

// Plan 08 gives the ClusterRole's table, and the operator writes no
// spec and no status of observatory-operator's resources.
func TestTheClusterRoleIsPlan08sTable(t *testing.T) {
	t.Parallel()
	rules := clusterRole(t)
	type need struct{ group, resource, verb string }
	var needs []need
	for _, resource := range []string{"deployments", "services"} {
		group := map[string]string{"deployments": "apps", "services": ""}[resource]
		for _, verb := range []string{"get", "list", "watch", "create", "update", "delete"} {
			needs = append(needs, need{group, resource, verb})
		}
	}
	for _, resource := range []string{"jobs", "pods"} {
		group := map[string]string{"jobs": "batch", "pods": ""}[resource]
		for _, verb := range []string{"get", "list", "watch", "create", "delete"} {
			needs = append(needs, need{group, resource, verb})
		}
	}
	for _, verb := range []string{"get", "list", "watch", "update", "patch"} {
		needs = append(needs, need{"", "persistentvolumeclaims", verb})
	}
	needs = append(needs, need{"", "events", "create"}, need{"", "events", "patch"})
	for _, n := range needs {
		if !grants(rules, n.group, n.resource, n.verb) {
			t.Errorf("the ClusterRole does not grant %s on %s in the group %q", n.verb, n.resource, n.group)
		}
	}
	for _, r := range rules {
		if slices.Contains(r.APIGroups, observatory.Group) && slices.ContainsFunc(r.Verbs, func(v string) bool {
			return !slices.Contains([]string{"get", "list", "watch"}, v)
		}) {
			t.Errorf("the ClusterRole grants %v on %v of observatory-operator", r.Verbs, r.Resources)
		}
	}
}

// setsLikenSystem answers whether one of a base's transformers sets the
// namespace liken-system on every object that states none, the way every
// base of the repository sets it.
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
			t.Fatal(err)
		}
		if transformer.Kind == "NamespaceTransformer" && transformer.Metadata.Namespace == "liken-system" && transformer.UnsetOnly {
			return true
		}
	}
	return false
}
