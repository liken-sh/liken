package main

import (
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// ownedConditionTypes lists the condition types this operator writes.
// Each pass keeps only the Machine's conditions of these types, so
// after a rollback the conditions that the newer release wrote are
// dropped. Without that, a stale False condition, such as one for an
// unplugged adapter, would make the machine Degraded under the older
// release's phase table and block every other machine's reboot turn
// until a person edited the status by hand.
//
// RebootApproved is not in the list because the cluster operator
// writes it, and ownsCondition keeps it. A type that a pass writes
// must be in the list, or each pass drops it at its start and writes
// it again at its end. TestEveryConditionAPassWritesIsOwned checks
// this.
var ownedConditionTypes = map[string]bool{
	"Ready":                      true,
	"FactsPublished":             true,
	"SysctlsApplied":             true,
	diskSettingsAppliedCondition: true,
	"HostEntriesApplied":         true,
	"WirelessJoined":             true,
	"StorageReady":               true,
	"ModulesLoaded":              true,
	"ModuleParametersApplied":    true,
	"FeaturesReady":              true,
	serioAttachedCondition:       true,
	clockSynchronizedCondition:   true,
	"NodeHealthy":                true,
	"NodeTaintsApplied":          true,
	"NodeLabelsApplied":          true,
	"NodeCurrent":                true,
	"SpecConverged":              true,
	"ClusterConverged":           true,
	clusterProvenCondition:       true,
	"VersionConverged":           true,
	"CredentialsConverged":       true,
	"ImportsConverged":           true,
	"RebootRequestHonored":       true,
}

// ownsCondition reports whether a pass keeps conditions of this type.
func ownsCondition(conditionType string) bool {
	return ownedConditionTypes[conditionType] || conditionType == machine.RebootApprovedCondition
}

// ownedConditions returns a new slice, so the caller can change it
// without changing the Machine it read.
func ownedConditions(conditions []api.Condition) []api.Condition {
	var kept []api.Condition
	for _, c := range conditions {
		if ownsCondition(c.Type) {
			kept = append(kept, c)
		}
	}
	return kept
}
