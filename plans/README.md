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
    a reader exercise: the seed channel and the minting primitive,
    not a vendored engine.
15. [x] [Observability below Kubernetes](15-observability-below-kubernetes.md) —
    every host log stream becomes a pod's stdout.
16. [x] [Adopting an existing cluster](16-adopting-a-cluster.md) —
    import an existing cluster's identity instead of minting one,
    join its etcd, rotate the old members out, promote.

Surveying a real deployment's workloads (part of milestone 16) turned
up capabilities the OS still needs before it can host a working
cluster's workloads. Each is written as the general capability, not
the specific workload that revealed it:

17. [ ] [Host clients for network storage](17-network-storage-clients.md) —
    iSCSI and NFS userspace and modules, so CSI node plugins can
    actually mount.
18. [ ] [Requestable kernel modules](18-requestable-kernel-modules.md) —
    deployments declare the drivers their hardware needs.
19. [ ] [Choosing the bundled components](19-choosing-bundled-components.md) —
    liken's disable list becomes a Cluster declaration, not an image
    hardcode.
20. [ ] [Private registries](20-private-registries.md) —
    containerd mirrors and credentials, and k3s's embedded registry.
21. [ ] [Node labels on the Machine](21-node-labels.md) —
    scheduling identity declared on the Machine spec and reconciled
    by the operator.

And one that looks past any single deployment:

22. [ ] [Public releases](22-public-releases.md) — releases of liken
    itself, with no deployment baked in, and the utilities someone
    needs to produce a cluster of their own from one.

Deferred until the fundamentals above are proven, the mastery tier:
UKIs, dm-verity, secure boot, TPM-sealed secrets.

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
