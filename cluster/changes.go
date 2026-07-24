package cluster

// This file classifies a cluster-document edit by how the system
// must apply it.
//
// liken has three tiers of convergence, decided by where a setting
// is read. Settings the kernel reads live (/proc/sys) reconcile in
// place. Settings k3s reads at process start (the boot drop-in,
// registries.yaml, the feature manifests, and the Go runtime
// environment init hands the process) apply by restarting the k3s
// child process. Everything read earlier in a boot (the address
// plan, storage, the time hierarchy) applies by rebooting the
// machine. The reboot tier always works in place of the other two,
// because a reboot is a k3s restart plus more. Classification must
// therefore always err toward the heavier tier.
//
// This file is the classifier for the middle tier. The operator
// consults it to decide whether a staged cluster document calls for
// a restart intent or a reboot intent. Init consults the same
// function before it acts on a restart intent, so the two programs
// can never disagree about what a restart may apply.

import "encoding/json"

// RestartApplies reports whether a k3s restart is enough to move a
// machine from the current spec to the desired one. The two specs
// must differ (no drift needs no disruption at all), and the
// difference must be confined to the restart-class fields, the ones
// k3s reads only at process start.
//
// The comparison works by subtraction rather than by a list of
// changed domains: it zeroes the restart-class fields on copies of
// both specs and asks whether anything else differs. Any remaining
// difference means the reboot tier. This makes the safety property
// structural: a future ClusterSpec field is reboot-class from the
// day it is added, with no classification table to remember to
// extend, and forgetting one could only ever cost an unnecessary
// reboot, never an under-applied restart.
//
// Both comparisons run over JSON renderings, the same bytes the
// document hash is built from, so this classification and the hash
// can never disagree about whether two specs differ. Version and
// Releases are excluded from both comparisons: canonical documents
// never carry them, because the operator strips them before hashing,
// and their actuation is a download, not a boot.
func RestartApplies(current, desired ClusterSpec) bool {
	current.Version, desired.Version = "", ""
	current.Releases, desired.Releases = ClusterReleasesSpec{}, ClusterReleasesSpec{}
	if jsonEqual(current, desired) {
		return false
	}
	current.Features, desired.Features = nil, nil
	current.Registries, desired.Registries = RegistriesSpec{}, RegistriesSpec{}
	current.Runtime, desired.Runtime = ClusterRuntimeSpec{}, ClusterRuntimeSpec{}
	return jsonEqual(current, desired)
}

// jsonEqual compares two values by their JSON bytes. A marshal error
// reads as "differs", the safe direction under this file's rule.
// Every type compared here is a plain data struct that cannot
// actually fail to marshal. But if one somehow did, reading it as
// "differs" costs at most an unneeded reboot, while a false "equal"
// result could leave a change unapplied.
func jsonEqual(a, b any) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ra) == string(rb)
}
