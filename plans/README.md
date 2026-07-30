# The plans

This directory holds one document for each milestone. Each document
gives the problem, the design, the reasons for each decision, and what
the lab measured when the work ran. [00-design.md](00-design.md) is the
design overview for the whole project.

A document's directory states its status:

* [`completed/`](completed/) holds the milestones that are built.
* [`rejected/`](rejected/) holds the milestones that were built and then
  removed. The document stays as the record of what came out and why.
* The markdown files in this directory are the milestones that are not
  built yet.

The numbers run in one sequence across all three directories. The next
milestone is 50.

## Completed

* **01.** [Boot to a hello world](completed/01-boot-to-hello-world.md) — a
  vendored kernel, a Go init, an initramfs, and QEMU.
* **02.** [Init starts k3s and nothing else](completed/02-init-starts-k3s.md)
  — the network, the machine identity, the Machine CRD, and the
  operator.
* **03.** [Remove the known hacks](completed/03-removing-the-hacks.md) — the
  boot path drops the fixes that depend on k3s internals.
* **04.** [Storage, declared by purpose](completed/04-storage-by-purpose.md) —
  storage roles, GPT claiming, and a refusal when a disk is ambiguous.
* **05.** [The spec becomes editable](completed/05-the-spec-becomes-editable.md)
  — staged manifests, a proven fallback, and convergence by reboot.
* **06.** [Growing the cluster past one node](completed/06-growing-past-one-node.md)
  — the Cluster CRD, the join token, static addressing, and one image
  for the whole fleet.
* **07.** [Cluster time](completed/07-cluster-time.md) — the leaders sync from
  declared upstreams and serve time to everyone else.
* **08.** [The Cluster converges](completed/08-the-cluster-converges.md) — the
  Cluster document uses the same staging machinery, promoted by the
  join.
* **09.** [Multiple leaders: quorum](completed/09-multiple-leaders.md) —
  sqlite grows into embedded etcd through one Cluster edit.
* **10.** [Fleet visibility](completed/10-fleet-visibility.md) — phases,
  heartbeat leases, the sweep, and a status vocabulary that says what
  would fix the machine.
* **11.** [Device management](completed/11-device-management.md) — the OS
  reports hardware that no driver claims, and a DRA driver delivers
  devices to unprivileged pods.
* **12.** [Declarative upgrades](completed/12-declarative-upgrades.md) — A/B
  slots, the digest chain, firmware fallback, and one field that moves
  the fleet.
* **13.** [Rolling reboots at the cluster level](completed/13-rolling-reboots.md)
  — the rollout conductor: budgets, drains, and one leader at a time.
* **14.** [GitOps from first boot](completed/14-gitops-from-first-boot.md) —
  the `flux` feature, the seed-once engine, the minted deploy key, and
  retraction.
* **15.** [Observability below Kubernetes](completed/15-observability-below-kubernetes.md)
  — each host log stream becomes a pod's stdout.
* **16.** [Adopting an existing cluster](completed/16-adopting-a-cluster.md) —
  import an existing cluster's identity, join its etcd, rotate the old
  members out, and promote.
* **17.** [Optional features: network storage clients and bundled components](completed/17-network-storage-clients.md)
  — one Cluster vocabulary for optional capabilities, with the iSCSI
  and NFS host clients as its first entries.
* **18.** [Requestable kernel modules](completed/18-requestable-kernel-modules.md)
  — a machine declares the drivers its hardware needs, the image ships
  them, init loads them, and status reports them.
* **19.** [Choosing the bundled components](completed/19-choosing-bundled-components.md)
  — absorbed by milestone 17's feature vocabulary.
* **20.** [Private registries and the k3s restart tier](completed/20-private-registries.md)
  — registry mirrors and credentials, and the changes that converge by
  restarting k3s in place.
* **21.** [Node labels on the Machine](completed/21-node-labels.md) —
  scheduling identity declared on the spec, registered at boot, and
  reconciled live.
* **22.** [Public releases](completed/22-public-releases.md) — releases of
  liken itself, with no deployment baked in.
