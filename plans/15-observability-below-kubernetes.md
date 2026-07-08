# Observability below Kubernetes

Milestone 15 — Done

Observability for everything below Kubernetes. The kernel,
init, k3s, and containerd log only to the serial console, and
for the first two the console is the only copy that exists. A
collector cannot tail a serial port, so nothing standard can
read these machines. The fix extends the project's core idea
to logs: rather than growing a log API, every host stream
becomes a pod's stdout, which the Kubernetes API already
serves. `kubectl logs` becomes the machine's log interface,
and any log stack someone later runs in the cluster consumes
these streams the way it consumes any pod's, with no host
privileges. The two-planes rule splits the work: *producing*
logs is machine-plane (init already does it; the console is
the proof), while *collecting* them is workload-plane, since
k3s depends on none of it. So init's half stays small, and
the relays are pods. Deliberately out of scope: any storage
backend or in-cluster stack, parsing message bodies (that is
collector-layer work for whoever runs a stack), and boots
that die before k3s starts, which leave only the console as
witness. The follow-ons for that last gap are persisting
init's log to disk and efi-pstore for kernel panics.
1. [x] Init logs to /dev/kmsg instead of writing the console
   directly. The kernel echoes kmsg writes back to the
   console (gaining printk timestamps), so console behavior
   is preserved, and every liken line also lands in the ring
   buffer as a structured record, interleaved with the
   kernel's own in true order. Userspace records carry
   facility 1 where the kernel's carry 0, so the two streams
   separate by a field rather than by string-matching the
   "liken:" prefix. The kernel rate-limits userspace kmsg
   writes by default, which would silently drop most of the
   boot report; init disables that by writing to the
   kernel.printk_devkmsg sysctl early in boot, which covers
   every boot path (including machines whose boot entries
   are already baked into firmware) without touching a
   kernel command line. If the sysctl or /dev/kmsg can't be
   opened, init keeps writing the console directly, so a
   degraded boot still narrates itself. The handful of
   lines printed before /dev exists (hello, the switch_root
   narration) stay console-only; there is nowhere else for
   them to go yet.
2. [x] The k3s log moves to clusterState, at a liken-owned path
   (/var/lib/rancher/k3s/liken/k3s.log) so it can't be
   mistaken for a file k3s manages. Storage settles before
   k3s starts, so the mount is always there first, and a
   memory-backed machine degrades to the tmpfs root exactly
   as today. Rotation is rotate-at-boot: init rotates both
   k3s.log and containerd's own log (which k3s writes to
   clusterState at agent/containerd/containerd.log, and
   never bounds) before starting k3s, which reopens both.
   Per-boot files are a small journald-style boot index: a
   boot that died with k3s leaves its log on disk for
   forensics, though the relays tail only the current boot's
   files (shipping prior boots belongs to the failed-boot
   follow-on work). One ordering note becomes
   load-bearing: the open log handle must close after k3s
   exits and before clusterState unmounts, or shutdown's
   unmount fails busy.
3. [x] The relay, hand-rolled: the kmsg record format and a
   rotation-aware tail (notice the inode change, reopen; the
   lesson tail -F embodies) are each small formats in the
   GPT-writer family, and the relay must live in the baked
   image anyway because what it parses is OS-version-coupled.
   One multi-call entrypoint behind one machine-logs
   DaemonSet with a container per source: kernel and liken
   both read /dev/kmsg (the device supports concurrent
   readers) and filter by facility; k3s and containerd run
   the same tailer at different paths. This was first built
   and proven as four separate DaemonSets, then consolidated
   into one four-container pod: containers share the sandbox
   and runtime shim (roughly halving the per-node overhead
   and shrinking the fleet's pod count by fifteen) while
   keeping every property the per-source split was for,
   since stdout, securityContext, and restart counters are
   all per-container. Container identity is the source tag,
   and privilege follows the source: the kmsg containers run
   privileged, the tailers with only a read-only hostPath.
   The consolidation rollout taught one thing about k3s's
   deploy controller: it applies each manifest file as an
   object set (wrangler stamps everything it creates with
   the file's identity), so when logs.yaml's contents went
   from four DaemonSets to one, the superseded four were
   garbage-collected by the same apply that created
   machine-logs, no manual deletion needed. The folklore
   that k3s never prunes is about removing a manifest file,
   whose resources do linger; within a still-present file,
   removed objects are cleaned up.
   The privilege was a lab lesson: CAP_SYSLOG (which the
   kernel demands for /dev/kmsg under dmesg_restrict) is
   necessary but not sufficient, because the container
   runtime's devices cgroup separately gates every device
   open through an allowlist Kubernetes cannot extend
   per-pod. The first drill crash-looped both kmsg relays
   on EPERM with the capability correctly granted.
   Delivery copies the operator wholesale: the :installed
   image resolution, OnDelete updates, and the steward's
   refresh. Each relay keeps a resume cursor in a per-pod
   emptyDir, which survives container restarts and dies
   with the pod or a reboot; those are exactly the moments
   replay-from-head is correct, since kmsg sequence numbers
   and the rotated files reset at boot. The cluster's KV
   facilities were considered and refused: cursors are
   node-local facts that change every batch, so
   checkpointing them through etcd is milestone 10's
   write-amplification lesson again, and a PVC can't be
   expressed per-DaemonSet-pod anyway.
4. [x] The output contract: a structured envelope around a
   verbatim body. Event time must ride in the payload,
   because the kmsg reader replays from the head of the
   buffer and the container runtime stamps lines at read
   time, which is wrong for everything replayed. The rule:
   the relay lifts exactly the fields the source format
   defines as its header and never parses the message. For
   kmsg that is facility, severity, sequence, and the
   monotonic timestamp converted to wall clock; for the
   tailed files it is the logrus time= and level= prefix,
   plus klog's Lmmdd hh:mm:ss header, because k3s's output
   mixes both (the embedded Kubernetes components log via
   klog). A line matching neither ships with the relay's
   observation time and info severity. Field
   names are plain (time, severity, seq, message; facility
   on the kmsg relays), seq makes replays dedupable
   downstream (kmsg sequence or byte offset per source),
   and `jq -r .message` gives a human the prose back. The
   console stays the literate, human surface; pod logs are
   the machine-readable one.
5. [x] Prove it in the lab: `kubectl logs` on each DaemonSet
   shows its stream from the head of the boot, a reboot
   shows rotation keeping the prior boot's k3s and
   containerd logs, a relay pod deleted mid-run comes back
   and replays with sequence numbers intact, a relay whose
   container restarts (pod intact) resumes from its cursor
   without replaying, and a fresh
   `kubectl logs --since` after settling shows the fleet
   quiet, not chattering. Proven across two releases rolled
   onto the live five-node lab. 0.3.0 carried the relays and
   found the privilege bug above; 0.3.1 carried the fix, and
   the whole fleet walked the rollout with all twenty-five
   OS pods (operator plus four relays, five nodes) refreshed
   by the steward and settling at zero restarts. kernel-logs
   ships the boot from sequence 0 ("Linux version ...");
   liken-logs opens with exactly the console-only boundary
   marker; k3s-logs shows the severity mix lifted from
   logrus (info, warning, err) and starts at offset 0 on the
   fresh boot's file, which is rotation working across a
   real reboot; and a deleted kernel-logs pod replayed
   byte-identical records under the same sequence numbers.
   The one behavior left to unit tests alone is cursor
   resume across a container restart: with no shell in the
   relay's image there is no way to kill just the container,
   and the tailer tests cover the resume path directly.
