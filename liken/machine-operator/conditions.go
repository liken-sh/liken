package main

// Each function here turns the result of one part of a reconcile pass
// into a condition of the Machine's status.

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func factsCondition(err error) api.Condition {
	if err != nil {
		return api.Condition{
			Type: "FactsPublished", Status: api.ConditionFalse,
			Reason:  "FactsUnreadable",
			Message: "The device inventory publishes no disk until the facts can be read: " + err.Error(),
		}
	}
	return api.Condition{Type: "FactsPublished", Status: api.ConditionTrue, Reason: "FactsRead"}
}

// sysctlsCondition reports the result of applying the default sysctls
// (defaultsErr) and the Machine spec's sysctls (specErr). Only a spec
// failure makes the condition False.
//
// The defaults come from machine.DefaultSysctls and are the same on
// every machine that runs a release. When one of them cannot be
// written, it fails on every machine of the fleet at once, and a False
// condition would no longer point at the machine with a fault of its
// own. So a failing default gives True with the reason
// DefaultsIncomplete, and the message names the parameter. The
// parameter is also absent from status.sysctls, because applySysctls
// does not return a parameter whose write failed.
func sysctlsCondition(defaultsErr, specErr error) api.Condition {
	if specErr != nil {
		message := "The declared sysctls are not applied: " + specErr.Error()
		if defaultsErr != nil {
			message += ". Some defaults are not applied either: " + defaultsErr.Error()
		}
		return api.Condition{
			Type: "SysctlsApplied", Status: api.ConditionFalse,
			Reason: "ApplyFailed", Message: message,
		}
	}
	if defaultsErr != nil {
		return api.Condition{
			Type: "SysctlsApplied", Status: api.ConditionTrue,
			Reason: "DefaultsIncomplete", Message: "Some default sysctls are not applied: " + defaultsErr.Error(),
		}
	}
	return api.Condition{Type: "SysctlsApplied", Status: api.ConditionTrue, Reason: "Applied"}
}

// hostEntriesCondition reports the result of applyHostEntries. podStale
// is the result of ownPodIsStale (staleness.go).
func hostEntriesCondition(desired []machine.HostEntry, err error, podStale bool) api.Condition {
	if err != nil {
		if awaitingPodRefresh(podStale, err) {
			return api.Condition{
				Type: "HostEntriesApplied", Status: api.ConditionFalse,
				Reason: "AwaitingPodRefresh",
				Message: "The pod's template is older than the release that this machine runs. " +
					"cluster-operator deletes the pod after a leader boots that release, and the DaemonSet creates it again: " + err.Error(),
			}
		}
		return api.Condition{
			Type: "HostEntriesApplied", Status: api.ConditionFalse,
			Reason: "ApplyFailed", Message: "The host entries are not applied: " + err.Error(),
		}
	}
	if len(desired) == 0 {
		return api.Condition{
			Type: "HostEntriesApplied", Status: api.ConditionTrue,
			Reason: "NothingDeclared", Message: "No host entries are declared",
		}
	}
	return api.Condition{Type: "HostEntriesApplied", Status: api.ConditionTrue, Reason: "Applied"}
}

// awaitingPodRefresh reports whether an apply failure comes from this
// pod's template being older than the release the machine runs, and
// not from a fault of the machine.
//
// System pods run the image tag :installed, and their DaemonSets use
// the OnDelete update strategy (cluster-operator/steward.go). So a
// reboot into a new release runs the new binary in the old pod spec.
// Only a leader's boot writes the manifests with the new template. A
// follower that reboots first keeps the old pod spec until
// cluster-operator deletes the pod and the DaemonSet creates it again.
// When the old spec lacks a mount that the new binary needs, the write
// fails with fs.ErrNotExist. A match on that error covers every mount
// that a later release adds, with no list of them.
func awaitingPodRefresh(podStale bool, err error) bool {
	return podStale && errors.Is(err, fs.ErrNotExist)
}

