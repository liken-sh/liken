# The rough path

1. [x] Boot to a hello world from an init I built: `make run` boots QEMU
       and PID 1 prints to the serial console. There is no shell and no
       prompt; the console is output-only by design.
   1. [x] `kernel/`: vendor a pre-built vanilla kernel from Ubuntu's
          mainline builds: fetch a pinned version, verify checksums,
          extract the image and modules, and run `depmod` at build time.
   2. [x] `init/`: a minimal Go init that mounts `/proc`, `/sys`, and
          `/dev`, prints a report of the hardware and kernel state it
          finds, and reaps zombie processes.
   3. [x] `image/`: assemble the initramfs, which is a cpio archive. The
          whole OS is `vmlinuz` plus `liken.cpio`.
   4. [x] `make run`: boot it headless in QEMU. A smoke test can watch
          the serial output for a marker, which is the starting point
          for CI. Use explicit flags (`-display none -serial stdio
          -monitor none -no-reboot`) rather than the `-nographic`
          bundle, so that each flag can carry its own explanatory
          comment.
2. [x] Init starts k3s and nothing else, and discover every host
       dependency (cgroups, kernel modules, time, entropy) by running
       into each one directly rather than reading about it.
   1. [x] Boot to network from a Machine manifest (`kind: Machine`,
          `apiVersion: liken.sh/v1alpha1`, file-delivered at boot): bring
          up the interface, run DHCP (the whole DORA exchange prints to
          the console), apply the lease via netlink, and prove it with a
          DNS lookup against an outside nameserver. Entropy was the
          dependency we predicted would surface here: without RDRAND the
          kernel RNG never initializes, so getrandom() blocks forever in
          the DHCP client.
   2. [x] Boot to a Ready node: init sets up everything k3s assumes
          exists (cgroup2, identity files, mount propagation, module
          preload, and iptables on a system with no shell), supervises
          k3s with backoff, and prints node and pod state to the
          console. `make run` ends at a settled single-node cluster;
          `make run-once` (`liken.oneshot`) powers the machine off
          whenever k3s exits, so a harness can measure the boot.
   3. [x] Machine identity is an input to the build, not something
          extracted from a running machine: `make` mints a CA bundle
          (gitignored, identity/) and pre-seeds k3s's TLS directory in
          the image, so an operator's kubeconfig is computed offline
          and never copied off the machine. `make kubeconfig` plus a
          loopback-only QEMU port-forward gets `kubectl get nodes` from
          the host; no `--tls-san` needed, since k3s's serving cert
          covers 127.0.0.1 by default. One discovery came out of this:
          kube-apiserver reads the ServiceAccount key with a parser
          that accepts SEC1 "EC PRIVATE KEY" but not PKCS#8.
   4. [x] The Kubernetes API is the machine API: the Machine manifest is
          now a real CRD (`kubectl get machines` works), reconciled by
          a liken operator. The operator ships inside the initramfs as
          a hand-assembled OCI tarball (operator/image.sh) and deploys
          through k3s's auto-manifests directory, so there are no
          registry pulls and no kubectl steps. Init never talks to k3s:
          it applies spec.sysctls at boot and writes facts to
          `/run/liken/`. The operator seeds the Machine from the
          manifest, publishes facts and observed sysctls into status,
          and reconciles sysctl edits live. It uses plain net/http
          against the API server rather than client-go, because writing
          the watch loop by hand is the lesson. The shared Go types
          live in the machine/ domain, used by both programs. A
          leftover mystery got mostly solved along the way: "The
          manifest file is empty, ignoring." fires once per embedded
          control-plane component as it parses its options, and has
          nothing to do with k3s's manifests directory.
