package main

import (
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// The condition types this operator writes. A pass starts from the
// conditions it owns and drops every other type, so a condition that
// a newer release wrote does not outlive a rollback. The older phase
// table reads a reason it does not know as Degraded, and a Degraded
// machine holds every other machine's reboot turn, so a stale False
// such as an unplugged adapter's would stall the fleet until a person
// edited the status by hand.
//
// RebootApproved is the one condition another program writes: the
// cluster operator grants a turn with it, and this operator carries
// it unchanged. A type that a pass writes but this list misses would
// vanish at the start of each pass and return at its end, and a test
// (ownedconditions_test.go) fails on that.
var ownedConditionTypes = map[string]bool{
	"Ready":                   true,
	"FactsPublished":          true,
	"SysctlsApplied":          true,
	"HostEntriesApplied":      true,
	"WirelessJoined":          true,
	"StorageReady":            true,
	"ModulesLoaded":           true,
	"ModuleParametersApplied": true,
	"FeaturesReady":           true,
	serioAttachedCondition:    true,
	"NodeHealthy":             true,
	"NodeTaintsApplied":       true,
	"NodeLabelsApplied":       true,
	"NodeCurrent":             true,
	"SpecConverged":           true,
	"ClusterConverged":        true,
	"VersionConverged":        true,
	"CredentialsConverged":    true,
	"ImportsConverged":        true,
	"RebootRequestHonored":    true,
}

// ownsCondition reports whether a pass keeps a condition of the type.
func ownsCondition(conditionType string) bool {
	return ownedConditionTypes[conditionType] || conditionType == machine.RebootApprovedCondition
}

// ownedConditions answers the conditions of the types a pass keeps,
// in a new slice.
func ownedConditions(conditions []api.Condition) []api.Condition {
	var kept []api.Condition
	for _, c := range conditions {
		if ownsCondition(c.Type) {
			kept = append(kept, c)
		}
	}
	return kept
}
