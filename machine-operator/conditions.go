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

func sysctlsCondition(err error) api.Condition {
	if err != nil {
		return api.Condition{
			Type: "SysctlsApplied", Status: api.ConditionFalse,
			Reason: "ApplyFailed", Message: err.Error(),
		}
	}
	return api.Condition{Type: "SysctlsApplied", Status: api.ConditionTrue, Reason: "Applied"}
}

// applySysctls writes spec.sysctls to the host's /proc/sys (dir).
// The pod runs privileged in the host's namespaces, so it reaches
// /proc/sys directly. After writing, the function reads every
// parameter back. The returned map holds what the kernel now
// reports, not what the function wrote. If another process resets a
// value, the next pass writes it again and reports what the pass
// actually observed. One failure never stops the function from
// applying the rest of the parameters. The function joins every
// failure into the returned error, because the condition built from
// it is the operator's whole report on this check. A message that
// names one bad parameter, when three parameters are failing, would
// send a person through this loop three times.
func applySysctls(dir string, desired map[string]string) (map[string]string, error) {
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
