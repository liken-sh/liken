package main

// The condition constructors that reconcile publishes on each pass.
// Each one checks one aspect of the machine: the facts, the sysctls,
// the storage, the modules, the features, or the Node's health. Each
// one reports its check as a standard Kubernetes condition.

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
			Reason: "FactsUnreadable", Message: err.Error(),
		}
	}
	return api.Condition{Type: "FactsPublished", Status: api.ConditionTrue, Reason: "FactsRead"}
}

// sysctlsCondition reports both halves of the sysctl pass, and only
// the spec's half can make the condition False.
//
// The reason is who wrote the failing parameter. A value from
// spec.sysctls belongs to this machine: a person asked for it here,
// nowhere else, and a machine that cannot honour its own spec is
// degraded. A value from machine.OSSysctls ships with the release, so
// every machine running that release applies the same table. A single
// bad entry there would take an entire fleet to Degraded in the same
// pass, which is the moment a per-machine health signal stops carrying
// any information and starts hiding the one machine with a real
// problem. So a failing default reports DefaultsIncomplete and leaves
// the machine Ready.
//
// A failing default is still visible twice. It names itself in this
// message, and its parameter is missing from status.sysctls, because
// applySysctls never reads back a value it could not write. That
// absence is what makes status.sysctls a list of the parameters that
// currently hold rather than the parameters somebody wanted.
func sysctlsCondition(defaultsErr, specErr error) api.Condition {
	if specErr != nil {
		message := specErr.Error()
		if defaultsErr != nil {
			message += "; " + defaultsErr.Error()
		}
		return api.Condition{
			Type: "SysctlsApplied", Status: api.ConditionFalse,
			Reason: "ApplyFailed", Message: message,
		}
	}
	if defaultsErr != nil {
		return api.Condition{
			Type: "SysctlsApplied", Status: api.ConditionTrue,
			Reason: "DefaultsIncomplete", Message: defaultsErr.Error(),
		}
	}
	return api.Condition{Type: "SysctlsApplied", Status: api.ConditionTrue, Reason: "Applied"}
}

// hostEntriesCondition reports the outcome of applyHostEntries, on
// the same terms as storageCondition and modulesCondition above:
// True and Applied on an ordinary pass, True and NothingDeclared when
// the spec declares no host entry at all, False and ApplyFailed when
// a read, a render, or a write failed, and False and
// AwaitingPodRefresh when that same failure is the pod-freshness
// guard's concern instead (awaitingPodRefresh, below). podStale is
// this pass's verdict from staleness.go.
func hostEntriesCondition(desired []machine.HostEntry, err error, podStale bool) api.Condition {
	if err != nil {
		if awaitingPodRefresh(podStale, err) {
			return api.Condition{
				Type: "HostEntriesApplied", Status: api.ConditionFalse,
				Reason: "AwaitingPodRefresh",
				Message: "the pod's template predates the release this machine runs; " +
					"the pod steward replaces the pod after a leader boots that release: " + err.Error(),
			}
		}
		return api.Condition{
			Type: "HostEntriesApplied", Status: api.ConditionFalse,
			Reason: "ApplyFailed", Message: err.Error(),
		}
	}
	if len(desired) == 0 {
		return api.Condition{
			Type: "HostEntriesApplied", Status: api.ConditionTrue,
			Reason: "NothingDeclared", Message: "no host entries declared",
		}
	}
	return api.Condition{Type: "HostEntriesApplied", Status: api.ConditionTrue, Reason: "Applied"}
}

// awaitingPodRefresh judges whether an actuation failure is the
// template lag itself, rather than a fault the machine actually has.
// System pods run the stable :installed tag and their DaemonSets
// update on OnDelete (cluster-operator/steward.go), so a reboot
// restarts a machine's own operator into a new binary without
// touching the pod spec around it. Only a leader's boot rewrites the
// AddOn manifests that produce a fresh template, so a follower that
// reboots first runs the new binary inside the old pod spec for a
// while. A path that does not exist inside that stale pod means a
// mount the old template lacks, whatever the mount is, so this rule
// covers every mount a future release may add without naming any of
// them by name.
//
// Two precedents already treat a release-wide condition as something
// other than one machine's own fault. The DRA plugin tolerates a
// mount its own stale pod lacks (main.go), because dying there would
// kill the very status publishing the pod steward waits on.
// sysctlsCondition reports a bad default as DefaultsIncomplete rather
// than ApplyFailed, because a fault every machine on the release
// carries at once tells a person nothing about which machine needs
// attention. This rule follows the same reasoning for a stale pod's
// missing mount.
//
// The reason this rule reports must not be AwaitingTurn. The rollout
// conductor scans a machine's conditions for that exact reason
// (cluster-operator/rollout.go, wantsTurn) to learn that the machine
// has a staged change ready for a disruption. AwaitingPodRefresh
// names a wait on the pod steward instead, so the conductor never
// reads this guard as a change the machine is asking permission to
// make.
func awaitingPodRefresh(podStale bool, err error) bool {
	return podStale && errors.Is(err, fs.ErrNotExist)
}

