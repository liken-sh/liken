# The CLI reaches the cluster

Milestone 45 — Completed. `liken` has a cluster client: one command
that grants a machine the reboot its policy withholds, and three that
run the tools an operator already uses against the right cluster.

One detail settled differently than the sketch below. `gateDisruption`
takes the annotation's value, not an `approved bool`, so the mismatch
report lives in the gate once instead of in four callers. The matching
rule accepts the full hash or a prefix of at least 12 characters,
because every condition message shows the hash in its short form and a
person pastes what they can see.

`liken` builds media, mints identity, and serves releases. Every one of
those runs offline. Nothing in the CLI talks to a running cluster, and
that gap costs an operator two things: a credential they must assemble
by hand, and a remedy that liken names but does not offer.

## The remedy nobody can reach

A machine whose `spec.rebootPolicy` is `Manual` reports that it is
waiting for a person to reboot it. liken offers no way to do that. There
is no command, no annotation, no shell, and no handling of the power
button. The operator's only move is to cut power to a machine that is
running Kubernetes workloads.

There is a second problem behind the first. Under `Auto`, a registry
credentials change converges with a k3s restart in place. Under
`Manual`, the same change costs a full reboot, because `Manual` gates
every disruption at the coarsest tier. An operator who chose `Manual`
to keep control of reboots gets more reboots, not fewer.

## The grant

`liken.sh/approve-disruption` on the Machine, valued with the hash of
the staged document it approves.

The hash makes the grant one-shot by construction. Once the change
applies, that hash is no longer pending, and the next change hashes
differently, so a stale annotation approves nothing. Nobody has to
consume it, and that matters: the machine operator holds `get`, `list`,
`watch`, `create` on machines and `update` on machines/status. Clearing
a boolean would need `patch` on machines, which would let a per-node
operator edit any machine's spec. A grant that expires on its own needs
no new verb.

`gateDisruption` would take an `approved bool`. An approved `Manual`
machine takes the path an `Auto` machine takes: it reports
`AwaitingTurn`, the rollout conductor grants it a turn against the
disruption budget, and the drain runs. This is safer than the state it
replaces, where an operator following the machine's own advice cuts
power and no budget applies at all.

An annotation naming some other hash is reported, not ignored. The
Pending message would carry both values, so a wrong paste is visible
where the person is already looking.

`status.pending` would list what each machine waits for: the condition
type, the kind (`Reboot` or `Restart`), the hash, and a one-line
summary. It exists so that nothing has to parse a condition message to
find a hash, and so the question "what is this machine waiting for me
to allow" has a field that answers it.

## The four commands

```
liken approve-reboot [-server URL] <deployment-dir> <machine>
liken kubectl        [-server URL] <deployment-dir> [args...]
liken stern          [-server URL] <deployment-dir> [args...]
liken flux           [-server URL] <deployment-dir> [args...]
```

Three of these are passthrough. They resolve the credential, set
`KUBECONFIG`, and hand the terminal to the real tool. `approve-reboot`
is the one that carries liken's own meaning: it reads `status.pending`,
reports what is waiting, and writes the annotation.

```
$ liken approve-reboot mycluster node-5
node-5 is waiting on one change:
  CredentialsConverged  RestartPending
  registry credentials for 2 hosts (3943abfa6adf)
  a k3s restart applies this; the machine does not reboot

approved: liken.sh/approve-disruption=3943abfa6adf
```

When two changes are pending it approves the `Reboot` one, because a
reboot applies every staged document. When nothing is pending it says
the machine is converged and writes nothing. Running it twice writes
the same annotation, so it is repeatable.

## Why exec, and what liken does not become

Each passthrough command runs the tool that is on `PATH` and replaces
itself with it, so the tool owns the terminal and its exit code is
liken's. liken does not reimplement `kubectl`, and it does not vendor
these binaries either. A missing tool is a plain message that names it.

This is the boundary the milestone must hold. liken ships a distribution
and a release channel. It is not a package manager for other people's
CLIs, and every command here is a credential and an endpoint handed to a
program the operator already chose to install. `stern` and `flux` are
third-party tools under their own licenses, and nothing here
redistributes them, so the licensing domain does not change.

