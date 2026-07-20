# Observability below Kubernetes

Milestone 15 — Done

This milestone covers observability for everything below Kubernetes.
The kernel, init, k3s, and containerd log only to the serial console.
For the kernel and init, the console is the only copy that exists. A
collector cannot tail a serial port, so no standard tool can read
these machines' logs.

The fix extends the project's core idea to logs. Instead of building
a new log API, every host stream becomes a pod's stdout, which the
Kubernetes API already serves. `kubectl logs` becomes the machine's
log interface. Any log stack that someone later runs in the cluster
consumes these streams the same way it consumes any pod's streams,
with no host privileges needed.

The two-planes rule splits the work. *Producing* logs is machine-plane
work; init already does this, and the console proves it. *Collecting*
logs is workload-plane work, because k3s does not depend on any of
it. So init's half of the work stays small, and the relays run as
pods.

This milestone deliberately leaves some things out of scope: any
storage backend or in-cluster log stack; parsing message bodies,
which is collector-layer work for whoever runs a stack; and boots
that die before k3s starts, which leave only the console as a
record. The follow-on work for that last gap is persisting init's log
to disk, and using efi-pstore for kernel panics.
1. [x] Init logs to `/dev/kmsg` instead of writing to the console
   directly. The kernel echoes kmsg writes back to the console and
   adds printk timestamps, so console behavior stays the same. Every
   liken line also lands in the ring buffer as a structured record,
   interleaved with the kernel's own records in the true time order.
   Userspace records carry facility 1, and the kernel's records carry
   facility 0. So the two streams separate by this field, rather than
   by matching the "liken:" prefix in the text. By default, the
   kernel rate-limits userspace kmsg writes, and this would silently
   drop most of the boot report. Init disables that limit by writing
   to the `kernel.printk_devkmsg` sysctl early in boot. This method
   covers every boot path, including machines whose boot entries are
   already baked into firmware, without touching a kernel command
   line. If init cannot open the sysctl or `/dev/kmsg`, it keeps
   writing to the console directly, so a degraded boot still shows
   what is happening. A few lines print before `/dev` exists (the
   hello message and the switch_root narration). These lines stay
   console-only, because there is nowhere else for them to go yet.
2. [x] The k3s log moves to clusterState, at a liken-owned path
   (`/var/lib/rancher/k3s/liken/k3s.log`). This path keeps the log
   from being mistaken for a file that k3s manages. Storage settles
   before k3s starts, so the mount is always in place first. A
   memory-backed machine degrades to the tmpfs root exactly as it
   does today.

   Rotation happens at boot. Init rotates both `k3s.log` and
   containerd's own log before starting k3s, and k3s reopens both
   files. k3s writes the containerd log to clusterState at
   `agent/containerd/containerd.log`, and k3s never bounds its size.
   Per-boot files form a small, journald-style boot index. A boot
   that died along with k3s leaves its log on disk for later
   forensics. The relays tail only the current boot's files; shipping
   prior boots' logs belongs to the failed-boot follow-on work.

   One ordering detail is load-bearing: the open log handle must
   close after k3s exits and before clusterState unmounts. Otherwise,
   shutdown's unmount fails because the mount is still busy.
