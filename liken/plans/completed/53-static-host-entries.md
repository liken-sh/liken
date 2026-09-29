# Static host entries

Milestone 53. Completed. A Machine declares names that resolve
without any DNS: a `hostEntries` list on the network spec. Init
writes the entries into `/etc/hosts` on every boot, together with an
`/etc/nsswitch.conf` that pins the hosts file ahead of DNS, and the
machine operator reconciles the file live, so an edit applies
without a reboot.

## The problem

init writes `/etc/hosts` on every boot, with three fixed lines:
`localhost`, its IPv6 form, and the machine's own name at `127.0.1.1`.
Nothing in the machine spec adds a fourth line. A name that must
resolve without cluster DNS has no place to be declared.

A machine that mounts NFS by hostname re-resolves that name on every
kubelet reconcile, because neither the kubelet nor the mount helper
keeps a DNS answer. One fleet measured about 130 lookups a second for
a single NAS name, all of them served by cluster DNS. The mount then
depends on cluster DNS to be up, which inverts the order a cold start
needs: storage should resolve before the services that name it. A
static entry ends the lookups and breaks that dependency. Milestone
[17](17-network-storage-clients.md) gave the fleet its NFS
and iSCSI clients, but no way to name their target without DNS.

A static entry has one failure mode worse than none: an entry that
DNS shadows. If a resolver queries DNS before it reads the hosts
file, the entry stays in the file and never answers, and nothing
reports that it lost. The node ships no `/etc/nsswitch.conf`, so the order
between `files` and `dns` is whatever each resolver compiled in. This
design starts by measuring those defaults.

## Every resolver on the node

Three resolver families exist, and the node runs two of them. What
each binary links is a build fact, checked against the build scripts
and the built artifacts:

* liken's own programs, init and `mount`, are static Go with
  `CGO_ENABLED=0` (`init/Makefile` states this and why). A Go binary
  built this way has no libc; it resolves with Go's own resolver.
* k3s, and the kubelet inside it, is also static Go with
  `CGO_ENABLED=0`. `go version -m` on the vendored `v1.36.3+k3s1`
  binary shows `build CGO_ENABLED=0`.
* `mount.nfs`, `iscsid`, and `iscsiadm` are static musl binaries,
  built inside a pinned Alpine container (`nfs-utils/fetch.sh`,
  `open-iscsi/fetch.sh`). `mount.nfs` is the program that resolves an
  NFS server's name, because the kernel's mount path runs it as the
  `nfs` filesystem's helper.

No glibc binary ships on the node today.

An experiment measured what each family does with no
`/etc/nsswitch.conf`. A hosts file said `203.0.113.7 nas.drill`, and
a nameserver answered every A query with `198.51.100.99`, so the
answer names the source that gave it:

* musl returned `203.0.113.7`. musl never reads `nsswitch.conf` at
  all; its lookup order is compiled in as hosts file first, then DNS.
* Go's resolver returned `203.0.113.7`. `net/conf.go` (checked at
  go1.26.5) returns its files-then-DNS order both when
  `nsswitch.conf` is absent and as the fallback when cgo is
  unavailable.
* glibc 2.31 returned `198.51.100.99`. With no `nsswitch.conf`,
  glibc's compiled-in default for hosts queries DNS first and reads
  the file only when DNS is unavailable. The static entry lost, and
  nothing reported it. With a `nsswitch.conf` of `hosts: files dns`,
  the same glibc returned `203.0.113.7`.

So every resolver the node runs today reads the hosts file first,
even with no `nsswitch.conf`. The one measured shadowing case is a
glibc binary on a machine without the file.

## Why liken writes nsswitch.conf

init writes `/etc/nsswitch.conf` with one line, `hosts: files dns`,
on every boot, beside `/etc/hosts`. Today the file changes no
behavior: musl ignores it, and it names the order Go already
defaults to. It exists for two reasons. It makes the resolution order
a declared fact on the machine, instead of three compiled-in defaults
that a person must know per binary. And it closes the measured
failure in advance: the first glibc binary that ever lands on a node,
in a future vendored component or an add-on image that bind-mounts
host files, would otherwise resolve DNS first and shadow every static
entry silently.