// applySysctls writes both sets of kernel parameters to the host's
// /proc/sys (dir): the settings every liken machine holds, and then
// the Machine spec's own. The pod runs privileged in the host's
// namespaces, so it reaches /proc/sys directly.
//
// spec.sysctls is an override: a name in both sets is applied with the
// spec's value alone, and the two spellings of one name, dots and
// slashes, count as one. init applies the two sets in order at boot,
// default first, and the operator skips the default instead. Each pass
// compares and writes, so applying both in order would write the
// default and then the spec's value on every pass of a converged
// machine, and the kernel would hold the default for a moment each
// time.
//
// After both sets, every parameter is read once more, and that read is
// what the function answers and what mem keeps. Two names can write
// one kernel variable: net.ipv4.ip_forward and
// net.ipv4.conf.all.forwarding are the same switch, and a write of
// vm.dirty_bytes zeroes vm.dirty_ratio. A read taken right after each
// write would hold a value that a later write in the same pass changed,
// and the check of the sysctls (backstop.go) would find it drifted on
// every check.
//
// One failure never stops the function from applying the rest of the
// parameters. The two errors stay apart because the condition treats
// them differently, and each joins every failure in its own set,
// because a message that names one bad parameter, when three are
// failing, would send a person through this loop three times. missing
// names each parameter whose file does not exist, for the check.
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

// withoutKeys answers the entries of m whose names are not in drop. A
// name matches in either spelling, net.ipv4.ip_forward or
// net/ipv4/ip_forward, because both name one file.
func withoutKeys(m, drop map[string]string) map[string]string {
	dropped := map[string]bool{}
	for name := range drop {
		dropped[sysctlFile(name)] = true
	}
	kept := maps.Clone(m)
	maps.DeleteFunc(kept, func(name, _ string) bool { return dropped[sysctlFile(name)] })
	return kept
}

// sysctlFile answers the path of a parameter under /proc/sys, with the
// rule machine.ApplySysctl uses: a name with a slash is a path already,
// and a name without one has dots for slashes.
func sysctlFile(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return strings.ReplaceAll(name, ".", "/")
}

// applySysctl writes one parameter. It is a variable so a test can play
// a kernel that stores a value in another form, or that changes a second
// parameter with the first.
var applySysctl = machine.ApplySysctl

// sysctlMemory keeps what the operator last wrote to each parameter, and
// what the kernel reported after. The kernel stores some values in
// another form than the one written: 0x10 reads back as 16, a write of
// one value to kernel.printk reads back as four, and vm.nr_hugepages
// reads back as many pages as the kernel could allocate. Compared with
// the spec's value, such a parameter differs on every pass, and every
// pass writes it again, which a backstop pass reports as a repair. So a
// parameter that still reads what the kernel reported after the
// operator's last write of the same value is current. A write-only
// parameter, such as vm.drop_caches, refuses every read, so it is
// written once for each value the spec gives it. Only the loop's
// goroutine applies sysctls, so the memory has no lock. A nil memory
// remembers nothing.
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

