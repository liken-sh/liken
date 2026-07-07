# The rough path

1. [x] Boot to a hello world from an init I built: `make run` boots QEMU
       and PID 1 speaks on the serial console. (There is no shell and no
       prompt — the console is output-only, by design.)
   1. [x] `kernel/`: vendor a pre-built vanilla kernel from Ubuntu's
          mainline builds — fetch a pinned version, verify checksums,
          extract the image and modules, run `depmod` at build time.
   2. [x] `init/`: a minimal Go init that mounts `/proc`, `/sys`, and
          `/dev`, prints a report of the world it woke up in, and reaps.
   3. [x] `image/`: assemble the initramfs — a cpio archive; the whole OS
          is `vmlinuz` plus `liken.cpio`.
   4. [x] `make run`: boot it headless in QEMU; a smoke test can watch the
          serial output for a marker, which is CI in embryo. Use explicit
          flags (`-display none -serial stdio -monitor none -no-reboot`)
          rather than the `-nographic` bundle, so each flag can explain
          itself.
2. [x] Init starts k3s and nothing else — and discover every host dependency
       (cgroups, kernel modules, time, entropy) the hard way.
   1. [x] Boot to network from a Machine manifest (`kind: Machine`,
          `apiVersion: liken.sh/v1alpha1`, file-delivered at boot): raise
          the interface, speak DHCP (the whole DORA exchange prints to
          the console), apply the lease via netlink, and prove it with a
          DNS lookup against an outside nameserver. (Entropy was the
          predicted hard-way discovery: no RDRAND → kernel RNG never
          initializes → getrandom() blocks forever in the DHCP client.)
   2. [x] Boot to a Ready node: init builds the world k3s assumes
          (cgroup2, identity files, mount propagation, module preload,
          the shell-less iptables story), supervises it with backoff,
          and narrates node and pod state to the console. `make run`
          ends at a settled single-node cluster; `make run-once`
          (`liken.oneshot`) makes any k3s death power the machine off
          so a harness can measure the boot.
   3. [x] Machine identity is an input, not an output: `make` mints a
          CA bundle (gitignored, identity/) and pre-seeds k3s's TLS
          directory in the image, so an operator's kubeconfig is
          computed offline — never copied off the machine. `make
          kubeconfig` plus a loopback-only QEMU port-forward gets
          `kubectl get nodes` from the host; no `--tls-san` needed,
          since k3s's serving cert covers 127.0.0.1 by default. (The
          hard-way discovery: kube-apiserver reads the ServiceAccount
          key with a parser that takes SEC1 "EC PRIVATE KEY" but not
          PKCS#8.)
   4. [x] The Kubernetes API is the machine API: the Machine manifest is
          now a real CRD (`kubectl get machines` works), reconciled by a
          liken operator that ships inside the initramfs as a
          hand-assembled OCI tarball (operator/image.sh) and deploys
          through k3s's auto-manifests directory — zero registry pulls,
          zero kubectl steps. Init never talks to k3s: it applies
          spec.sysctls at boot and writes facts to `/run/liken/`; the
          operator (plain net/http against the API server — no
          client-go, the watch loop is the lesson) seeds the Machine
          from the manifest, publishes facts + observed sysctls into
          status, and reconciles sysctl edits live. The shared Go types
          live in the machine/ domain, used by both programs. (A
          leftover mystery got mostly solved on the way: "The manifest
          file is empty, ignoring." fires once per embedded
          control-plane component as it parses its options — unrelated
          to k3s's manifests directory.)
3. [x] Unwind the known hacks before building on top of them. These are
       fixes from the boot-to-k3s work that encode knowledge k3s never
       promised us; each works today and is guarded by the version pin
       + `make run-once`, but every milestone below stacks more weight
       on the boot path, so the coupling gets settled first.
   1. [x] Init's PATH hardcoded k3s's internal layout — it was indeed
          redundant. Removed; the console shows k3s prepending its own
          unpacked userland to the PATHs it builds for children, and
          the cluster settles without the tail.
   2. [x] The `/sbin/iptables` dangling symlinks are gone: the
          netfilter userspace is now its own vendored domain
          (`xtables/`), fetched from k3s-root — the same project that
          builds k3s's bundled copy, pinned to the same version the
          vendored k3s uses — so `/sbin/iptables` is a real static
          binary from the image build onward. The machine also reports
          its xtables version in the Machine's status.version, observed
          via `iptables -V` like every other fact.
   3. [x] switch_root onto a plain tmpfs early in boot, the way k3OS
          did, so the root filesystem is an ordinary measurable mount
          instead of the kernel's magic initramfs rootfs. This let us
          drop `local-storage-capacity-isolation=false` entirely and
          silenced kubelet's recurring filesystem-stat errors — kubelet
          now measures / like any other machine's.
   4. [x] The CA bundle came from whichever machine ran the build
          (build.sh's own comment confessed it). Now vendored like
          everything else: a `trust/` domain pinning a dated snapshot
          of the Mozilla bundle, so what the machine trusts is a
          reviewable version bump instead of a build-host accident.
4. [x] Storage — which starts with a disk existing at all. The whole
       machine is RAM today; the prize is k3s's state on persistent
       storage (container images stop re-importing every boot, cluster
       state survives a reboot). Storage is declared by *purpose*, not
       by mount path: `spec.storage` is a map keyed by singleton role
       (`clusterState` first), each entry naming a device and an
       optional fixed size. liken derives GPT partition tables from the
       roles grouped by device, formats blank disks at runtime, and
       names each partition `liken:<role>` — so recognition on every
       later boot is by partition name read from sysfs (no udev;
       `device:` is first-boot claiming input only, since kernel
       enumeration order is not a promise). Reconciling never destroys
       data — blank → claim, ours → mount, anything foreign or
       ambiguous → refuse — and never serves on a broken promise: a
       declared role that can't be reconciled stops the boot (the full
       story on the console, then power off, never k3s), because a
       machine promised persistent cluster state that boots ephemeral
       anyway is a data-loss machine; down is recoverable, wrong is
       not. Undeclared roles simply land where everything lands today
       — the root tmpfs — and `status.storage` enumerates where every
       role actually landed (`Partition` or `Memory`), while
       `status.hardware.blockDevices` reports the raw inventory.
   1. [x] A disk exists: `make run` attaches a gitignored qcow2, and
          init discovers block devices from `/sys/block` — the world
          report learns a new question.
   2. [x] Claiming: init writes the GPT itself (it's a small,
          checksummed binary format — a good lesson), makes the
          filesystem (the one open mechanism: the image has no libc,
          so mkfs must be static or Go), and mounts `clusterState`
          under k3s's world, all before k3s starts. Every reason a
          spec can be refused (foreign disks, cloned disks, disks too
          small, partial claims) is unit-tested in init/, against
          fake sysfs trees; a refusal halts the boot from one place
          in main.go.
   3. [x] Prove persistence: boot, power off, boot again — images
          import once, the cluster comes back. (Proven by milestone
          5's reboot cycles: the cluster survived staged-spec reboots
          and a hard power cut, on the same disks.)
   4. [x] The API: `spec.storage` and `status.storage` in the Machine
          CRD, the operator publishing the landing table and the
          hardware inventory.
5. [x] The spec becomes editable: a Machine edit in the cluster
       actually converges, by reboot. The roles speak for their owners
       now (`machineState` and `machineEphemeral` are the machine's,
       `clusterState` awaits `kind: Cluster`), and the new
       `machineState` role holds the machine's manifests: the operator
       detects drift between the cluster's spec and the boot's
       boot record, validates against the machine's reality
       (grow-only sizes, attached devices; CEL rules refuse shrinks at
       admission), stages the manifest durably, and per
       `spec.rebootPolicy` requests a reboot or reports one pending.
       Init prefers the staged manifest, promotes it on success, and
       falls back to the proven last-known-good on failure, so a bad
       edit degrades the machine instead of downing it. Partitions are
       grow-only: sized roles grow into free space, remainder roles
       follow a grown disk (relocating the backup GPT), and ext4 grows
       by ioctl, no resize2fs.
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
          `kubectl replace --force` — untenable once Flux owns the
          spec. The rules now compare the spec against
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
       server CA that identity/ already mints — so make computes the
       whole token offline and bakes it into the identity bundle.
       The spec carries topology; the identity bundle carries secrets;
       nothing is ever extracted from a running machine (which a
       machine with no shell could never hand over anyway). Machines
       get static addresses declared in their manifests, and a machine
       finds its own manifest by `liken.machine=<name>` on the kernel
       command line — the one channel the bootloader already owns. One
       boot medium carries cluster.yaml and a manifest per machine
       (node-1.yaml, node-2.yaml, ...), so a single image boots the
       whole fleet. Which fleet is a *deployment's* decision, not the
       OS's: the manifests are an input to the image build, and the
       repo's own deployment is the dev-cluster/ domain — the
       manifests and the QEMU guests that boot them, together.
   1. [x] The Cluster CRD (cluster.yaml, file-delivered like the
          Machine manifest and seeded by the operator, every node's
          operator racing to create it and the losers' 409s reading
          as success): spec.leaders names the machines that run
          control planes, and spec.network holds the facts k3s
          requires every node to agree on (cluster CIDR, service
          CIDR, cluster DNS, cluster domain) — cluster-scoped truths
          that masquerade as per-node flags — plus nodeCIDR, the
          subnet nodes address each other on. A machine's role is
          derived, not declared: am I named in spec.leaders?
          Promoting a follower is then a Cluster edit, not a
          coordinated pair of Machine edits.
   2. [x] The token joins the identity bundle: mint.sh hashes the
          server CA, appends a random secret, and writes the token
          next to the TLS material (idempotently — re-running mint.sh
          fills gaps but never replaces an identity machines already
          carry). It rides at /etc/liken/token, outside k3s's data
          directory, because the clusterState filesystem mounts over
          that; init hands k3s the *path* (token-file), so the secret
          never appears in a config file or on a command line.
   3. [x] Static networking: spec.network grows an interfaces list
          (name, address in CIDR form, optional gateway and
          nameservers; no address means DHCP, and an empty spec still
          means DHCP on the first real NIC). This was an open
          problem; the lab forced it onto the critical path, because
          the shared segment joining two QEMU guests is a dumb wire
          with no DHCP server on it. Each machine runs two
          interfaces: a DHCP uplink and the statically-addressed
          cluster segment, and the Cluster's nodeCIDR is what picks
          which address becomes the node IP (k3s left to itself picks
          the default-route interface — the uplink, exactly wrong).
   4. [x] liken.machine=: init reads its name from the kernel command
          line and selects its seed from the manifests the image
          carries; after first boot, machineState carries the proven
          manifest forward exactly as before. Selection refuses to
          guess: a name matching no manifest, or many manifests with
          no name, powers the machine off with the story on the
          console (a first boot under the wrong identity could join
          the wrong cluster or claim another machine's disks; wrong
          is worse than down). A cluster manifest that won't parse is
          fatal the same way: a machine that can't tell if it's a
          leader must not guess, because guessing "leader" starts a
          rival control plane.
   5. [x] The lab grows a node dimension: per-node dist directories,
          MACs, and command lines. Two NICs per guest — user-mode NAT
          stays as each guest's internet uplink, and a multicast
          socket segment (no root, no bridges: every QEMU naming the
          same group is one virtual hub) is the wire the cluster
          speaks over. The API-server hostfwd lives on the leader
          node only. Two terminals (`make run`, `make run
          NODE=node-2`), two serial consoles, side by side. (The
          quiet supporting discovery: k3s reads drop-in config from
          <config>.yaml.d/, so the image's static files stay
          untouched and init writes only a boot.yaml drop-in of
          derived facts — and followers need their own config file
          entirely, because `k3s agent` refuses leader-only keys.)
   6. [x] Prove it: `kubectl get nodes` shows two Ready nodes,
          `kubectl get machines` shows a leader and a follower with
          their segment addresses, `kubectl get clusters` shows the
          topology, a pod pinned to the follower runs with a
          cluster-CIDR address and resolves cluster DNS across the
          VXLAN, and both machines come back from a power cut booting
          their Proven manifests, still remembering the cluster and
          the pod. (The hard-way discovery: on first join, k3s mints
          each node a "node password", records it server-side, and
          demands the same one on every reconnect — it's what stops a
          stranger from registering as an existing node. k3s keeps it
          at /etc/rancher/node/password, which on liken is the RAM
          root, so a rebooted follower knocked on its own cluster's door
          with a fresh secret and was refused. The password is
          machine identity, so /etc/rancher/node is now a symlink
          onto machineState — and the honest way to verify a re-join
          is the node's kube-node-lease renewTime, because Node
          status replayed from the persisted datastore reads Ready
          for a while whether the kubelet came back or not.)
7. [x] Cluster time: the servers sync from NTP upstreams declared on
       the Cluster — declared, never defaulted, because a distro that
       ships pool.ntp.org as a default volunteers every deployment's
       machines to a volunteer service without asking — and serve
       time to the rest of the fleet, so followers need no internet
       access at all. Followers ask the leaders themselves — every one
       of them, resolved from the Machine manifests the image already
       carries, with the endpoint's host as the fallback for leaders
       that declare no address. There is no discovery mechanism, on
       purpose; every hop in the hierarchy is somebody's explicit
       input. The client uses a vendored library
       (beevik/ntp — what Talos uses, the same call as the DHCP
       client: take the blessed protocol library, teach the protocol
       in the comments), while the respond-from-my-own-clock server
       on the leaders is written by hand, a 48-byte format in the same
       genre as the GPT writer. The client runs before k3s starts, because TLS
       fails on a skewed clock: a machine with bad time can't even
       join the cluster it means to serve. (Deliberately ahead of
       multiple leaders: it needs only the topology milestone 6
       built, the lab can fake a broken clock with QEMU's -rtc base=,
       and etcd — coming next-plus-one — is the first component in
       the stack that genuinely cares how clocks behave.)
   1. [x] The precedent, written down before it's built on: liken has
          two planes and no third. Machine-plane concerns are
          goroutines in init; workload-plane software runs under k3s;
          k3s is the only child process init supervises. Admission to
          the machine plane is strict: a concern belongs in init only
          when k3s depends on it to exist — anything the cluster
          could host for itself belongs in the cluster. Time
          qualifies only because a machine with a skewed clock fails
          TLS and can't join; a concern without that kind of claim
          gets pushed in-cluster, not adopted by init. Init grows a
          small component framework — each loop is a `Run(ctx) error`,
          started by a supervisor that recovers panics and restarts
          with backoff, stopped by context cancellation and awaited
          with a bounded timeout so a stuck loop can't hang a reboot —
          and the loops init already runs informally (reaper, reboot
          watcher) become its first registered components. Shutdown
          runs the dependency stack in reverse: stop k3s, cancel the
          machine plane, unmount, reboot. The escape hatch is part of
          the precedent: a component earns promotion to a child
          process (the same binary re-exec'd, busybox multi-call
          style — one artifact, still one program to read) only when
          it parses untrusted network input, needs fewer privileges
          than PID 1, or must not take the machine down when it
          fatals; the time responder is the first named candidate,
          promoted in a hardening pass, not now. All of this lands in
          init's package documentation.
   2. [x] The API: `spec.time` on the Cluster (the upstream list —
          empty is legal and means the fleet free-runs), and
          `status.time` on the Machine (synchronized, source, stratum,
          offset, lastSync) under the console-parity rule: whatever
          init prints about time also reaches the cluster. A
          free-running fleet agrees with itself but not the world —
          fine until something checks a certificate's notBefore
          against a clock that never met one, so status must make
          free-running visible rather than dress it up as synced.
   3. [x] The discipline loop, one goroutine on every machine:
          measure with beevik/ntp (the four-timestamp exchange and
          why symmetric delay cancels belongs in the comments), step
          the clock once at boot before k3s starts, then only ever
          slew (adjtimex) for the life of the machine — stepping a
          running node yanks time out from under lease renewals and
          etcd heartbeats, so the one step happens while nobody is
          watching the clock yet. Sources differ by role: leaders ask
          the declared upstreams; followers ask every leader, resolved
          from the image's Machine manifests, with the endpoint's
          host as the fallback. Failure is humble: bounded attempts
          at boot, then keep trying forever, never touch the clock on
          bad data, never block the boot.
   4. [x] The responder, a second goroutine on leaders only: hold UDP
          :123, answer each 48-byte query from the machine's own
          clock — a responder, not a proxy; the lead serves the clock
          its discipline loop maintains and never forwards a query
          upstream — advertising stratum upstream+1 when synced and
          the local-clock convention (~10) when free-running, so
          followers can always tell pedigree from confidence.
          Followers run no responder: nothing in the design ever asks
          a follower for time, and a shell-less OS should have no listener without a
          caller. (The known wrinkle, owed to milestone 9: when the
          endpoint becomes a VIP or load balancer for HA, UDP 123 may
          not ride along, and followers may want the leader list
          instead — the same question k3s registration faces there.)
   5. [x] The RTC: Linux never writes the hardware clock back on its
          own — that's a distro's shutdown script elsewhere, so here
          it's init's job. Write it (RTC_SET_TIME) at exactly two
          moments: once after the first successful sync, so even a
          machine that later loses power dirty carries decent time
          into its next boot, and once at clean shutdown, so the RTC
          holds the best final estimate.
   6. [x] Prove it in the lab: boot a node with QEMU's -rtc base= set
          years wrong, watch the console tell the story — the skewed
          clock, the sync, the step — and watch k3s join a cluster it
          could not have joined before the step, because the CA's
          certificates would not exist yet. Then check `kubectl get
          machines` reports the follower following the leader and
          the leader following its upstreams. (Proven with `make run-lab
          RTC=2001-01-01`: node-1 stepped 25.5 years from Cloudflare,
          node-2 — booted believing 1999 — stepped 27 years from
          node-1's responder, both before k3s, and both wrote the
          correction to their RTCs. A node-1 reboot then booted just
          -574ms off, the written RTC having carried real time
          through, and `kubectl get machines -o wide` showed both
          nodes synchronized at sub-millisecond offsets.)
8. [x] The Cluster converges: the in-cluster Cluster resource was
       seed-only — init read the image's cluster.yaml every boot,
       the operator seeded the API copy once, and nothing ever
       compared the two, so `kubectl edit cluster` changed a
       document no machine consults. The Machine already has the
       whole lifecycle this needs (drift detection, durable staging
       on machineState, proven fallback, SpecConverged); the Cluster
       document rides the same machinery, staged per machine and
       applied by the next boot. The convergence machinery is
       per-machine but the Cluster is cluster-scoped, so every
       machine stages its own copy, machines can transiently disagree
       about which Cluster spec they booted, and status makes that
       visible per machine. Fetching cluster config live at boot was
       considered and rejected: it's circular (the endpoint is inside
       the document being fetched), followers hold no API credentials,
       and it would make a leader outage block follower boots — while
       the operator pod on every node already IS the live,
       credentialed reader; disk is just the crash-safe handoff from
       runtime read to boot-time consumer. This lands before HA
       leaders on purpose (growing spec.leaders is precisely a
       Cluster edit — the HA milestone needs edits that converge) and
       before GitOps (git will own the Cluster document, and a
       document git owns must actually take effect).
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
          and the CRD schema as usual. (Simpler than the machine
          manifest's peek on purpose: the Cluster document doesn't
          drive storage, so by the time it's read, machineState is
          an ordinary mounted filesystem and no peek mount is
          needed.)
   3. [x] Promotion, the genuinely new mechanism: the join is the
          proof. A machine manifest is proven by storage
          reconciliation within the boot, but a cluster manifest's
          failure modes are downstream (a bad endpoint means the
          follower never joins), so init can't prove it at settle time.
          Init boots a staged cluster document tentatively and writes
          an attempted marker (the staged hash); the operator — whose
          own existence as a pod proves containerd, kubelet, and the
          join all worked under this config — promotes on its first
          reconcile pass and clears the marker. A boot that finds the
          marker still matching the staged hash knows the last try
          never got promoted: reject, fall back to proven. One
          proving boot, crash-only, no boot counters.
   4. [x] The operator's other half: read the Cluster resource every
          pass (RBAC already allows it; seeding stays create-only and
          the operator still never writes spec), render canonical
          bytes, compare against the boot record, and run the same
          decision table as the Machine — withdraw stale staged specs
          and clear rejections when current, hold on
          rejected-last-boot, stage drift and request a reboot per
          the Machine's spec.rebootPolicy (one knob governs both
          kinds of staging). A new ClusterConverged condition with
          the same reason vocabulary; Ready rolls it up. Deliberately
          NO fleet orchestration: a Cluster edit is drift on every
          machine at once, and with Auto everywhere that's a
          simultaneous fleet reboot — Manual stays the default,
          pending reboots are visible per machine, and rolling
          coordination is milestone 13's job.
   5. [x] Guardrails: the five network-plan fields (nodeCIDR,
          clusterCIDR, serviceCIDR, clusterDNS, clusterDomain) become
          immutable-once-set via CEL oldSelf rules — k3s can't
          re-plumb any of them in place, so an edit there is a lie
          waiting for a reboot to expose. (oldSelf is correct here,
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
          no reboot. (All three ran as designed; the doomed-endpoint
          boot was recovered by power cut — the honest way, since a
          machine that never joined has no operator to ask for a
          reboot.)
9. [x] Multiple leaders: quorum for the control plane, and the whole
       growth story is one Cluster edit converging — spec.leaders
       grows from [node-1] to three names, every machine stages the
       new document via milestone 8, and no separate "add a leader"
       mechanism exists. sqlite (via kine) serves one leader; more
       than one means embedded etcd.
   1. [x] Config derivation by leader count (init/k3s.go): one
          leader means exactly today's config — sqlite, no etcd
          keys, single-node stays cheap on purpose. More than one:
          the first entry in spec.leaders is the founding leader and
          renders cluster-init: true (on the migration boot, k3s migrates
          the sqlite datastore into embedded etcd in place, which is
          what made starting on sqlite safe rather than a dead end);
          every other leader renders server: pointing at the founder,
          resolved from the fleet's Machine manifests the way time
          sources already are (reuse leaderAddresses). Followers are
          unchanged. Rejoins keep the same flags every boot — the
          founder keeps cluster-init, the rest keep server: — which
          is k3s's recommended steady state. (The founding leader is
          a config-derivation role — the first name in the list —
          not etcd's raft leader, which is elected and moves; the
          comment must draw that line.)
   2. [x] The endpoint stays one explicit input. Followers use it for
          first contact only: after joining, k3s agents maintain a
          client-side load balancer that learns every leader, so a
          dead endpoint strands only new followers, never running
          ones (and time queries already bypass it, asking each by
          address). A VIP or DNS name is a deployment's choice to
          make; the manifest documents the tradeoff.
   3. [x] Quorum, plainly: three voices, odd on purpose. No CEL rule
          on leader-count parity — growing 1→3 in one edit never
          passes through two, and a transient even state during some
          future migration shouldn't be refused at admission. A
          simultaneous all-leader reboot loses quorum transiently
          and reforms from disk; milestone 13's rollout now keeps
          Auto-policy leaders to one at a time regardless of budget.
   4. [x] The lab grows to five machines: three leaders (node-1
          founding, node-3 and node-4 fresh) and two followers (node-2
          staying, node-5 new) — MACs, dist dirs, and manifests
          extending the existing NODE dimension, plus a MEM knob on
          the Makefile since five 4G guests don't fit a 30G laptop
          (followers run at 2G, leaders-to-be at 3G). All five came
          up Ready and Converged on the existing cluster in under a
          minute, node-3/4/5 booting as followers until the growth
          edit promotes them.
   5. [x] Drills: the 1→3 growth edit end to end (migration on
          node-1, two joiners, followers riding through it); kill one
          leader and watch the cluster keep serving while machine
          status tells the story; then ATTEMPT follower→leader
          promotion on node-2 by growing spec.leaders to include it
          — k3s's tolerance for a same-name role flip is uncertain,
          so recorded findings are the deliverable, and node-2 can
          be rebuilt fresh if it balks. (All of it ran, and the
          findings were better than feared. The growth edit converged
          machine by machine: node-1's migration boot moved sqlite
          into embedded etcd in place, node-3 and node-4 joined at
          the founder's address, and the whole thing was one kubectl
          patch plus per-machine reboot policies. Promotion of
          node-2 — a follower since milestone 6, same name, same
          disks — worked cleanly: it rebooted straight into
          control-plane,etcd. DEMOTION is the rough edge: rebooting
          node-4 as a follower left its Node object claiming
          control-plane,etcd and, worse, its etcd membership
          registered — a phantom voice that would have broken quorum
          math on the next leader reboot. `kubectl delete node`
          triggers k3s's etcd member-removal controller, but a
          kubelet whose Node vanishes mid-run won't re-register, so
          the full demotion recipe today is: Cluster edit, reboot,
          delete the Node object, one more boot. (That recipe is now
          fully automated: the demoted machine's own operator notices
          its Node still claims control-plane, writes the reboot
          intent, and deletes its own Node — intent first, since the
          delete kills the very pod doing it — and the next proven
          follower boot also purges the leftover etcd datastore,
          which etcd would otherwise refuse to let rejoin. The drill
          re-ran hands-off in both directions: promote node-4 with
          one edit, demote it with another. It also flushed out a
          rejection-authority bug: the decision tables read the
          quarantine record from facts, which are frozen at boot, so
          a rejection cleared by a revert kept blocking a retry until
          reboot — the durable record on machineState is the
          authority now.) The
          loss drills: with one leader dead the cluster kept
          serving and scheduling (quorum 2 of 3) and the revived
          leader rejoined; with ALL THREE leaders dead the API went
          dark while the followers' machines stayed up, and
          relaunching the leaders reformed etcd from disk — the
          cluster re-established itself and the followers
          reconnected without rebooting.)