3. [x] Unwind the known hacks before building on top of them. These are
       fixes from the boot-to-k3s work that depend on k3s internals it
       never promised us. Each works today and is guarded by the
       version pin and `make run-once`, but every milestone below
       builds on the boot path, so the coupling gets settled first.
   1. [x] Init's PATH hardcoded k3s's internal layout, and it turned
          out to be redundant, so it is removed. The console shows k3s
          prepending its own unpacked userland to the PATHs it builds
          for children, and the cluster settles without the extra
          entries.
   2. [x] The `/sbin/iptables` dangling symlinks are gone: the
          netfilter userspace is now its own vendored domain
          (`xtables/`), fetched from k3s-root, the same project that
          builds k3s's bundled copy, pinned to the same version the
          vendored k3s uses. `/sbin/iptables` is now a real static
          binary from the image build onward. The machine also reports
          its xtables version in the Machine's status.version, observed
          via `iptables -V` like every other fact.
   3. [x] switch_root onto a plain tmpfs early in boot, the way k3OS
          did, so the root filesystem is an ordinary measurable mount
          instead of the kernel's magic initramfs rootfs. This let us
          drop `local-storage-capacity-isolation=false` entirely and
          silenced kubelet's recurring filesystem-stat errors; kubelet
          now measures / the way it would on any other machine.
   4. [x] The CA bundle came from whichever machine ran the build
          (build.sh's own comment said as much). It is now vendored
          like everything else: a `trust/` domain pins a dated snapshot
          of the Mozilla bundle, so what the machine trusts changes by
          a reviewable version bump instead of by build-host accident.
4. [x] Storage, which starts with a disk existing at all. The whole
       machine is RAM today. The goal is to put k3s's state on
       persistent storage, so that container images stop re-importing
       every boot and cluster state survives a reboot. Storage is
       declared by *purpose*, not by mount path: `spec.storage` is a
       map keyed by singleton role (`clusterState` first), each entry
       naming a device and an optional fixed size. liken derives GPT
       partition tables from the roles grouped by device, formats blank
       disks at runtime, and names each partition `liken:<role>`.
       Recognition on every later boot is by partition name read from
       sysfs. There is no udev, and `device:` is an input for
       first-boot claiming only, since kernel enumeration order is not
       guaranteed. Reconciling never destroys data: a blank disk is
       claimed, our own partitions are mounted, and anything foreign or
       ambiguous is refused. A declared role that can't be reconciled
       stops the boot: init prints the full explanation to the console
       and powers the machine off, and k3s never starts. The reasoning:
       a machine that promised persistent cluster state but boots
       ephemeral anyway will silently lose data, and a machine that is
       down can be recovered while data written to the wrong place
       cannot. Undeclared roles simply land where everything lands
       today, the root tmpfs. `status.storage` enumerates where every
       role actually landed (`Partition` or `Memory`), while
       `status.hardware.blockDevices` reports the raw inventory.
   1. [x] A disk exists: `make run` attaches a gitignored qcow2, and
          init discovers block devices from `/sys/block` and adds them
          to its boot-time report.
   2. [x] Claiming: init writes the GPT itself (a small, checksummed
          binary format, and a good lesson), makes the filesystem (the
          open question was mechanism: the image has no libc, so mkfs
          must be a static binary or Go code), and mounts
          `clusterState` where k3s will use it, all before k3s starts.
          Every reason a spec can be refused (foreign disks, cloned
          disks, disks too small, partial claims) is unit-tested in
          init/, against fake sysfs trees; a refusal halts the boot
          from one place in main.go.
   3. [x] Prove persistence: boot, power off, and boot again; images
          import once and the cluster comes back. (Proven by milestone
          5's reboot cycles: the cluster survived staged-spec reboots
          and a hard power cut, on the same disks.)
   4. [x] The API: `spec.storage` and `status.storage` in the Machine
          CRD, the operator publishing the landing table and the
          hardware inventory.
5. [x] The spec becomes editable: a Machine edit in the cluster
       actually converges, by reboot. The roles are now named for
       their owners (`machineState` and `machineEphemeral` belong to
       the machine; `clusterState` awaits `kind: Cluster`), and the
       new `machineState` role holds the machine's manifests. The
       operator detects drift between the cluster's spec and the
       boot's boot record, validates against the machine's reality
       (grow-only sizes, attached devices; CEL rules refuse shrinks at
       admission), stages the manifest durably, and per
       `spec.rebootPolicy` requests a reboot or reports one pending.
       Init prefers the staged manifest, promotes it on success, and
       falls back to the proven last-known-good on failure, so a bad
       edit degrades the machine instead of taking it down. Partitions
       are grow-only: sized roles grow into free space, remainder
       roles follow a grown disk (relocating the backup GPT), and ext4
       grows by ioctl, with no resize2fs.
   1. [x] The `machine*` role vocabulary, and `machineState` first in
          canonical order so a boot can find it before reading any spec.
   2. [x] A GPT reader (both copies, CRC-checked, identities preserved
          through edits) and grow-only partition resize, with the
          filesystem grown online via EXT4_IOC_RESIZE_FS.
   3. [x] The manifest lifecycle on machineState: staged/proven/
          rejected, durable writes, the settle loop with last-known-good
          fallback, and the boot record in facts and status.
   4. [x] The operator's convergence loop: drift detection, staging
          validation, the SpecConverged condition vocabulary,
          `spec.rebootPolicy`, and CEL no-shrink rules in the CRD.
   5. [x] The reboot protocol: the operator's intent file, init's
          watcher, a graceful k3s stop, and `make run-lab` (a QEMU run
          that survives reboots) plus `grow-pods` for the disk-growth
          drill.
   6. [x] Prove the full cycle in the lab: edit the spec via kubectl,
          watch the machine stage, reboot, grow, and converge; drill
          the rejections (CEL refuses a shrink at admission, the
          operator refuses an invalid spec with StagingRejected, and a
          staged spec that fails at boot falls back to proven and
          holds at RejectedLastBoot without a reboot loop). The
          disk-growth drill grew podEphemeral's partition and
          filesystem from 1.5 to 5.5 GiB in place.
   7. [x] Editing back to a good state. The first CEL rules compared
          the spec against its previous value, which wedged: after
          declaring a size the machine couldn't satisfy, reverting the
          spec was also refused as a shrink, and the only exit was
          `kubectl replace --force`, which would be untenable once
          Flux owns the spec. The rules now compare the spec against
          `status.boot.storage` (the sizes the machine actually booted
          with), so a failed aspiration can always be edited back to
          reality, and only a real on-disk shrink is refused. When the
          spec returns to what the machine runs, the operator also
          withdraws any manifest still staged (the next boot would
          have applied it) and clears the standing rejection.
6. [x] Growing the cluster past one node, driven by a `kind: Cluster`
       resource: one leader and one follower, with every decision made
       explicitly rather than discovered at runtime. The join token is
       an input like the rest of identity: k3s's secure token format
       is `K10<CA-hash>::user:pass`, and the CA it hashes is the
       server CA that identity/ already mints, so make computes the
       whole token offline and bakes it into the identity bundle.
       The spec carries topology; the identity bundle carries secrets;
       nothing is ever extracted from a running machine (and a machine
       with no shell has no way to extract secrets anyway). Machines
       get static addresses declared in their manifests, and a machine
       finds its own manifest by `liken.machine=<name>` on the kernel
       command line, the one input channel the bootloader already
       controls. One boot medium carries cluster.yaml and a manifest
       per machine (node-1.yaml, node-2.yaml, ...), so a single image
       boots the whole fleet. Which fleet it boots is a *deployment's*
       decision, not the OS's: the manifests are an input to the image
       build. The repo's own deployment is the dev-cluster/ domain,
       which holds those manifests and the QEMU guests that boot them.
   1. [x] The Cluster CRD: cluster.yaml is file-delivered like the
          Machine manifest and seeded by the operator; every node's
          operator races to create it, and the losers treat their 409
          responses as success. spec.leaders names the machines that
          run control planes, and spec.network holds the facts k3s
          requires every node to agree on (cluster CIDR, service
          CIDR, cluster DNS, cluster domain), cluster-scoped facts
          even though k3s configures them as per-node flags. It also
          holds nodeCIDR, the subnet nodes address each other on. A
          machine's role is derived, not declared: a machine is a
          leader if its name appears in spec.leaders. Promoting a
          follower is then a Cluster edit, not a coordinated pair of
          Machine edits.
   2. [x] The token joins the identity bundle: mint.sh hashes the
          server CA, appends a random secret, and writes the token
          next to the TLS material. This is idempotent: re-running
          mint.sh fills gaps but never replaces an identity machines
          already carry. The token lives at /etc/liken/token, outside
          k3s's data directory, because the clusterState filesystem
          mounts over that. Init gives k3s the *path* (token-file), so
          the secret never appears in a config file or on a command
          line.
   3. [x] Static networking: spec.network grows an interfaces list
          (name, address in CIDR form, optional gateway and
          nameservers; no address means DHCP, and an empty spec still
          means DHCP on the first real NIC). This was an open
          problem; the lab forced it onto the critical path, because
          the shared segment joining two QEMU guests is a dumb wire
          with no DHCP server on it. Each machine runs two
          interfaces: a DHCP uplink and the statically-addressed
          cluster segment, and the Cluster's nodeCIDR is what picks
          which address becomes the node IP (left to itself, k3s picks
          the interface with the default route, which is the uplink
          and exactly the wrong choice).
   4. [x] liken.machine=: init reads its name from the kernel command
          line and selects its seed from the manifests the image
          carries; after first boot, machineState carries the proven
          manifest forward exactly as before. Selection refuses to
          guess: a name matching no manifest, or many manifests with
          no name, powers the machine off after printing the reason
          to the console. A first boot under the wrong identity could
          join the wrong cluster or claim another machine's disks,
          which is worse than failing to boot. A cluster manifest
          that won't parse is fatal the same way: a machine that
          can't tell if it's a
          leader must not guess, because guessing "leader" starts a
          rival control plane.
   5. [x] The lab grows a node dimension: per-node dist directories,
          MACs, and command lines. Each guest gets two NICs: user-mode
          NAT stays as each guest's internet uplink, and a multicast
          socket segment (no root, no bridges: every QEMU naming the
          same group is one virtual hub) is the wire the cluster
          communicates over. The API-server hostfwd lives on the
          leader node only. Two terminals (`make run`, `make run
          NODE=node-2`) show two serial consoles side by side. A
          supporting discovery: k3s reads drop-in config from
          <config>.yaml.d/, so the image's static files stay untouched
          and init writes only a boot.yaml drop-in of derived facts.
          Followers also need their own config file entirely, because
          `k3s agent` refuses leader-only keys.
   6. [x] Prove it: `kubectl get nodes` shows two Ready nodes,
          `kubectl get machines` shows a leader and a follower with
          their segment addresses, `kubectl get clusters` shows the
          topology, a pod pinned to the follower runs with a
          cluster-CIDR address and resolves cluster DNS across the
          VXLAN, and both machines come back from a power cut booting
          their Proven manifests, with the cluster and the pod intact.
          One discovery came out of this: on first join, k3s creates a
          "node password" for each node, records it server-side, and
          requires the same one on every reconnect. That is what stops
          a stranger from registering as an existing node. k3s keeps
          the password at /etc/rancher/node/password, which on liken
          was the RAM root, so a rebooted follower presented a freshly
          generated password to its own cluster and was refused. The
          password is machine identity, so /etc/rancher/node is now a
          symlink onto machineState. The reliable way to verify a
          re-join is the node's kube-node-lease renewTime, because
          Node status replayed from the persisted datastore reads
          Ready for a while whether or not the kubelet actually came
          back.
7. [x] Cluster time: the servers sync from NTP upstreams declared on
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
8. [x] The Cluster converges: the in-cluster Cluster resource was
       seed-only. Init read the image's cluster.yaml every boot, the
       operator seeded the API copy once, and nothing ever compared
       the two, so `kubectl edit cluster` changed a document no
       machine consulted. The Machine already has the whole lifecycle
       this needs (drift detection, durable staging on machineState,
       proven fallback, SpecConverged), so the Cluster document uses
       the same machinery, staged per machine and applied by the next
       boot. The convergence machinery is per-machine but the Cluster
       is cluster-scoped, so every machine stages its own copy,
       machines can transiently disagree about which Cluster spec
       they booted, and status makes that visible per machine.
       Fetching cluster config live at boot was considered and
       rejected: it's circular (the endpoint is inside the document
       being fetched), followers hold no API credentials, and it
       would make a leader outage block follower boots. Meanwhile the
       operator pod on every node already is a live, credentialed
       reader of the API; disk is just the crash-safe handoff from
       the runtime read to the boot-time consumer. This deliberately
       lands before HA leaders, because growing spec.leaders is
       precisely a Cluster edit and the HA milestone needs edits that
       converge. It also lands before GitOps, because git will own
       the Cluster document, and a document git owns must actually
       take effect.
   1. [x] The staging store generalizes: machine/staging.go operates
          on a directory instead of the hardcoded manifests/ path, so
          machine manifests stay at machineState's manifests/ and
          cluster manifests land beside them at cluster/, with the
          same four files, the same hashing, and the same durable
          writes. A memory-backed machine stays seed-only for both
          kinds, exactly as today.
   2. [x] Init selects the cluster manifest staged → proven → seed. A
          staged document that won't parse (or isn't kind: Cluster)
          is rejected at vetting. The boot record grows
          clusterManifestSource, clusterManifestHash, and
          clusterRejection next to the machine fields, through facts
          and the CRD schema as usual. This is deliberately simpler
          than the machine manifest's peek: the Cluster document
          doesn't drive storage, so by the time it's read,
          machineState is an ordinary mounted filesystem and no peek
          mount is needed.
   3. [x] Promotion, the genuinely new mechanism: the join itself is
          the proof. A machine manifest is proven by storage
          reconciliation within the boot, but a cluster manifest's
          failure modes are downstream (a bad endpoint means the
          follower never joins), so init can't prove it at settle time.
          Init boots a staged cluster document tentatively and writes
          an attempted marker (the staged hash). The operator promotes
          the document on its first reconcile pass and clears the
          marker; its own existence as a running pod proves that
          containerd, the kubelet, and the join all worked under this
          config. A boot that finds the marker still matching the
          staged hash knows the last try never got promoted: reject,
          fall back to proven. One proving boot is enough, the design
          is crash-only, and no boot counters are needed.
   4. [x] The operator's other half: read the Cluster resource every
          pass (RBAC already allows it; seeding stays create-only and
          the operator still never writes spec), render canonical
          bytes, compare against the boot record, and run the same
          decision table as the Machine: withdraw stale staged specs
          and clear rejections when current, hold on
          rejected-last-boot, stage drift and request a reboot per
          the Machine's spec.rebootPolicy (one knob governs both
          kinds of staging). A new ClusterConverged condition uses
          the same reason vocabulary; Ready rolls it up. There is
          deliberately NO fleet orchestration: a Cluster edit is
          drift on every machine at once, and with Auto everywhere
          that would be a simultaneous fleet reboot. Manual stays the
          default, pending reboots are visible per machine, and
          rolling coordination is milestone 13's job.
   5. [x] Guardrails: the five network-plan fields (nodeCIDR,
          clusterCIDR, serviceCIDR, clusterDNS, clusterDomain) become
          immutable-once-set via CEL oldSelf rules. k3s can't re-plumb
          any of them in place, so an edit there could never take
          effect, and the mismatch would only surface at a reboot.
          (oldSelf is correct here,
          unlike the storage rules of milestone 5.7: these facts can
          never be edited "back to reality," because their reality
          never changes.) leaders, endpoint, and time stay freely
          editable.
   6. [x] Drill it on the two-node lab: add a second NTP upstream via
          kubectl edit cluster and watch both machines stage, report
          RebootPending, apply on reboot, and show the new source in
          status.time with ClusterConverged True. Point the endpoint
          somewhere dead and reboot the follower: no join, no operator,
          no promotion, and the next boot rejects on the attempted
          marker and falls back to proven, rejoining the real cluster
          with the rejection visible in status. Then edit back to
          good and watch staged withdraw and the rejection clear with
          no reboot. All three drills ran as designed. The
          dead-endpoint boot was recovered by power cut, which is the
          only available recovery: a machine that never joined has no
          operator to request a reboot through.
