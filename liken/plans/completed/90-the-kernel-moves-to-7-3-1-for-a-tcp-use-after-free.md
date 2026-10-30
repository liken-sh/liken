# 90. The kernel moves to 7.3.1 for a TCP use-after-free

Milestone 90. Recorded as an open problem on 2026-10-30, and built,
drilled, and released on the same day as release `2026.10.30-001`
([As built](#as-built)). `liken` set no stopgap, and
`net.mptcp.enabled` keeps the kernel's default.

The text below is the open problem as it was written before the bump.

`kernel/VERSION` pins `7.2.6`, and that
kernel has CVE-2026-98432, a use-after-free in the IPv4 TCP handshake.
A peer that can open a TCP connection to any IPv4 listener on a
machine can trigger it, with no credentials. The fix is in `7.2.10`
and in `7.3`. Canonical's mainline archive has no amd64 build of any
`7.2.y` release from `7.2.7` to `7.2.14`, so the first fixed kernel
that `kernel/fetch.sh` can vendor today is `7.3.1`. That bump moves
the fleet to a new kernel series.

## The vulnerability

The kernel CVE team published CVE-2026-98432 on 2026-10-15 with the
title "ipv4: free inet_opt and ireq_opt after an RCU grace period".
The bug is in `net/ipv4/tcp_ipv4.c` and `net/ipv4/af_inet.c`.

When a SYN carries IPv4 options, such as Record Route or Timestamp,
the kernel saves them on the request socket in `ireq->ireq_opt`. When
the handshake completes, `tcp_v4_syn_recv_sock()` moves that pointer
to the child socket without a copy. On another CPU, a retransmitted
SYN for the same request socket can send a SYNACK through
`tcp_check_req()` and `tcp_v4_send_synack()`, which read the options
under `rcu_read_lock()` only. `inet_sock_destruct()` frees the options
with a plain `kfree()`, so when the child socket closes during that
window, `ip_options_build()` reads freed memory. A second path does the
same through request socket migration, which needs
`net.ipv4.tcp_migrate_req=1` or a `BPF_SK_REUSEPORT_SELECT_OR_MIGRATE`
program. The fix frees both objects with `kfree_rcu()`.

The bug entered in 4.4 with commit `079096f103fa` ("tcp/dccp: install
syn_recv requests into ehash table"). The fixes are:

| Branch | Fixed in | Commit |
| --- | --- | --- |
| mainline | `7.3` | `e3f33b0a1d89d738e2b913dab519226eaf76e15b` |
| `7.2.y` | `7.2.10` | `26d4c36d318cffbbd160884a0e4cec0da4da4936` |
| `6.18.y` | `6.18.56` | `a47406c769f72c070386985488f1c4d057df86b2` |
| `6.12.y` | `6.12.113` | `11a03ce3c4a62d84c10ccdfe47f8075006498568` |

The kernel CNA scores it 9.8, `AV:N/AC:L/PR:N/UI:N`. SUSE scores it
8.1 with high attack complexity, because the attacker must win a race.
The CNA's scoring notes say that a lost race fails only the handshake,
so the attacker can try again. The stale `optlen` can copy kernel heap
bytes into the SYNACK that goes back to the peer, steer writes in
`ip_options_build()`, or push past the headroom of the packet and call
`skb_under_panic()`.

The Go security team's announcement on 2026-10-29 says that the
demonstrated exploit reaches remote code execution against a server
with a Multipath TCP (MPTCP) listener, and that the bug itself does not
depend on MPTCP. Anthropic's write-up of that exploit reports 48
minutes to leak a kernel pointer and five more minutes to run a shell
command.

## How the bug reaches a `liken` machine

The vulnerable code is in the core IPv4 stack. `CONFIG_INET=y` in the
`7.2.6` config, so the code is in `vmlinuz` and no module is involved.
`spec.modules` does not change anything here: a machine with an empty
module list still runs this code for every TCP connection it accepts.

Every network namespace on a machine shares the one kernel. So any IPv4
TCP listener is enough: k3s's API server on a leader, the kubelet, a
`NodePort` or `LoadBalancer` service, or a port in any pod. The attacker
needs a network path that delivers IPv4 packets with Record Route or
Timestamp options to one of these ports. `machine/sysctldefaults.go`
sets `net.ipv4.conf.all.accept_source_route=0`, which drops the
source route options, but it does not drop Record Route or Timestamp.
Routers on the public internet often drop packets with IP options, so
an attacker on the same network as the machine has the shortest path.
How often a path across the internet keeps these options is not known
here.

Three more settings make the demonstrated exploit more likely to apply:

- `CONFIG_MPTCP=y`, and `liken` does not set `net.mptcp.enabled`, so it
  keeps the kernel's default of 1.
- Since Go 1.24, a Go program's listen sockets use MPTCP by default
  when the main module's `go` line is 1.24 or later. k3s
  `v1.37.1+k3s1` declares `go 1.26.8` in its `go.mod` and has no
  `godebug multipathtcp=0` line. This review did not check whether k3s
  or the Kubernetes code turns MPTCP off for each listener, so it is
  likely, but not confirmed, that k3s's listeners accept MPTCP.
- `machine/sysctldefaults.go` sets `kernel.panic_on_oops=1`. An oops
  in `ip_options_build()` panics the machine, and it reboots after
  `panic=10`. A peer that does not win the race for code execution can
  still reboot a machine.

`liken` does not set `net.ipv4.tcp_migrate_req`, so the migration path
needs a workload that loads a reuseport BPF program. The first path
needs nothing beyond a listener.

## The target kernel

Bump `kernel/VERSION` to `7.3.1`. `7.3.1` contains the mainline fix,
because the fix landed in `7.3`. Canonical published the amd64 build
of `7.3.1` on 2026-10-30. Its `CHECKSUMS` file names
`linux-image-unsigned-7.3.1-070301-generic_7.3.1-070301.202610301300_amd64.deb`
and `linux-modules-7.3.1-070301-generic_7.3.1-070301.202610301300_amd64.deb`,
and `kernel/fetch.sh 7.3.1` extracts them with release string
`7.3.1-070301-generic`. `kernel/latest.sh --bump` also writes `7.3.1`.

The fixed `7.2.y` releases are not available from the archive:

- The mainline index has no directory for `7.2.7` through `7.2.13`.
  kernel.org released `7.2.10` on 2026-10-11 and `7.2.13` on
  2026-10-26.
- The index lists `7.2.14`, released 2026-10-30, but its page reported
  "Test amd64 missing" at 16:58 UTC that day, and
  `v7.2.14/amd64/CHECKSUMS` returned 404. The build may still be in
  progress. If it completes, `7.2.14` is a smaller step than `7.3.1`.

The licensing domain tracks `kernel/VERSION`. The source pin in
`licensing/sources.sh` moves from the `7.2.6` tarball's digest to
`fa595aad93734f829c963c2a90ef7c3673224fd7fdae7b49406e397cde2de231`,
the digest that kernel.org's `sha256sums.asc` gives for
`linux-7.3.1.tar.xz`. `licensing/sources.sh --repin` writes it.

## What makes the bump risky

`7.3.1` is the first point release of a new series, so it has had less
use than a late `7.2.y`. A comparison of the two Canonical configs
found:

- 101 options added and 25 removed. The only value changes are the
  toolchain versions (GCC 15.2 to 15.3, rustc 1.92 to 1.97) and
  `CONFIG_LSM`, which adds `bpf` to the end of the list.
- `CONFIG_PROC_SYSCTL` is gone. In `7.3`, `fs/proc/Kconfig` declares
  `CONFIG_SYSCTL` ("Sysctl support (/proc/sys)") in its place, and the
  `7.3.1` config sets `CONFIG_SYSCTL=y`, so `/proc/sys` stays. Init and
  the machine operator write every sysctl through `/proc/sys`, so a
  first boot must show `status.sysctls` read back with the expected
  values.
- The removed options include `CONFIG_OPENVSWITCH_GENEVE`,
  `CONFIG_OPENVSWITCH_GRE`, and `CONFIG_OPENVSWITCH_VXLAN`. `liken`
  loads no Open vSwitch module.
- Every module named in `image/boot-modules.conf`,
  `image/etc/liken/modules.conf`, `open-iscsi/modules.conf`, and
  `nfs-utils/modules.conf` is present in the `7.3.1` module tree. The
  tree has 6945 modules, against 6897 in `7.2.6`.
- `CONFIG_SOFTLOCKUP_DETECTOR=y` and `CONFIG_HARDLOCKUP_DETECTOR=y`
  are unchanged, which plan 68 depends on.

Canonical labels these builds "Mainline Test". The `7.3.1` page reports
the amd64 self-tests as failed with `rc=2`. The `7.2.6` page reports the
same result, and in both logs the failure is in compiling
`tools/testing/selftests`, not in the kernel build.

The comparison covers the config and the module list only. It does not
cover driver behavior in the new series. The drivers that the extension
operators use, for GPUs, sound, CEC, Bluetooth, and USB devices, need a
boot on real hardware. Run the smoke drills and a physical test cluster
before the release, and let the rollout's fallback slot cover a machine
that fails its first boot.

## Stopgaps until the bump ships

None of these fixes the bug. Each one narrows a path to it.

- Set `net.mptcp.enabled: "0"` in a Machine's `spec.sysctls`. This
  removes the MPTCP precondition of the demonstrated exploit. A reply
  on oss-security notes that a Go program that is already running can
  fail new connections after this change, and that a program which
  requires MPTCP stops working.
- Drop IPv4 packets with options at the network edge in front of the
  machines.

## The dependency on Canonical's schedule

The fix reached `7.2.y` on 2026-10-11, and the archive never built that
release. A `liken` fleet that tracks the archive stayed on a vulnerable
kernel until a new series built. Plan 72 moves the kernel to a build
from source, which would let `liken` build `7.2.10` itself and keep a
security bump inside one series.

## As built

Commit `5482ab4d` moves `kernel/VERSION` to `7.3.1` and the kernel
source pin in `licensing/sources.sh` to
`fa595aad93734f829c963c2a90ef7c3673224fd7fdae7b49406e397cde2de231`.
At 17:27 UTC on 2026-10-30, `v7.2.14/amd64/CHECKSUMS` still returned
404, so `7.2.14` was not an option.

`smoke-uefi`, `smoke-bios`, and `smoke-hardware` reached Ready in 15
seconds on `7.3.1`, and `smoke-emmc` in 110 seconds against its limit
of 420. Each one read `status.sysctls` back with `liken`'s values, so
`/proc/sys` works under `CONFIG_SYSCTL`.

Lab release `2026.10.30-901` rolled to liken-1 and stick-1 in about
four minutes, one machine at a time. Both machines came back Ready
with `kernel.pid_max` at 4194304, every node published its
`ResourceSlice`s for `audio.liken.sh`, `bluetooth.liken.sh`,
`display.liken.sh`, `liken.sh`, and `media.liken.sh`, and every pod
returned to Running and ready. No device count from before the bump
was taken, so the drill does not prove that no device went missing.

Release `2026.10.30-001`, cut from `5482ab4d`, has the catalog entry
`sha256:13bfa8c2c3bcbb8238920909955f82fe5e4fac257d8c1a75dd6cf6e9c33d911d`.
It rolled to liken-1, stick-1, and a ten-machine home cluster in 17
minutes. On one machine of the home cluster, `ClockSynchronized` was
False with `OffsetTooLarge` and an offset bound of ±1s for about 17
minutes after its reboot. It then became True at ±1ms with no action.
The other eleven machines read between ±1ms and ±25ms. This was seen
once, and the cause is not known.

## Sources

- [CVE-2026-98432 on NVD](https://nvd.nist.gov/vuln/detail/CVE-2026-98432)
- [The kernel CVE team's announcement, in `vulns.git`](https://git.kernel.org/pub/scm/linux/security/vulns.git/plain/cve/published/2026/CVE-2026-98432.mbox)
- [The mainline fix, `e3f33b0a1d89`](https://git.kernel.org/stable/c/e3f33b0a1d89d738e2b913dab519226eaf76e15b)
  and [the `7.2.10` backport, `26d4c36d318c`](https://git.kernel.org/stable/c/26d4c36d318cffbbd160884a0e4cec0da4da4936)
- [`ChangeLog-7.2.10`](https://cdn.kernel.org/pub/linux/kernel/v7.x/ChangeLog-7.2.10)
- [Ubuntu's CVE tracker entry](https://ubuntu.com/security/CVE-2026-98432)
- [SUSE's CVE entry](https://www.suse.com/security/cve/CVE-2026-98432)
- [The Go security team's announcement, forwarded to oss-security](https://www.openwall.com/lists/oss-security/2026/10/30/1)
  and [the follow-up on disabling MPTCP](https://www.openwall.com/lists/oss-security/2026/10/30/6)
- [Anthropic's write-up of the exploit](https://www.anthropic.com/research/discovering-vulnerabilities-with-claude)
- [Canonical's `7.3.1` mainline build](https://kernel.ubuntu.com/mainline/v7.3.1/)
  and [the `7.2.14` page](https://kernel.ubuntu.com/mainline/v7.2.14/)
