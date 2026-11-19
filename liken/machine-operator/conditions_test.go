package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func TestFactsConditionReadsTrue(t *testing.T) {
	c := factsCondition(nil)
	if c.Type != "FactsPublished" || c.Status != api.ConditionTrue || c.Reason != "FactsRead" {
		t.Errorf("the condition is %+v, want FactsPublished True FactsRead", c)
	}
}

func TestFactsConditionCarriesTheReadError(t *testing.T) {
	c := factsCondition(errors.New("facts are unreadable this boot"))
	if c.Status != api.ConditionFalse || c.Reason != "FactsUnreadable" {
		t.Errorf("the condition is %+v, want False FactsUnreadable", c)
	}
	if !strings.Contains(c.Message, "unreadable this boot") {
		t.Errorf("the message is %q, want it to include the read error", c.Message)
	}
}

func TestHostEntriesConditionApplied(t *testing.T) {
	entries := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}
	c := hostEntriesCondition(entries, nil, false)
	if c.Type != "HostEntriesApplied" || c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Errorf("the condition is %+v, want HostEntriesApplied True Applied", c)
	}
}

func TestHostEntriesConditionNothingDeclared(t *testing.T) {
	c := hostEntriesCondition(nil, nil, false)
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True NothingDeclared", c)
	}
}

func TestHostEntriesConditionCarriesTheApplyError(t *testing.T) {
	entries := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}
	c := hostEntriesCondition(entries, errors.New("writing /host/etc/hosts: permission denied"), false)
	if c.Status != api.ConditionFalse || c.Reason != "ApplyFailed" {
		t.Errorf("the condition is %+v, want False ApplyFailed", c)
	}
	if !strings.Contains(c.Message, "permission denied") {
		t.Errorf("the message is %q, want it to include the apply error", c.Message)
	}
}

// A missing path in a pod whose template is older than the release is
// reported as AwaitingPodRefresh, not as ApplyFailed.
func TestHostEntriesConditionStalePodMissingMountAwaitsRefresh(t *testing.T) {
	entries := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}
	err := fmt.Errorf("writing /host/etc/hosts.tmp-1: %w", fs.ErrNotExist)
	c := hostEntriesCondition(entries, err, true)
	if c.Status != api.ConditionFalse || c.Reason != "AwaitingPodRefresh" {
		t.Errorf("the condition is %+v, want False AwaitingPodRefresh", c)
	}
	if !strings.Contains(c.Message, "older than the release") {
		t.Errorf("the message is %q, want it to say the pod template is older than the release", c.Message)
	}
}

// An old template explains a missing mount, but not a permission error
// or a full disk, so those errors stay ApplyFailed.
func TestHostEntriesConditionStalePodOtherErrorStaysApplyFailed(t *testing.T) {
	entries := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}
	c := hostEntriesCondition(entries, errors.New("writing /host/etc/hosts: permission denied"), true)
	if c.Status != api.ConditionFalse || c.Reason != "ApplyFailed" {
		t.Errorf("the condition is %+v, want False ApplyFailed because the missing path is not the only cause", c)
	}
}

// A pod with a current template has no reason to lack a mount, so a
// missing path stays ApplyFailed.
func TestHostEntriesConditionFreshPodMissingMountStaysApplyFailed(t *testing.T) {
	entries := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}
	err := fmt.Errorf("writing /host/etc/hosts.tmp-1: %w", fs.ErrNotExist)
	c := hostEntriesCondition(entries, err, false)
	if c.Status != api.ConditionFalse || c.Reason != "ApplyFailed" {
		t.Errorf("the condition is %+v, want False ApplyFailed because the pod is current", c)
	}
}

func TestSysctlsConditionApplied(t *testing.T) {
	c := sysctlsCondition(nil, nil)
	if c.Type != "SysctlsApplied" || c.Status != api.ConditionTrue || c.Reason != "Applied" {
		t.Errorf("the condition is %+v, want SysctlsApplied True Applied", c)
	}
}