9. [x] Multiple leaders: quorum for the control plane. The whole
       growth path is one Cluster edit converging: spec.leaders
       grows from [node-1] to three names, every machine stages the
       new document via milestone 8, and no separate "add a leader"
       mechanism exists. sqlite (via kine) serves one leader; more
       than one means embedded etcd.
   1. [x] Config derivation by leader count (init/k3s.go): one
          leader means exactly today's config, sqlite and no etcd
          keys, because single-node should stay cheap. More than one:
          the first entry in spec.leaders is the founding leader and
          renders cluster-init: true. On the migration boot, k3s
          migrates the sqlite datastore into embedded etcd in place,
          which is what made starting on sqlite safe rather than a
          dead end. Every other leader renders server: pointing at
          the founder, resolved from the fleet's Machine manifests
          the way time sources already are (reuse leaderAddresses).
          Followers are unchanged. Rejoins keep the same flags every
          boot (the founder keeps cluster-init, the rest keep
          server:), which is k3s's recommended steady state. The
          founding leader is a config-derivation role, meaning the
          first name in the list. It is not etcd's raft leader, which
          is elected and moves; the comment must draw that
          distinction.
   2. [x] The endpoint stays one explicit input. Followers use it for
          first contact only: after joining, k3s agents maintain a
          client-side load balancer that learns every leader, so a
          dead endpoint strands only new followers, never running
          ones (and time queries already bypass it, asking each by
          address). A VIP or DNS name is a deployment's choice to
          make; the manifest documents the tradeoff.
   3. [x] Quorum: three leaders, an odd number by design. No CEL rule
          on leader-count parity: growing 1→3 in one edit never
          passes through two, and a transient even state during some
          future migration shouldn't be refused at admission. A
          simultaneous all-leader reboot loses quorum transiently
          and reforms from disk; milestone 13's rollout now keeps
          Auto-policy leaders to one at a time regardless of budget.
   4. [x] The lab grows to five machines: three leaders (node-1
          founding, node-3 and node-4 fresh) and two followers (node-2
          staying, node-5 new). MACs, dist dirs, and manifests extend
          the existing NODE dimension, plus a MEM knob on the
          Makefile, since five 4G guests don't fit on a 30G laptop
          (followers run at 2G, leaders-to-be at 3G). All five came
          up Ready and Converged on the existing cluster in under a
          minute, node-3/4/5 booting as followers until the growth
          edit promotes them.
   5. [x] Drills: the 1→3 growth edit end to end (migration on
          node-1, two joiners, followers riding through it); kill one
          leader and watch the cluster keep serving while machine
          status reports the loss; then ATTEMPT follower→leader
          promotion on node-2 by growing spec.leaders to include it.
          k3s's tolerance for a same-name role flip was uncertain, so
          recorded findings were the deliverable, and node-2 could be
          rebuilt fresh if the flip failed. All of it ran, and the
          findings were better than feared. The growth edit converged
          machine by machine: node-1's migration boot moved sqlite
          into embedded etcd in place, node-3 and node-4 joined at
          the founder's address, and the whole thing was one kubectl
          patch plus per-machine reboot policies. Promotion of node-2
          (a follower since milestone 6, same name, same disks)
          worked cleanly: it rebooted straight into
          control-plane,etcd. Demotion turned out to be the rough
          edge: rebooting node-4 as a follower left its Node object
          claiming control-plane,etcd and, worse, left its etcd
          membership registered, a phantom member that would have
          broken quorum arithmetic on the next leader reboot.
          `kubectl delete node` triggers k3s's etcd member-removal
          controller, but a kubelet whose Node vanishes mid-run won't
          re-register, so the full demotion recipe was: Cluster edit,
          reboot, delete the Node object, one more boot. That recipe
          is now fully automated. The demoted machine's own operator
          notices its Node still claims control-plane, writes the
          reboot intent, and then deletes its own Node; the intent
          comes first, because the delete kills the very pod doing
          it. The next proven follower boot also purges the leftover
          etcd datastore, which etcd would otherwise refuse to let
          rejoin. The drill re-ran with no manual steps in both
          directions: promote node-4 with one edit, demote it with
          another. It also exposed a rejection-authority bug: the
          decision tables read the quarantine record from facts,
          which are frozen at boot, so a rejection cleared by a
          revert kept blocking a retry until reboot. The durable
          record on machineState is the authority now. The loss
          drills: with one leader dead the cluster kept serving and
          scheduling (quorum 2 of 3) and the revived leader rejoined.
          With ALL THREE leaders dead the API went dark while the
          followers' machines stayed up, and relaunching the leaders
          reformed etcd from disk; the cluster re-established itself
          and the followers reconnected without rebooting.
