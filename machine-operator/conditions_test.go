package main

// The condition constructors: how each aspect of the machine,
// meaning the facts, the sysctls, the storage, the modules, the
// features, and the Node's health, reads as a standard Kubernetes
// condition.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

func TestFactsConditionReadsTrue(t *testing.T) {
	c := factsCondition(nil)
	if c.Type != "FactsPublished" || c.Status != api.ConditionTrue || c.Reason != "FactsRead" {
		t.Errorf("got %+v", c)
	}
}

func TestFactsConditionCarriesTheReadError(t *testing.T) {
	c := factsCondition(errors.New("facts are unreadable this boot"))
	if c.Status != api.ConditionFalse || c.Reason != "FactsUnreadable" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "unreadable this boot") {
		t.Errorf("the message should carry the error: %q", c.Message)
	}
}

func TestSysctlsConditionApplied(t *testing.T) {
	c := sysctlsCondition(nil, nil)
	if c.Type != "SysctlsApplied" || c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Errorf("got %+v", c)
	}
}

func TestSysctlsConditionCarriesTheApplyError(t *testing.T) {
	c := sysctlsCondition(nil, errors.New("sysctl vm.nope: not there"))
	if c.Status != api.ConditionFalse || c.Reason != "ApplyFailed" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "vm.nope") {
		t.Errorf("the message should name the parameter: %q", c.Message)
	}
}

// A default that will not apply is a defect in the release, not in
// this machine. Every machine running the release would report it in
// the same pass, so degrading them all would say nothing about which
// machine needs attention.
func TestSysctlsConditionKeepsAFailingDefaultOutOfTheHealthVerdict(t *testing.T) {
	c := sysctlsCondition(errors.New("sysctl net.core.default_qdisc: no such file"), nil)
	if c.Status != api.ConditionTrue || c.Reason != "DefaultsIncomplete" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "default_qdisc") {
		t.Errorf("the message should name the parameter: %q", c.Message)
	}
}

// When both halves fail, the spec's verdict sets the status, and
// the message still carries both, so one look explains the whole pass.
func TestSysctlsConditionPrefersTheSpecVerdictAndNamesBoth(t *testing.T) {
	c := sysctlsCondition(errors.New("sysctl net.core.default_qdisc: no such file"),
		errors.New("sysctl vm.nope: not there"))
	if c.Status != api.ConditionFalse || c.Reason != "ApplyFailed" {
		t.Errorf("got %+v", c)
	}
	for _, name := range []string{"default_qdisc", "vm.nope"} {
		if !strings.Contains(c.Message, name) {
			t.Errorf("both halves surface: %q", c.Message)
		}
	}
}

// sysctlDir builds a fake /proc/sys holding the given parameter
// files, because ApplySysctl deliberately refuses to create files
// that the kernel did not put there.
func sysctlDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(name, ".", "/")))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestApplySysctlSetAppliesAndReadsBack(t *testing.T) {
	dir := sysctlDir(t, "vm.swappiness", "vm.overcommit_memory")
	observed, err := applySysctlSet(dir, map[string]string{
		"vm.swappiness":        "10",
		"vm.overcommit_memory": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed["vm.swappiness"] != "10" || observed["vm.overcommit_memory"] != "1" {
		t.Errorf("the kernel's values come back: %+v", observed)
	}
}

func TestApplySysctlSetReportsEveryFailure(t *testing.T) {
	// The condition built from this error is the operator's whole
	// report, so one bad parameter must not hide another. A person
	// fixing the spec should see the full list in one pass.
	dir := sysctlDir(t, "vm.swappiness")
	observed, err := applySysctlSet(dir, map[string]string{
		"vm.swappiness": "10",
		"vm.missing":    "1",
		"kernel.absent": "1",
	})
	if err == nil {
		t.Fatal("parameters this kernel does not have should fail")
	}
	for _, name := range []string{"vm.missing", "kernel.absent"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("every failing parameter surfaces: %v", err)
		}
	}
	if observed["vm.swappiness"] != "10" {
		t.Errorf("failures must not stop the rest from applying: %+v", observed)
	}
}

func TestApplySysctlsObservesBothSets(t *testing.T) {
	dir := sysctlDir(t, "vm.watermark_scale_factor", "vm.swappiness")
	observed, defaultsErr, specErr := applySysctls(dir,
		map[string]string{"vm.watermark_scale_factor": "100"},
		map[string]string{"vm.swappiness": "10"})
	if defaultsErr != nil || specErr != nil {
		t.Fatalf("defaults=%v spec=%v", defaultsErr, specErr)
	}
	if observed["vm.watermark_scale_factor"] != "100" || observed["vm.swappiness"] != "10" {
		t.Errorf("status reports every parameter liken sets: %+v", observed)
	}
}

// The observed value for a name in both sets has to be the one the
// kernel holds after the pass, which is the spec's. Reading the
// default back happens before the spec writes over it, so merging the
// two the other way round would publish a value that no longer exists.
func TestApplySysctlsReportsTheSpecValueForAnOverriddenName(t *testing.T) {
	const name = "vm.max_map_count"
	dir := sysctlDir(t, name)
	observed, defaultsErr, specErr := applySysctls(dir,
		map[string]string{name: "262144"},
		map[string]string{name: "524288"})
	if defaultsErr != nil || specErr != nil {
		t.Fatalf("defaults=%v spec=%v", defaultsErr, specErr)
	}
	if observed[name] != "524288" {
		t.Errorf("got %q, want the value the kernel holds after the pass", observed[name])
	}
}

func TestApplySysctlsKeepsTheTwoFailuresApart(t *testing.T) {
	dir := sysctlDir(t, "vm.swappiness")
	observed, defaultsErr, specErr := applySysctls(dir,
		map[string]string{"vm.absent_default": "1"},
		map[string]string{"vm.swappiness": "10", "vm.absent_spec": "1"})
	if defaultsErr == nil || !strings.Contains(defaultsErr.Error(), "vm.absent_default") {
		t.Fatalf("the defaults error names its own parameter: %v", defaultsErr)
	}
	if strings.Contains(defaultsErr.Error(), "vm.absent_spec") {
		t.Errorf("the spec's failure must not leak into the defaults error: %v", defaultsErr)
	}
	if specErr == nil || !strings.Contains(specErr.Error(), "vm.absent_spec") {
		t.Errorf("the spec error names its own parameter: %v", specErr)
	}
	// A parameter that could not be written is never read back, which
	// is what makes this map the list of values that actually hold.
	if _, ok := observed["vm.absent_default"]; ok {
		t.Errorf("a failed write must not appear in status: %+v", observed)
	}
}

func TestStorageConditionAllPlaced(t *testing.T) {
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: "/dev/vda"}}
	status := machine.AllRolesInMemory()
	status.ClusterState = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda1"}
	c := storageCondition(spec, status)
	if c.Type != "StorageReady" || c.Status != api.ConditionTrue || c.Reason != "AllRolesPlaced" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "clusterState on vda1") {
		t.Errorf("message should name the landing: %q", c.Message)
	}
}

