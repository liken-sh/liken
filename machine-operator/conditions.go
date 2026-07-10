package main

// The condition constructors reconcile publishes each pass: each one
// judges one aspect of the machine — the facts, the sysctls, the
// storage, the modules, the features, the Node's health — and reports
// it as a standard Kubernetes condition.

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chrisguidry/liken/machine"
)

func factsCondition(err error) machine.Condition {
	if err != nil {
		return machine.Condition{
			Type: "FactsPublished", Status: machine.ConditionFalse,
			Reason: "FactsUnreadable", Message: err.Error(),
		}
	}
	return machine.Condition{Type: "FactsPublished", Status: machine.ConditionTrue, Reason: "FactsRead"}
}

func sysctlsCondition(err error) machine.Condition {
	if err != nil {
		return machine.Condition{
			Type: "SysctlsApplied", Status: machine.ConditionFalse,
			Reason: "ApplyFailed", Message: err.Error(),
		}
	}
	return machine.Condition{Type: "SysctlsApplied", Status: machine.ConditionTrue, Reason: "Applied"}
}

// applySysctls actuates spec.sysctls against the host's /proc/sys
// (dir, reachable directly because this pod runs privileged in the
// host's namespaces), then reads every parameter back. The returned
// map is what the kernel now reports, not what we wrote: if some
// other agent resets a value, the next pass both re-asserts it and
// reports what was actually observed. One failure never stops the
// rest of the parameters from being applied, and every failure is
// joined into the returned error, because the condition built from it
// is the operator's whole report: a message naming one bad parameter
// when three are failing would send a person around this loop three
// times.
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
// condition, comparing what the spec declared against where each role
// is actually backed. True means every declared role sits on its
// partition. False should be unreachable on a running machine, since
// init powers off rather than boot with a declared role unsatisfied.
// But a condition has to be able to express every state it names, and
// a future, softer failure mode may need it.
func storageCondition(spec machine.StorageSpec, status machine.StorageStatus) machine.Condition {
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
		return machine.Condition{
			Type: "StorageReady", Status: machine.ConditionFalse, Reason: "RolesInMemory",
			Message: fmt.Sprintf("declared roles backed by memory: %s", strings.Join(inMemory, ", ")),
		}
	case len(placed) > 0:
		return machine.Condition{
			Type: "StorageReady", Status: machine.ConditionTrue, Reason: "AllRolesPlaced",
			Message: strings.Join(placed, ", "),
		}
	default:
		return machine.Condition{
			Type: "StorageReady", Status: machine.ConditionTrue, Reason: "NothingDeclared",
			Message: "no storage declared; all roles backed by memory",
		}
	}
}

// outcomesCondition reduces a boot's per-item outcomes (modules,
// features) to one condition: any problem makes it False carrying
// every item's message, all healthy is True with a summary, and
// nothing declared is True on its own terms.
func outcomesCondition(condType string, observed int, problems []string, failedReason, healthyReason, healthyMessage, noneMessage string) machine.Condition {
	switch {
	case len(problems) > 0:
		return machine.Condition{
			Type: condType, Status: machine.ConditionFalse, Reason: failedReason,
			Message: strings.Join(problems, "; "),
		}
	case observed > 0:
		return machine.Condition{
			Type: condType, Status: machine.ConditionTrue, Reason: healthyReason,
			Message: healthyMessage,
		}
	default:
		return machine.Condition{
			Type: condType, Status: machine.ConditionTrue, Reason: "NothingDeclared",
			Message: noneMessage,
		}
	}
}

// modulesCondition summarizes the boot's declared-module outcomes as
// one condition. Loaded and Builtin are both healthy; anything else
// carries init's message, which names the fix (a rebuilt image for a
// Missing module, the hardware's error for a Failed one), because a
// status that says what would repair it beats one that only says
// what's wrong.
func modulesCondition(observed []machine.ModuleStatus) machine.Condition {
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
// condition, the same shape modulesCondition takes. Anything not
// Active carries init's message, which names the fix: for a Missing
// feature that is a release whose image carries the payload, because
// enabling a feature never rebuilds anything by itself.
func featuresCondition(observed []machine.FeatureStatus) machine.Condition {
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
// Machine's vocabulary. A missing Ready condition on the Node reads
// as unhealthy: a kubelet that has never reported in cannot be
// assumed to be serving.
func nodeHealthyCondition(node *nodeObject) machine.Condition {
	for _, c := range node.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}
		if c.Status == machine.ConditionTrue {
			return machine.Condition{Type: "NodeHealthy", Status: machine.ConditionTrue, Reason: "KubeletReady",
				Message: "the Node reports Ready; the kubelet is serving this machine to the cluster"}
		}
		return machine.Condition{Type: "NodeHealthy", Status: machine.ConditionFalse, Reason: "NodeNotReady",
			Message: fmt.Sprintf("the Node reports Ready=%s: %s", c.Status, c.Message)}
	}
	return machine.Condition{Type: "NodeHealthy", Status: machine.ConditionFalse, Reason: "NodeNotReady",
		Message: "the Node carries no Ready condition; the kubelet has never reported in"}
}