3. [x] The relay is hand-rolled. The kmsg record format and a
   rotation-aware tail (which notices an inode change and reopens the
   file, the lesson that `tail -F` teaches) are each small formats in
   the GPT-writer family. The relay must live in the baked image
   anyway, because what it parses is coupled to the OS version.

   One multi-call entrypoint sits behind one `machine-logs` DaemonSet,
   with one container per source. The kernel and liken containers
   both read `/dev/kmsg` (the device supports concurrent readers) and
   filter by facility. The k3s and containerd containers run the same
   tailer at different paths.

   The team first built and proved this as four separate DaemonSets,
   then consolidated it into one four-container pod. Containers in
   one pod share the sandbox and runtime shim. This roughly halves
   the per-node overhead and shrinks the fleet's pod count by
   fifteen. The consolidation keeps every property that the
   per-source split existed for, because stdout, securityContext, and
   restart counters all stay per-container. Container identity is the
   source tag. Privilege follows the source: the kmsg containers run
   privileged, and the tailers run with only a read-only hostPath.

   The consolidation rollout taught one thing about k3s's deploy
   controller. It applies each manifest file as one object set;
   wrangler stamps everything it creates with the file's identity. So
   when `logs.yaml`'s contents changed from four DaemonSets to one,
   the same apply that created `machine-logs` also garbage-collected
   the four superseded DaemonSets. No manual deletion was needed.
   There is folklore that k3s never prunes resources; that folklore
   is about removing a whole manifest file, whose resources do
   linger. Within a manifest file that still exists, removed objects
   do get cleaned up.

   The privilege requirement was a lab lesson. `CAP_SYSLOG` (which the
   kernel demands for `/dev/kmsg` under `dmesg_restrict`) is
   necessary but not sufficient. The container runtime's devices
   cgroup separately gates every device open through an allowlist,
   and Kubernetes cannot extend that allowlist per pod. In the first
   drill, both kmsg relays crash-looped on `EPERM`, even with the
   capability correctly granted.

   Delivery copies the operator's approach wholesale: `:installed`
   image resolution, `OnDelete` updates, and the steward's refresh.
   Each relay keeps a resume cursor in a per-pod emptyDir. This
   cursor survives container restarts, and it is deleted when the pod
   is deleted or the machine reboots. Those are exactly the moments
   when replaying from the head of the log is correct, because kmsg
   sequence numbers and the rotated files both reset at boot. The
   team considered and rejected using the cluster's key-value
   facilities for this. Cursors are node-local facts that change on
   every batch, so checkpointing them through etcd would repeat
   milestone 10's write-amplification lesson. Also, a PVC cannot be
   expressed per DaemonSet pod.
4. [x] The output contract wraps a structured envelope around a
   verbatim message body. Event time must travel in the payload
   itself. This is necessary because the kmsg reader replays from the
   head of the buffer, and the container runtime stamps lines at read
   time, which gives the wrong time for anything replayed.

   The rule is this: the relay lifts exactly the fields that the
   source format defines as its header, and it never parses the
   message body. For kmsg, those fields are facility, severity,
   sequence, and the monotonic timestamp converted to wall-clock
   time. For the tailed files, those fields are the logrus `time=`
   and `level=` prefix, plus klog's `Lmmdd hh:mm:ss` header. k3s's
   output mixes both formats, because the embedded Kubernetes
   components log through klog. A line that matches neither format
   ships with the relay's own observation time and info severity.

   Field names stay plain: `time`, `severity`, `seq`, `message`, and
   `facility` on the kmsg relays. The `seq` field lets downstream
   consumers deduplicate replays; it holds the kmsg sequence number
   or a byte offset, depending on the source. Running `jq -r
   .message` gives a human the original text back. The console stays
   the narrative, human-readable surface. Pod logs are the
   machine-readable one.
5. [x] Prove it in the lab. `kubectl logs` on each DaemonSet shows its
   stream from the start of the boot. A reboot shows rotation keeping
   the prior boot's k3s and containerd logs. A relay pod deleted
   mid-run comes back and replays with its sequence numbers intact. A
   relay whose container restarts, with the pod still intact, resumes
   from its cursor without replaying. A fresh `kubectl logs --since`
   after the fleet settles shows the fleet quiet, not chattering.

   The team proved this across two releases rolled onto the live
   five-node lab. Release 0.3.0 carried the relays and found the
   privilege bug described above. Release 0.3.1 carried the fix. The
   whole fleet went through the rollout, and all twenty-five OS pods
   (the operator plus four relays, on five nodes) refreshed through
   the steward and settled at zero restarts.

   `kernel-logs` ships the boot starting from sequence 0 ("Linux
   version ..."). `liken-logs` opens with exactly the console-only
   boundary marker. `k3s-logs` shows the severity mix lifted from
   logrus (info, warning, err), and it starts at offset 0 on the
   fresh boot's file, which shows rotation working across a real
   reboot. A deleted `kernel-logs` pod replayed byte-identical records
   under the same sequence numbers.

   One behavior is left to unit tests alone: cursor resume across a
   container restart. The relay's image has no shell, so there is no
   way to kill just the container in the lab. The tailer tests cover
   the resume path directly instead.
