# Cluster time

Milestone 07 — Completed. The leaders sync from NTP upstreams declared
on the Cluster, and they serve time to the rest of the fleet.

Because the leaders serve time, followers need no internet access. The
upstreams are declared, never defaulted. A distribution that ships
pool.ntp.org as a default enrolls every deployment's machines in a
volunteer service without permission.

Followers query every leader directly. Init resolves leader addresses
from the Machine manifests that the image already carries. For a leader
that declares no address, init uses the endpoint's host. There is no
discovery mechanism: every step in the hierarchy comes from an explicit
input.

The client uses a vendored library, beevik/ntp, the same library that
Talos uses. This is the same approach as the DHCP client: use an
established protocol library, and explain the protocol in the comments.
The server on the leaders answers from the machine's own clock and is
written by hand. It uses a 48-byte format in the same family as the GPT
writer.

The client runs before k3s starts, because TLS fails on a skewed clock.
A machine with bad time cannot join the cluster that it must serve.
This milestone comes before multiple leaders, because it needs only the
topology that milestone 6 built. The lab can fake a broken clock with
QEMU's -rtc base=. etcd, which arrives two milestones later, is the
first component in the stack that depends on clock behavior.

1. [x] The precedent, written down before anything uses it: liken has
   two planes and no third. Machine-plane concerns run as goroutines in
   init. Workload-plane software runs under k3s. k3s is the only child
   process that init supervises. Admission to the machine plane is
   strict: a concern belongs in init only when k3s depends on it to
   exist. Anything that the cluster can host for itself belongs in the
   cluster. Time qualifies only because a machine with a skewed clock
   fails TLS and cannot join. A concern without that kind of claim goes
   in-cluster; init does not adopt it. Init gains a small component
   framework: each loop is a `Run(ctx) error`, and a supervisor starts
   it, recovers its panics, and restarts it with backoff. Context
   cancellation stops each loop, and a bounded timeout waits for it, so
   a stuck loop cannot hang a reboot. The loops that init already ran
   informally, the reaper and the reboot watcher, become its first
   registered components. Shutdown runs the dependency stack in
   reverse: stop k3s, cancel the machine plane, unmount, reboot. The
   precedent also defines an exception. A component becomes a child
   process only when it parses untrusted network input, needs fewer
   privileges than PID 1, or must not take the machine down when it
   fails fatally. Such a child process is the same binary re-exec'd,
   busybox multi-call style, so there is still one artifact and one
   program to read. The time responder is the first named candidate for
   promotion, in a later hardening pass, not now. All of this is in
   init's package documentation.
2. [x] The API: `spec.time` on the Cluster holds the upstream list. An
   empty list is legal and means the fleet free-runs. `status.time` on
   the Machine holds synchronized, source, stratum, offset, and
   lastSync, under the console-parity rule: whatever init prints about
   time also reaches the cluster. A free-running fleet is consistent
   with itself, but not with the outside world. That is satisfactory
   until a component compares a certificate's notBefore field with a
   clock that was never set. For this reason, status must show
   free-running as its own state, not as synchronized.
3. [x] The discipline loop is one goroutine on every machine. It
   measures time with beevik/ntp. The comments explain the
   four-timestamp exchange and why symmetric delay cancels out. It
   steps the clock once at boot, before k3s starts. After that it only
   slews the clock (adjtimex) for the life of the machine. A step on a
   running node pulls time out from under lease renewals and etcd
   heartbeats, so the one step must happen before anything depends on
   the clock. Sources differ by role. Leaders ask the declared
   upstreams. Followers ask every leader, resolved from the image's
   Machine manifests, with the endpoint's host as the fallback. Failure
   handling is conservative: init makes a bounded number of attempts at
   boot, then continues to try forever. It never touches the clock on
   bad data, and it never blocks the boot.
4. [x] The responder is a second goroutine, and it runs on leaders
   only. It holds UDP port 123 and answers each 48-byte query with the
   machine's own clock. It is a responder, not a proxy: the leader
   serves the clock that its discipline loop maintains, and it never
   forwards a query upstream. It advertises stratum upstream+1 when
   synced, and the local-clock convention (approximately 10) when
   free-running. A follower can then identify where the time comes from
   and how much to trust it. Followers run no responder. Nothing in the
   design asks a follower for time, and an OS with no shell must not
   open a listener that no client uses. One known problem is deferred
   to milestone 9: when the endpoint becomes a VIP or load balancer for
   HA, UDP port 123 may not reach the leaders, and followers may need
   the leader list instead. k3s registration has the same problem
   there.
5. [x] The RTC: Linux never writes the hardware clock back on its own.
   On other distributions, a shutdown script does this. On liken, init
   does it. Init writes the RTC (RTC_SET_TIME) at two moments: once
   after the first successful sync, so a machine that later loses power
   without a clean shutdown still carries good time into its next boot,
   and once at clean shutdown, so the RTC holds the best final
   estimate.
6. [x] Prove it in the lab: boot a node with QEMU's -rtc base= set
   years wrong. The console reports the skewed clock, then the sync,
   then the step. k3s then joins a cluster that it could not join
   before the step, because with the skewed clock the CA certificates
   were not yet valid. Then `kubectl get machines` reports the follower
   following the leader, and the leader following its upstreams. This
   was proven with `make run-lab RTC=2001-01-01`. node-1 stepped 25.5
   years using Cloudflare. node-2, which booted with its clock reading
   1999, stepped 27 years using node-1's responder. Both steps happened
   before k3s started, and both machines wrote the correction to their
   RTCs. A later node-1 reboot then came up only -574ms off, because
   the written RTC carried real time through. `kubectl get machines -o
   wide` showed both nodes synchronized at sub-millisecond offsets.
