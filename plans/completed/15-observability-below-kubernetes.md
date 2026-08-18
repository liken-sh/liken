# Observability below Kubernetes

Milestone 15. Completed. Each host log stream below Kubernetes becomes
a pod's stdout, so `kubectl logs` is the machine's log interface.

This milestone covers observability for everything below Kubernetes.
The kernel, init, k3s, and containerd log only to the serial console.
For the kernel and init, the console is the only copy that exists. A
collector cannot tail a serial port, so no standard tool can read these
machines' logs.

The fix applies the project's core idea to logs. liken does not build a
new log API. Instead, every host stream becomes a pod's stdout, which
the Kubernetes API already serves. `kubectl logs` becomes the machine's
log interface. A log stack that someone runs in the cluster later
consumes these streams the same way it consumes any pod's streams, and
it needs no host privileges.

The two-planes rule splits the work. Production of logs is
machine-plane work, and init already does it, as the console shows.
Collection of logs is workload-plane work, because k3s does not depend
on any of it. Init's half of the work stays small, and the relays run
as pods.

These items are out of scope: a storage backend, an in-cluster log
stack, and the parsing of message bodies, which is collector-layer work
for whoever runs a stack. Boots that die before k3s starts are also out
of scope, and they leave only the console as a record. The follow-on
work for that last gap is to persist init's log to disk, and to use
efi-pstore for kernel panics.

1. [x] Init logs to `/dev/kmsg` instead of writing to the console
   directly. The kernel echoes kmsg writes back to the console and adds
   printk timestamps, so console behavior stays the same. Every liken
   line also lands in the ring buffer as a structured record,
   interleaved with the kernel's own records in the true time order.
   Userspace records have facility 1, and the kernel's records have
   facility 0. The two streams therefore separate by this field, and
   not by a match on the "liken:" prefix in the text. By default, the
   kernel rate-limits userspace kmsg writes, and this limit would
   silently drop most of the boot report. Init disables the limit by
   writing to the `kernel.printk_devkmsg` sysctl early in boot. This
   method covers every boot path, including machines whose boot entries
   are already baked into firmware, and it does not change a kernel
   command line. If init cannot open the sysctl or `/dev/kmsg`, it
   keeps writing to the console directly, so a degraded boot still
   shows what is happening. A few lines print before `/dev` exists: the
   hello message and the switch_root lines. These lines stay
   console-only, because there is nowhere else for them to go yet.
2. [x] The k3s log moves to clusterState, at a liken-owned path
   (`/var/lib/rancher/k3s/liken/k3s.log`). This path keeps the log from
   being mistaken for a file that k3s manages. Storage settles before
   k3s starts, so the mount is always in place first. A memory-backed
   machine degrades to the tmpfs root exactly as it does today.

   Rotation happens at boot. Init rotates both `k3s.log` and
   containerd's own log before it starts k3s, and k3s reopens both
   files. k3s writes the containerd log to clusterState at
   `agent/containerd/containerd.log`, and k3s never bounds its size.
   Per-boot files form a small boot index, in the style of journald. A
   boot that died with k3s leaves its log on disk for later forensics.
   The relays tail only the current boot's files. Shipment of prior
   boots' logs belongs to the failed-boot follow-on work.

   One ordering detail is load-bearing: the open log handle must close
   after k3s exits and before clusterState unmounts. If it does not,
   shutdown's unmount fails because the mount is still busy.