// applySysctls writes the default sysctls and then the spec's sysctls
// under dir, the host's /proc/sys. It returns the value of each
// parameter as the kernel reports it after all writes, the names of the
// parameters whose files do not exist, and one error for the defaults
// and one for the spec. Each error joins every failure of its set, so a
// person sees every bad parameter in one message.
//
// For a name in both sets, the function writes only the spec's value.
// init already wrote the default at boot, and a write of the default
// before the spec's value would set the kernel to the default for a
// moment on every pass. A name spelled with dots and the same name
// spelled with slashes count as one name.
//
// The values are read again after all writes, because some writes
// change other parameters. net.ipv4.ip_forward and
// net.ipv4.conf.all.forwarding are the same switch, and a write of
// vm.dirty_bytes sets vm.dirty_ratio to 0. A value read right after its
// own write can be wrong by the end of the pass, and the check in
// backstop.go would then find a difference on every run.
func applySysctls(dir string, defaults, desired map[string]string, out *passOutcome, mem *sysctlMemory) (map[string]string, []string, error, error) {
	observed, defaultsMissing, defaultsErr := applySysctlSet(dir, withoutKeys(defaults, desired), out, mem)
	fromSpec, specMissing, specErr := applySysctlSet(dir, desired, out, mem)
	maps.Copy(observed, fromSpec)
	for name := range observed {
		if value, err := machine.ReadSysctl(dir, name); err == nil {
			observed[name] = value
		}
	}
	mem.readBack(dir, observed)
	return observed, append(defaultsMissing, specMissing...), defaultsErr, specErr
}

// withoutKeys returns a copy of m without the names in drop. It treats
// net.ipv4.ip_forward and net/ipv4/ip_forward as the same name.
func withoutKeys(m, drop map[string]string) map[string]string {
	dropped := map[string]bool{}
	for name := range drop {
		dropped[sysctlFile(name)] = true
	}
	kept := maps.Clone(m)
	maps.DeleteFunc(kept, func(name, _ string) bool { return dropped[sysctlFile(name)] })
	return kept
}

// sysctlFile returns a parameter's path under /proc/sys, by the same
// rule as machine.ApplySysctl: a name that contains a slash is already
// a path, and otherwise each dot is a slash.
func sysctlFile(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return strings.ReplaceAll(name, ".", "/")
}

// applySysctl is a variable so that a test can replace the kernel's
// behavior, for example a kernel that stores a value in another form or
// that changes a second parameter on a write.
var applySysctl = machine.ApplySysctl

// sysctlMemory records, for each parameter, the last value the
// operator wrote and the value the kernel returned when the operator
// read it after the write. The kernel often returns a value in a
// different form than it was written: 0x10 reads as 16, kernel.printk
// reads as four values, and vm.nr_hugepages reads as the number of
// pages the kernel could allocate. Such a parameter never equals the
// spec, so without this record the operator would write it on every
// pass, and the backstop would report each write as a repair. A
// parameter that still reads as it did after the last write of the
// same value counts as current.
//
// The kernel refuses reads of a write-only parameter such as
// vm.drop_caches, so the operator writes it once for each value in the
// spec.
//
// Only the reconcile goroutine uses it, so it has no lock. Every method
// does nothing on a nil *sysctlMemory.
type sysctlMemory struct {
	written map[string]sysctlWrite
}

type sysctlWrite struct {
	value    string
	readBack string
	// writeOnly is true for a parameter whose read the kernel refused
	// after the write.
	writeOnly bool
}

func newSysctlMemory() *sysctlMemory {
	return &sysctlMemory{written: map[string]sysctlWrite{}}
}

// matchesLastWrite reports whether the parameter still reads as it did
// after the last write of value. kernel and readErr are the result of
// a read now.
func (m *sysctlMemory) matchesLastWrite(name, value, kernel string, readErr error) bool {
	if m == nil {
		return false
	}
	w, ok := m.written[name]
	if !ok || w.value != value {
		return false
	}
	if readErr != nil {
		return w.writeOnly && errors.Is(readErr, fs.ErrPermission)
	}
	return !w.writeOnly && sameSysctlValue(kernel, w.readBack)
}

