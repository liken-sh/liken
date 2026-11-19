package machine

// OSSysctls is the table of kernel settings that every liken machine
// applies, whatever it runs. A setting that depends on the workload belongs in
// the Machine's spec.sysctls.
//
// A stock distribution sets these from files under /usr/lib/sysctl.d,
// which systemd applies at boot. liken runs no systemd, so without this
// table a machine keeps the kernel's defaults.
//
// init applies the table before it brings up the network and before k3s
// starts. machine-operator applies it again on every reconcile pass, so
// a value that something else changed is written back. Both apply
// spec.sysctls after the table, so the spec overrides an entry.
//
// A net.ipv4.conf parameter exists under all, under default, and for
// each interface. The kernel copies default into an interface when the
// interface registers. The network card registers when its driver
// loads, before init applies this table, so a default entry reaches only
// interfaces that CNI creates later, and the network card needs the all
// entry too. TestEveryDefaultEntryHasAnAllEntry fails on a default entry
// without it.
//
// Do not add net.ipv6.conf.all.forwarding. The kernel copies it to every
// interface, and an interface that forwards ignores router
// advertisements unless its accept_ra is 2, so the machine would lose
// the addresses and routes that router advertisements configure.
//
// When an entry fails to apply, the machine stays Ready, and the
// SysctlsApplied condition stays True with the reason
// DefaultsIncomplete. Every machine of a release applies the same
// table, so a bad entry that made a machine Degraded would make the
// whole fleet Degraded at once.
var OSSysctls = map[string]string{
	// kswapd, the background reclaimer, starts when free memory falls
	// below a low watermark and stops at a high one. This sets the gap
	// between them, in units of 0.01% of memory. With the default gap of
	// 0.1%, kswapd on a small machine starts only when allocations are
	// about to fail, and many allocations then stall in direct reclaim at
	// once, which can make the k3s datastore miss its IO deadlines. With
	// a gap of 1%, kswapd starts earlier, so a burst finds free memory,
	// at the cost of a slightly smaller page cache.
	"vm.watermark_scale_factor": "100",

	// Inotify limits are per uid. Everything on a liken machine runs as
	// root, so kubelet, containerd, k3s, init, and machine-operator share
	// one quota, and the kernel default of 128 instances is too few for
	// kubelet and containerd alone. A watch uses about a kilobyte of
	// kernel memory only while it exists, so a high limit costs nothing.
	"fs.inotify.max_user_instances": "8192",
	"fs.inotify.max_user_watches":   "524288",

	// Kubernetes networking needs the node to forward packets. The value
	// is in this table, and not written by the code that starts k3s, so
	// that status.sysctls shows it and spec.sysctls can override it.
	"net.ipv4.ip_forward": "1",

	// The kernel default, pfifo_fast, has no queue management, so a bulk
	// transfer fills the queue and delays every later packet. fq_codel
	// gives each flow its own queue and drops from the head of a queue
	// that stays full.
	//
	// The kernel reads this value only when an interface comes up, and
	// accepts only a scheduler that is registered. So
	// image/etc/liken/modules.conf loads sch_fq_codel before init applies
	// this table, and init applies it before it brings up the network
	// card. A later write by machine-operator affects only interfaces
	// that come up after it.
	"net.core.default_qdisc": "fq_codel",

	// By default the kernel restarts slow start on a TCP connection that
	// was idle, because the path may have changed. An API server watch or
	// a gRPC stream stays open for hours and sends bursts between
	// silences, so with the default every burst starts slow.
	"net.ipv4.tcp_slow_start_after_idle": "0",

	// Some paths drop a packet that is too large and send no ICMP
	// message back, so a TCP connection across them stalls instead of
	// failing. flannel's VXLAN encapsulation adds a header to each
	// packet between pods on different nodes, so the largest packet that
	// such a path carries is smaller than the MTU of the host's network
	// interface. With 1, TCP probes for a smaller packet size only after
	// it detects such a stall.
	"net.ipv4.tcp_mtu_probing": "1",

	// The queue length of a Unix domain datagram socket. A sender blocks
	// when the queue is full, and the kernel default of 10 is low. Stock
	// distributions raise it.
	"net.unix.max_dgram_qlen": "512",

	// The highest process ID before the kernel wraps around. The default
	// of 32768 is low for a machine that runs many short-lived
	// containers. 4194304 is the largest value a 64-bit kernel accepts,
	// and the value stock distributions set.
	"kernel.pid_max": "4194304",

	// The number of memory mappings one process may have. Every mmap,
	// shared library, and guard page uses one. A container runtime, a
	// JVM, or a program that maps many small files can reach the default
	// of 65530, and then mmap fails with ENOMEM on a machine that has
	// free memory. Fedora, Arch, and Ubuntu raise it to 1048576.
	"vm.max_map_count": "262144",

	// Protections against a file that one user plants for another user's
	// program to open, mostly in a world-writable sticky directory such
	// as /tmp: a symbolic link, a hard link to a file the user cannot
	// write, a FIFO, or a regular file. The kernel leaves them off so
	// that old programs keep working, and stock distributions turn them
	// on. Everything on a liken host runs as root, so they matter for a
	// pod that runs as a non-root user and shares a directory with the
	// host. The value 2 for fs.protected_regular also applies the check
	// in group-writable sticky directories.
	"fs.protected_symlinks":  "1",
	"fs.protected_hardlinks": "1",
	"fs.protected_regular":   "2",
	"fs.protected_fifos":     "1",

	// Kernel addresses in /proc tell an exploit where the kernel loaded,
	// which address randomization hides. With 1, the kernel prints zeros
	// to a reader without CAP_SYSLOG. Stock distributions set it.
	"kernel.kptr_restrict": "1",

	// After an oops the kernel keeps running in an unknown state and can
	// corrupt what it writes. A panic is better on a liken machine: the
	// panic=10 kernel argument reboots ten seconds later, and pstore keeps
	// the kernel log across the reboot, so the next boot reports the
	// crash. When the panic happens in the trial boot of a new release,
	// the one-shot trial is already used, so the firmware boots the
	// proven slot (init/proving.go).
	//
	// kubelet also sets this when k3s starts, so this entry covers the
	// time before that, and a machine that never starts k3s.
	"kernel.panic_on_oops": "1",

	// By default the lockup detectors only print a warning, and a hung
	// machine stays hung until someone cuts its power. A soft lockup is a
	// CPU that stays in kernel mode for more than twice
	// kernel.watchdog_thresh, 20 seconds by default. A hard lockup is a
	// CPU that runs with interrupts off. A panic is better for both, for
	// the reason at kernel.panic_on_oops.
	//
	// kernel.hung_task_panic stays off, because a task that waits on a
	// slow iSCSI or NFS server trips it, and a storage outage would then
	// reboot every machine that mounts from that server.
	"kernel.softlockup_panic": "1",
	"kernel.hardlockup_panic": "1",

	// Reverse path filtering drops a packet whose source address has no
	// route back. The strict mode, 1, also drops a packet that arrives on
	// an interface other than the one the route back uses. Pod traffic
	// across an overlay often arrives that way, so strict mode breaks
	// Kubernetes. The loose mode, 2, still drops a forged source address
	// that no interface routes to. The kernel uses the larger of the all
	// value and the interface's value, so all=2 also turns strict mode
	// into loose mode on an interface that a CNI sets to 1.
	"net.ipv4.conf.all.rp_filter":     "2",
	"net.ipv4.conf.default.rp_filter": "2",

	// A source-routed packet names the path that it and its reply take,
	// which can reach an address that normal routing protects. Nothing
	// legitimate on a server uses it. The kernel accepts source routes
	// only when both the all value and the interface's value are on, so
	// all=0 turns it off everywhere.
	"net.ipv4.conf.all.accept_source_route":     "0",
	"net.ipv4.conf.default.accept_source_route": "0",

	// By default, when an interface's primary address is removed, the
	// kernel deletes its secondary addresses too, so a machine that
	// renews a lease or renumbers loses addresses it still uses. With 1,
	// the kernel promotes a secondary address instead. It promotes when
	// either the all value or the interface's value is on.
	"net.ipv4.conf.all.promote_secondaries":     "1",
	"net.ipv4.conf.default.promote_secondaries": "1",
}
