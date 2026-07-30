package cluster

// This file classifies a cluster-document edit by how the system
// must apply it.
//
// liken has four tiers of convergence, determined by where a setting
// is read and by what still acts on it once the boot that read it is
// over.
//
// Settings the kernel reads live (/proc/sys) reconcile in place.
//
// Settings k3s reads at process start (the boot drop-in,
// registries.yaml, the feature manifests, and the Go runtime
// environment init hands the process) apply by restarting the k3s
// child process.
//
// Settings a boot reads to reach a decision it never revisits apply
// by staging the document and waiting. The two are which datastore to
// join, and the URL to join through. The machine's next boot reads the
// staged copy, whenever that boot comes and for whatever reason. This
// tier asks for no disruption at all. NextBootApplies below names
// these fields and every reader of them. It also states the one value
// a running machine still holds after such an edit.
//
// Everything else a boot acted on applies by rebooting the machine.
// The running system carries the old value's effects, and only a boot
// undoes them. The address plan is in the node's addresses and routes.
// Storage is in the mounts. The time upstreams are the servers a
// leader's discipline loop queries for the life of the boot.
//
// A reboot works in place of every other tier, because a reboot is a
// k3s restart plus more, and it is also a next boot. So an edit that
// spans tiers is safe at the reboot tier, and classification must
// always err toward it. The one pair that needs its own argument is a
// restart-class field beside a next-boot-class one, which
// RestartApplies below sends to the restart tier.
//
// This file is the classifier for the two middle tiers. The operator
// consults it to determine whether a staged cluster document calls
// for a restart intent, a reboot intent, or no intent at all. Init
// consults RestartApplies before it acts on a restart intent, so the
// two programs can never disagree about what a restart may apply.

import "encoding/json"

// RestartApplies reports whether a k3s restart is enough to move a
// machine from the current spec to the desired one. The two specs
// must differ (no drift needs no disruption at all), and the
// difference must be confined to the restart-class fields, the ones
// k3s reads only at process start, and to the next-boot-class fields
// below them.
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
	// A feature that leaves host state behind is the one exception to
	// the rule that features are restart-class. Netfilter chains,
	// mounts, live storage sessions, and loaded modules outlive the
	// k3s process, so a restart stops the controller and leaves its
	// programming in force with nothing maintaining it. A boot
	// discards all of it. The machine also runs the controller right
	// up to that boot, so there is no interval where the programming
	// stands without the controller that maintains it.
	if RetractionLeavesHostState(&Cluster{Spec: current}, &Cluster{Spec: desired}) {
		return false
	}
	current.Features, desired.Features = nil, nil
	current.Registries, desired.Registries = RegistriesSpec{}, RegistriesSpec{}
	current.Runtime, desired.Runtime = ClusterRuntimeSpec{}, ClusterRuntimeSpec{}
	// The rest of the address plan is reboot-class, because those
	// fields set a machine's node IP and the ranges k3s hands out,
	// both of which a boot has already acted on by the time k3s
	// starts. The NodePort list is the exception: nothing reads it
	// before k3s does, so it is zeroed here with the other
	// restart-class fields rather than with its own section.
	current.Network.NodePortCIDRs, desired.Network.NodePortCIDRs = nil, nil
	// Origin and Endpoint belong to the next-boot tier
	// (NextBootApplies below), so they zero here too. An edit that
	// changes one of them beside a restart-class field converges by a
	// restart, not by a reboot. The restart re-renders the k3s drop-in
	// from the staged document (init/restart.go). That render writes
	// exactly the join keys a boot would write from the same document.
	// A restart re-runs every reader but one, the follower's time
	// source list, and leaving that list to the next boot is what the
	// next-boot tier promises anyway.
	current.Origin, desired.Origin = "", ""
	current.Endpoint, desired.Endpoint = "", ""
	return jsonEqual(current, desired)
}

// NextBootApplies reports whether a machine can adopt the desired
// spec by staging it and waiting for its next boot, with no reboot
// and no k3s restart. The two specs must differ, and the difference
// must be confined to Origin and Endpoint.
//
// These two fields have three readers, and all three run during a
// boot. leaderJoinConfig (init/k3s.go) reads Origin to decide whether
// the founding leader renders cluster-init or joins a datastore that
// already exists, and it reads Endpoint for the URL a joining leader
// points at. k3sBootConfig (init/k3s.go) writes Endpoint into a
// follower's server: key. timeSources (init/time.go) puts the
// endpoint's host at the end of a follower's time sources, as the
// fallback for a leader that declares no address.
//
// Nothing re-reads either field after that. A joined member keeps a
// client-side load balancer that holds every leader's address, so it
// never asks the endpoint for anything again. A running k3s never
// reads the drop-in again.
//
// One value does outlive the boot that read it: the endpoint's host
// stays in a follower's time source list until the next boot. That
// entry is the last resort, behind every leader that declares an
// address inside nodeCIDR. It costs nothing on a fleet whose leaders
// declare their addresses. On a fleet whose leaders declare none, the
// endpoint's host is a follower's only time source, and the follower
// asks the old host until it boots. So keep the old address answering
// until every machine has adopted the edit. This is the one cost of
// the tier. The failure it risks is clock drift on that follower,
// not a lost cluster.
//
// The tier is the same for every machine, whatever its role. A
// follower, a founding leader, and an ordinary leader all read these
// fields only during a boot. No role needs a heavier tier than
// another.
//
// The staged document therefore costs little to hold. The machine
// keeps serving under the document it booted. The next boot, for an
// upgrade or for any other change, reads the staged copy and proves
// it.
//
// The comparison uses the same subtraction over JSON that
// RestartApplies uses, for the same reason. A field this function does
// not name stays in the comparison. So a future ClusterSpec field can
// never fall into this tier by accident.
func NextBootApplies(current, desired ClusterSpec) bool {
	current.Version, desired.Version = "", ""
	current.Releases, desired.Releases = ClusterReleasesSpec{}, ClusterReleasesSpec{}
	if jsonEqual(current, desired) {
		return false
	}
	current.Origin, desired.Origin = "", ""
	current.Endpoint, desired.Endpoint = "", ""
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