func TestSysctlsConditionCarriesTheApplyError(t *testing.T) {
	c := sysctlsCondition(nil, errors.New("sysctl vm.nope: not there"))
	if c.Status != api.ConditionFalse || c.Reason != "ApplyFailed" {
		t.Errorf("the condition is %+v, want False ApplyFailed", c)
	}
	if !strings.Contains(c.Message, "vm.nope") {
		t.Errorf("the message is %q, want it to name vm.nope", c.Message)
	}
}

// A default that cannot be applied fails on every machine of the
// release, so it must not make the condition False.
func TestSysctlsConditionKeepsAFailingDefaultOutOfTheHealthVerdict(t *testing.T) {
	c := sysctlsCondition(errors.New("sysctl net.core.default_qdisc: no such file"), nil)
	if c.Status != api.ConditionTrue || c.Reason != "DefaultsIncomplete" {
		t.Errorf("the condition is %+v, want True DefaultsIncomplete", c)
	}
	if !strings.Contains(c.Message, "default_qdisc") {
		t.Errorf("the message is %q, want it to name default_qdisc", c.Message)
	}
}

// When both fail, the spec's failure sets the status, and the message
// has both errors.
func TestSysctlsConditionPrefersTheSpecVerdictAndNamesBoth(t *testing.T) {
	c := sysctlsCondition(errors.New("sysctl net.core.default_qdisc: no such file"),
		errors.New("sysctl vm.nope: not there"))
	if c.Status != api.ConditionFalse || c.Reason != "ApplyFailed" {
		t.Errorf("the condition is %+v, want False ApplyFailed", c)
	}
	for _, name := range []string{"default_qdisc", "vm.nope"} {
		if !strings.Contains(c.Message, name) {
			t.Errorf("the message is %q, want it to include %s", c.Message, name)
		}
	}
}

// sysctlDir creates a fake /proc/sys with a file for each name, each
// containing 0. machine.ApplySysctl does not create files.
func sysctlDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(name, ".", "/")))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the directory of %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte("0\n"), 0o644); err != nil {
			t.Fatalf("writing the sysctl file %s: %v", name, err)
		}
	}
	return dir
}

func TestApplySysctlSetAppliesAndReadsBack(t *testing.T) {
	dir := sysctlDir(t, "vm.swappiness", "vm.overcommit_memory")
	observed, _, err := applySysctlSet(dir, map[string]string{
		"vm.swappiness":        "10",
		"vm.overcommit_memory": "1",
	}, nil, nil)
	if err != nil {
		t.Fatalf("applying the sysctl set: %v", err)
	}
	if observed["vm.swappiness"] != "10" || observed["vm.overcommit_memory"] != "1" {
		t.Errorf("the observed values are %+v, want vm.swappiness 10 and vm.overcommit_memory 1", observed)
	}
}

// An unchanged modification time shows that the file was not written.
func TestApplySysctlSetSkipsAConvergedParameter(t *testing.T) {
	dir := sysctlDir(t, "vm.swappiness")
	path := filepath.Join(dir, "vm", "swappiness")
	if err := os.WriteFile(path, []byte("10\n"), 0o644); err != nil {
		t.Fatalf("writing the sysctl file: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file status before the pass: %v", err)
	}

	observed, _, err := applySysctlSet(dir, map[string]string{"vm.swappiness": "10"}, nil, nil)
	if err != nil {
		t.Fatalf("applying the sysctl set: %v", err)
	}
	if observed["vm.swappiness"] != "10" {
		t.Errorf("the observed values are %+v, want vm.swappiness 10", observed)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file status after the pass: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the modification time of a converged parameter changed from %v to %v, want no write", before.ModTime(), after.ModTime())
	}
}

