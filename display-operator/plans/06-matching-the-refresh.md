# 06, Matching the refresh

Queued on 2026-08-19, behind plan 05. A claim's `mode` grows a
refresh: `mode: "3840x1600@24"` runs a 24 fps film without the
3:2 cadence a 60 Hz mode forces on it.

## What plan 05 already carries

Weston's `mode=` accepts `WIDTHxHEIGHT@refresh`, so the config
write and the restart need nothing new. The claim parameter, the
validation seam, the readback, and the restart budget all exist.
This plan is the refresh half of the vocabulary, deferred from
plan 05 because it carries two traps of its own.

## The two traps

* Weston parses the refresh as an integer. `@59.94` reads as 59,
  matches nothing, and falls back silently, the same silent
  fallback plan 05 guards with validation and readback. So the
  claim's refresh is an integer, and validation must compare it
  against the kernel's rounded `vrefresh`, not a millihertz
  value.
* The aspect-ratio flags skew which entry matches. A name that
  exists only with an aspect flag routes through Weston's
  fallback slot, which still outranks `preferred`, so the request
  lands, but the entry that wins among several sharing a name and
  refresh is the kernel's first.

## Validation and inventory

The `modes` attribute stays as it is: names carry no refresh, and
the attribute budget has no room for a refresh list per name.
Validation reads the kernel instead: the `GETCONNECTOR` ioctl
returns every mode with its name and its `vrefresh`, on the card
node the operator already holds, through the same walk the
`currentMode` read uses. A refresh the connector does not offer
for that name fails the prepare, and the error names the refreshes
that exist.

`currentMode` should grow the refresh when this plan lands,
`3840x1600@24`, so the readback and the slice state the whole
fact.

## Who writes the claim

A person, in the manifest, for now. The pod cannot ask after
discovering its film's frame rate, because the claim is set before
the pod starts. A controller that probes the file and templates
the claim is a media-orchestration job above this operator, and
nothing here blocks it: the API it would write is exactly this
parameter.