10. [x] Every state a fleet listing shows is now an enumerated word
        instead of a boolean. Machines carry `status.phase`, derived
        from the conditions on every pass: Ready, UpdatePending,
        Updating, Blocked, Degraded, Booting, Unknown, or Lost. (The
        Ready condition remains for `kubectl wait`; only its printer
        column went away.) The time boolean became `status.time.state`
        (Synchronized/FreeRunning/Unsynchronized, because free-running
        by design and unsynchronized by outage are different
        situations), and the convergence columns print the conditions'
        *reasons* (Converged, RebootPending, RejectedLastBoot, …),
        which say what kind of problem exists and what would fix it.
        The fleet also detects machines that have gone silent: every
        operator heartbeats `status.observedAt`, and the leaders run a
        fleet sweep (leaders, because a follower that can reach the
        API is by definition reaching a leader) that marks a silent
        machine Lost. The sweep is a safe multi-writer because it only
        touches machines whose own writer has provably stopped. It
        also publishes the Cluster's first status: a phase
        (Ready/Updating/Degraded) and a ready-out-of-total headcount
        ("4/5" in `kubectl get clusters`). A NodeHealthy condition
        mirrors the Node's Ready onto the Machine, catching the one
        gap the heartbeat can't: the operator lives on the host
        network and can keep reporting while the kubelet under it is
        dead. One state is deliberately not shown: quorum lost.
        Losing a leader majority takes the API down, and the status
        writer with it, so a frozen status is itself the symptom.
        Health checks surveyed and deferred: leaders cross-checking
        each other's clocks, etcd quorum margin as a Cluster condition
        (pairs with milestone 13), storage-capacity watermarks (a full
        machineState silently breaks staging), and the cluster-wide
        clock spread. The fleet sweep already reads every Machine's
        status, so it could publish max minus min of the reported
        time offsets on the Cluster, one number that says how well
        timekeeping is working across the whole fleet. Two findings
        came from the first lab run. First, the heartbeat created a
        feedback loop: the operator reconciles on every watch event,
        including the event its own status write causes, and a
        timestamp that moved every pass made every write real, so the
        operator wrote to the API server as fast as it could loop.
        Renewing observedAt on a cadence instead restores the no-op
        writes that let the loop settle. Second, three leaders
        sweeping at once worked (the verdicts are deterministic and
        optimistic concurrency serialized the writes) but filled the
        logs with 409s that were all noise. The sweep now runs under a
        coordination.k8s.io Lease, the same leader election
        kube-controller-manager uses to run hot standbys, built here
        from a GET and two conditional writes. A later idiom-review
        pass carried this further: the heartbeat itself moved out of
        status into a per-machine Lease in liken-machine-lease,
        kube-node-lease's arrangement, escaping the same write
        amplification. The whole API was also brought up to metav1's
        conventions: typed string vocabularies, conditions validated
        like metav1.Condition with observedGeneration, list-type
        annotations, admission patterns on spec strings, Cluster
        conditions beneath its phase, watch bookmarks, and a coverage
        gate ratcheted past half.
