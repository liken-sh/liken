package main

// These tests read the manifests in deploy/ and check the facts that
// the code depends on.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func documents(t *testing.T, file string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("deploy", file))
	if err != nil {
		t.Fatalf("reading deploy/%s: %v", file, err)
	}
	var out []map[string]any
	for _, part := range strings.Split(string(raw), "\n---\n") {
		var document map[string]any
		if err := yaml.Unmarshal([]byte(part), &document); err != nil {
			t.Fatalf("parsing a document of deploy/%s: %v", file, err)
		}
		if document != nil {
			out = append(out, document)
		}
	}
	return out
}

func find(t *testing.T, file, kind string) map[string]any {
	t.Helper()
	for _, document := range documents(t, file) {
		if document["kind"] == kind {
			return document
		}
	}
	t.Fatalf("deploy/%s holds no %s", file, kind)
	return nil
}

// dig returns the value at a path of map keys and list indexes, or nil
// when the path does not exist.
func dig(object any, path ...any) any {
	for _, step := range path {
		switch key := step.(type) {
		case string:
			object, _ = object.(map[string]any)[key]
		case int:
			list, _ := object.([]any)
			if key >= len(list) {
				return nil
			}
			object = list[key]
		}
	}
	return object
}

func TestTheDeploymentSetsTheOperatorsEnvironment(t *testing.T) {
	container := dig(find(t, "operator.yaml", "Deployment"), "spec", "template", "spec", "containers", 0)
	var names []string
	for _, variable := range dig(container, "env").([]any) {
		names = append(names, dig(variable, "name").(string))
	}
	for _, want := range []string{podNameVariable, podNamespaceVariable} {
		if !slices.Contains(names, want) {
			t.Errorf("the operator's environment %v has no %s", names, want)
		}
	}
}

func TestTheUploadClassSelectsUSBDevicesOfLiken(t *testing.T) {
	class := find(t, "upload.yaml", "DeviceClass")
	if got := dig(class, "metadata", "name"); got != uploadClass {
		t.Errorf("the DeviceClass is %v, want %s", got, uploadClass)
	}
	expression, _ := dig(class, "spec", "selectors", 0, "cel", "expression").(string)
	for _, want := range []string{`device.driver == "liken.sh"`, `device.attributes["liken.sh"].bus == "usb"`} {
		if !strings.Contains(expression, want) {
			t.Errorf("the class selects %q, want it to require %s", expression, want)
		}
	}
	if _, err := newSelectors().compile(expression); err != nil {
		t.Errorf("the class's selector does not compile: %v", err)
	}
}

// A NetworkPolicy that lists Ingress and Egress with no rules for
// either blocks all traffic.
func TestTheUploadPodsHaveNoNetwork(t *testing.T) {
	policy := find(t, "upload.yaml", "NetworkPolicy")
	if got := dig(policy, "spec", "podSelector", "matchLabels", uploadLabel); got != uploadLabelValue {
		t.Errorf("the policy selects %v, want the label %s=%s", dig(policy, "spec", "podSelector"), uploadLabel, uploadLabelValue)
	}
	types := dig(policy, "spec", "policyTypes")
	if !slices.Equal(types.([]any), []any{"Ingress", "Egress"}) {
		t.Errorf("the policy's types are %v, want Ingress and Egress", types)
	}
	if dig(policy, "spec", "ingress") != nil || dig(policy, "spec", "egress") != nil {
		t.Errorf("the policy allows %v and %v, want nothing", dig(policy, "spec", "ingress"), dig(policy, "spec", "egress"))
	}
}

