package main

// These tests read the manifests in deploy/ for the facts the code
// depends on: the environment the operator reads, the class an upload's
// claim names, the policy that keeps an upload pod off the network, the
// policy that lets a scan pod reach only DNS and HTTPS, and the lvfs
// FirmwareCatalog.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// documents answers each YAML document of a manifest, decoded.
func documents(t *testing.T, file string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("deploy", file))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, part := range strings.Split(string(raw), "\n---\n") {
		var document map[string]any
		if err := yaml.Unmarshal([]byte(part), &document); err != nil {
			t.Fatal(err)
		}
		if document != nil {
			out = append(out, document)
		}
	}
	return out
}

// find answers the one document of a kind in a manifest.
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

// dig answers the value at a path of keys and list indexes.
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

// The operator reads its pod's name and namespace from the environment
// the Deployment sets.
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

// The class an upload's claim names exists, and selects only the USB
// devices of liken.sh.
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

// The network policy selects every upload pod, names both directions,
// and allows nothing, so it denies every packet.
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

// The scan pods' policy selects every scan pod, allows nothing in, and
// allows out only DNS and HTTPS.
func TestTheScanPodsReachOnlyDNSAndHTTPS(t *testing.T) {
	policy := find(t, "scan.yaml", "NetworkPolicy")
	if got := dig(policy, "spec", "podSelector", "matchLabels", scanLabel); got != scanLabelValue {
		t.Errorf("the policy selects %v, want the label %s=%s", dig(policy, "spec", "podSelector"), scanLabel, scanLabelValue)
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
}

// The lvfs FirmwareCatalog names LVFS's catalog.
func TestTheLVFSCatalog(t *testing.T) {
	lvfs := find(t, "lvfs.yaml", catalogKind)
	if dig(lvfs, "metadata", "name") != "lvfs" || dig(lvfs, "spec", "url") != lvfsURL {
		t.Errorf("the FirmwareCatalog is %v, want lvfs at %s", lvfs, lvfsURL)
	}
}

// The kustomization names every manifest in deploy/, so a file added
// there is installed.
func TestTheKustomizationNamesEveryManifest(t *testing.T) {
	listed := dig(documents(t, "kustomization.yaml")[0], "resources").([]any)
	files, err := filepath.Glob("deploy/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := filepath.Base(file)
		if name != "kustomization.yaml" && !slices.Contains(listed, any(name)) {
			t.Errorf("the kustomization does not name %s", name)
		}
	}
}