10. [x] Say it in words, not booleans: every state a fleet listing
        shows is now an enumerated word. Machines carry
        `status.phase` — Ready, UpdatePending, Updating, Blocked,
        Degraded, Booting, Unknown, or Lost — derived from the
        conditions on every pass (the Ready condition remains for
        `kubectl wait`; only its printer column went). The time
        boolean became `status.time.state`
        (Synchronized/FreeRunning/Unsynchronized — by-design and
        by-outage are different stories), and the convergence columns
        print the conditions' *reasons* (Converged, RebootPending,
        RejectedLastBoot, …), which say what kind of wrong and what
        would fix it. The fleet also learned to notice its dead:
        every operator heartbeats `status.observedAt`, and the
        leaders' fleet sweep — leaders because a follower that can
        reach the API is by definition reaching a leader — marks a
        silent machine Lost (safe multi-writer: the sweep only
        touches machines whose own writer has provably stopped) and
        publishes the Cluster's first status: a phase
        (Ready/Updating/Degraded) and a ready-out-of-total headcount
        ("4/5" in `kubectl get clusters`). A NodeHealthy condition
        mirrors the Node's Ready onto the Machine, catching the one
        gap the heartbeat can't: the operator lives on the host
        network and can keep reporting while the kubelet under it is
        dead. Deliberately not shown: quorum lost — losing a leader
        majority takes the API (and thus the status writer) down with
        it; a frozen status is itself the symptom. Health checks
        surveyed and deferred: leaders cross-checking each other's
        clocks, etcd quorum margin as a Cluster condition (pairs with
        milestone 13), storage-capacity watermarks (a full
        machineState silently breaks staging), and the cluster-wide
        clock spread — the fleet sweep already reads every Machine's
        status, so it could publish max minus min of the reported
        time offsets on the Cluster, one number that says how well
        timekeeping is working across the whole fleet. (Two findings from the
        first lab run. The heartbeat found a feedback loop: the
        operator reconciles on every watch event, including the event
        its own status write causes, and a timestamp that moved every
        pass made every write real — the operator spun flat-out
        against the API server. Renewing observedAt on a cadence
        instead restores the no-op writes that let the loop settle.
        And three leaders sweeping at once worked — the verdicts are
        deterministic and optimistic concurrency serialized the
        writes — but filled the logs with 409s that were all noise;
        the sweep now runs under a coordination.k8s.io Lease, the
        same leader election kube-controller-manager uses to run hot
        standbys, built here from a GET and two conditional writes.
        An idiom review pass later finished the thought: the
        heartbeat itself moved out of status into a per-machine
        Lease in liken-machine-lease — kube-node-lease's
        arrangement, escaping the same write amplification — and
        the whole API grew up to metav1's conventions: typed string
        vocabularies, conditions validated like metav1.Condition
        with observedGeneration, list-type annotations, admission
        patterns on spec strings, Cluster conditions beneath its
        phase, watch bookmarks, and a coverage gate ratcheted past
        half.)
