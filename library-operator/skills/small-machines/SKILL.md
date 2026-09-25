---
name: small-machines
description: "The memory each library-operator pod takes on a one-gigabyte screen machine, and the settings that lower it. Use when a screen pod is evicted or killed for memory, or when sizing a small node."
---

This skill is the guide at https://library.liken.sh/docs/guides/small-machines/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Running on a small machine

A screen runs on the machine that holds its display, and on a `liken`
cluster that is often a box with one gigabyte of memory. This guide
gives the numbers the operator's pods take, and the settings that
change them.

## What a screen machine runs

A screen pod is two containers. The catalog agent holds the namespace's
catalog and requests `64Mi` of memory with a `512Mi` limit. The limit is
wide because the agent's first sync holds the whole catalog in memory as
it applies it. The agent settles far below that limit once the sync
completes. The browser has no limit of its own.

Measured on a one-gigabyte box, after the screen's catalog synced,
the browser rested at 216 MiB, most of it the page-size backdrops in
its cache. On a workstation, a walk through a wall with focus resting
at every step peaked at 186 MiB and rested at 153 MiB. Two things in
the browser hold those numbers down:

* It pins glibc's `mmap` and trim thresholds at 128 KiB, so a
  page-size decode comes from `mmap` and returns to the kernel when
  freed. Without the pin, decode buffers dirtied the allocator's
  arenas, and the browser held up to 300 MiB it never released.
* It decodes one page-size image at a time. Four at once dirtied four
  arenas on a one-gigabyte box.

The browser keeps 96 decoded posters and three decoded backdrops in
memory, sized from the window. Every piece of art it scales goes to a
disk cache on the screen's art claim, sized by
`spec.screens.artCache.size` with 2Gi as the default. A screen in a
namespace with no single `Catalog` keeps that cache on an `emptyDir`
capped at `640Mi`, with a 512 MiB budget inside it.

## Give the screens a claim

The largest measured cost on a small box is a screen restart on an
`emptyDir`, where the agent syncs the whole catalog again every time.
A `Catalog` in the namespace gives every screen a claim, and the sync
happens once:

| Screen restart | On an `emptyDir` | On a claim |
|---|---|---|
| Time to the full catalog | 157 s | 0 to 1 s |
| Agent memory after | 211 MiB | 11 MiB |

    apiVersion: library.liken.sh/v1alpha1
    kind: Catalog
    metadata:
      name: catalog
      namespace: media
    spec:
      storage:
        size: 1Gi
      screens:
        storageClassName: local-path

## The worker pods

A `Library` runs one `Job` at a time, and every container of that `Job`
is a regular container that runs at the same time as the others, beside
one catalog agent. Kubernetes schedules the pod on the sum of their
requests:

| Container | Memory request | Memory limit | CPU request |
|---|---|---|---|
| the catalog agent | `64Mi` | `512Mi` | `10m` |
| `scan`, in a walk | `32Mi` | `64Mi` | `10m` |
| `probe` | `32Mi` | `256Mi` | `10m` |
| `art` | `32Mi` | `256Mi` | `10m` |
| `trickplay`, when enabled | `32Mi` | `512Mi` | `500m` |
| `trailer-files`, when enabled | `32Mi` | `256Mi` | `10m` |
| every other phase, and `close` | `32Mi` | `64Mi` | `10m` |

A walk of a `Library` whose sources serve every phase runs twelve
containers beside the agent, so the pod requests about `448Mi`. A `Job`
that fills gaps runs only the phases with work, and requests less. The
probe and trickplay limits are wide because `ffmpeg` and `ffprobe` hold
a decoded stream, and the art limit because the art container holds an
image while it writes it. Every pod that runs an agent has a
sixty-second termination grace period, twice the default, because a
busy agent flushes its database as it stops.

The peak memory of a whole `Job` on a one-gigabyte machine is not
measured yet.

## What to turn down

* **Leave `spec.trickplay.enabled` off**, its default. The first pass
  reads every video end to end, hours of CPU on any library. Where it
  is on, `spec.trickplay.render` moves the decode onto the GPU.
* **Walk less often.** `spec.scan.schedule` defaults to once an hour.
  A webhook covers imports between walks:

        spec:
          scan:
            schedule: "0 */6 * * *"

* **Skip what is not media.** `spec.ignore` keeps the walk out of
  recycle bins and staging directories:

        spec:
          ignore:
            - "@eaDir"
            - ".recycle"

* **Refresh one fact at a time.** Each name in `spec.refresh` reopens
  every title for that fact, and a `Job` fills it before the next.

The agent's `512Mi` limit and the `emptyDir` cache's `640Mi` cap are
constants of the operator's build, not fields. The art claim's size
is a field, `spec.screens.artCache.size`.