// wrote records a write of value. The parameter counts as write-only
// until readBack records a value for it.
func (m *sysctlMemory) wrote(name, value string) {
	if m != nil {
		m.written[name] = sysctlWrite{value: value, writeOnly: true}
	}
}

// readBack records the value in observed for each written parameter. A
// parameter that is not in observed stays write-only.
func (m *sysctlMemory) readBack(dir string, observed map[string]string) {
	if m == nil {
		return
	}
	for name, w := range m.written {
		if value, ok := observed[name]; ok {
			w.readBack, w.writeOnly = value, false
			m.written[name] = w
		}
	}
}

// applySysctlSet writes each parameter of desired whose value in the
// kernel differs, in sorted order. It reads before it writes, so a
// parameter that is already correct costs one read and no write. A
// parameter also counts as correct when mem.matchesLastWrite returns
// true for it.
//
// The returned map has the values that the kernel returns, not the
// values the function wrote. So a value that another process changes
// differs from the spec on the next pass and is written again. It also
// returns the names whose files do not exist, and an error that joins
// every failure. out records each write and each failure.
func applySysctlSet(dir string, desired map[string]string, out *passOutcome, mem *sysctlMemory) (map[string]string, []string, error) {
	var errs []error
	var missing []string
	observed := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(desired)) {
		value := desired[name]
		current, readErr := machine.ReadSysctl(dir, name)
		if mem.matchesLastWrite(name, value, current, readErr) || readErr == nil && sameSysctlValue(current, value) {
			if readErr == nil {
				observed[name] = current
			}
			continue
		}
		if err := applySysctl(dir, name, value); err != nil {
			errs = append(errs, err)
			out.fail("writing the sysctl "+name, err)
			if errors.Is(err, fs.ErrNotExist) {
				missing = append(missing, name)
			}
			continue
		}
		out.wrote("writing the sysctl " + name)
		mem.wrote(name, value)
		if value, err := machine.ReadSysctl(dir, name); err == nil {
			observed[name] = value
		}
	}
	return observed, missing, errors.Join(errs...)
}

// sameSysctlValue compares two values and ignores the whitespace
// between their fields. The kernel returns a parameter such as
// net.ipv4.ip_local_port_range as "1024	65535", and a spec writes
// "1024 65535". A comparison of the exact strings would write the
// parameter on every pass.
func sameSysctlValue(a, b string) bool {
	return slices.Equal(strings.Fields(a), strings.Fields(b))
}

// storageCondition reports whether every storage role that the spec
// declares is on a partition. When init cannot place a declared role on
// a partition, it powers the machine off and does not start k3s
// (init/storage.go), so RolesInMemory is not expected on a running
// machine.
func storageCondition(spec machine.StorageSpec, status machine.StorageStatus) api.Condition {
	var placed, inMemory []string
	for _, role := range spec.Roles() {
		rs := status.Role(role.Name)
		if rs != nil && rs.Backing == machine.BackingPartition {
			placed = append(placed, fmt.Sprintf("%s on %s", role.Name, rs.Device))
		} else {
			inMemory = append(inMemory, string(role.Name))
		}
	}
	switch {
	case len(inMemory) > 0:
		return api.Condition{
			Type: "StorageReady", Status: api.ConditionFalse, Reason: "RolesInMemory",
			Message: fmt.Sprintf("These declared roles are backed by memory: %s", strings.Join(inMemory, ", ")),
		}
	case len(placed) > 0:
		return api.Condition{
			Type: "StorageReady", Status: api.ConditionTrue, Reason: "AllRolesPlaced",
			Message: strings.Join(placed, ", "),
		}
	default:
		return api.Condition{
			Type: "StorageReady", Status: api.ConditionTrue, Reason: "NothingDeclared",
			Message: "No storage is declared, so every role is backed by memory",
		}
	}
}

