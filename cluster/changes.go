package cluster

// Classifying a cluster-document edit by how it must be applied.
//
// liken has three tiers of convergence, decided by where a setting
// is read. Settings the kernel reads live (/proc/sys) reconcile in
// place; settings k3s reads at process start (the boot drop-in,
// registries.yaml, the feature manifests) apply by restarting the
// k3s child; everything read earlier in a boot (the address plan,
// storage, the time hierarchy) applies by rebooting the machine.
// The reboot tier always works for the other two, because a reboot
// is a k3s restart plus more, so classification must only ever err
// toward the heavier tier.
//
// This file is the classifier for the middle tier. The operator
// consults it to decide whether a staged cluster document warrants a
// restart intent or a reboot intent, and init consults the same
// function before acting on a restart intent, so the two programs
// can never disagree about what a restart may apply.

import "encoding/json"

// RestartApplies reports whether a k3s restart is enough to move a
// machine from the current spec to the desired one: the specs must differ
// (no drift needs no disruption at all), and the difference must be
// confined to the restart-class fields, the ones k3s reads only at
// process start.
//
// The comparison is deliberately subtractive rather than a list of
// changed domains: zero out the restart-class fields on copies of
// both specs and ask whether anything else differs. Any residual
// difference means the reboot tier, which makes the safety property
// structural — a future ClusterSpec field is reboot-class the day it
// is added, with no classification table to remember to extend, and
// forgetting one could only ever cost an unnecessary reboot, never
// an under-applied restart.
//
// Both comparisons run over JSON renderings, the same bytes the
// document hash is built from, so this classification and the hash
// can never disagree about whether two specs differ. Version and
// Releases are excluded from both: canonical documents never carry
// them (the operator strips them before hashing, because their
// actuation is a download, not a boot).
func RestartApplies(current, desired ClusterSpec) bool {
	current.Version, desired.Version = "", ""
	current.Releases, desired.Releases = ClusterReleasesSpec{}, ClusterReleasesSpec{}
	if jsonEqual(current, desired) {
		return false
	}
	current.Features, desired.Features = nil, nil
	current.Registries, desired.Registries = RegistriesSpec{}, RegistriesSpec{}
	return jsonEqual(current, desired)
}

// jsonEqual compares two values by their JSON bytes. A marshal error
// reads as "differs", the safe direction under this file's rule:
// every type compared here is a plain data struct that cannot
// actually fail to marshal, but if one somehow did, "differs" costs
// at most an unneeded reboot, while a false "equal" could leave a
// change unapplied.
func jsonEqual(a, b any) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ra) == string(rb)
}
