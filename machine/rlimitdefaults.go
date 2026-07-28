package machine

// The resource limits every liken machine holds.
//
// A stock distribution does not run k3s on the kernel's own limits.
// k3s's packaged systemd unit sets three of them, and systemd applies
// them when it forks the service:
//
//	LimitNOFILE=1048576
//	LimitNPROC=infinity
//	LimitCORE=infinity
//
// liken runs no systemd, so nothing applies those directives, and the
// whole machine keeps the kernel's defaults for PID 1: 1024 open files
// soft and 4096 hard. Limits pass down through fork and exec, so that
// pair is what k3s gets, what containerd gets, and what every
// container gets. A cluster that moves a workload from a systemd
// distribution onto liken loses the raised ceiling at the moment the
// workload lands. This table is liken's answer, and init applies it to
// itself before it starts anything (init/rlimits.go).
//
// This is the same class of gap as the sysctls next door in
// sysctldefaults.go, and it converges differently. A sysctl is a file
// the kernel re-reads, so the operator reconciles one live. A resource
// limit is fixed when a process forks, so nothing can raise the
// ceiling of a k3s that is already running. An edit to spec.rlimits
// stages and waits for a reboot, the way storage and network do.
//
// A limit earns a place here when every liken machine should hold it
// whatever it runs. A limit that depends on the workload belongs in
// spec.rlimits, where the person who knows the workload can set it.
//
// One note on how these values reach a process. Go's runtime raises
// its own soft RLIMIT_NOFILE to just under the hard limit at startup,
// which is why a Go program on an untuned liken machine reads 4095 and
// not 1024. That adjustment applies to the Go process only. It does
// nothing for a container, and it is not a substitute for this table.
var OSRlimits = map[string]string{
	// The number of file descriptors one process may hold open. Both
	// halves are set, which is what LimitNOFILE=1048576 means in a
	// unit file, so a program that never adjusts its own soft limit
	// gets the ceiling anyway.
	//
	// 1048576 is the kernel's own fs.nr_open default, and nr_open is
	// the ceiling on what any RLIMIT_NOFILE may be set to. Asking for
	// more would fail rather than clamp. This value is therefore the
	// largest that works without also raising a sysctl, and it is
	// exactly what k3s's unit asks for.
	//
	// A soft limit this high has two costs, and they are the reason
	// some distributions leave the soft limit at 1024. glibc's
	// select(2) cannot represent a descriptor above 1023, so a program
	// that mixes a high descriptor with select corrupts memory rather
	// than failing cleanly. And a program that closes every descriptor
	// from 0 to the soft limit before it execs does a million syscalls
	// each time it spawns, instead of a thousand. Both are old
	// patterns, both are rare in a container image built this decade,
	// and parity with the unit liken replaces is worth more here than
	// protection from them. A deployment that hosts such a program can
	// lower its own soft limit in spec.rlimits without giving up the
	// hard ceiling.
	"nofile": "1048576",

	// The number of processes one real user ID may have. The kernel
	// derives its default from the machine's memory, so a small
	// machine gets a small ceiling, and a machine running many
	// short-lived containers reaches it. Root is exempt from the
	// check, which hides the problem on a machine whose containers all
	// run as root and exposes it on one whose containers do not: every
	// pod running as the same non-root user draws from one pool.
	//
	// The unit's own comment explains why the value is infinity rather
	// than a larger number: "Having non-zero Limit*s causes performance
	// problems due to accounting overhead in the kernel. We recommend
	// using cgroups to do container-local accounting." The point is to
	// stop the kernel counting, not to raise a ceiling. Kubernetes does
	// this accounting per pod, in cgroups, which is where a limit on a
	// workload belongs.
	"nproc": "infinity",

	// RLIMIT_CORE is deliberately absent, and this is liken's one
	// deviation from the unit.
	//
	// The kernel already grants a hard limit of infinity, so
	// LimitCORE=infinity changes only the soft limit, from 0 to
	// unlimited. That is safe on a systemd machine because
	// kernel.core_pattern there pipes a dump into systemd-coredump,
	// which bounds the size, compresses it, and expires it. liken has
	// no such collector, so core_pattern is the kernel's literal
	// "core": a crashing process writes a file the size of its
	// resident memory into its working directory. For a container that
	// is its own writable layer, which lives on clusterState, the
	// filesystem that also holds etcd. logrotate.go caps the k3s log
	// against exactly this hazard, and a gigabyte-scale core dump
	// beside the datastore is the larger version of it.
	//
	// Nothing is blocked by leaving this out. The hard limit stays
	// infinity, so a process that wants a core can raise its own soft
	// limit, and a deployment that wants cores by default can set
	// "core" in spec.rlimits. Turning them on for a whole fleet is a
	// decision that needs somewhere bounded to put the output, and
	// that is a feature rather than a default.
}
