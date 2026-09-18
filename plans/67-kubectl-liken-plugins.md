# 67. kubectl liken: a plugin per operator

## The problem

An operator gives a cluster a capability, and a person or an agent
needs a short command to use it: pair a controller, capture a stream,
ask a library to rescan. Today the only workstation command is the
`liken` toolkit binary, and every operator exposes its verbs inside
the cluster with no local face.

We want one command surface, `kubectl liken ...`, that reaches every
operator. We want each operator to own its own verbs in its own
repository. We do not want a shared code module between them, a plugin
index, or a registry listing to maintain. And we do not want a person
to install six things.

## The shape

`kubectl` already dispatches by longest prefix. For
`kubectl liken audio capture room`, `kubectl` selects
`kubectl-liken-audio` on `PATH` and runs it with `capture room`. So
each operator ships one binary named `kubectl-liken-<domain>`, and the
two-layer command falls out of `kubectl`'s own rule with no code.

Three names reach the same binary:

* `kubectl liken audio capture`: `kubectl` walks the prefix.
* `kubectl-liken-audio capture`: an agent runs the binary directly.
* `liken audio capture`: the base binary walks the prefix itself, so
  the toolkit name keeps working.

The base binary, `kubectl-liken`, is the existing `liken` toolkit
binary under a second name. It owns the `plugins` command group and
the machine verbs it already has (`request-reboot`, `approve-reboot`).
It is installed the normal way, from a release download or a package, not
from the cluster, because it is the thing that reaches the cluster.

### Who ships a CLI

Six repositories, and no others:

* **liken**: the base binary. Machine verbs already exist. New: the
  `plugins` group and the second name.
* **audio**: capture a sink to stdout.
* **display**: capture an output to stdout.
* **media**: capture what a Player shows to stdout.
* **bluetooth**: interactive pairing, and unpair.
* **library**: request a re-enrichment pass. (A full rescan trigger is deferred; see the open problem.)

git-csi, per-node, equipment, and people ship no CLI. There is no
local verb worth a binary there yet.

## Distribution

Every build already pushes an operator image to ghcr under an exact
version: `2026.09.03-007` for a release, and
`2026.09.03-007-dev-003-abcdef01` for a push to main. Each CLI build
publishes a second, runnable container image beside it, under the same
version:

```
ghcr.io/liken-sh/audio-operator:2026.09.03-007        # the operator
ghcr.io/liken-sh/audio-operator-cli:2026.09.03-007    # its CLI
ghcr.io/liken-sh/audio-operator-cli:2026.09.03-007-dev-003-abcdef01
```

The `-cli` image is a real image over a distroless or scratch base
with the binary as its entrypoint, not a bare OCI blob. So the same
artifact serves three uses: `kubectl liken plugins sync` pulls the
binary out of it, a person can `docker run` it, and later a Job can
run it in-cluster.

`plugins sync` does this, once per operator Deployment it finds:

1. List Deployments across the cluster that carry the operator label.
2. Read each one's image reference and its exact version tag.
3. Pull `ghcr.io/liken-sh/<repo>-cli:<same version>`, built for the
   workstation's architecture, and write the binary into the plugin
   directory.

The pull uses `go-containerregistry` compiled into the base binary, so
the workstation needs no `docker`, `oras`, or `crane`. Because the CLI
comes from the same version as the running operator, the two never
drift. A dev build works with no extra path, because ghcr already
holds its tag.

Release builds also attach the plain binary to the release, so a
person can download one without any liken tooling.

The `-cli` image is built multi-architecture (amd64 and arm64), so the
workstation pulls its own architecture regardless of the node's.

## The `plugins` command group

* `kubectl liken plugins sync`: the step above. Idempotent.
* `kubectl liken plugins list`: what is installed, each one's version,
  and the operator version it faces, with drift marked.
* `kubectl liken plugins remove <domain>`: delete one installed CLI.
* Cold start: when `kubectl liken audio` finds no plugin, `kubectl`
  reports a raw "not found". The base binary cannot intercept that,
  so `kubectl liken plugins list` names the operators that run in the
  cluster but have no local CLI, and tells the person to run `sync`.