* **23.** [Crash-safe image imports](completed/23-crash-safe-image-imports.md)
  — a machine killed during an unpack heals itself on the next boot.
* **24.** [A real repository and CI builds](completed/24-repo-and-ci.md) — a
  public home for the code, and CI that builds every commit and boots
  the result.
* **25.** [The liken.sh website](completed/25-liken-sh-website.md) — the
  project's domain serves a web page from the project's own cluster.
* **26.** [The public release channel](completed/26-the-public-release-channel.md)
  — digest-verified downloads from object storage, published by CI on
  every version tag.
* **27.** [Documentation on the website](completed/27-documentation-on-the-website.md)
  — a user manual at liken.sh/docs/, with a CRD reference generated
  from the schemas.
* **28.** [Internet updates](completed/28-internet-updates.md) — every update
  after the first boot comes from liken's public release channel.
* **29.** [Root on disk](completed/29-root-on-disk.md) — the machine runs from
  a read-only system image on its own disk instead of from RAM.
* **30.** [Upgrades under BIOS](completed/30-bios-upgrades.md) — the upgrade
  path's second actuator, with the same one-shot trial and fallback.
* **31.** [TLS for the website](completed/31-website-tls.md) — liken.sh
  answers over HTTPS with a certificate from Let's Encrypt.
* **32.** [Hardware support in the image](completed/32-hardware-support-in-the-image.md)
  — the whole kernel module tree, the firmware those modules request,
  and CPU microcode.
* **35.** [The machine reports its last crash](completed/35-crash-capture.md)
  — the next boot preserves the kernel's pstore records and publishes
  a summary as `status.lastCrash`.
* **36.** [The hardware report](completed/36-the-hardware-report.md) — a
  report boot that changes no disk and writes a proposed manifest to
  the stick.
* **37.** [A reinstall formats every partition](completed/37-a-reinstall-formats-every-partition.md)
  — a reinstall erases every role it claims, and the proposed layout
  scales with the disk.
* **38.** [Device attributes and shared devices](completed/38-device-attributes-and-sharing.md)
  — a published device says whether it may be shared and what kind of
  node it delivers.
* **40.** [Pod logs belong on a disk](completed/40-pod-logs-belong-on-a-disk.md)
  — a bind mount puts the pod log directory on the podEphemeral role.
* **41.** [Editing the network spec](completed/41-editing-the-network-spec.md)
  — the boot records the network it came up under, so an edit to
  `spec.network` drifts, stages, and applies.
* **42.** [Turning a feature off turns it off](completed/42-turning-a-feature-off.md)
  — a retraction waits for the cluster, reboots when it leaves kernel
  state, and stops leaving a controller's work behind.
* **43.** [A browsable release channel](completed/43-a-browsable-release-channel.md)
  — an index page for the channel and one for each release, rendered
  from the documents that the channel already serves.
* **47.** [The timezone database](completed/47-the-timezone-database.md)
  — the image carries IANA's zone files, so a CronJob can name the
  zone its schedule means, and a weekly job in CI opens the pull
  request that moves the pin.
* **48.** [Watching the pins](completed/48-watching-the-pins.md) —
  every domain that vendors something reports what it pins and what
  its upstream has now, and moves the pin when asked. One `latest.sh`
  beside each `fetch.sh`, and `make versions` for the whole table.

## Rejected

* **39.** [Naming an interface by MAC address](rejected/39-naming-an-interface-by-mac-address.md)
  — built, drilled, and removed. The hardware report already names the
  cabled port, and an optional name cost the interfaces list its merge
  key.

## Not built yet

