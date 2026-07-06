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
8. [ ] The Cluster converges: today the in-cluster Cluster resource
       is seed-only. Init reads the image's cluster.yaml every boot,
       the operator seeds the API copy once, and nothing ever
       compares the two — so `kubectl edit cluster` changes a
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
   4. [ ] The operator's other half: read the Cluster resource every
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
   5. [ ] Guardrails: the five network-plan fields (nodeCIDR,
          clusterCIDR, serviceCIDR, clusterDNS, clusterDomain) become
          immutable-once-set via CEL oldSelf rules — k3s can't
          re-plumb any of them in place, so an edit there is a lie
          waiting for a reboot to expose. (oldSelf is correct here,
          unlike the storage rules of milestone 5.7: these facts can
          never be edited "back to reality," because their reality
          never changes.) leaders, endpoint, and time stay freely
          editable.
   6. [ ] Drill it on the two-node lab: add a second NTP upstream via
          kubectl edit cluster and watch both machines stage, report
          RebootPending, apply on reboot, and show the new source in
          status.time with ClusterConverged True. Point the endpoint
          somewhere dead and reboot the follower: no join, no operator,
          no promotion, and the next boot rejects on the attempted
          marker and falls back to proven, rejoining the real cluster
          with the rejection visible in status. Then edit back to
          good and watch staged withdraw and the rejection clear with
          no reboot.
9. [ ] Multiple leaders: quorum for the control plane, and the whole
       growth story is one Cluster edit converging — spec.leaders
       grows from [node-1] to three names, every machine stages the
       new document via milestone 8, and no separate "add a leader"
       mechanism exists. sqlite (via kine) serves one leader; more
       than one means embedded etcd.
   1. [ ] Config derivation by leader count (init/k3s.go): one
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
   2. [ ] The endpoint stays one explicit input. Followers use it for
          first contact only: after joining, k3s agents maintain a
          client-side load balancer that learns every leader, so a
          dead endpoint strands only new followers, never running
          ones (and time queries already bypass it, asking each by
          address). A VIP or DNS name is a deployment's choice to
          make; the manifest documents the tradeoff.
   3. [ ] Quorum, plainly: three voices, odd on purpose. No CEL rule
          on leader-count parity — growing 1→3 in one edit never
          passes through two, and a transient even state during some
          future migration shouldn't be refused at admission. A
          simultaneous all-leader reboot loses quorum transiently
          and reforms from disk; Manual stays the sane policy for
          leaders until milestone 13 does rolling reboots.
   4. [ ] The lab grows to five machines: three leaders (node-1
          founding, node-3 and node-4 fresh) and two followers (node-2
          staying, node-5 new) — MACs, dist dirs, and manifests
          extending the existing NODE dimension. If five 4G guests
          crowd the host, followers run at 2G.
   5. [ ] Drills: the 1→3 growth edit end to end (migration on
          node-1, two joiners, followers riding through it); kill one
          leader and watch the cluster keep serving while machine
          status tells the story; then ATTEMPT follower→leader
          promotion on node-2 by growing spec.leaders to include it
          — k3s's tolerance for a same-name role flip is uncertain,
          so recorded findings are the deliverable, and node-2 can
          be rebuilt fresh if it balks.
10. [ ] GitOps from first boot — without baking an engine into the OS.
        The OS grows two generic primitives rather than Flux support: a
        seed channel (manifests delivered alongside the Machine manifest
        land in k3s's auto-manifests directory, applied at first boot
        and owned by the repo afterward — which needs the same staged/
        promoted care the Machine manifest gets) and a minting primitive
        (the machine creates an SSH keypair in a Secret if one is
        missing and publishes the public half in status and on the
        console, so the user registers a deploy key at the forge without
        ever handling private material; the key may be read-write, since
        image-update automation will eventually commit tag bumps back to
        the repo). Flux itself is blessed content, not a vendored
        domain: its install manifest and sync objects ride the seed
        channel, that first apply plays the part `flux bootstrap`'s CLI
        plays, and Flux self-manages from the repo afterward — the
        standard pattern, and another engine could ride the same
        channel. This is where the manifest authority story resolves:
        git wins, and the seeded Machine and Cluster copies are
        downstream of it.
11. [ ] Explore device management: how does a shell-less, udev-less OS
        expose `/dev` beyond the basics — USB devices arriving after
        boot, GPUs, serial adapters? devtmpfs gives us the nodes, but
        hotplug means fielding kernel uevents and loading modules,
        which is the job udev does elsewhere. Then the Kubernetes half:
        how workloads get to the hardware (device plugins, dynamic
        resource allocation) and whether devices belong in
        `status.hardware` alongside CPUs and memory.
12. [ ] Explore declarative upgrades: setting `spec.version` on a
        Machine should upgrade the machine — liken's version drives the
        k3s version, so one field moves the whole stack. For a two-file
        OS an upgrade is "replace vmlinuz and liken.cpio and reboot",
        which makes A/B slots and roll-back-on-failed-boot the natural
        shape — but it also means liken finally needs a bootloader
        story, since QEMU's `-kernel` has been playing that role.
13. [ ] Rolling upgrades at the *cluster* level: once one machine can
        upgrade itself, the cluster should upgrade its fleet without an
        operator babysitting it — cordon a node, drain its workloads,
        upgrade, restart, confirm it rejoined healthy, then move to the
        next, honoring quorum on the leaders. Not designed yet, just
        owed: it's the layer where the Machine's upgrade machinery and
        the Cluster's convergence machinery meet.

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