The plugin directory is `~/.liken/plugins/bin`. `plugins sync` prints
the one line to add it to `PATH` when it is not already there.

## Version safety

Each CLI is stamped with its own version at build time, through the
same `-ldflags "-X main.version=..."` the operator images use. Before
it runs a command, it reads the operator's version cheaply. The
Deployment's image tag is the universal source, and an operator that
serves a version route may use that. It then compares:

* Same version: run.
* Different release: warn on stderr, name `plugins sync`, and run.
  `--force` silences the warning.
* The operator declares a higher minimum CLI version than this binary:
  refuse with an error, and name `plugins sync`.

The transport of the minimum-CLI signal is each operator's choice; the
Deployment image tag is the floor every CLI can read with no new API.

## The convention, not shared code

The binaries share no Go module. They share a written contract on the
site and one label. A CLI:

* is named `kubectl-liken-<domain>`, and answers to a bare `--help`,
  `--version`, and `--context`/`--kubeconfig` through
  `k8s.io/cli-runtime`;
* writes machine-readable output to stdout and everything else to
  stderr, so a capture pipes clean;
* takes the cluster's identity from the standard kube flags and the
  environment, the same as `kubectl`.

One workload has the label the base binary selects on: the
workload that runs the operator's own image at the release version.
That is a Deployment for some operators and a DaemonSet for others.
Bluetooth runs only a DaemonSet, so `plugins sync` selects on the
label across Deployments, DaemonSets, and StatefulSets, and reads the
image from the pod template of whichever it finds.

```yaml
metadata:
  labels:
    cli.liken.sh/plugin: <domain>   # audio, display, media, bluetooth, library
```

## The verbs, first cut

* **liken:** `request-reboot`, `approve-reboot` (exist); reachable now
  as `kubectl liken request-reboot ...`.
* **audio:** `capture <claim>` streams the sound to stdout (wav, and
  `--format flac|opus`), so `... | mpv -` or `> room.wav` works.
  Pairing lives with bluetooth, so audio ships no `pair` verb.
* **display:** `capture <output>` streams the framebuffer to stdout
  (mp4 or png).
* **media:** `capture <player>` streams what the Player shows to
  stdout.
* **bluetooth:** `pair` opens the pairing window and live-renders the
  devices the radio sees, so a person picks one to approve; `unpair
  <device>`.
* **library:** `reenrich <library> [--only art|trailers|...]` requests a
  pass by writing `spec.refresh`. `rescan` (a full walk) has no field the
  operator watches yet, so it is deferred to [library-rescan-trigger](open-problems/library-rescan-trigger.md).

Each capture verb is a thin client over its operator's existing stream
API. It authenticates with the caller's own kubeconfig credential:
the API verifies the client certificate against the cluster's client
authority and reads the caller's identity from it, or accepts a bearer
token. So a person captures with the credential they already hold. Where that server-side capability does not exist yet, the CLI
work stops at a documented stub and the plan for that repo records it,
rather than building the server blind.

## Decisions

* **kubectl's own prefix walk, not a dispatcher we write.** The
  two-layer command is a property of `kubectl`, so there is nothing to
  maintain for it. We write a walk only in the base binary, so the
  bare `liken` and `kubectl-liken` names behave the same.
* **From ghcr, not from the pod.** An earlier idea copied the binary
  out of the running pod with `exec`. ghcr already holds every build
  at the same version, so the registry gives the same version match
  with a standard pull and no exec, and it yields a runnable image for
  free.
* **A convention on a page, not a shared module.** The binaries stay
  in their own domains with the lightest possible coupling, which is
  what the org's layout asks for.

## Follow-on, separate track

Graduate release tags to GitHub Releases across the org, for upgrade
notes in one place. It touches every repository's CI and is orthogonal
to this: the CLI build attaches its binary the same way whether the
tag is a bare tag or a Release.
