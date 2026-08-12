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
milestone is 54.

[`open-problems/`](open-problems/) holds the questions that liken owes
an answer to. Those documents carry no number, because nobody has
decided yet what work they become.

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
* **44.** [Naming a disk by identity](completed/44-naming-a-disk-by-identity.md)
  — a Machine declares its storage by a name that belongs to the disk,
  and `/dev/disk` grows the `by-id` and `by-uuid` trees beside the
  `by-path` tree that iSCSI needed.
* **45.** [The CLI reaches the cluster](completed/45-the-cli-reaches-the-cluster.md)
  — `approve-reboot` grants a Manual machine the one disruption it
  waits for, and `kubectl`, `stern`, and `flux` run against the right
  cluster with a credential the CLI resolves itself.
* **47.** [The timezone database](completed/47-the-timezone-database.md)
  — the image carries IANA's zone files, so a CronJob can name the
  zone its schedule means, and a weekly job in CI opens the pull
  request that moves the pin.
* **48.** [Watching the pins](completed/48-watching-the-pins.md) —
  every domain that vendors something reports what it pins and what
  its upstream has now, and moves the pin when asked. One `latest.sh`
  beside each `fetch.sh`, and `make versions` for the whole table.
* **49.** [Sharing integrated graphics](completed/49-sharing-integrated-graphics.md)
  — a device's delivery groups by kernel subsystem, and each group
  publishes as its own slice device, so a real integrated GPU shares
  while its display outputs stay exclusive.
* **52.** [Node taints on the Machine](completed/52-node-taints.md) —
  `spec.nodeTaints` declares the repelling half of a machine's
  scheduling identity: init registers the fresh node with its taints,
  and the operator reconciles them live.
* **53.** [Static host entries](completed/53-static-host-entries.md) —
  a `hostEntries` list on the Machine's network spec: init writes
  `/etc/hosts` and an `/etc/nsswitch.conf` that pins the hosts file
  ahead of DNS at every boot, and the operator reconciles the file
  live, writing only on divergence.

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
* **46.** [Configuring etcd snapshots](46-configuring-etcd-snapshots.md)
  — `spec.datastore.snapshots` gives a schedule, a retention, and an
  S3 destination to the snapshots every leader already takes.
* **50.** [Netboot for a declared machine](50-netboot-for-a-declared-machine.md)
  — a `netboot` feature serves proxyDHCP, iPXE, and the leader's own
  slot artifacts, so a new machine boots the report when unknown, the
  installer when declared, and its own disk once installed.
* **51.** [Enrollment over the network](51-enrollment-over-the-network.md)
  — the netbooted report posts to the cluster as an Enrollment, and
  approval is applying the proposed Machine, with a CLI verb as
  sugar.
The hardening tier waits until the milestones above are proven: UKIs,
dm-verity, secure boot, TPM-sealed secrets, and signed releases.

## Open problems

These are the questions that liken owes an answer to. It does not have
one yet. Each one has a document in [`open-problems/`](open-problems/).

* [Claiming unknown machines](open-problems/claiming-unknown-machines.md)
  — `liken.machine=` identifies a machine that somebody declared before
  it booted, and nothing identifies the machine that nobody declared.
* [containerd and the kubelet write lines that no log level explains](open-problems/unexplained-log-volume.md)
  — the levels are settable, and what stays open is the volume they do
  not reach.