// outcomesCondition builds the condition for a list of items that init
// reported on, such as modules or features. observed is the number of
// items, and problems has one message for each item that failed.
func outcomesCondition(condType string, observed int, problems []string, failedReason, healthyReason, healthyMessage, noneMessage string) api.Condition {
	switch {
	case len(problems) > 0:
		return api.Condition{
			Type: condType, Status: api.ConditionFalse, Reason: failedReason,
			Message: strings.Join(problems, "; "),
		}
	case observed > 0:
		return api.Condition{
			Type: condType, Status: api.ConditionTrue, Reason: healthyReason,
			Message: healthyMessage,
		}
	default:
		return api.Condition{
			Type: condType, Status: api.ConditionTrue, Reason: "NothingDeclared",
			Message: noneMessage,
		}
	}
}

// modulesCondition reports the load state of the declared modules.
// Loaded and Builtin are healthy. For any other state the message is
// init's, which says how to fix it: a rebuilt image for a Missing
// module, or the hardware's error for a Failed one.
func modulesCondition(observed []machine.ModuleStatus) api.Condition {
	var problems []string
	for _, s := range observed {
		if s.State == machine.ModuleLoaded || s.State == machine.ModuleBuiltin {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: %s", s.Name, s.Message))
	}
	return outcomesCondition("ModulesLoaded", len(observed), problems,
		"ModulesNotLoaded", "AllLoaded",
		fmt.Sprintf("All %d declared modules are in the kernel", len(observed)),
		"No extra modules are declared")
}

// moduleParametersCondition reports the declared module parameters
// that cannot have reached the kernel, because the module is built in
// or was already loaded when the declared modules loaded. It does not
// compare the declared values with the ones in /sys, because the kernel
// shows a bool as Y or N and an array with its own separators, so the
// comparison would report a difference that is not one. Each problem
// message says how to fix it.
func moduleParametersCondition(declared map[string]string, observed []machine.ModuleStatus) api.Condition {
	byName := map[string]machine.ModuleStatus{}
	for _, s := range observed {
		byName[s.Name] = s
	}
	var problems []string
	// A module that was declared after the last boot has no load to
	// report on, so it is skipped. SpecConverged reports the change,
	// and decideConvergence loads the module live or reboots.
	loaded := 0
	for _, name := range machine.ModuleParameterModules(declared) {
		s, seen := byName[name]
		if !seen {
			continue
		}
		// A Failed or Missing module is reported by modulesCondition,
		// so only Loaded modules count toward the healthy message.
		switch {
		case s.State == machine.ModuleBuiltin:
			problems = append(problems, fmt.Sprintf(
				"%s: the kernel has %s built in, so no module load passed %s. Set it on the kernel command line",
				name, name, machine.ModuleParameterString(name, declared)))
		case s.AlreadyResident:
			problems = append(problems, fmt.Sprintf(
				"%s: %s was already in the kernel when the declared modules loaded, so no module load passed %s. "+
					"The module comes from the image's fixed list, a cluster feature, or the dependencies of an earlier declared module",
				name, name, machine.ModuleParameterString(name, declared)))
		case s.State == machine.ModuleLoaded:
			loaded++
		}
	}
	// Parameters can be declared while no load succeeded, so the
	// message must not say that none were declared.
	none := "No module parameters are declared"
	if len(declared) != 0 {
		none = "No module load in this boot passed a declared parameter"
	}
	return outcomesCondition("ModuleParametersApplied", loaded, problems,
		"ParametersNotApplied", "Applied",
		fmt.Sprintf("Every declared parameter was passed to the kernel when its module loaded. Loaded modules: %d", loaded),
		none)
}

// serioAttachedCondition is the type of the condition that
// serioCondition builds. phase.go skips it when it computes the phase
// and the Ready condition.
const serioAttachedCondition = "SerioAttached"

// serioCondition reports status.serio. It is True when every entry is
// Attached. Otherwise it reports the first entry that is not Attached,
// with the reason Missing when that entry is Missing and Refused for any
// other state. The message is the entry followed by init's message,
// which has the kernel's error or the module to declare. The condition
// can change without a reboot, because an adapter can be unplugged
// while the machine runs.
func serioCondition(observed []machine.SerioStatus) api.Condition {
	for _, s := range observed {
		if s.State == machine.SerioAttached {
			continue
		}
		reason := string(machine.SerioRefused)
		if s.State == machine.SerioMissing {
			reason = string(machine.SerioMissing)
		}
		entry := s.Attachment().String()
		if s.TTY != "" {
			entry += " on " + s.TTY
		}
		return api.Condition{
			Type: serioAttachedCondition, Status: api.ConditionFalse,
			Reason: reason, Message: entry + ": " + s.Message,
		}
	}
	return outcomesCondition(serioAttachedCondition, len(observed), nil, "", "AllAttached",
		fmt.Sprintf("All %d serio entries are attached", len(observed)),
		"No serio entries are declared")
}

// featuresCondition reports the state of the enabled features. For a
// state other than Active, the message is init's. A Missing feature
// needs a release whose image has the feature's files, because enabling
// a feature does not build it.
func featuresCondition(observed []machine.FeatureStatus) api.Condition {
	var problems []string
	for _, s := range observed {
		if s.State == machine.FeatureActive {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: %s", s.Name, s.Message))
	}
	return outcomesCondition("FeaturesReady", len(observed), problems,
		"FeaturesNotReady", "AllActive",
		fmt.Sprintf("All %d enabled features are active on this machine", len(observed)),
		"The Cluster enables no features")
}

// wirelessCondition reports whether each wireless interface that the
// spec declares is Connected. The message for an interface is init's
// message from the supplicant, which tells a wrong passphrase apart
// from an access point that is switched off.
//
// The reason is Joining when every interface that is not Connected is
// still Associating, and phase.go does not count Joining as Degraded.
// An interface is Associating while init joins it in the background,
// which init does when a wired interface already reaches the cluster
// (backgroundable in init/radios.go). A radio that loses its access
// point after it joined is also Associating, because the supplicant
// joins again on its own.
func wirelessCondition(interfaces []machine.InterfaceStatus) api.Condition {
	declared, joining := 0, 0
	var problems []string
	for _, iface := range interfaces {
		w := iface.Wireless
		if w == nil {
			continue
		}
		declared++
		if w.State == machine.WirelessConnected {
			continue
		}
		if w.State == machine.WirelessAssociating {
			joining++
		}
		problems = append(problems, fmt.Sprintf("%s (%s): %s", iface.Name, w.SSID, wirelessReason(*w)))
	}
	// One interface that is not Associating makes the reason NotJoined,
	// so that a wrong passphrase is not hidden by another radio that is
	// still joining.
	reason := "NotJoined"
	if joining == len(problems) {
		reason = "Joining"
	}
	return outcomesCondition("WirelessJoined", declared, problems,
		reason, "AllJoined",
		fmt.Sprintf("All %d declared wireless networks are joined", declared),
		"No wireless network is declared")
}

// wirelessReason returns init's message for an interface, or its state
// when init has written none yet, as for an interface that is still
// Associating.
func wirelessReason(w machine.WirelessStatus) string {
	if w.Message != "" {
		return w.Message
	}
	return string(w.State)
}

// nodeHealthyCondition copies the Node's Ready condition into the
// Machine's NodeHealthy condition. A Node with no Ready condition is
// unhealthy, because a kubelet that has not reported cannot be assumed
// to be serving.
func nodeHealthyCondition(node *nodeObject) api.Condition {
	for _, c := range node.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}
		if c.Status == api.ConditionTrue {
			return api.Condition{Type: "NodeHealthy", Status: api.ConditionTrue, Reason: "KubeletReady",
				Message: "The Node reports Ready, so the kubelet runs and renews its node lease"}
		}
		return api.Condition{Type: "NodeHealthy", Status: api.ConditionFalse, Reason: "NodeNotReady",
			Message: fmt.Sprintf("The Node reports Ready=%s: %s", c.Status, c.Message)}
	}
	return api.Condition{Type: "NodeHealthy", Status: api.ConditionFalse, Reason: "NodeNotReady",
		Message: "The Node has no Ready condition, because the kubelet has never reported"}
}