11. [ ] Explore device management: how does a shell-less, udev-less OS
        expose `/dev` beyond the basics — USB devices arriving after
        boot, GPUs, serial adapters? devtmpfs gives us the nodes, but
        hotplug means fielding kernel uevents and loading modules,
        which is the job udev does elsewhere. Then the Kubernetes half:
        how workloads get to the hardware (device plugins, dynamic
        resource allocation) and whether devices belong in
        `status.hardware` alongside CPUs and memory.
12. [ ] Declarative upgrades: one field on the Cluster moves the whole
        fleet to a new liken version. `spec.version` names the target —
        on the Cluster, not the Machines, because a fleet should be
        retargeted in one edit; a machine's version belongs in its
        status, where reality is reported, not its spec. Machines that
        aren't running the target download the release, write it into
        the boot slot they are *not* running from, and reboot into it
        through milestone 13's rollout — workers first, leaders one at
        a time, nobody babysitting. For a two-file OS an upgrade really
        is "replace vmlinuz and liken.cpio and reboot", which makes A/B
        slots and roll-back-on-failed-boot the natural shape — and it
        finally answers the bootloader question QEMU's `-kernel` flag
        has been deferring. The answer is that there is no bootloader:
        the kernel is already an EFI executable (the stub), UEFI
        firmware's own boot menu picks a slot, and "try the new one
        once, fall back if it dies" is the firmware's BootNext —
        fallback as firmware arithmetic, not software cleverness. Trust
        stays where liken's trust already lives, in explicit inputs:
        the Cluster carries a release source URL and a catalog mapping
        each version to the digest of its release manifest, which in
        turn carries every artifact's sha256 — API → catalog digest →
        manifest → bytes, no signatures until the mastery tier. The
        target and catalog are read live and never count as
        cluster-document drift (the sysctls precedent: a catalog append
        must not stage a fleet-wide reboot). Promotion mirrors the
        cluster document exactly: boot the staged slot tentatively
        (attempted marker + BootNext), let the operator's first
        reconcile be the proof, and let init re-assert BootOrder from
        the durable record every boot — machineState is the authority,
        the firmware a cache of it.
    1. [x] The slot vocabulary and the FAT32 formatter: `systemA` and
           `systemB` join the storage roles at the head of the
           canonical order (the firmware is the earliest reader of any
           partition liken owns), typed in the GPT as EFI System
           Partitions — the first roles whose partition type isn't
           "Linux filesystem data", because the type GUID is precisely
           how firmware recognizes a candidate. Formatting is a
           hand-written FAT32 formatter in the GPT-writer genre (a boot
           sector describing the geometry, two copies of the allocation
           table, a root directory that is just cluster 2), because
           FAT is the only filesystem firmware promises to read; the
           kernel's vfat module handles file I/O afterward, which
           moves module loading ahead of storage settling in init —
           some roles' filesystems are modules now. Prove it: a node
           with a blank third disk claims both slots into
           status.storage, and a file written to a mounted slot
           survives a reboot. (Proven on node-5 via a live Machine
           edit riding the milestone-13 rollout: AwaitingTurn, a
           granted reboot, the claim and both FAT32 formats narrated
           on the console, both slots Partition-backed at 512Mi. The
           power-cut drill taught the milestone's next lesson early:
           an *unsynced* file written seconds before a power cut was
           gone afterward — FAT has no journal and the page cache is
           not a promise — while a synced file rode through two
           power cuts intact. The download step's fsync-and-reverify
           design is built for exactly this.)
    2. [ ] Speaking EFI: init mounts efivarfs when the firmware is
           UEFI and learns the boot variables — each a small binary
           format (EFI_LOAD_OPTION: attributes, a UTF-16 name, a
           device path ending at a file on a partition, and free-form
           arguments that are exactly a kernel command line),
           unit-tested against known-good bytes, with the immutable
           flag the kernel puts on every variable handled in one
           narrated helper. The lab gains OVMF: real UEFI firmware,
           split as read-only code shared by every guest plus a
           per-node writable variable store — the "CMOS" where boot
           entries live, surviving reboots the way a motherboard's
           does (and removed by `make clean`, so factory reset forgets
           firmware memory too). Verify here, before anything is built
           on it, that the EFI stub's initrd= argument works under
           OVMF, since upstream calls it deprecated. Prove it: the
           console and Machine status report the firmware, BootCurrent,
           and a decoded BootOrder — console parity as always.
    3. [ ] Self-install, the USB-stick story: `make install NODE=x`
           boots via -kernel one last time — QEMU playing the install
           medium the way an installer stick or PXE would — and init,
           seeing liken.install, claims the boot disk, verifies the
           release payload the installer carries, copies it into slot
           A, writes both boot entries and BootOrder, and powers off.
           install.cpio is liken.cpio with a second archive
           concatenated on, carrying vmlinuz, liken.cpio, and
           release.yaml — the kernel unpacks concatenated cpios, the
           same mechanism early microcode rides, so the installer's
           payload is byte-identical to what the digest chain
           describes. Each entry's baked command line carries the
           machine's name, its slot, and panic=10 — load-bearing,
           because a panicking trial kernel must reset into the
           firmware's fallback rather than hang. `make run` becomes
           firmware-from-disk: no -kernel, no -append (`run-once`
           keeps direct boot; its oneshot knob can't ride a baked
           entry). Prove it: a fresh node installs and boots to Ready
           from disk; killing QEMU mid-install and re-running
           converges, since claiming skips claimed disks and copying
           re-verifies.
    4. [ ] The releases domain and the API: `make release VERSION=x`
           rebuilds init, operator, and image with the overridden
           version stamp into a separate build tree (the domain
           Makefiles learn overridable version and output knobs; the
           everyday dist/ trees are never touched) and publishes
           releases/dist/<v>/ — vmlinuz, liken.cpio, install.cpio, and
           release.yaml listing every artifact's sha256; the digest a
           catalog carries is the sha256 of that file's exact bytes.
           `make serve` is a small logged file server the guests reach
           at the host's NAT address, the lab's stand-in for a release
           host on the internet. The Cluster grows spec.version and
           spec.releases (source plus catalog); CEL holds the target
           to catalog membership at admission — a same-object check,
           so it can never wedge the way the storage rules once did —
           and a machine with no slots reports NoSystemSlots rather
           than pretending it can comply. The fleet sweep computes
           status.releases.newest (a hand-written semver comparison),
           and the printer columns say it plainly: the Cluster shows
           the target VERSION and the NEWEST the catalog offers, while
           each Machine's LIKEN column shows who has arrived. Prove
           it: an edit whose target names no catalog entry is refused
           at the door, and `kubectl get clusters` tells the
           target-versus-newest story at a glance.
    5. [ ] The download: an asynchronous fetcher in the operator — a
           blocking 116MB GET inside a reconcile pass would starve the
           heartbeat and read as a death, milestone 10's lesson made
           structural — streams each artifact through sha256 into the
           inactive slot, temp-and-rename, resumable by
           re-verification: FAT has no journal, so a torn download is
           just files that fail their hashes and are fetched again.
           Nothing is ever staged until every byte on the slot
           verifies against the catalog's chain. Downloading and
           DigestMismatch join the condition vocabulary — a down
           server is transient by definition, so retry forever with
           the story in the message; a wrong digest is Blocked until
           the catalog itself changes, and is never staged. Prove it:
           the serve log shows the fetch; killing the server
           mid-download and restarting it converges; a deliberately
           corrupted publish holds at DigestMismatch with nothing
           staged.
    6. [ ] The proving reboot: a verified download becomes a staged
           record in a third staging store — system/ beside manifests/
           and cluster/ on machineState, the same four files, the same
           durable writes. Init's reboot path finds it, writes the
           attempted marker and the firmware's BootNext (boot the
           other slot, once), and reboots. The proving boot knows
           itself by liken.slot=; the operator's first reconcile —
           living proof that the new kernel, init, k3s, and the join
           all work — promotes the record; and init flips BootOrder
           when promotion lands, re-asserting it from the durable
           record on every boot thereafter. Every power-cut gap in
           that timeline boots something proven. Prove it: one Cluster
           edit upgrades one node — the LIKEN column flips, BootOrder
           prefers the new slot, and a plain reboot stays on the new
           version.
    7. [ ] The fallbacks: a proving-boot watchdog in init — armed only
           when the running slot's record is still staged, disarmed by
           promotion, ten minutes of patience, the fleet's established
           RolloutStalled number — reboots a machine that came up but
           never settled, and the already-consumed BootNext lands it
           back on the proven slot, where the attempted marker renders
           the verdict: RejectedLastBoot, no reboot loop, cleared by
           the next version edit. A kernel that panics outright
           funnels into the same verdict through panic=10, with no
           software involved at all. Prove it with two fault releases:
           one built to panic at first breath (the firmware-fallback
           drill) and one built to wedge k3s (the watchdog drill),
           both ending Ready on the old version with the rejection
           visible in status.
    8. [ ] The fleet: migrate the five-node lab to disk boot — a
           Machine edit adds the slot roles on a new disk, one install
           boot per node, no fresh claims, no data loss — then the
           grand drill: one Cluster edit (a catalog append and the
           target bump) rolls all five machines through milestone 13's
           rollout, the cluster serving throughout, `kubectl get
           machines -o wide` narrating the walk from AwaitingTurn to
           Ready on the new version. Then the power-cut drills: cut
           mid-download (resumes by re-verification) and mid-install
           (re-running the installer converges).
