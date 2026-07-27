package machine

// The kernel settings every liken machine holds.
//
// A stock Linux distribution does not run on the kernel's own
// defaults. It ships a set of sysctl files, usually under
// /usr/lib/sysctl.d, and systemd applies them at boot. liken runs no
// systemd, so nothing applies those files and the machine keeps the
// kernel's defaults for every one of them. Some of those defaults are
// decades old and were chosen for a single-user machine with one
// network interface. This table is liken's answer: the values a server
// should hold, applied by liken itself.
//
// Two programs apply this table, on two cadences. Init applies it at
// boot, before k3s starts, so every value is set by the time anything
// reads it. The liken operator applies it again on every reconcile
// pass, so a value that something else changed returns within one
// pass. Both apply this table first and spec.sysctls second, so a name
// in a Machine's spec always wins: the last write is the one that
// holds. status.sysctls reports the union of both, read back from the
// kernel, so an operator sees every parameter liken sets and its
// actual value in one place.
//
// A parameter earns a place here when every liken machine should hold
// it whatever it runs. A value that depends on the workload belongs in
// spec.sysctls instead, where the person who knows the workload can
// set it.
//
// Two rules for anyone adding an entry.
//
// First, the three-way scope. A net.ipv4.conf parameter exists three
// times: once under all, once under default, and once per interface.
// The kernel copies default into an interface when that interface
// registers, and interfaces register when their driver loads, which
// happens before any of these values are applied. So default reaches
// the interfaces that CNI creates later and never reaches the
// machine's own network card. Reaching that card takes the all entry,
// and how all combines with the per-interface value differs by
// parameter: rp_filter takes the larger of the two, accept_source_route
// requires both, and promote_secondaries takes either. An entry under
// default alone is usually a mistake. sysctldefaults_test.go checks
// for the missing twin.
//
// Second, no IPv6 forwarding entry. Writing
// net.ipv6.conf.all.forwarding stops the machine accepting router
// advertisements, and at this point in boot that would land before any
// interface comes up.
//
// One extension this table does not have: every entry is advisory. A
// value that fails to apply leaves the machine Ready and reports
// DefaultsIncomplete, because this table ships with the release, so a
// bad entry would otherwise take a whole fleet out at once. If some
// entry ever has to be load-bearing, the shape for that is a named set
// of required parameters, not a second table.
var OSSysctls = map[string]string{
	// The gap between the memory watermarks that start and stop
	// kswapd, the kernel's background reclaimer, in units of 0.01% of
	// the machine's memory. At the default gap of 0.1%, kswapd on a
	// small machine runs rarely, only once allocation is close to
	// failing. When that happens, many allocations stall in direct
	// reclaim at once. This is the worst possible moment for a stall,
	// because it produces the kind of latency spike that makes k3s's
	// datastore miss its IO deadlines under load. A gap of one percent
	// makes reclaim run steadily in the background instead: pages that
	// the boot touched once are freed while the machine is calm, so a
	// burst of activity finds free memory waiting. The cost is a
	// somewhat smaller page cache and a little more background CPU
	// use. A gap of two percent, twice this value, kept kswapd visibly
	// busy even on a well-filled machine, reclaiming pages that
	// nothing needed yet.
	"vm.watermark_scale_factor": "100",

	// The two inotify limits raise the kernel's defaults so that init
	// and the operator always have watches to spend. inotify limits
	// are per-uid, and every inotify user on the machine runs as root
	// and draws from the one root quota: kubelet's watches on config
	// and secrets, containerd's watches, k3s's own watches, init's
	// watch on the operator's intent directory, and the operator's
	// watch on the facts tree. The ceiling on instances rises from the
	// kernel's 128 to the value a Kubernetes node commonly runs with,
	// because kubelet and containerd alone can open dozens and the
	// machine plane needs two more that never contend with them. The
	// ceiling on watched inodes rises for the same reason.
	//
	// These numbers are caps, not allocations. An unused cap costs no
	// memory. A single watch costs about a kilobyte of unswappable
	// kernel memory, but only once something registers it, so raising
	// the ceiling on a machine that watches little changes nothing
	// about its memory use. The cap's job is to never be the thing
	// that fails when a legitimate watcher asks for a watch, while it
	// still stands as a backstop against a runaway watcher.
	//
	// The consequence matters more than the numbers. With this
	// headroom guaranteed at boot, a watch that fails to start is a
	// real fault, not an expected shortage to paper over. This is why
	// init has no polling fallback for its intent watch: a failed
	// watch surfaces on the console and the component plane retries
	// it, rather than degrading to a poll that hides the fault.
	"fs.inotify.max_user_instances": "8192",
	"fs.inotify.max_user_watches":   "524288",

	// A node that cannot forward packets cannot route pod traffic.
	// Every Kubernetes networking layer assumes this old parameter is
	// on. It belongs in this table rather than in the code that
	// prepares the machine for k3s, because a value liken sets should
	// be a value an operator can see in status and override in a
	// spec, and a value written straight to /proc is neither.
	"net.ipv4.ip_forward": "1",

	// The queueing discipline a network interface gets when it comes
	// up. The kernel's default, pfifo_fast, is one queue with three
	// priority bands and no queue management: a bulk transfer fills
	// it, and every later packet waits out the whole backlog. fq_codel
	// gives each flow a queue of its own and drops from the head of a
	// queue that stays full, so a large transfer cannot delay a small
	// request. This is the value every systemd distribution sets, and
	// the reason bufferbloat has a name.
	//
	// The scheduler must be registered before anything can name it, so
	// sch_fq_codel is in the image's module list and loads earlier in
	// boot than this table applies. Init applies this table before it
	// brings interfaces up, which is what puts fq_codel on the
	// machine's own network card. The operator's later writes reach
	// only interfaces created after them, because the kernel reads
	// this parameter when an interface comes up and not again.
	"net.core.default_qdisc": "fq_codel",

	// Whether a TCP connection that has gone idle resets its
	// congestion window and starts slow again. The kernel does this by
	// default, on the reasoning that the path may have changed while
	// the connection was quiet. A Kubernetes machine's own traffic is
	// the worst case for it: an API server watch or a gRPC stream
	// stays open for hours and carries a burst, a silence, and another
	// burst, so every burst after a pause pays the slow start again on
	// a path that did not change. Turning it off is standard practice
	// on a server that holds long connections.
	"net.ipv4.tcp_slow_start_after_idle": "0",

	// Whether TCP probes for the largest packet a path will carry.
	// Some paths drop a packet that needs fragmenting without sending
	// back the message that says so, and a connection across one of
	// them stalls rather than fails. flannel's VXLAN encapsulation
	// makes every pod-to-pod path smaller than the interface it
	// travels on, which is exactly the shape that produces this. This
	// value starts probing once a connection looks stalled, which
	// costs nothing on a path that works.
	"net.ipv4.tcp_mtu_probing": "1",

	// The queue depth of a Unix domain datagram socket. The kernel's
	// default of 10 dates from a time when this was a rare kind of
	// socket, and every systemd distribution raises it, because a
	// sender that fills the queue blocks. liken has no systemd to
	// raise it.
	"net.unix.max_dgram_qlen": "512",

	// The ceiling on process IDs before the kernel wraps around and
	// reuses the low numbers. The kernel's default of 32768 predates
	// the container, and a machine running many short-lived
	// containers reaches it. This value is the largest a 64-bit kernel
	// accepts, and every stock distribution sets it. A 32-bit kernel
	// caps at 32768 and would refuse this value, which matters only if
	// liken ever builds for one.
	//
	// This is a ceiling, not an allocation. It costs nothing until the
	// processes exist.
	"kernel.pid_max": "4194304",

	// The number of distinct memory mappings one process may hold.
	// Every mmap, every shared library, and every guard page costs
	// one. A container runtime, a JVM, or anything that maps many
	// small files reaches the kernel's default of 65530, and the
	// failure is confusing: mmap refuses with an out-of-memory error
	// on a machine with free memory.
	//
	// No distribution sets this. The justification is the workload
	// liken exists to run, not parity with anything else: before this
	// value moved here, every liken deployment wrote the same number
	// into its own spec.sysctls by hand.
	"vm.max_map_count": "262144",

	// Four restrictions on following a link or opening a file in a
	// directory that anyone may write to, which on this machine means
	// /tmp. Without them, one user can leave a symbolic link, a hard
	// link to a file they cannot read, or a file of their own where a
	// program expects to create its own, and a program that follows it
	// acts on the wrong file. The kernel leaves all four off for
	// compatibility with programs written before the checks existed.
	// Every distribution turns them on.
	//
	// Everything on a liken host runs as root, and root opening a file
	// root owns never trips these checks, so the exposure they close
	// is narrow: a pod running as a normal user that shares a
	// directory with the host. The value for regular files is the
	// stricter of the two the kernel offers, which extends the check
	// to a world-writable directory that is not marked sticky.
	"fs.protected_symlinks":  "1",
	"fs.protected_hardlinks": "1",
	"fs.protected_regular":   "2",
	"fs.protected_fifos":     "1",

	// Whether an unprivileged reader sees real kernel addresses in
	// /proc and other interfaces. Real addresses tell an exploit where
	// the kernel loaded, which is the one thing address randomisation
	// exists to hide. This value replaces them with zeros for a reader
	// without the capability to see them. Every distribution sets it.
	"kernel.kptr_restrict": "1",

	// Whether a kernel bug that did not kill the machine outright
	// becomes a panic. An oops leaves the kernel running in a state it
	// does not understand, and a machine in that state can corrupt
	// what it writes. liken has somewhere better to go: a panic
	// reboots after ten seconds through the panic= parameter on the
	// command line, the firmware boots the slot it already proved, and
	// the pstore journal carries the log across the reboot so the next
	// boot reports the crash. Continuing past an oops throws all of
	// that away.
	//
	// kubelet writes this same value once k3s starts, so what liken
	// adds is the window before that and the machine that never
	// reaches k3s at all.
	"kernel.panic_on_oops": "1",

	// Whether the machine checks that a packet arrived on the
	// interface it would use to reply, and drops it if not. liken
	// does no such check at all today. The loose form, this value,
	// drops a packet only when no interface at all would route back to
	// its source, which catches a forged source address without
	// assuming traffic is symmetric.
	//
	// The strict form, one lower, drops a packet that arrived on any
	// interface other than the best return path, and it breaks
	// Kubernetes: pod traffic routinely arrives asymmetrically across
	// an overlay. Because the kernel takes the larger of the all value
	// and the per-interface value, setting all to the loose form
	// prevents the strict form anywhere on the machine, including on
	// an interface a CNI configures for itself. That is a deliberate
	// commitment and not a neutral default: liken chooses working pod
	// networking over the stricter check.
	"net.ipv4.conf.all.rp_filter":     "2",
	"net.ipv4.conf.default.rp_filter": "2",

	// Whether the machine honours a packet that carries its own route.
	// Source routing lets a sender choose the path its reply takes,
	// which is a way to reach an address that ordinary routing
	// protects. Nothing legitimate on a server uses it.
	//
	// The kernel requires both the all value and the per-interface
	// value to be on, so the all entry is what turns this off
	// everywhere. Distributions set only the default entry, which
	// reaches interfaces created later and leaves the machine's own
	// network card as the kernel left it.
	"net.ipv4.conf.all.accept_source_route":     "0",
	"net.ipv4.conf.default.accept_source_route": "0",

	// Whether removing an interface's primary address promotes a
	// secondary address to take its place. The kernel's default is to
	// delete every secondary address on that interface instead, so a
	// machine that renews a lease or renumbers loses addresses it
	// still holds.
	//
	// The kernel takes either the all value or the per-interface
	// value, so the all entry is what reaches the machine's own
	// network card. That card is the interface that has secondary
	// addresses in the first place, which makes the default entry on
	// its own worth nothing here.
	"net.ipv4.conf.all.promote_secondaries":     "1",
	"net.ipv4.conf.default.promote_secondaries": "1",
}
