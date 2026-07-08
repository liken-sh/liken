# Cluster time

Milestone 7 — Done

Cluster time: the servers sync from NTP upstreams declared on
the Cluster and serve time to the rest of the fleet, so
followers need no internet access at all. The upstreams are
declared, never defaulted, because a distro that ships
pool.ntp.org as a default enrolls every deployment's machines
in a volunteer service without asking. Followers query every
leader directly, resolved from the Machine manifests the image
already carries, with the endpoint's host as the fallback for
leaders that declare no address. There is deliberately no
discovery mechanism; every hop in the hierarchy is somebody's
explicit input. The client uses a vendored library (beevik/ntp,
the one Talos uses), following the same approach as the DHCP
client: use an established protocol library and teach the
protocol in the comments. The server on the leaders, which
answers from the machine's own clock, is written by hand; it is
a 48-byte format in the same family as the GPT writer. The
client runs before k3s starts, because TLS fails on a skewed
clock: a machine with bad time can't even join the cluster it
is meant to serve. This deliberately lands ahead of multiple
leaders: it needs only the topology milestone 6 built, the lab
can fake a broken clock with QEMU's -rtc base=, and etcd,
coming two milestones later, is the first component in the
stack that genuinely depends on clock behavior.
1. [x] The precedent, written down before it's built on: liken has
   two planes and no third. Machine-plane concerns are
   goroutines in init; workload-plane software runs under k3s;
   k3s is the only child process init supervises. Admission to
   the machine plane is strict: a concern belongs in init only
   when k3s depends on it to exist. Anything the cluster could
   host for itself belongs in the cluster. Time qualifies only
   because a machine with a skewed clock fails TLS and can't
   join; a concern without that kind of claim gets pushed
   in-cluster, not adopted by init. Init grows a small
   component framework: each loop is a `Run(ctx) error`,
   started by a supervisor that recovers panics and restarts
   with backoff, stopped by context cancellation, and awaited
   with a bounded timeout so a stuck loop can't hang a reboot.
   The loops init already runs informally (reaper, reboot
   watcher) become its first registered components. Shutdown
   runs the dependency stack in reverse: stop k3s, cancel the
   machine plane, unmount, reboot. The escape hatch is part of
   the precedent: a component is promoted to a child process
   (the same binary re-exec'd, busybox multi-call style, so
   there is still one artifact and one program to read) only
   when it parses untrusted network input, needs fewer
   privileges than PID 1, or must not take the machine down
   when it fatals. The time responder is the first named
   candidate, to be promoted in a hardening pass, not now. All
   of this lands in init's package documentation.
2. [x] The API: `spec.time` on the Cluster (the upstream list;
   empty is legal and means the fleet free-runs), and
   `status.time` on the Machine (synchronized, source, stratum,
   offset, lastSync) under the console-parity rule: whatever
   init prints about time also reaches the cluster. A
   free-running fleet agrees with itself but not with the
   outside world. That holds up until something checks a
   certificate's notBefore against a clock that was never set,
   so status must make free-running visible rather than report
   it as synchronized.
3. [x] The discipline loop, one goroutine on every machine:
   measure with beevik/ntp (the four-timestamp exchange, and
   why symmetric delay cancels, belongs in the comments), step
   the clock once at boot before k3s starts, then only ever
   slew (adjtimex) for the life of the machine. Stepping a
   running node pulls time out from under lease renewals and
   etcd heartbeats, so the one step happens before anything is
   depending on the clock. Sources differ by role: leaders ask
   the declared upstreams; followers ask every leader, resolved
   from the image's Machine manifests, with the endpoint's
   host as the fallback. Failure handling is conservative:
   bounded attempts at boot, then keep trying forever; never
   touch the clock on bad data, and never block the boot.
4. [x] The responder, a second goroutine on leaders only: hold UDP
   :123 and answer each 48-byte query from the machine's own
   clock. It is a responder, not a proxy: the leader serves the
   clock its discipline loop maintains and never forwards a
   query upstream. It advertises stratum upstream+1 when synced
   and the local-clock convention (~10) when free-running, so
   followers can always tell where the time comes from and how
   much to trust it. Followers run no responder: nothing in the
   design ever asks a follower for time, and a shell-less OS
   should have no listener without a caller. One known wrinkle
   is deferred to milestone 9: when the endpoint becomes a VIP
   or load balancer for HA, UDP 123 may not come along, and
   followers may want the leader list instead. k3s registration
   faces the same question there.
5. [x] The RTC: Linux never writes the hardware clock back on its
   own. On other distros that is a shutdown script's job, so
   here it is init's. Write it (RTC_SET_TIME) at exactly two
   moments: once after the first successful sync, so even a
   machine that later loses power without a clean shutdown
   carries decent time into its next boot, and once at clean
   shutdown, so the RTC holds the best final estimate.
6. [x] Prove it in the lab: boot a node with QEMU's -rtc base= set
   years wrong, watch the console report the skewed clock, the
   sync, and the step, and watch k3s join a cluster it could
   not have joined before the step, because the CA's
   certificates would not exist yet. Then check `kubectl get
   machines` reports the follower following the leader and
   the leader following its upstreams. Proven with `make run-lab
   RTC=2001-01-01`: node-1 stepped 25.5 years using Cloudflare,
   and node-2, which booted with its clock reading 1999,
   stepped 27 years using node-1's responder. Both steps
   happened before k3s started, and both machines wrote the
   correction to their RTCs. A node-1 reboot then came up only
   -574ms off, because the written RTC carried real time
   through, and `kubectl get machines -o wide` showed both
   nodes synchronized at sub-millisecond offsets.
