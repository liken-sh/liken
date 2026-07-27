package main

// The condition constructors that reconcile publishes on each pass.
// Each one checks one aspect of the machine: the facts, the sysctls,
// the storage, the modules, the features, or the Node's health. Each
// one reports its check as a standard Kubernetes condition.

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
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

// applySysctls writes both sets of kernel parameters to the host's
// /proc/sys (dir): the settings every liken machine holds, and then
// the Machine spec's own. The pod runs privileged in the host's
// namespaces, so it reaches /proc/sys directly.
//
// The order is the same order init uses at boot, and it is what makes
// spec.sysctls an override: a name in both sets is written twice, and
// the second write is the one that holds.
//
// The returned map merges the two observations with the spec's on top,
// for the same reason. A name in both sets was read back once before
// the spec wrote it and once after, and only the second reading
// describes the kernel as it now stands.
//
// One failure never stops the function from applying the rest of the
// parameters. The two errors stay apart because the condition treats
// them differently, and each joins every failure in its own set,
// because a message that names one bad parameter, when three are
// failing, would send a person through this loop three times.
func applySysctls(dir string, defaults, desired map[string]string) (map[string]string, error, error) {
	observed, defaultsErr := applySysctlSet(dir, defaults)
	fromSpec, specErr := applySysctlSet(dir, desired)
	maps.Copy(observed, fromSpec)
	return observed, defaultsErr, specErr
}

// applySysctlSet writes one set of parameters and reads each one back.
// The returned map holds what the kernel now reports, not what the
// function wrote. If another process resets a value, the next pass
// writes it again and reports what the pass actually observed.
func applySysctlSet(dir string, desired map[string]string) (map[string]string, error) {
	var errs []error
	observed := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(desired)) {
		if err := machine.ApplySysctl(dir, name, desired[name]); err != nil {
			errs = append(errs, err)
			continue
		}
		if value, err := machine.ReadSysctl(dir, name); err == nil {
			observed[name] = value
		}
	}
	return observed, errors.Join(errs...)
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
