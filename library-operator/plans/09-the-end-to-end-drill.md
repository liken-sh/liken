# The end-to-end drill

Plan 09. The proof of plans 01 to 08 as one thing, on `liken-1`, from
a cluster that has never run the operator to a film playing from the
browser, with the numbers written down.

## The problem

Each plan proves its own slice. None proves that a person can install
the operator on a cluster, point it at a volume, and watch a film from
the couch. That is the outcome the design promises, and the later
plans budget against the numbers this proof records.

## The drill

From `media-operator` and the hardware operators already running:

1. Install the operator from a release. Create the claims for the
   lab's film and series volumes. Create one `Library` of each kind.
2. Watch both scanners walk their roots. Record the wall time to the
   first full report, the title counts, the unidentified counts, and
   the catalog's size on disk.
3. Point one `Player` at the browser image with the sidecar. Record
   the time from the pod's start to a catalog in full sync, and the
   sidecar's and the browser's resident memory after the sidecar's
   restart.
4. From the room's remote: wake the screen, open the films, find a
   title, play it, watch the scrub bar's thumbnails, stop it, and see
   the wall again. Then the same through a series to an episode.
5. Add a film to the volume and send the webhook. Record the time
   until it is on the wall.
6. Reboot the machine that runs the screen. Record the time until the
   wall is back with its catalog.

## What is recorded

The completed plan has the numbers from every step, the release they
were taken on, and anything that had to be fixed. Those numbers are
the floor the later plans build on: the memory a screen has left, the
time a catalog takes to arrive, and the delay between a file landing
and a poster appearing.

## Proof

The drill itself, on `liken-1`, on a tagged release, with the numbers
in this document when it moves to `completed/`.
