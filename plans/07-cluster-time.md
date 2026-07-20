# Cluster time

Milestone 7 — Done

Cluster time: the leaders sync from NTP upstreams declared on the
Cluster, and they serve time to the rest of the fleet. This means
followers need no internet access at all. The upstreams are
declared, never defaulted, because a distro that ships pool.ntp.org
as a default enrolls every deployment's machines in a volunteer
service without asking permission.

Followers query every leader directly. Init resolves leader
addresses from the Machine manifests that the image already
carries, and it falls back to the endpoint's host for leaders that
declare no address. There is deliberately no discovery mechanism:
every hop in the hierarchy comes from somebody's explicit input.

The client uses a vendored library, beevik/ntp, the same library
that Talos uses. This follows the same approach as the DHCP client:
use an established protocol library, and teach the protocol in the
comments. The server on the leaders answers from the machine's own
clock and is written by hand. It uses a 48-byte format in the same
family as the GPT writer.

The client runs before k3s starts, because TLS fails on a skewed
clock: a machine with bad time cannot join the cluster it is meant
to serve. This milestone deliberately lands ahead of multiple
leaders. It needs only the topology that milestone 6 built. The lab
can fake a broken clock with QEMU's -rtc base=. And etcd, which
arrives two milestones later, is the first component in the stack
that genuinely depends on clock behavior.
1. [x] The precedent, written down before anything uses it: liken has
   two planes and no third. Machine-plane concerns run as goroutines
   in init. Workload-plane software runs under k3s. k3s is the only
   child process that init supervises. Admission to the machine plane
   is strict: a concern belongs in init only when k3s depends on it
   to exist. Anything the cluster can host for itself belongs in the
   cluster. Time qualifies only because a machine with a skewed clock
   fails TLS and cannot join. A concern without that kind of claim
   goes in-cluster; init does not adopt it. Init gains a small
   component framework: each loop is a `Run(ctx) error`, and a
   supervisor starts it, recovers its panics, and restarts it with
   backoff. Context cancellation stops each loop, and a bounded
   timeout awaits it, so a stuck loop cannot hang a reboot. The loops
   that init already ran informally, the reaper and the reboot
   watcher, become its first registered components. Shutdown runs the
   dependency stack in reverse: stop k3s, cancel the machine plane,
   unmount, reboot. The precedent also defines an exception: a
   component is promoted to a child process (the same binary
   re-exec'd, busybox multi-call style, so there is still one
   artifact and one program to read) only when it parses untrusted
   network input, needs fewer privileges than PID 1, or must not
   take the machine down when it fails fatally. The time responder is
   the first named candidate for promotion, in a later hardening
   pass, not now. All of this is documented in init's package
   documentation.
2. [x] The API: `spec.time` on the Cluster holds the upstream list. An
   empty list is legal and means the fleet free-runs. `status.time`
   on the Machine holds synchronized, source, stratum, offset, and
   lastSync, under the console-parity rule: whatever init prints
   about time also reaches the cluster. A free-running fleet agrees
   with itself but not with the outside world. This holds true until
   something checks a certificate's notBefore field against a clock
   that was never set. For this reason, status must show free-running
   as its own state, not report it as synchronized.
3. [x] The discipline loop is one goroutine on every machine. It
   measures time with beevik/ntp (the comments explain the
   four-timestamp exchange and why symmetric delay cancels out). It
   steps the clock once at boot, before k3s starts, and after that it
   only ever slews the clock (adjtimex) for the life of the machine.
   Stepping a running node pulls time out from under lease renewals
   and etcd heartbeats, so the one step must happen before anything
   depends on the clock. Sources differ by role. Leaders ask the
   declared upstreams. Followers ask every leader, resolved from the
   image's Machine manifests, with the endpoint's host as the
   fallback. Failure handling is conservative: init makes a bounded
   number of attempts at boot, then keeps trying forever. It never
   touches the clock on bad data, and it never blocks the boot.
4. [x] The responder is a second goroutine, and it runs on leaders
   only. It holds UDP port 123 and answers each 48-byte query with
   the machine's own clock. It is a responder, not a proxy: the
   leader serves the clock that its discipline loop maintains, and it
   never forwards a query upstream. It advertises stratum upstream+1
   when synced, and the local-clock convention (~10) when
   free-running, so followers can always tell where the time comes
   from and how much to trust it. Followers run no responder. Nothing
   in the design ever asks a follower for time, and a shell-less OS
   should have no listener without a caller for it. One known problem
   is deferred to milestone 9: when the endpoint becomes a VIP or
   load balancer for HA, UDP port 123 may not reach the leaders, and
   followers may need the leader list instead. k3s registration faces
   the same problem there.
5. [x] The RTC: Linux never writes the hardware clock back on its own.
   On other distros, a shutdown script does this. On liken, init does
   it. Init writes the RTC (RTC_SET_TIME) at exactly two moments:
   once after the first successful sync, so a machine that later
   loses power without a clean shutdown still carries decent time
   into its next boot, and once at clean shutdown, so the RTC holds
   the best final estimate.
6. [x] Prove it in the lab: boot a node with QEMU's -rtc base= set
   years wrong. Watch the console report the skewed clock, then the
   sync, then the step. Watch k3s join a cluster it could not have
   joined before the step, because to the skewed clock, the CA's
   certificates did not exist yet. Then check that `kubectl get
   machines` reports the follower following the leader, and the
   leader following its upstreams. This was proven with `make
   run-lab RTC=2001-01-01`: node-1 stepped 25.5 years using
   Cloudflare, and node-2, which booted with its clock reading 1999,
   stepped 27 years using node-1's responder. Both steps happened
   before k3s started, and both machines wrote the correction to
   their RTCs. A later node-1 reboot then came up only -574ms off,
   because the written RTC carried real time through. `kubectl get
   machines -o wide` showed both nodes synchronized at
   sub-millisecond offsets.
</content>