// The result is the kernel's string, with its tab.
func TestApplySysctlSetSkipsAMultiValueParameterTheKernelSeparatesWithTabs(t *testing.T) {
	const name = "net.ipv4.ip_local_port_range"
	dir := sysctlDir(t, name)
	path := filepath.Join(dir, "net", "ipv4", "ip_local_port_range")
	if err := os.WriteFile(path, []byte("1024\t65535\n"), 0o644); err != nil {
		t.Fatalf("writing the sysctl file: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file status before the pass: %v", err)
	}

	observed, _, err := applySysctlSet(dir, map[string]string{name: "1024 65535"}, nil, nil)
	if err != nil {
		t.Fatalf("applying the sysctl set: %v", err)
	}
	if observed[name] != "1024\t65535" {
		t.Errorf("the observed value is %q, want the kernel's own reading", observed[name])
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file status after the pass: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the modification time of a converged parameter changed from %v to %v, want no write", before.ModTime(), after.ModTime())
	}
}

func TestApplySysctlSetReportsEveryFailure(t *testing.T) {
	// The condition message comes from this error, so it must list
	// every failing parameter.
	dir := sysctlDir(t, "vm.swappiness")
	observed, _, err := applySysctlSet(dir, map[string]string{
		"vm.swappiness": "10",
		"vm.missing":    "1",
		"kernel.absent": "1",
	}, nil, nil)
	if err == nil {
		t.Fatal("applying parameters the kernel does not have returned no error, want one")
	}
	for _, name := range []string{"vm.missing", "kernel.absent"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error is %v, want it to name %s", err, name)
		}
	}
	if observed["vm.swappiness"] != "10" {
		t.Errorf("the observed values are %+v, want vm.swappiness 10 despite the failures", observed)
	}
}

func TestApplySysctlsObservesBothSets(t *testing.T) {
	dir := sysctlDir(t, "vm.watermark_scale_factor", "vm.swappiness")
	observed, _, defaultsErr, specErr := applySysctls(dir,
		map[string]string{"vm.watermark_scale_factor": "100"},
		map[string]string{"vm.swappiness": "10"}, nil, nil)
	if defaultsErr != nil || specErr != nil {
		t.Fatalf("applying the sysctls: defaults error %v, spec error %v", defaultsErr, specErr)
	}
	if observed["vm.watermark_scale_factor"] != "100" || observed["vm.swappiness"] != "10" {
		t.Errorf("the observed values are %+v, want both parameters", observed)
	}
}

// For a name in both sets, the result is the spec's value, which is
// what the kernel has after the pass.
func TestApplySysctlsReportsTheSpecValueForAnOverriddenName(t *testing.T) {
	const name = "vm.max_map_count"
	dir := sysctlDir(t, name)
	observed, _, defaultsErr, specErr := applySysctls(dir,
		map[string]string{name: "262144"},
		map[string]string{name: "524288"}, nil, nil)
	if defaultsErr != nil || specErr != nil {
		t.Fatalf("applying the sysctls: defaults error %v, spec error %v", defaultsErr, specErr)
	}
	if observed[name] != "524288" {
		t.Errorf("the observed value is %q, want 524288, the value the kernel has after the pass", observed[name])
	}
}

// When the kernel already has the spec's value, the pass does not
// write the default of the overridden name either.
func TestApplySysctlsWritesNothingForAConvergedOverride(t *testing.T) {
	const name = "vm.max_map_count"
	dir := sysctlDir(t, name)
	defaults, desired := map[string]string{name: "262144"}, map[string]string{name: "524288"}
	applySysctls(dir, defaults, desired, nil, nil)
	out := &passOutcome{}

	observed, _, _, _ := applySysctls(dir, defaults, desired, out, nil)

	if len(out.writes) != 0 || observed[name] != "524288" {
		t.Errorf("a converged pass wrote %q and observed %q, want no write and 524288", out.writes, observed[name])
	}
}