3. [x] The relay is hand-rolled. The kmsg record format and a
   rotation-aware tail are each small formats in the GPT-writer family.
   The tail detects an inode change and reopens the file, the same
   behavior that `tail -F` gives. The relay must be in the baked image
   anyway, because what it parses is coupled to the OS version.

   One multi-call entrypoint is behind one `machine-logs` DaemonSet,
   with one container per source. The kernel and liken containers both
   read `/dev/kmsg`, which supports concurrent readers, and each one
   filters by facility. The k3s and containerd containers run the same
   tailer at different paths.

   The relays first ran as four separate DaemonSets, and they are now
   one four-container pod. Containers in one pod share the sandbox and
   the runtime shim. The consolidation is about half of the per-node
   overhead, and it removes fifteen pods from the fleet's pod count. It
   keeps every property that the per-source split existed for, because
   stdout, securityContext, and restart counters all stay per-container.
   Container identity is the source tag. Privilege follows the source:
   the kmsg containers run privileged, and the tailers run with only a
   read-only hostPath.

   The consolidation rollout showed one behavior of k3s's deploy
   controller. It applies each manifest file as one object set, and
   wrangler stamps everything it creates with the file's identity. When
   `logs.yaml`'s contents changed from four DaemonSets to one, the same
   apply that created `machine-logs` also garbage-collected the four
   superseded DaemonSets. No manual deletion was necessary. There is
   folklore that k3s never prunes resources. That folklore is about
   removal of a whole manifest file, whose resources do stay. Within a
   manifest file that still exists, removed objects are cleaned up.

   The privilege requirement came out of a drill. `CAP_SYSLOG`, which
   the kernel demands for `/dev/kmsg` under `dmesg_restrict`, is
   necessary but not sufficient. The container runtime's devices cgroup
   separately gates every device open through an allowlist, and
   Kubernetes cannot extend that allowlist per pod. In the first drill,
   both kmsg relays crash-looped on `EPERM`, with the capability
   correctly granted.

   Delivery copies the operator's approach: `:installed` image
   resolution, `OnDelete` updates, and the steward's refresh. Each
   relay keeps a resume cursor in a per-pod emptyDir. This cursor
   survives container restarts, and it is deleted when the pod is
   deleted or the machine reboots. Those are exactly the moments when a
   replay from the head of the log is correct, because kmsg sequence
   numbers and the rotated files both reset at boot. The cluster's
   key-value facilities were considered for the cursors and rejected.
   Cursors are node-local facts that change on every batch, so a
   checkpoint through etcd would repeat milestone 10's
   write-amplification result. A PVC also cannot be expressed per
   DaemonSet pod.
4. [x] The output contract wraps a structured envelope around a
   verbatim message body. Event time must travel in the payload itself.
   The kmsg reader replays from the head of the buffer, and the
   container runtime stamps lines at read time, which gives the wrong
   time for anything replayed.

   The rule is this: the relay lifts exactly the fields that the source
   format defines as its header, and it never parses the message body.
   For kmsg, those fields are facility, severity, sequence, and the
   monotonic timestamp converted to wall-clock time. For the tailed
   files, those fields are the logrus `time=` and `level=` prefix, plus
   klog's `Lmmdd hh:mm:ss` header. k3s's output mixes both formats,
   because the embedded Kubernetes components log through klog. A line
   that matches neither format ships with the relay's own observation
   time and info severity.

   Field names stay plain: `time`, `severity`, `seq`, `message`, and
   `facility` on the kmsg relays. The `seq` field lets downstream
   consumers deduplicate replays. It holds the kmsg sequence number or
   a byte offset, which depends on the source. `jq -r .message` gives a
   human the original text back. The console stays the narrative,
   human-readable surface. Pod logs are the machine-readable one.
5. [x] Prove it in the lab. `kubectl logs` on each DaemonSet shows its
   stream from the start of the boot. A reboot shows rotation that
   keeps the prior boot's k3s and containerd logs. A relay pod deleted
   mid-run comes back and replays with its sequence numbers intact. A
   relay whose container restarts, with the pod still intact, resumes
   from its cursor and does not replay. A fresh `kubectl logs --since`
   after the fleet settles shows the fleet quiet, not chattering.

   Two releases rolled onto the live five-node lab and proved this.
   Release 0.3.0 shipped the relays and found the privilege bug
   described above. Release 0.3.1 shipped the fix. The whole fleet went
   through the rollout, and all twenty-five OS pods, which are the
   operator plus four relays on five nodes, refreshed through the
   steward and settled at zero restarts. That count belongs to the
   four-DaemonSet layout that these two releases shipped. Commit
   030900b folded the four relays into one four-container pod, so a
   fleet of this size runs fewer pods now.

   `kernel-logs` ships the boot from sequence 0 ("Linux version ...").
   `liken-logs` opens with exactly the console-only boundary marker.
   `k3s-logs` shows the severity mix lifted from logrus (info, warning,
   err), and it starts at offset 0 on the fresh boot's file, which
   shows rotation working across a real reboot. A deleted `kernel-logs`
   pod replayed byte-identical records under the same sequence numbers.

   One behavior is left to unit tests alone: cursor resume across a
   container restart. The relay's image has no shell, so there is no
   way to kill only the container in the lab. The tailer tests cover
   the resume path directly instead.
