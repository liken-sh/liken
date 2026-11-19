package machine

// OSRlimits are the resource limits that every liken machine sets,
// whatever it runs. init applies them to itself before it starts k3s
// (init/rlimits.go), and child processes inherit them through fork
// and exec, so k3s, containerd, and every container get them.
//
// k3s's packaged systemd unit sets three limits, and systemd applies
// them when it starts the service:
//
//	LimitNOFILE=1048576
//	LimitNPROC=infinity
//	LimitCORE=infinity
//
// liken has no systemd, so without this table PID 1 keeps the kernel
// defaults of 1024 open files (soft) and 4096 (hard), and a workload
// that moves to liken from a distribution with systemd gets a lower
// file limit. So this table sets the limits that the unit sets,
// except core.
//
// A process inherits its limits when it starts, so unlike the sysctls
// in sysctldefaults.go, a change does not reach a running k3s. An edit
// to spec.rlimits needs a reboot, as storage and network do.
//
// A limit belongs in this table when every machine should have it. A
// limit that depends on the workload belongs in spec.rlimits.
//
// Go's runtime raises its own soft RLIMIT_NOFILE to the hard limit at
// startup, so a Go program on a machine without this table reads 4096
// and not 1024. That applies to the Go process only. It does not
// affect containers.
var OSRlimits = map[string]string{
	// The number of file descriptors one process may have open. The
	// value sets both the soft and the hard limit, as
	// LimitNOFILE=1048576 does, so a program that never raises its own
	// soft limit still gets the full limit.
	//
	// 1048576 is the kernel's default fs.nr_open, the maximum that
	// RLIMIT_NOFILE accepts. A larger value fails and is not reduced
	// to the maximum. So this is the largest value that needs no sysctl
	// change.
	//
	// A high soft limit has two costs, which is why some distributions
	// keep it at 1024. glibc's select(2) cannot use a descriptor above
	// 1023, so a program that calls select with a higher descriptor
	// corrupts memory. A program that closes every descriptor up to the
	// soft limit before it execs makes a million syscalls each time in
	// place of a thousand. liken matches the k3s unit anyway, and a
	// workload that needs a low soft limit can set one in spec.rlimits
	// and keep the high hard limit.
	"nofile": "1048576",

	// The number of processes one real user ID may have. The kernel
	// computes its default from the machine's memory, so a small
	// machine that runs many short-lived containers can reach it. The
	// limit does not apply to root, so it fails only containers that
	// run as a non-root user, and every pod with the same user ID
	// counts against the same limit.
	//
	// The value is infinity, not a larger number, for the reason that
	// the unit's own comment gives: "Having non-zero Limit*s causes
	// performance problems due to accounting overhead in the kernel.
	// We recommend using cgroups to do container-local accounting."
	// The kubelet can limit the processes of each pod through the pod's
	// cgroup, with its podPidsLimit setting.
	"nproc": "infinity",

	// The table leaves out "core", unlike the unit's
	// LimitCORE=infinity.
	//
	// The kernel's default hard core limit is already infinity, so the
	// unit only raises the soft limit from 0 to unlimited. A systemd
	// machine can do that because kernel.core_pattern pipes each dump
	// to systemd-coredump, which limits, compresses, and deletes the
	// dumps. liken has no such collector, so core_pattern stays "core"
	// and a crashing process writes a file as large as its resident
	// memory into its working directory. For a container, that is its
	// writable layer on clusterState, the filesystem that also has
	// etcd's data. init/logrotate.go limits the size of the k3s log for
	// the same reason.
	//
	// The hard limit stays infinity, so a process can raise its own
	// soft limit to get a core dump, and a Machine can set "core" in
	// spec.rlimits.
}