func TestApplySysctlsKeepsTheTwoFailuresApart(t *testing.T) {
	dir := sysctlDir(t, "vm.swappiness")
	observed, _, defaultsErr, specErr := applySysctls(dir,
		map[string]string{"vm.absent_default": "1"},
		map[string]string{"vm.swappiness": "10", "vm.absent_spec": "1"}, nil, nil)
	if defaultsErr == nil || !strings.Contains(defaultsErr.Error(), "vm.absent_default") {
		t.Fatalf("the defaults error is %v, want it to name vm.absent_default", defaultsErr)
	}
	if strings.Contains(defaultsErr.Error(), "vm.absent_spec") {
		t.Errorf("the defaults error is %v, want no mention of vm.absent_spec", defaultsErr)
	}
	if specErr == nil || !strings.Contains(specErr.Error(), "vm.absent_spec") {
		t.Errorf("the spec error is %v, want it to name vm.absent_spec", specErr)
	}
	// A parameter that could not be written is not read back, so it is
	// not in the result.
	if _, ok := observed["vm.absent_default"]; ok {
		t.Errorf("the observed values are %+v, want no entry for vm.absent_default because its write failed", observed)
	}
}

func TestStorageConditionAllPlaced(t *testing.T) {
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: "/dev/vda"}}
	status := machine.AllRolesInMemory()
	status.ClusterState = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda1"}
	c := storageCondition(spec, status)
	if c.Type != "StorageReady" || c.Status != api.ConditionTrue || c.Reason != "AllRolesPlaced" {
		t.Errorf("the condition is %+v, want StorageReady True AllRolesPlaced", c)
	}
	if !strings.Contains(c.Message, "clusterState on vda1") {
		t.Errorf("the message is %q, want it to say clusterState is on vda1", c.Message)
	}
}

func TestStorageConditionDeclaredButInMemory(t *testing.T) {
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: "/dev/vda"}}
	c := storageCondition(spec, machine.AllRolesInMemory())
	if c.Status != api.ConditionFalse || c.Reason != "RolesInMemory" {
		t.Errorf("the condition is %+v, want False RolesInMemory", c)
	}
	if !strings.Contains(c.Message, "clusterState") {
		t.Errorf("the message is %q, want it to name clusterState", c.Message)
	}
}

func TestStorageConditionNothingDeclared(t *testing.T) {
	c := storageCondition(machine.StorageSpec{}, machine.AllRolesInMemory())
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True NothingDeclared", c)
	}
}

func TestModulesConditionAllHealthy(t *testing.T) {
	c := modulesCondition([]machine.ModuleStatus{
		{Name: "nvidia", State: machine.ModuleLoaded},
		{Name: "loop", State: machine.ModuleBuiltin},
	})
	if c.Type != "ModulesLoaded" || c.Status != api.ConditionTrue || c.Reason != "AllLoaded" {
		t.Errorf("the condition is %+v, want ModulesLoaded True AllLoaded", c)
	}
}

func TestModulesConditionNamesTheFix(t *testing.T) {
	c := modulesCondition([]machine.ModuleStatus{
		{Name: "nvidia", State: machine.ModuleLoaded},
		{Name: "nbd", State: machine.ModuleMissing, Message: "not in this image; rebuild the deployment's image, or upgrade to a release built from manifests that declare it"},
	})
	if c.Status != api.ConditionFalse || c.Reason != "ModulesNotLoaded" {
		t.Errorf("the condition is %+v, want False ModulesNotLoaded", c)
	}
	if !strings.Contains(c.Message, "nbd: not in this image; rebuild") {
		t.Errorf("the message is %q, want it to include the message of the module status", c.Message)
	}
}

func TestModulesConditionNothingDeclared(t *testing.T) {
	c := modulesCondition(nil)
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True NothingDeclared", c)
	}
}

func TestFeaturesConditionAllActive(t *testing.T) {
	c := featuresCondition([]machine.FeatureStatus{
		{Name: "metrics-server", State: machine.FeatureActive},
		{Name: "iscsi", State: machine.FeatureActive},
	})
	if c.Type != "FeaturesReady" || c.Status != api.ConditionTrue || c.Reason != "AllActive" {
		t.Errorf("the condition is %+v, want FeaturesReady True AllActive", c)
	}
}

