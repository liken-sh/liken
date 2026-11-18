package main

import (
	"strings"
	"testing"
)

// namespacesTree writes a repository that passes checkNamespaces, with
// changes written over it. Its OS manifest in flux-system is outside any
// deploy/, so the check must not read it.
func namespacesTree(t *testing.T, changes map[string]string) string {
	t.Helper()
	tree := map[string]string{
		"a/deploy/kustomization.yaml":        "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\ntransformers:\n  - |-\n    apiVersion: builtin\n    kind: NamespaceTransformer\n    metadata:\n      name: liken-system\n      namespace: liken-system\n    unsetOnly: true\n",
		"a/deploy/rbac.yaml":                 "kind: ClusterRole\nmetadata:\n  name: a\n---\nkind: Role\nmetadata:\n  name: a-lease\n  namespace: liken-system\n",
		"a/deploy/operator.yaml":             "kind: Deployment\nmetadata:\n  name: a\n",
		"a/deploy/api-authentication.yaml":   "kind: RoleBinding\nmetadata:\n  name: a-api-authentication-reader\n  namespace: kube-system\n",
		"a/deploy/monitoring/dashboard.yaml": "kind: ConfigMap\nmetadata:\n  name: a-dashboard\n  namespace: monitoring\n",
		"liken/b/manifests/b.yaml":           "kind: Role\nmetadata:\n  name: b-secrets\n  namespace: flux-system\n",
	}
	for name, text := range changes {
		tree[name] = text
	}
	return writeTree(t, tree)
}

func TestEveryDeployObjectStaysWhereANamespaceOverrideCannotMoveIt(t *testing.T) {
	findings, err := checkNamespaces(namespacesTree(t, nil))
	if err != nil {
		t.Fatalf("checking the namespaces: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("the tree has findings, want none:\n%s", strings.Join(findings, "\n"))
	}
}

func TestAnObjectOutsideLikenSystemIsFound(t *testing.T) {
	cases := map[string]struct {
		file, text, wants string
	}{
		"an events Role in default": {"a/deploy/rbac.yaml", "kind: Role\nmetadata:\n  name: a-events\n  namespace: default\n",
			"a/deploy/rbac.yaml: Role a-events is in the namespace default"},
		"a binding in kube-system outside its file": {"a/deploy/api.yaml", "kind: RoleBinding\nmetadata:\n  name: a-api-authentication-reader\n  namespace: kube-system\n",
			"a/deploy/api.yaml: RoleBinding a-api-authentication-reader is in the namespace kube-system"},
		"another object in kube-system": {"a/deploy/api-authentication.yaml", "kind: Role\nmetadata:\n  name: a-reader\n  namespace: kube-system\n",
			"a/deploy/api-authentication.yaml: Role a-reader is in the namespace kube-system"},
		"a dashboard outside deploy/monitoring/": {"a/deploy/dashboard.yaml", "kind: ConfigMap\nmetadata:\n  name: a-dashboard\n  namespace: monitoring\n",
			"a/deploy/dashboard.yaml: ConfigMap a-dashboard is in the namespace monitoring"},
		"a top-level namespace field": {"a/deploy/kustomization.yaml", "kind: Kustomization\nnamespace: liken-system\n",
			"a/deploy/kustomization.yaml sets a top-level namespace"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			findings, err := checkNamespaces(namespacesTree(t, map[string]string{c.file: c.text}))
			if err != nil {
				t.Fatalf("checking the namespaces: %v", err)
			}
			if !strings.Contains(strings.Join(findings, "\n"), c.wants) {
				t.Errorf("the findings are:\n%s\nwant one with %q", strings.Join(findings, "\n"), c.wants)
			}
		})
	}
}

func TestTheNamespacesCommandFailsOnAFinding(t *testing.T) {
	cases := map[string]struct {
		changes map[string]string
		fails   bool
	}{
		"a tree that keeps the rule": {nil, false},
		"a tree with a Role in default": {map[string]string{
			"a/deploy/rbac.yaml": "kind: Role\nmetadata:\n  name: a-events\n  namespace: default\n",
		}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := newFakeEnv(t, &fakeRegistry{})
			if err := w.run([]string{"namespaces", "-root", namespacesTree(t, c.changes)}); (err != nil) != c.fails {
				t.Errorf("the namespaces command returned %v, want an error: %t", err, c.fails)
			}
		})
	}
}

func TestADeployFileThatDoesNotParseIsAnError(t *testing.T) {
	root := namespacesTree(t, map[string]string{"a/deploy/broken.yaml": "kind: [\n"})
	if _, err := checkNamespaces(root); err == nil {
		t.Error("checkNamespaces returned no error, want one for a file that is not YAML")
	}
}