## The declaration

The field is `spec.network.hostEntries`: a list of entries, each an
address with one or more names.

```yaml
spec:
  network:
    hostEntries:
      - address: 10.10.0.20
        names: [nas, nas.home.arpa]
```

It belongs under `network` because it is name-resolution
configuration, and that is where resolution already is: each interface's
`nameservers` reach `/etc/resolv.conf` the same way these entries
reach `/etc/hosts`. That is the whole argument for the placement.
The field does not share the rest of the network spec's convergence
machinery: an interface edit stages and waits for a reboot, while a
host entry edit applies live, within one reconcile pass (see the
Convergence section).

The schema, in the style the CRD already uses:

* The list is a map-typed list keyed on `address`, with a maximum of
  64 entries. One address is one line of the file, so the API server
  refuses a manifest that declares the same address twice, the same
  way it refuses a duplicate interface name.
* `address` is required, with the same literal-address pattern and
  length the `gateway` field uses. IPv4 and IPv6 both pass. init
  parses the literal again when it writes the file, and the file
  parser refuses a value that is not an address, because a manifest
  carried in on a stick never reached the API server
  (`NetworkSpec.Validate` is the precedent).
* `names` is required with at least one item and at most twenty: an
  atomic list of DNS names, lowercase letters, digits, dashes, and
  dots, each label starting and ending with a letter or digit, at
  most 253 characters. Lowercase only, because DNS compares names
  without case and one spelling keeps the file readable. The upper
  bound exists for the CEL rule below: the API server prices a rule
  by the worst case the schema allows, and a rule that iterates an
  unbounded list prices past the budget, so the API server refuses
  the whole CRD.
* One CEL rule refuses the name `localhost` in any entry. The fixed
  lines define `localhost`, and an entry that redefines it could only
  shadow or contradict them.

The schema descriptions become the Machine reference in the manual,
so they are written as manual text: what the field does, that entries
land below the OS's fixed lines, and that an edit applies live,
within one reconcile pass.

The three fixed lines always come first, so on any conflict the OS's
own lines win: an entry that names the machine's own hostname never
overrides `127.0.1.1`, because resolvers take the first match.

## What init writes

`configureNameResolution` (init/system.go), a dedicated step in the
boot order, writes `/etc/hosts` on every boot. It runs after init has
resolved the boot's manifest, so the spec is in hand at that point.
The write grows from three fixed lines to the fixed lines plus one
line per entry, in spec order:

```
127.0.0.1 localhost
::1 localhost
127.0.1.1 node-1
10.10.0.20 nas nas.home.arpa
```

The same step writes `/etc/nsswitch.conf` with `hosts: files dns`,
whether or not the spec declares any entry.

One function renders the file, and it is in the machine package,
which holds the logic that init and the operator must agree on
(machine/drift.go states that rule). Both writers call it, so the
two programs can never produce two shapes of the file (the
Convergence section explains why the file has two writers).

init prints each entry as it writes it, one line per entry, so the
serial console shows what this boot's file holds. The console-parity
rule then requires the same facts on the Machine, and the live
status reports them: `status.hostEntries` reports the entries as the
file actually holds them, observed on the pass that publishes them.
There is no `boot/network/hostEntries` facts record. A boot record
answers what one boot actuated, and a file the operator may rewrite
on any pass is not one boot's fact. The precedent is sysctls: no
boot record exists for them either, and `status.sysctls` is the
current view.

## Convergence

Host entries reconcile live. The system applies them twice, by
design, the way it applies sysctls. Init writes `/etc/hosts` at
boot, so the entries hold before k3s starts, and every boot proves
the cold-start order on its own. The operator then reconciles the
file on every pass, so an edit applies within one pass, with no
reboot. Sysctls, node taints, and node labels already converge this
way. An interface edit still stages and waits for its reboot,
because an address cannot move under a running cluster; a hosts line
can change under one, because each resolver reads the file again at
each lookup. The operator reconciles `/etc/hosts` and nothing else:
`/etc/nsswitch.conf` holds the same one line on every machine, so
init's write at boot is the only write it needs.