`flux` earns its place for a reason the others do not have: liken plants
the Flux engine, mints its deploy key, and tears it down when the
cluster stops declaring the feature. An operator inspecting that engine
should not have to build a kubeconfig by hand to do it.

## The deployment directory

The one positional argument is the deployment directory, not an
identity directory. Identity sits at `<deployment-dir>/identity` and the
endpoint comes from `<deployment-dir>/cluster.yaml`. That is the layout
GETTING-STARTED.md documents and the layout `dev-cluster/` already uses.

`liken kubeconfig` changes with them, from `<identity-dir>` to
`[-server URL] <deployment-dir>`, and it writes the endpoint it
resolved. That deletes the step in GETTING-STARTED.md that tells a
person to edit the `server:` line by hand.

**`-server` is needed, not merely convenient.** `spec.endpoint` is the
address a follower joins through from inside the cluster segment. For
the dev lab that is `https://10.10.0.1:6443`, which the workstation
cannot reach; the host reaches those guests through QEMU's forwarded
`127.0.0.1:16443`, and the gitops lab through `17443`. The endpoint from
`cluster.yaml` is the default and `-server` overrides it. The
dev-cluster Makefile would pass the forwarded address, which also
deletes the `sed` that rewrites the gitops kubeconfig by hand.

## Where the kubeconfig lives

An exec replaces the process, so no deferred cleanup runs and a
temporary file would leak. The kubeconfig would go to a predictable
path that the commands rewrite and reuse, with 0600 permissions, rather
than to a fresh file per run. The path belongs next to the identity it
is derived from, because that is already the directory an operator
guards.

## Verification

`machine-operator` tests cover the gate: `Manual` with a matching
approval requests the disruption, `Manual` with a mismatched approval
stays pending and names both hashes, `Manual` with no approval is
unchanged, `Auto` is unchanged, and an approval for a spent hash
approves nothing. All four callers share `gateDisruption`, so one case
per document type covers them.

The `cli` package has a 79% coverage floor. Each exec stays in a thin
wrapper, and the decisions stay in pure functions: resolving the
endpoint, choosing which pending entry to approve, and building each
argument list.

On the dev cluster: set `rebootPolicy: Manual` on a node, create a
`registry-credentials` Secret, and confirm `CredentialsConverged`
reports `RestartPending` and `status.pending` names the hash. Run
`liken approve-reboot`, and confirm k3s restarts in place, the machine
never reboots, and the condition converges. Annotate a stale hash and
confirm the message reports the mismatch.

## What the lab measured

The lab ran the drill above on 2026-07-31, on the four-guest dev
cluster, after `liken kubectl` itself watched the fleet roll to the
drill build: all four machines moved in 3 minutes 30 seconds, the
worker first and one leader at a time.

With `rebootPolicy: Manual` on node-4 and a two-host Secret created,
`CredentialsConverged` reported `RestartPending` and `status.pending`
carried one entry: `CredentialsConverged`, kind `Restart`, the full
hash, and the summary `registry credentials for 2 hosts`. A hand-pasted
annotation naming `deadbeefdead` put both values in the condition
message within one reconcile pass.

`liken approve-reboot` printed the report this document sketches, word
for word, and wrote the short hash. The condition read `Converged`
about 25 seconds after the grant. The machine's uptime ran from 3m33s
to 3m47s across that window, so k3s restarted in place and the machine
never rebooted. A second run printed `node-4 is converged; nothing is
waiting` and wrote nothing, and the spent annotation stayed on the
Machine approving nothing.

`liken stern` tailed both machine-operator pods, and `liken flux`
ran its prerequisite checks, each through an exec of the tool already
on `PATH`, against the kubeconfig the command had just rewritten.

## The manual

`docs/content/docs/reference/cli.md` gains all five commands.
`docs/content/docs/guides/upgrade.md` gains the `Manual` flow: what
`RebootPending` means, and how to grant one disruption.

## Not in this milestone

The ACPI power button. It is the only stop signal a person standing at
a machine with no shell and no network can give, and the grant above
removes the need for it in this case. It is its own work: an init
component that opens the power button's evdev device and treats
`KEY_POWER` as a request for a clean stop.