// holds answers whether the parameter is as the operator's last write
// of value left it.
func (m *sysctlMemory) holds(name, value, kernel string, readErr error) bool {
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

// wrote records a write of value. readBack completes the record.
func (m *sysctlMemory) wrote(name, value string) {
	if m != nil {
		m.written[name] = sysctlWrite{value: value, writeOnly: true}
	}
}

// readBack records what the kernel reports for each parameter written
// this pass. A parameter the kernel refused to read stays write-only.
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

// applySysctlSet reconciles one set of parameters against the kernel,
// under the same write-on-divergence rule as applyHostEntries
// (hosts.go): read a parameter first, and write it only when the
// kernel's reported value differs from the desired one, and from what
// the kernel reported after the last write of it (sysctlMemory). A
// converged parameter costs one read and no write, which is the common
// case on every pass after the first.
//
// The comparison ignores how the values are spaced. A parameter that
// holds several values, such as net.ipv4.ip_local_port_range, is
// written as "1024 65535", and the kernel reports it as "1024\t65535".
// Without this, every such parameter differs from its spec, and every
// pass writes it again.
//
// The returned map holds what the kernel now reports, not what the
// function wrote. If another process resets a value, the next pass
// finds the divergence and writes it again. The pass's outcome records
// each write, and each failure for the retry, by the parameter's name.
func applySysctlSet(dir string, desired map[string]string, out *passOutcome, mem *sysctlMemory) (map[string]string, []string, error) {
	var errs []error
	var missing []string
	observed := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(desired)) {
		value := desired[name]
		current, readErr := machine.ReadSysctl(dir, name)
		if mem.holds(name, value, current, readErr) || readErr == nil && sameSysctlValue(current, value) {
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

// sameSysctlValue reports whether two spellings of a parameter's value
// hold the same values in the same order. The kernel separates the
// values of a parameter with tabs, and a spec separates them with
// spaces.
func sameSysctlValue(a, b string) bool {
	return slices.Equal(strings.Fields(a), strings.Fields(b))
}

// storageCondition summarizes storage as one standard Kubernetes
// condition. It compares what the spec declared against where the
// system actually backs each role. True means every declared role
// sits on its partition. False should not happen on a running
// machine, because init powers off instead of booting with a
// declared role left unsatisfied. But a condition must be able to
// report every state it names, and a future, softer failure mode may
// need this one.
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
			Message: fmt.Sprintf("declared roles backed by memory: %s", strings.Join(inMemory, ", ")),
		}
	case len(placed) > 0:
		return api.Condition{
			Type: "StorageReady", Status: api.ConditionTrue, Reason: "AllRolesPlaced",
			Message: strings.Join(placed, ", "),
		}
	default:
		return api.Condition{
			Type: "StorageReady", Status: api.ConditionTrue, Reason: "NothingDeclared",
			Message: "no storage declared; all roles backed by memory",
		}
	}
}

// outcomesCondition reduces a boot's outcomes for individual items
// (modules, features) to one condition. Any problem makes the
// condition False and carries every item's message. When every item
// is healthy, the condition is True with a summary. When nothing is
// declared, the condition is also True, with its own message.
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

// modulesCondition summarizes the boot's outcomes for declared
// modules as one condition. Loaded and Builtin are both healthy
// states. Any other state carries init's message, which names the
// fix: a rebuilt image for a Missing module, or the hardware's error
// for a Failed one. A status that names the fix is more useful than
// one that only names the problem.
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
		fmt.Sprintf("all %d declared modules are in the kernel", len(observed)),
		"no extra modules declared")
}

// moduleParametersCondition reports the two cases where a declared
// parameter structurally cannot have reached the kernel: the module
// is built in, or it was already resident when the declared pass got
// to it. Both are facts about the load, not about values. The
// declared string is never compared against the /sys readback,
// because the kernel renders a bool as Y or N and an array with its
// own separators, so a machine comparison would report false drift
// on the most common parameter types; a person compares the two
// status fields that sit beside each other. Each problem message
// names its own fix, the way every other outcome message does.
func moduleParametersCondition(declared map[string]string, observed []machine.ModuleStatus) api.Condition {
	byName := map[string]machine.ModuleStatus{}
	for _, s := range observed {
		byName[s.Name] = s
	}
	var problems []string
	// Only a module the boot observed can say whether its load
	// carried the parameters. A module declared since the last boot
	// has no load to report on, so it counts toward nothing here;
	// the convergence machinery already carries it to the reboot.
	loaded := 0
	for _, name := range machine.ModuleParameterModules(declared) {
		s, seen := byName[name]
		if !seen {
			continue
		}
		// Only a load that succeeded can have carried the string, so
		// only Loaded modules count toward the healthy message. A
		// Failed or Missing module is ModulesLoaded's problem, and
		// claiming its parameters "reached the kernel" would be
		// false.
		switch {
		case s.State == machine.ModuleBuiltin:
			problems = append(problems, fmt.Sprintf(
				"%s: the kernel builds %s in, so no load carried %s; set it on the kernel command line",
				name, name, machine.ModuleParameterString(name, declared)))
		case s.AlreadyResident:
			problems = append(problems, fmt.Sprintf(
				"%s: %s was already in the kernel when the declared modules loaded, so no load carried %s; "+
					"it comes from the image's fixed list, a cluster feature, or an earlier declared module's dependencies",
				name, name, machine.ModuleParameterString(name, declared)))
		case s.State == machine.ModuleLoaded:
			loaded++
		}
	}
	// Parameters were declared even when no load succeeded, so the
	// message must not claim nothing was declared; it says no load
	// carried one, which is the fact.
	none := "no module parameters declared"
	if len(declared) != 0 {
		none = "no load this boot carried a declared parameter"
	}
	return outcomesCondition("ModuleParametersApplied", loaded, problems,
		"ParametersNotApplied", "Applied",
		fmt.Sprintf("every parameter declared for %d modules reached the kernel at the load", loaded),
		none)
}