* **33.** [Updating the machine's own firmware](33-firmware-updates.md) —
  fwupd as a feature slug, using the rolling-reboot orchestration that
  liken already has. A shim variable store turns fwupd's firmware
  writes into requests that init decides on. It waits for experience
  with bare metal. Its boot-path prerequisite is built: a UEFI machine
  writes its boot entries again on every boot, and its proven slot
  carries a loader that a firmware at its defaults finds, so NVRAM loss
  no longer needs an install stick.
* **34.** [GPU add-ons](34-gpu-add-ons.md) — a machine that needs a GPU
  compute stack declares an add-on: a second read-only image on its
  boot slot. The first add-on would be NVIDIA compute.
* **44.** [Naming a disk by identity](44-naming-a-disk-by-identity.md) —
  a Machine declares its storage by a name that belongs to the disk,
  and `/dev/disk` grows the `by-id` and `by-uuid` trees beside the
  `by-path` tree that iSCSI needed.
* **45.** [The CLI reaches the cluster](45-the-cli-reaches-the-cluster.md)
  — one command grants a machine the reboot its policy withholds, and
  three run `kubectl`, `stern`, and `flux` against the right cluster.
* **46.** [Configuring etcd snapshots](46-configuring-etcd-snapshots.md)
  — `spec.datastore.snapshots` gives a schedule, a retention, and an
  S3 destination to the snapshots every leader already takes.
* **49.** [Sharing integrated graphics](49-sharing-integrated-graphics.md)
  — the sharing rule tests every node a device delivers, so the i2c
  nodes an integrated GPU registers for its display outputs keep it
  exclusive.

The hardening tier waits until the milestones above are proven: UKIs,
dm-verity, secure boot, TPM-sealed secrets, and signed releases.

## Open problems

These are the questions that liken owes an answer to. It does not have
one yet.

**Claiming unknown machines.** `liken.machine=` identifies a machine
that somebody declared before it booted. The other half is the machine
that nobody declared. A Machine template on the Cluster would let an
unknown node claim an identity on its first boot. The node would take
its name from a hardware fact, probably its MAC address, because the
network already forces that address to be unique. It would take its
address from a pool, probably by ARP-probe claiming, in the same way
that storage claiming works: probe reality, take what is free, and
refuse an ambiguous case. This waits until the declared-machine flow is
proven.

**The apiservers abort requests that liken's operators send.** On a
five-machine fleet, the apiservers logged "Timeout or abort while
handling" for 13% of the cluster operator's engine-probe GETs, and for
a few of each machine's status PUTs every hour. The aborts include
requests to `127.0.0.1:6443`, so the network between machines is not
the cause. Neither operator reports an error, so from liken's side
every request succeeds, and the noise lands in the apiserver's log at
ERROR, near 2,800 lines a day. The log pair matches a request whose
client closed the connection while the handler still ran, and
`ResponseHeaderTimeout` in `kubernetes/apiclient.go` is the client's
only ten-second deadline. What stalls a response for ten seconds is
the open question, and the loopback case reproduces on one machine.
Whatever the answer, the engine probe could ask every 60 seconds
instead of every 10, or hold a watch, and a deleted engine would still
heal in seconds.

**An edit that no machine reads still reboots the fleet.**
`RestartApplies` in `cluster/changes.go` names the restart-class
fields, and every other difference is reboot-class. `spec.origin`
renders only k3s's `cluster-init` flag, which k3s applies only when no
datastore exists yet, and no machine reads `spec.endpoint` on a fleet
of leaders that declare their own addresses. Editing either on a
running fleet changes nothing on any machine, and still costs one
reboot per machine, in turn. The status could report which tier a
proposed edit lands in, because `RestartApplies` already computes
that. A tier below restart could also exist: stage the document, adopt
it at the machine's next boot, and request no turn.

**No log level for containerd or k3s.** containerd takes a level in
its own configuration and k3s takes `--debug`, but no field in either
CRD reaches them, and a machine serves no shell. On a five-machine
fleet, the machines' own streams wrote 295,000 lines a day, and
containerd's pod lifecycle at info was 171,000 of them. A field could
set the level the way `rebootPolicy` sets a policy, with info as the
default. Two of the measurements need their own answer. containerd
logs `container event discarded` when nothing consumes its event
stream, and that line alone is a third of containerd's volume, so it
may mark a missing subscriber rather than verbosity. And a reboot is
followed by about a day of elevated logging, while the kubelet's
garbage collector removes the sandboxes the reboot orphaned.