13. [x] Rolling reboots at the *cluster* level: the fleet applies
        staged changes without an operator babysitting it, one
        machine at a time. (This milestone was written as "rolling
        upgrades," but the sequencing turned out to be independent of
        what the reboot applies — a config change today, a version
        upgrade once milestone 12 exists. The machinery is the same
        either way.) On a cluster member, rebootPolicy: Auto now
        means "reboot when the cluster says it's safe": the machine
        stages its change, publishes AwaitingTurn, and waits for the
        sweep leader — already elected, already reading the whole
        fleet — to grant its turn by writing a RebootApproved
        condition onto the Machine, the way the scheduler owns
        PodScheduled on Pods it doesn't manage. The budget is one
        field, spec.disruption.maxUnavailable (default 1), a
        machine-level PodDisruptionBudget reduced to one number; it
        counts unplanned trouble too, so a hurting fleet pauses its
        own rollout, and the leaders have an automatic floor no
        budget can raise: one leader down at a time, because quorum
        is arithmetic. A granted machine drains itself first —
        cordon its own Node, evict everything movable through the
        Eviction API so workloads' own PDBs hold, then write the
        reboot intent — incrementally across reconcile passes, since
        a blocked pass would stop the heartbeat and read as a death.
        It uncordons itself after converging; a human's cordon stays
        put. The sweep reads granted silence as the reboot it asked
        for, and a machine that never returns flips the Cluster's
        Progressing condition to False/RolloutStalled (Deployment's
        vocabulary) and halts granting until someone looks.
        Demotion reboots join the same queue. Still owed, someday:
        workload-aware ordering; a drain that waits on more than a
        deadline when a PDB can never be satisfied; and strict
        workers-first ordering at rollout start — today the order is
        among machines that have *asked* by sweep time, so a leader
        that stages fast (the conductor itself, whose sweep runs in
        the same pass as its own staging) can take the first turn
        before a slower worker has raised its hand. Safe either way;
        one leader at a time holds regardless.