func TestStorageConditionDeclaredButInMemory(t *testing.T) {
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: "/dev/vda"}}
	c := storageCondition(spec, machine.AllRolesInMemory())
	if c.Status != api.ConditionFalse || c.Reason != "RolesInMemory" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "clusterState") {
		t.Errorf("message should name the role: %q", c.Message)
	}
}

func TestStorageConditionNothingDeclared(t *testing.T) {
	c := storageCondition(machine.StorageSpec{}, machine.AllRolesInMemory())
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func TestModulesConditionAllHealthy(t *testing.T) {
	c := modulesCondition([]machine.ModuleStatus{
		{Name: "nvidia", State: machine.ModuleLoaded},
		{Name: "loop", State: machine.ModuleBuiltin},
	})
	if c.Type != "ModulesLoaded" || c.Status != api.ConditionTrue || c.Reason != "AllLoaded" {
		t.Errorf("got %+v", c)
	}
}

func TestModulesConditionNamesTheFix(t *testing.T) {
	c := modulesCondition([]machine.ModuleStatus{
		{Name: "nvidia", State: machine.ModuleLoaded},
		{Name: "nbd", State: machine.ModuleMissing, Message: "not in this image; rebuild the deployment's image, or upgrade to a release built from manifests that declare it"},
	})
	if c.Status != api.ConditionFalse || c.Reason != "ModulesNotLoaded" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "nbd: not in this image; rebuild") {
		t.Errorf("message should carry init's fix: %q", c.Message)
	}
}

func TestModulesConditionNothingDeclared(t *testing.T) {
	c := modulesCondition(nil)
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func TestFeaturesConditionAllActive(t *testing.T) {
	c := featuresCondition([]machine.FeatureStatus{
		{Name: "metrics-server", State: machine.FeatureActive},
		{Name: "iscsi", State: machine.FeatureActive},
	})
	if c.Type != "FeaturesReady" || c.Status != api.ConditionTrue || c.Reason != "AllActive" {
		t.Errorf("got %+v", c)
	}
}

func TestFeaturesConditionNamesTheFix(t *testing.T) {
	c := featuresCondition([]machine.FeatureStatus{
		{Name: "metrics-server", State: machine.FeatureActive},
		{Name: "iscsi", State: machine.FeatureMissing, Message: "this image predates the iscsi feature; upgrade to a release whose image carries it"},
	})
	if c.Status != api.ConditionFalse || c.Reason != "FeaturesNotReady" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "iscsi: this image predates") {
		t.Errorf("message should carry init's fix: %q", c.Message)
	}
}

func TestFeaturesConditionNothingEnabled(t *testing.T) {
	c := featuresCondition(nil)
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func nodeWithReady(status api.ConditionStatus) *nodeObject {
	n := &nodeObject{}
	n.Status.Conditions = []api.Condition{
		{Type: "MemoryPressure", Status: "False"},
		{Type: "Ready", Status: status, Message: "kubelet says so"},
	}
	return n
}

func TestAReadyNodeIsHealthy(t *testing.T) {
	c := nodeHealthyCondition(nodeWithReady("True"))
	if c.Status != "True" || c.Reason != "KubeletReady" {
		t.Errorf("got %s/%s", c.Status, c.Reason)
	}
}

func TestANotReadyNodeIsUnhealthy(t *testing.T) {
	c := nodeHealthyCondition(nodeWithReady("Unknown"))
	if c.Status != "False" || c.Reason != "NodeNotReady" {
		t.Errorf("a silent kubelet is not serving this machine: %s/%s", c.Status, c.Reason)
	}
}

func TestANodeWithoutAReadyConditionIsUnhealthy(t *testing.T) {
	c := nodeHealthyCondition(&nodeObject{})
	if c.Status != "False" || c.Reason != "NodeNotReady" {
		t.Errorf("a kubelet that never reported in cannot be assumed healthy: %s/%s", c.Status, c.Reason)
	}
}