func TestFeaturesConditionNamesTheFix(t *testing.T) {
	c := featuresCondition([]machine.FeatureStatus{
		{Name: "metrics-server", State: machine.FeatureActive},
		{Name: "iscsi", State: machine.FeatureMissing, Message: "this image predates the iscsi feature; upgrade to a release whose image carries it"},
	})
	if c.Status != api.ConditionFalse || c.Reason != "FeaturesNotReady" {
		t.Errorf("the condition is %+v, want False FeaturesNotReady", c)
	}
	if !strings.Contains(c.Message, "iscsi: this image predates") {
		t.Errorf("the message is %q, want it to include the message of the feature status", c.Message)
	}
}

func TestFeaturesConditionNothingEnabled(t *testing.T) {
	c := featuresCondition(nil)
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True NothingDeclared", c)
	}
}

func wiredAndWireless(w *machine.WirelessStatus) []machine.InterfaceStatus {
	return []machine.InterfaceStatus{
		{Name: "eth0", Address: "10.10.0.5/24"},
		{Name: "wlan0", Address: "10.10.0.6/24", Wireless: w},
	}
}

func TestWirelessConditionJoined(t *testing.T) {
	c := wirelessCondition(wiredAndWireless(&machine.WirelessStatus{
		SSID: "homenet", State: machine.WirelessConnected,
	}))
	if c.Type != "WirelessJoined" || c.Status != api.ConditionTrue || c.Reason != "AllJoined" {
		t.Errorf("the condition is %+v, want WirelessJoined True AllJoined", c)
	}
}

func TestWirelessConditionCarriesTheSupplicantsReason(t *testing.T) {
	// The kernel reports the same state for a wrong passphrase and for
	// an access point that is switched off. Only the supplicant's
	// message tells them apart.
	c := wirelessCondition(wiredAndWireless(&machine.WirelessStatus{
		SSID: "homenet", State: machine.WirelessWrongKey,
		Message: "the access point refused the passphrase (WRONG_KEY); fix it on the install media",
	}))
	if c.Status != api.ConditionFalse || c.Reason != "NotJoined" {
		t.Errorf("the condition is %+v, want False NotJoined", c)
	}
	for _, want := range []string{"wlan0", "homenet", "WRONG_KEY"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("the message is %q, want it to include %q", c.Message, want)
		}
	}
}

func TestWirelessConditionFallsBackToTheStateWhenNothingSaidWhy(t *testing.T) {
	c := wirelessCondition(wiredAndWireless(&machine.WirelessStatus{
		SSID: "homenet", State: machine.WirelessAssociating,
	}))
	if c.Status != api.ConditionFalse || !strings.Contains(c.Message, "Associating") {
		t.Errorf("the condition is %+v, want False with the state Associating in the message", c)
	}
}

func TestWirelessConditionNothingDeclared(t *testing.T) {
	// A machine with no wireless interface must stay Ready.
	c := wirelessCondition(wiredAndWireless(nil))
	if c.Status != api.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True NothingDeclared", c)
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
		t.Errorf("the condition is %s/%s, want True/KubeletReady", c.Status, c.Reason)
	}
}

func TestANotReadyNodeIsUnhealthy(t *testing.T) {
	c := nodeHealthyCondition(nodeWithReady("Unknown"))
	if c.Status != "False" || c.Reason != "NodeNotReady" {
		t.Errorf("the condition is %s/%s, want False/NodeNotReady because the kubelet reports Ready Unknown", c.Status, c.Reason)
	}
}

func TestANodeWithoutAReadyConditionIsUnhealthy(t *testing.T) {
	c := nodeHealthyCondition(&nodeObject{})
	if c.Status != "False" || c.Reason != "NodeNotReady" {
		t.Errorf("the condition is %s/%s, want False/NodeNotReady because the Node has no Ready condition", c.Status, c.Reason)
	}
}