14. [ ] GitOps from first boot — without baking an engine into the OS.
        Deferred on purpose: git-driven delivery is one way to feed
        this system, not its core mode — the Machine and Cluster
        resources are the real interface, and everything above works
        without a repo in the loop. When it lands, the OS grows two
        generic primitives rather than Flux support: a seed channel
        (manifests delivered alongside the Machine manifest land in
        k3s's auto-manifests directory, applied at first boot and
        owned by the repo afterward — which needs the same staged/
        promoted care the Machine manifest gets) and a minting
        primitive (the machine creates an SSH keypair in a Secret if
        one is missing and publishes the public half in status and on
        the console, so the user registers a deploy key at the forge
        without ever handling private material; the key may be
        read-write, since image-update automation will eventually
        commit tag bumps back to the repo). Flux itself is blessed
        content, not a vendored domain: its install manifest and sync
        objects ride the seed channel, that first apply plays the
        part `flux bootstrap`'s CLI plays, and Flux self-manages from
        the repo afterward — the standard pattern, and another engine
        could ride the same channel. This is where the manifest
        authority story resolves: git wins, and the seeded Machine
        and Cluster copies are downstream of it.

Deferred until the fundamentals above are proven — the
public-consumption tier:

* The public bootstrapping story: today the identity bundle is minted by
  make in a private checkout, but a released OS needs a way for anyone
  to mint theirs — an installer step, a tiny CLI, or a documented
  openssl recipe.
* The mastery tier: UKIs, dm-verity, secure boot, TPM-sealed secrets.

# Open problems

Questions we know we owe answers, without pretending to have them yet:

* **Claiming unknown machines.** `liken.machine=` identifies machines
  someone declared ahead of time. The deferred half is the machine
  nobody declared: a Machine template carried on the Cluster that an
  unknown node claims on first boot — named from a hardware fact
  (probably its MAC, the one identity the network already forces to be
  unique), addressed from a pool (probably by ARP-probe claiming, in
  the same spirit as storage claiming: probe reality, take what's
  free, refuse ambiguity). Waits until the declared-machine flow is
  proven.