func TestTheScanRehearsalAndApplyPodsReachOnlyDNSAndHTTPS(t *testing.T) {
	cases := []struct{ file, label, value string }{
		{"scan.yaml", scanLabel, scanLabelValue},
		{"rehearsal.yaml", rehearsalLabel, rehearsalLabelValue},
		{"apply.yaml", applyLabel, applyLabelValue},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			policy := find(t, c.file, "NetworkPolicy")
			if got := dig(policy, "spec", "podSelector", "matchLabels", c.label); got != c.value {
				t.Errorf("the policy selects %v, want the label %s=%s", dig(policy, "spec", "podSelector"), c.label, c.value)
			}
			if types := dig(policy, "spec", "policyTypes"); !slices.Equal(types.([]any), []any{"Ingress", "Egress"}) {
				t.Errorf("the policy's types are %v, want Ingress and Egress", types)
			}
			if dig(policy, "spec", "ingress") != nil || dig(policy, "spec", "egress", 0, "to") != nil || dig(policy, "spec", "egress", 1) != nil {
				t.Errorf("the policy allows %v in and %v out, want nothing in and one rule out on ports alone",
					dig(policy, "spec", "ingress"), dig(policy, "spec", "egress"))
			}
			var ports []string
			for _, port := range dig(policy, "spec", "egress", 0, "ports").([]any) {
				ports = append(ports, fmt.Sprint(dig(port, "protocol"), "/", dig(port, "port")))
			}
			if want := []string{"UDP/53", "TCP/53", "TCP/443"}; !slices.Equal(ports, want) {
				t.Errorf("the policy allows out %v, want %v", ports, want)
			}
		})
	}
}

// The operator patches a Disruption to remove spec.hold.
func TestTheClusterRoleGrantsEveryRequestTheOperatorSendsOnDisruptions(t *testing.T) {
	var verbs []any
	for _, rule := range dig(find(t, "rbac.yaml", "ClusterRole"), "rules").([]any) {
		if slices.Contains(dig(rule, "resources").([]any), any("disruptions")) {
			verbs = append(verbs, dig(rule, "verbs").([]any)...)
		}
	}
	for _, want := range []string{"get", "list", "watch", "create", "patch", "delete"} {
		if !slices.Contains(verbs, any(want)) {
			t.Errorf("the ClusterRole grants %v on disruptions, want %s too", verbs, want)
		}
	}
}

// The operator removes spec.hold after machine-operator drains the
// machine, and the operator's own pod can run on that machine, so the
// drain must not evict it. The drain skips a pod that tolerates the
// cordon taint, and the scheduler can place such a pod on a cordoned
// machine.
func TestTheOperatorToleratesTheCordon(t *testing.T) {
	tolerations, _ := dig(find(t, "operator.yaml", "Deployment"), "spec", "template", "spec", "tolerations").([]any)
	want := map[string]any{"key": "node.kubernetes.io/unschedulable", "operator": "Exists", "effect": "NoSchedule"}
	if !slices.ContainsFunc(tolerations, func(toleration any) bool { return reflect.DeepEqual(toleration, want) }) {
		t.Errorf("the operator tolerates %v, want %v", tolerations, want)
	}
}

// The operator signals an apply pod to write by patching an annotation
// onto it.
func TestTheRoleGrantsPatchOnPodsToSignalAnApplyPod(t *testing.T) {
	var verbs []any
	for _, rule := range dig(find(t, "rbac.yaml", "Role"), "rules").([]any) {
		if slices.Contains(dig(rule, "resources").([]any), any("pods")) {
			verbs = append(verbs, dig(rule, "verbs").([]any)...)
		}
	}
	if !slices.Contains(verbs, any("patch")) {
		t.Errorf("the Role grants %v on pods, want patch too", verbs)
	}
}

func TestTheLVFSFirmwareCatalogPointsAtLVFS(t *testing.T) {
	lvfs := find(t, "lvfs.yaml", catalogKind)
	if dig(lvfs, "metadata", "name") != "lvfs" || dig(lvfs, "spec", "url") != lvfsURL {
		t.Errorf("the FirmwareCatalog is %v, want lvfs at %s", lvfs, lvfsURL)
	}
}

// A manifest that the kustomization does not list is never installed.
func TestTheKustomizationListsEveryManifest(t *testing.T) {
	listed := dig(documents(t, "kustomization.yaml")[0], "resources").([]any)
	files, err := filepath.Glob("deploy/*.yaml")
	if err != nil {
		t.Fatalf("listing the manifests of deploy/: %v", err)
	}
	for _, file := range files {
		name := filepath.Base(file)
		if name != "kustomization.yaml" && !slices.Contains(listed, any(name)) {
			t.Errorf("deploy/kustomization.yaml does not list %s", name)
		}
	}
}
