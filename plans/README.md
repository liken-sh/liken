# The rough path

The full story of each milestone — the design, the reasoning, and
what the lab taught when it ran — lives beside this file, one
numbered document per milestone, with [00-design.md](00-design.md)
as the overview. This file is the index and the scratch space for
what's still ahead.

1. [x] [Boot to a hello world](01-boot-to-hello-world.md) —
   a vendored kernel, a Go init, an initramfs, and QEMU.
2. [x] [Init starts k3s and nothing else](02-init-starts-k3s.md) —
   network, identity, the Machine CRD, and the operator.
3. [x] [Unwinding the known hacks](03-unwinding-the-hacks.md) —
   settle the couplings to k3s internals before building on them.
4. [x] [Storage, declared by purpose](04-storage-by-purpose.md) —
   roles, GPT claiming, and refusing ambiguity.
5. [x] [The spec becomes editable](05-the-spec-becomes-editable.md) —
   staged manifests, proven fallback, convergence by reboot.
6. [x] [Growing the cluster past one node](06-growing-past-one-node.md) —
   the Cluster CRD, the join token, static addressing, one image
   boots the fleet.
7. [x] [Cluster time](07-cluster-time.md) — leaders sync from
   declared upstreams and serve everyone else; the two-planes rule
   written down.
8. [x] [The Cluster converges](08-the-cluster-converges.md) —
   the cluster document rides the same staging machinery, promoted
   by the join itself.
9. [x] [Multiple leaders: quorum](09-multiple-leaders.md) —
   sqlite grows into embedded etcd by one Cluster edit; promotion
   and demotion both automated.
10. [x] [Fleet visibility](10-fleet-visibility.md) — phases,
    heartbeat leases, the sweep, and a status vocabulary that says
    what would fix it.
11. [ ] [Device management](11-device-management.md) — hotplug,
    GPUs, and how workloads reach hardware, still an open
    exploration.
12. [x] [Declarative upgrades](12-declarative-upgrades.md) —
    A/B slots, the digest chain, firmware fallback, and one field
    that moves the fleet.
13. [x] [Rolling reboots](13-rolling-reboots.md) — the rollout
    conductor: budgets, drains, and one leader at a time.
14. [ ] [GitOps from first boot](14-gitops-from-first-boot.md) —
    flux as an opt-in feature with parameters (repository, path,
    branch): the vocabulary's first parameterized feature, riding
    the machinery 17 built.
15. [x] [Observability below Kubernetes](15-observability-below-kubernetes.md) —
    every host log stream becomes a pod's stdout.
16. [x] [Adopting an existing cluster](16-adopting-a-cluster.md) —
    import an existing cluster's identity instead of minting one,
    join its etcd, rotate the old members out, promote.

Surveying a real deployment's workloads (part of milestone 16) turned
up capabilities the OS still needs before it can host a working
cluster's workloads. Each is written as the general capability, not
the specific workload that revealed it:

17. [x] [Opt-in features](17-network-storage-clients.md) — one
    Cluster vocabulary for optional capabilities: iSCSI and NFS host
    clients, and the k3s bundled components (absorbing 19). Drilled
    end to end against the lab's storage server, retraction janitor
    included; a CSI driver's own proof belongs to the deployment
    that runs one.
18. [x] [Requestable kernel modules](18-requestable-kernel-modules.md) —
    machines declare the drivers their hardware needs; the image
    ships them, init loads them, status reports them. Ran ahead of
    17, which builds on it.
19. [x] [Choosing the bundled components](19-choosing-bundled-components.md) —
    folded into 17's feature vocabulary.
20. [x] [Private registries](20-private-registries.md) —
    spec.registries (mirrors and Spegel), credentials by Secret, and
    the k3s restart tier: changes k3s reads only at process start
    converge by bouncing k3s in place, pods surviving, with the
    feature toggles migrated onto it.
21. [x] [Node labels on the Machine](21-node-labels.md) —
    scheduling identity declared on the Machine spec: registered at
    boot, reconciled live, and retractions actually retract.

One the milestone-17 lab work demanded:

23. [x] [Crash-safe image imports](23-crash-safe-image-imports.md) —
    a machine killed mid-unpack is no longer left permanently unable
    to run its own operator: image imports ride the staged/proven
    lifecycle, an unproven trial discards the container store, and
    the operator proves the unpacks before anything trusts them.

And the arc that looks past any single deployment, where liken stops
being this checkout and becomes a public project. Milestone 22 was
numbered before the arc existed; it belongs second in this order:

24. [x] [A real repository and CI builds](24-repo-and-ci.md) — a
    public home for the code, and CI that fetches every pin, builds
    every domain, runs the tests, assembles an image, and boots it.
22. [x] [Public releases](22-public-releases.md) — releases of liken
    itself, with no deployment baked in, and the utilities someone
    needs to produce a cluster of their own from one.
28. [x] [Internet updates](28-internet-updates.md) — the deployment
    layer becomes a separate file on the boot slot, machines carry it
    forward themselves, and every update after the first boot comes
    straight from liken's public releases: nothing composed, nothing
    hosted, per deployment.
25. [ ] [A website on liken.sh](25-liken-sh-website.md) — the
    project's domain answers for people the way it already does for
    CRDs: what liken is, and where to start reading.
26. [x] [The public release channel](26-releases-on-the-website.md) —
    the release channel gets its public home at releases.liken.sh:
    digest-verified downloads from object storage, published by CI on
    every version tag; release pages wait for the website.
27. [ ] [Documentation on the website](27-documentation-on-the-website.md) —
    the repo stays the documentation; the site extracts and arranges
    it, plus the reading order and getting-started path a repo can't
    impose.
29. [x] [Root on disk](29-root-on-disk.md) — the OS stops living in
    RAM: the system artifact becomes a read-only filesystem image
    mounted from the boot slot, and a 1 GB machine becomes the lab's
    standing proof that liken stays light.
30. [ ] [Upgrades under BIOS](30-bios-upgrades.md) — the upgrade
    path's second actuator: where UEFI machines flip firmware
    variables, BIOS machines rewrite what GRUB reads, with the same
    one-shot trial and fallback. What the liken.sh node needs before
    it can upgrade itself.
31. [x] [TLS for the website](31-website-tls.md) — liken.sh answers
    over HTTPS via Let's Encrypt, sized for a 1 GB node: Traefik's
    built-in ACME rather than cert-manager's three always-on
    controllers, staged first, and a hard lesson in the node's
    memory envelope.

Deferred until the fundamentals above are proven, the hardening
tier: UKIs, dm-verity, secure boot, TPM-sealed secrets, and signed
releases.

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