`hostEntries` therefore takes no part in `NetworkDrift`. An edit
never reports drift and never stages a manifest, because the pass
that read the edit also applied it. What an operator sees instead is
`status.hostEntries`, parallel to `status.sysctls`: the entries as
the file holds them, observed on the pass that publishes them. A
`HostEntriesApplied` condition reports a write error, the way
`SysctlsApplied` does.

The operator writes only on divergence. This is the rule for
everything that reconciles live: read the current state first, and
write only what differs from the spec. Here that means render the
desired file with the shared renderer, read the actual file, and
write only when the bytes differ. A converged machine is the common
case, and a pass that writes nothing when nothing changed costs less
I/O and leaves no false modification signals, file mtimes and
inotify events, for programs that watch those files. The rule keeps
the healing property: a file that an outside edit changed differs
from the rendered file, so the next pass rewrites it. This milestone
also applies the rule to sysctls: `applySysctlSet` reads each
parameter first and writes only the ones that differ. That change is
small and ships here, not in a milestone of its own.

The write needs one new mount. The operator pod gains a hostPath
volume for `/etc`, the directory rather than the file, because an
atomic replace writes a temporary file beside the target and renames
it into place, and a bind mount of the file itself would pin the
inode the rename discards. The pod is already privileged, so the
mount is the only change to its manifest.

Two objections argue for converging by reboot instead, and both have
answers. The first: the entry's purpose is the cold start, storage
resolving before cluster DNS, and only a boot proves the entry in
that order. That proof holds, because init still writes the file at
every boot, before k3s starts; the operator's live writes add
convergence without changing what a boot proves. The second: a live
writer gives `/etc/hosts` a second author beside init. The sysctls
design already has this shape: two writers, one desired state,
and one application path. The shared renderer is that path here, so
the two writers can only ever produce one file.

## Scope: the machine plane

These entries serve the programs that resolve with the host's own
files: `mount.nfs` for NFS volumes, `iscsiadm` for portal addresses,
and k3s itself. Pods never receive them, because the kubelet writes each
pod's `/etc/hosts` itself and cluster DNS serves pod lookups. That is
the correct boundary: the motivating mount happens on the host, and a
workload that needs a static name has `hostAliases` in the pod spec
already.

## The manual

The Machine reference regenerates from the CRD schema, so the schema
descriptions above are that page's change. No hand-written page
changes: no guide names DNS or an NFS host mount today, and the
feature needs no procedure beyond editing the spec, which the guides
already cover generally.

## What the lab measured

A QEMU drill on the dev cluster, against the storage fixture
(dev-cluster/storage) serving an NFSv4 export:

* The files after boot. A guest booted with entries declared. A
  drill pod with a hostPath mount read `/etc/hosts` and found the
  three fixed lines followed by the entries, in spec order, and read
  `/etc/nsswitch.conf` and found `hosts: files dns`.
* A mount with no DNS at all. A PersistentVolume named the export by
  a name that exists in no DNS zone, only in `hostEntries`. With
  CoreDNS scaled to zero, and again after a reboot, the pod mounted
  the export, wrote, and read back. The `addr=` option in
  `/proc/mounts` showed the entry's address, so the shipped
  `mount.nfs` resolved the name from the file on the real node.
* Live convergence. An added entry landed in `/etc/hosts` 4.4
  seconds after the patch, and a removed entry left the file in 4.7
  seconds, on the operator's 10-second pass. `status.pending` stayed
  empty, `SpecConverged` never transitioned, and no reboot happened.
  `status.hostEntries` matched the file before the edit and after.
* Write on divergence. With the spec stable, the file's mtime held
  unchanged across six reconcile passes: a converged pass writes
  nothing.
* Healing. A drill pod appended a bogus line by hand. The next pass
  rewrote the file to the spec's shape, one write, 4.5 seconds after
  the edit.

Two checks from the design stay unexecuted in the lab. The
shadowing variant, a nameserver that answers a declared name with a
wrong address, ran only in the resolver experiment's containers,
because the lab has no nameserver of its own to poison. And the
lookup-volume count, the steady query rate that a static entry ends,
depends on the fleet measurement in the problem statement rather than
on a lab count.
