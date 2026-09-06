---
title: Running on a small machine
weight: 90
---

# Running on a small machine

A screen runs on the machine that holds its display, and on a `liken`
cluster that is often a box with one gigabyte of memory. This guide
gives the numbers the operator's pods take, and the settings that
change them.

## What a screen machine runs

A screen pod is two containers. The catalog agent holds the
namespace's catalog and requests `64Mi` of memory with a `512Mi`
limit. The limit is wide because the agent's first sync holds the
whole catalog in memory as it applies it, and it settles far below
that once the sync completes. The browser has no limit of its own.

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
memory, sized from the window. Scaled posters go to a disk cache of
512 MiB, on an `emptyDir` capped at `640Mi`.

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

A scan `Job` requests `32Mi` with a `64Mi` limit, and its own catalog
agent the same. The enrich `Job`'s art container is limited to `256Mi`.
The trickplay container, when enabled, requests half a CPU with a
`512Mi` limit, because it runs `ffmpeg` where every other container
reads rows and files. Every pod that runs an agent has a sixty-second
termination grace period, twice the default, because a busy agent
flushes its database on the way out.

## What to turn down

* **Leave `spec.trickplay.enabled` off**, its default. The first pass
  reads every video end to end, hours of CPU on any library, and it is
  the one enrich container with a CPU request of its own.
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
  every title for that fact, and the enricher fills it before the
  next.

The agent's `512Mi` limit and the poster cache's `640Mi` cap are
constants of the operator's build, not fields.