// serioAttachedCondition is the type of the condition serioCondition
// builds. The phase and the Ready roll-up skip it by this name
// (phase.go).
const serioAttachedCondition = "SerioAttached"

// serioCondition summarizes status.serio. It is True when every entry
// is Attached. Otherwise its reason is the first unattached entry's
// state, Missing or Refused, and its message names that entry and
// carries init's message, which gives the kernel's error text or the
// module to declare. Unlike the modules, the attachments change while
// the machine runs, because an adapter can be unplugged, so this
// condition moves between passes without a boot.
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
		fmt.Sprintf("all %d serio attachments hold", len(observed)),
		"no serio entries declared")
}

// featuresCondition summarizes the boot's feature outcomes as one
// condition, in the same form as modulesCondition. Any state other
// than Active carries init's message, which names the fix. For a
// Missing feature, the fix is a release whose image carries the
// needed payload, because enabling a feature never rebuilds anything
// by itself.
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
		fmt.Sprintf("all %d enabled features are active on this machine", len(observed)),
		"the cluster enables no features")
}

// wirelessCondition summarizes every radio the boot was asked to
// join as one condition, in the same form as modulesCondition and
// featuresCondition. Only Connected is a healthy state. A machine
// with no wireless entry declares nothing and stays Ready. The
// message carries the supplicant's own reason, the one fact that
// tells a wrong passphrase apart from an access point that is
// switched off.
//
// A radio still associating is work in progress, not a failure: the
// boot handed it to the background on purpose, and the verdict
// arrives in seconds. The Joining reason marks that window so the
// phase mapping can leave the machine Ready while it lasts.
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
	// One settled failure makes the reason NotJoined whatever the
	// other radios are doing, because a wrong key or a stuck raise
	// must not hide behind a neighbor that is merely slow.
	reason := "NotJoined"
	if joining == len(problems) {
		reason = "Joining"
	}
	return outcomesCondition("WirelessJoined", declared, problems,
		reason, "AllJoined",
		fmt.Sprintf("all %d declared wireless networks are joined", declared),
		"no wireless network declared")
}

// wirelessReason names why one radio is not joined. Init writes a
// message for every failure it has words for. The state is the
// fallback, for a radio that is still associating and has said
// nothing yet.
func wirelessReason(w machine.WirelessStatus) string {
	if w.Message != "" {
		return w.Message
	}
	return string(w.State)
}

// nodeHealthyCondition translates the Node's Ready condition into the
// Machine's own condition. When the Node carries no Ready condition,
// this function reports the machine as unhealthy: a kubelet that has
// never reported in cannot be assumed to be serving.
func nodeHealthyCondition(node *nodeObject) api.Condition {
	for _, c := range node.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}
		if c.Status == api.ConditionTrue {
			return api.Condition{Type: "NodeHealthy", Status: api.ConditionTrue, Reason: "KubeletReady",
				Message: "the Node reports Ready; the kubelet is serving this machine to the cluster"}
		}
		return api.Condition{Type: "NodeHealthy", Status: api.ConditionFalse, Reason: "NodeNotReady",
			Message: fmt.Sprintf("the Node reports Ready=%s: %s", c.Status, c.Message)}
	}
	return api.Condition{Type: "NodeHealthy", Status: api.ConditionFalse, Reason: "NodeNotReady",
		Message: "the Node carries no Ready condition; the kubelet has never reported in"}
}