11. [ ] Explore device management: how does a shell-less, udev-less OS
        expose `/dev` beyond the basics: USB devices arriving after
        boot, GPUs, serial adapters? devtmpfs gives us the nodes, but
        hotplug means fielding kernel uevents and loading modules,
        which is the job udev does elsewhere. Then the Kubernetes half:
        how workloads get to the hardware (device plugins, dynamic
        resource allocation) and whether devices belong in
        `status.hardware` alongside CPUs and memory.
12. [x] Declarative upgrades: one field on the Cluster moves the whole
        fleet to a new liken version. `spec.version` names the target.
        It lives on the Cluster, not the Machines, because a fleet
        should be retargeted in one edit; a machine's version belongs
        in its status, where reality is reported, not in its spec.
        Machines that aren't running the target download the release,
        write it into the boot slot they are *not* running from, and
        reboot into it through milestone 13's rollout: workers first,
        leaders one at a time, with no human supervising. For a
        two-file OS an upgrade really is "replace vmlinuz and
        liken.cpio and reboot", which makes A/B slots and
        roll-back-on-failed-boot the natural shape. It also finally
        answers the bootloader question QEMU's `-kernel` flag has been
        deferring, and the answer is that there is no bootloader: the
        kernel is already an EFI executable (the stub), UEFI
        firmware's own boot menu picks a slot, and "try the new one
        once, fall back if it fails" is the firmware's BootNext, so
        fallback is handled by the firmware itself rather than by
        software. Trust stays where liken's trust already lives, in
        explicit inputs: the Cluster carries a release source URL and
        a catalog mapping each version to the digest of its release
        manifest, which in turn carries every artifact's sha256. The
        verification chain runs from the API to the catalog digest to
        the manifest to the bytes, with no signatures until the
        mastery tier. The target and catalog are read live and never
        count as cluster-document drift (the sysctls precedent: a
        catalog append must not stage a fleet-wide reboot). Promotion
        mirrors the cluster document exactly: boot the staged slot
        tentatively (attempted marker + BootNext), let the operator's
        first reconcile be the proof, and let init re-assert BootOrder
        from the durable record every boot. machineState is the
        authority; the firmware is a cache of it.
    1. [x] The slot vocabulary and the FAT32 formatter: `systemA` and
           `systemB` join the storage roles at the head of the
           canonical order (the firmware is the earliest reader of any
           partition liken owns), typed in the GPT as EFI System
           Partitions. They are the first roles whose partition type
           isn't "Linux filesystem data", because the type GUID is
           precisely how firmware recognizes a candidate. Formatting
           is a hand-written FAT32 formatter in the same style as the
           GPT writer (a boot sector describing the geometry, two
           copies of the allocation table, a root directory that is
           just cluster 2), because FAT is the only filesystem
           firmware promises to read. The kernel's vfat module handles
           file I/O afterward, which moves module loading ahead of
           storage settling in init, since some roles' filesystems are
           now modules. Prove it: a node with a blank third disk
           claims both slots into status.storage, and a file written
           to a mounted slot survives a reboot. Proven on node-5 via a
           live Machine edit that rode the milestone-13 rollout: the
           machine reported AwaitingTurn, was granted a reboot,
           printed the claim and both FAT32 formats to the console,
           and reported both slots Partition-backed at 512Mi. The
           power-cut drill taught the milestone's next lesson early:
           an *unsynced* file written seconds before a power cut was
           gone afterward, because FAT has no journal and the page
           cache guarantees nothing, while a synced file survived two
           power cuts intact. The download step's fsync-and-reverify
           design exists for exactly this reason.
    2. [x] Speaking EFI: init mounts efivarfs when the firmware is
           UEFI and reads the boot variables. Each is a small binary
           format (EFI_LOAD_OPTION: attributes, a UTF-16 name, a
           device path ending at a file on a partition, and free-form
           arguments that are exactly a kernel command line),
           unit-tested against known-good bytes, with the immutable
           flag the kernel puts on every variable handled in one
           helper that prints what it does. The lab gains OVMF: real
           UEFI firmware, split as read-only code shared by every
           guest plus a per-node writable variable store. The store is
           the equivalent of a motherboard's CMOS, where boot entries
           live and survive reboots; `make clean` removes it, so a
           factory reset clears firmware memory too. Prove it: the
           console and Machine status report the firmware,
           BootCurrent, and a decoded BootOrder (console parity as
           always). Proven on node-5 under OVMF: efivarfs mounted,
           mode UEFI, an accurate "BootCurrent not set" for the
           direct-kernel boot, and OVMF's own default entries decoded
           by name into status.firmware. The codec also decoded every
           real entry on a physical laptop's firmware, including
           vendor-only ones. The EFI stub's initrd= argument is
           deprecated upstream but still shipped; it gets verified at
           the installer's first from-disk boot, the first moment a
           file-path boot exists to test, and nothing beyond that
           boot builds on it untested.
    3. [x] Self-install, in the shape of a USB stick: `make install
           NODE=x` boots via -kernel one last time, with QEMU standing
           in for an installer stick or a PXE server. Init, seeing
           liken.install, claims the boot disk, verifies the release
           payload the installer carries, copies it into slot A,
           writes both boot entries and BootOrder, and powers off.
           install.cpio is liken.cpio with a second archive
           concatenated on, carrying vmlinuz, liken.cpio, and
           release.yaml. The kernel unpacks concatenated cpios (the
           same mechanism early microcode updates use), so the
           installer's payload is byte-identical to what the digest
           chain describes. Each entry's baked command line carries
           the machine's name, its slot, and panic=10. The panic
           setting matters because a panicking trial kernel must
           reset into the firmware's fallback rather than hang. `make
           run` becomes firmware-from-disk: no -kernel, no -append.
           (`run-once` keeps direct boot, because its oneshot knob
           can't be passed through a baked entry.) Prove it: a fresh
           node installs and boots to Ready from disk; killing QEMU
           mid-install and re-running converges, since claiming skips
           claimed disks and copying re-verifies. Proven on node-5:
           the installer verified and copied both artifacts, wrote
           Boot0002/Boot0003, and the firmware boot came up "booted
           via Boot0002 (liken slot A)" with the baked command line
           intact, rejoining the cluster Ready. That also answered
           milestone risk 3: the EFI stub's initrd= argument works
           under OVMF. A second install run converged onto the same
           entry numbers with everything re-verified in place.
           BOOT=disk is a knob rather than the default until step 8
           migrates the fleet.
    4. [x] The releases domain and the API: `make release VERSION=x`
           rebuilds init, operator, and image with the overridden
           version stamp into a separate build tree (the domain
           Makefiles learn overridable version and output knobs; the
           everyday dist/ trees are never touched) and publishes
           releases/dist/<v>/: vmlinuz, liken.cpio, install.cpio, and
           release.yaml listing every artifact's sha256. The digest a
           catalog carries is the sha256 of that file's exact bytes.
           `make serve` is a small logged file server the guests reach
           at the host's NAT address, the lab's stand-in for a release
           host on the internet. The Cluster grows spec.version and
           spec.releases (source plus catalog). CEL holds the target
           to catalog membership at admission; this is a same-object
           check, so it can never wedge the way the storage rules once
           did. A machine with no slots reports NoSystemSlots rather
           than claiming it can comply. The fleet sweep computes
           status.releases.newest (a hand-written semver comparison),
           and the printer columns report it plainly: the Cluster
           shows the target VERSION and the NEWEST the catalog offers,
           while each Machine's LIKEN column shows the version it is
           actually running. Prove it: an edit whose target names no
           catalog entry is refused at admission, and `kubectl get
           clusters` shows target versus newest at a glance. Proven on
           the lab: 0.1.0 and 0.2.0 published with the stamp carried
           through the init binary, the operator image's name, and the
           DaemonSet's pin, with the everyday dist/ trees untouched.
           Setting
           spec.version with no catalog was refused at admission, the
           catalog entry plus target landed in one edit and the
           VERSION column showed 0.1.0, and a bogus 9.9.9 target was
           refused even with a catalog present. `make corrupt` flipped
           one byte and the published digest check failed exactly as
           designed. The publish reuses the install payload's own
           release.yaml, so the stick's document and the server's are
           byte-identical. NEWEST stays blank until a leader runs this
           build's operator, since the sweep is what writes it; the
           fleet migration in step 8 delivers that.
    5. [x] The download: an asynchronous fetcher in the operator,
           because a blocking 116MB GET inside a reconcile pass would
           starve the heartbeat and read as a death (milestone 10's
           lesson, made structural). It streams each artifact through
           sha256 into the inactive slot, writes temp-and-rename, and
           resumes by re-verification: FAT has no journal, so a torn
           download is just files that fail their hashes and are
           fetched again. Nothing is ever staged until every byte on
           the slot verifies against the catalog's chain. Downloading
           and DigestMismatch join the condition vocabulary. A down
           server is transient by definition, so the fetcher retries
           forever with the reason in the condition message; a wrong
           digest is Blocked until the catalog itself changes, and is
           never staged. Prove it: the serve log shows the fetch;
           killing the server mid-download and restarting it
           converges; a deliberately corrupted publish holds at
           DigestMismatch with nothing staged. Proven on node-5,
           which also learned to report which slot it booted from:
           liken.slot= now lands in status.boot.slot, and downloads
           aim at the other one. The down-server drill surfaced a
           real bug: a failed fetch restarts on the next pass, so the
           Failed state lived only between passes and the condition
           forever said "starting". Now the retry carries the
           previous failure's message, and the drill read "retrying
           after: connection refused". The corrupted 0.1.1 fetched
           once in full, held at DigestMismatch/Blocked with the
           recovery spelled out (publish a corrected release under a
           new version), and never touched the network again.
           Retargeting clean 0.2.0 cleared the hold and converged as
           "1 artifacts fetched, the rest already verified in place":
           the two releases share a kernel, so resume-by-verification
           was exercised without a dedicated drill. The drills also
           taught two mixed-fleet lessons for step 8's migration: a
           leader's k3s restart re-applies the CRDs and DaemonSet
           baked into *its* image, which pruned the new fields
           fleet-wide and left node-5's operator pod without its
           slots mount until the new manifests were re-applied by
           hand. The schema is part of the OS image, so a fleet
           upgrade is also a schema upgrade.
    6. [x] The proving reboot: a verified download becomes a staged
           record in a third staging store, system/ beside manifests/
           and cluster/ on machineState, with the same four files and
           the same durable writes. Init's reboot path finds it, writes
           the attempted marker and the firmware's BootNext (boot the
           other slot, once), and reboots. The proving boot recognizes
           itself by liken.slot=. The operator's first reconcile
           promotes the record, since an operator running as a pod
           proves that the new kernel, init, k3s, and the join all
           work. Init flips BootOrder when promotion lands, and
           re-asserts it from the durable record on every boot
           thereafter. Every power-cut gap in that timeline boots
           something proven. Prove it: one Cluster edit upgrades one
           node, the LIKEN column flips, BootOrder prefers the new
           slot, and a plain reboot stays on the new version. Proven
           on node-5, where one catalog edit ran the whole chain
           unattended: the download, the staged record, the rollout
           granting its turn, the drain, "BootNext armed at Boot0003
           ... once", the proving boot on slot B, promotion by the
           0.2.0-stamped operator's first pass, and the LIKEN column
           flipping to 0.2.0. A power cut landed by accident in
           exactly the gap the design worries about, after promotion
           but before the BootOrder flip, and the machine recovered on
           its own: it booted the old slot, re-staged, re-proved, and
           a deliberate power-cycle after that came up directly on
           slot B. One anomaly to watch in step 7's drills: the
           old-slot boot's BootOrder repair didn't visibly fire. Its
           early returns printed nothing; they now print their
           reasons, so a recurrence will explain itself. The catalog
           digest also proved worth including in the record's
           identity: re-cutting 0.2.0 changed the digest, and the
           machine held at DigestMismatch until the catalog was
           updated to match. The API, not the server, decides what
           runs.
    7. [x] The fallbacks: a proving-boot watchdog in init, armed only
           when the running slot's record is still staged and disarmed
           by promotion, with a ten-minute timeout (the fleet's
           established RolloutStalled number). It reboots a machine
           that came up but never settled, and the already-consumed
           BootNext lands that reboot back on the proven slot, where
           the attempted marker records the failure: RejectedLastBoot,
           no reboot loop, cleared by the next version edit. A kernel
           that panics outright reaches the same outcome through
           panic=10, with no software involved at all. Prove it with
           two fault releases: one built to panic immediately (the
           firmware-fallback drill) and one built to wedge k3s (the
           watchdog drill), both ending Ready on the old version with
           the rejection visible in status. Proven on node-5, but only
           after the drills exposed the milestone's deepest bug: the
           fallback they depend on had never actually existed.
           BootOrder had never once been rewritten after install,
           because promotion had never once happened. The cluster's
           DaemonSet, applied by the old leaders, pins the old
           operator image, so every proving boot ran the *old*
           operator, which rightly refused to promote a record that
           didn't match its own version stamp. The convergence
           tidy-up, which judged by init's version, then read the
           machine as converged and withdrew the trial's staging
           records. And the proving watch treated the staged file's
           absence as promotion. The first panic drill therefore
           looped: 41 panics, with the fallback aimed at the panicking
           slot, exactly the loop this step exists to forbid. Three
           fixes at the root: promotion now judges
           facts.version.liken, the version of the OS that actually
           booted, so the operator pod's own version is irrelevant;
           the proving watch flips BootOrder only when the proven
           record matches its own trial; and withdrawal clears the
           attempted marker. Arming is also hardened: fallbackInPlace
           re-asserts BootOrder and verifies it by readback before any
           trial arms. The re-run went cleanly: promotion printed its
           steps, "BootOrder now leads with Boot0003" was confirmed in
           the NVRAM file itself, and a power-cycle booted slot B on
           the first try. Then the panic release panicked exactly once
           and fell back, and the wedge release sat unpromoted for its
           ten minutes before the watchdog rebooted it onto the proven
           slot. Both drills ended on the old version,
           RejectedLastBoot, phase Blocked, serving the cluster the
           whole time, and a retarget edit cleared each rejection.
           Machines report the standing rejection in
           status.boot.systemRejection.
    8. [x] The fleet: migrate the five-node lab to disk boot, then
           run the full drill: one Cluster edit (a catalog append and
           the target bump) rolls all five machines through milestone
           13's rollout, with the cluster serving throughout and
           `kubectl get machines -o wide` showing the walk from
           AwaitingTurn to Ready on the new version. Migrating the
           pre-slot lab in place lost out to a rebuild: the old
           builds' operators couldn't even see the slot roles in
           their specs, and every old leader's k3s restart re-applied
           its baked CRDs, pruning the new schema fleet-wide. So the
           lab was factory-reset and every node took the designed
           path instead, one `make install` and a firmware boot each,
           and five machines were Ready in ninety seconds. The first
           full drill then found the milestone's last real bug: the
           operator's DaemonSet pinned a versioned image, so the
           first upgraded leader's manifests rolled a 0.2.1 pod onto
           a node still running 0.1.0. With imagePullPolicy: Never,
           that pod could not start, and the rollout had just killed
           the one operator the machine needed to drive its own
           upgrade. The machine was deadlocked by its own update
           mechanism. The fix makes the operator pod genuinely part
           of the OS: every release tags its build
           liken.sh/operator:installed, so one unchanging pod spec
           resolves per-node to that node's own baked image; the
           DaemonSet updates OnDelete, so applying manifests never
           kills a pod; and the sweep leader's pod steward refreshes
           each machine's pod only after its upgrade lands
           (operator/steward.go documents the design). The proof was
           the 0.2.3 drill: one patch, zero manual actions. Five
           machines walked workers-first through the rollout in under
           four minutes, every one flipping to its inactive slot, and
           the steward refreshed all five operator pods behind them.
           A power cut afterward booted straight to the proven slot
           via its firmware entry, which verified the boot path
           itself and not just the outcome.
13. [x] Rolling reboots at the *cluster* level: the fleet applies
        staged changes without a human supervising it, one machine at
        a time. (This milestone was written as "rolling upgrades,"
        but the sequencing turned out to be independent of what the
        reboot applies: a config change today, a version upgrade once
        milestone 12 exists. The machinery is the same either way.)
        On a cluster member, rebootPolicy: Auto now means "reboot
        when the cluster says it's safe": the machine stages its
        change, publishes AwaitingTurn, and waits for the sweep
        leader (already elected, already reading the whole fleet) to
        grant its turn by writing a RebootApproved condition onto the
        Machine, the way the scheduler owns PodScheduled on Pods it
        doesn't manage. The budget is one field,
        spec.disruption.maxUnavailable (default 1), a machine-level
        PodDisruptionBudget reduced to one number. It counts
        unplanned trouble too, so a fleet that is already degraded
        pauses its own rollout, and the leaders have an automatic
        floor no budget can raise: one leader down at a time, because
        quorum depends on a majority of members and no budget setting
        changes that. A granted machine drains itself first: it
        cordons its own Node, evicts everything movable through the
        Eviction API so workloads' own PDBs hold, and then writes the
        reboot intent. The drain proceeds incrementally across
        reconcile passes, since a blocked pass would stop the
        heartbeat and read as a death. The machine uncordons itself
        after converging; a human's cordon stays put. The sweep
        treats silence from a granted machine as the reboot it asked
        for, and a machine that never returns flips the Cluster's
        Progressing condition to False/RolloutStalled (Deployment's
        vocabulary) and halts granting until someone looks.
        Demotion reboots join the same queue. Still owed, someday:
        workload-aware ordering; a drain that waits on more than a
        deadline when a PDB can never be satisfied; and strict
        workers-first ordering at rollout start. Today the order is
        among machines that have *asked* by sweep time, so a leader
        that stages quickly (the sweep leader itself, whose sweep
        runs in the same pass as its own staging) can take the first
        turn before a slower worker has asked. That is safe either
        way; one leader at a time holds regardless.
14. [ ] GitOps from first boot, without baking an engine into the OS.
        This is deliberately deferred: git-driven delivery is one way
        to feed this system, not its core mode. The Machine and
        Cluster resources are the real interface, and everything
        above works without a repo in the loop. When it lands, the OS
        grows two generic primitives rather than Flux support. The
        first is a seed channel: manifests delivered alongside the
        Machine manifest land in k3s's auto-manifests directory,
        applied at first boot and owned by the repo afterward, which
        needs the same staged/promoted handling the Machine manifest
        gets. The second is a minting primitive: the machine creates
        an SSH keypair in a Secret if one is missing and publishes
        the public half in status and on the console, so the user
        registers a deploy key at the forge without ever handling
        private material. (The key may be read-write, since
        image-update automation will eventually commit tag bumps back
        to the repo.) Flux itself is delivered content, not a
        vendored domain: its install manifest and sync objects ride
        the seed channel, that first apply does what `flux
        bootstrap`'s CLI would do, and Flux self-manages from the
        repo afterward. That is the standard pattern, and another
        engine could ride the same channel. This is also where the
        question of manifest authority resolves: git wins, and the
        seeded Machine and Cluster copies are downstream of it.
15. [x] Observability for everything below Kubernetes. The kernel,
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

Deferred until the fundamentals above are proven, the
public-consumption tier:

* Public bootstrapping: today the identity bundle is minted by make in
  a private checkout, but a released OS needs a way for anyone to mint
  theirs: an installer step, a tiny CLI, or a documented openssl
  recipe.
* The mastery tier: UKIs, dm-verity, secure boot, TPM-sealed secrets.

# Open problems

Questions we know we owe answers, without pretending to have them yet:

* **Claiming unknown machines.** `liken.machine=` identifies machines
  someone declared ahead of time. The deferred half is the machine
  nobody declared: a Machine template carried on the Cluster that an
  unknown node claims on first boot. It would be named from a hardware
  fact (probably its MAC, the one identity the network already forces
  to be unique) and addressed from a pool (probably by ARP-probe
  claiming, in the same spirit as storage claiming: probe reality,
  take what's free, refuse ambiguity). This waits until the
  declared-machine flow is proven.
