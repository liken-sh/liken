# 06, Matching the refresh

Built, and drilled on liken-1 on 2026-08-19 with release
2026.08.19-007. A claim's `mode` grows a refresh:
`mode: "3840x1600@24"` runs a 24 fps film without the 3:2 cadence
a 60 Hz mode forces on it.

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

## The stale mode list

This plan absorbs the open problem "A mode list read too early
goes stale". The adoption of 2026.08.19-004 measured it: the
operator's first pass ran while the compositor was still bringing
the link up, the connector answered six modes with the three
largest missing, and the slice published that. Minutes later the
kernel's own file held all sixteen names, and the slice still said
six, because the only trigger for a re-read was a hotplug uevent,
and the compositor's own startup probe raises none. A restart of
the pod republished the full list.

The fix has two halves, one for each reader of the list:

* The prepare path never reads the slice. Validation reads
  `GETCONNECTOR` at prepare time, so a claim is judged against
  what the kernel holds at that moment, and a stale publication
  cannot fail a valid claim.
* Every prepare that reached the card republishes the slice, on
  the restart path after the readback, and on the idempotent path
  too. The prepare just read the kernel's live list, so it is the
  moment the operator can know the slice is behind, and the write
  still happens only on divergence. The restart path can never
  leave a short list behind, because its readback ends after the
  restart's own link probe.

Two shapes were weighed and set aside. A periodic re-read would
converge every stale list, but it would be this operator's only
polling loop. Refusing to write a list that shrank while its EDID
held still would keep the last good list, which is stale in the
other direction.

One window stays open: a first pass that runs mid-probe still
publishes a short list, and it holds until the next prepare, wake,
or hotplug. The drill measures whether that window matters on real
hardware.

## Who writes the claim

A person, in the manifest, for now. The pod cannot ask after
discovering its film's frame rate, because the claim is set before
the pod starts. A controller that probes the file and templates
the claim is a media-orchestration job above this operator, and
nothing here blocks it: the API it would write is exactly this
parameter.

## What a drill must show

The drill runs on liken-1, against the monitor plan 05 already
drives.

1. A claim that states the panel's resolution at a second refresh
   the connector offers prepares, the compositor restarts once,
   and `currentMode` reads back the name with that refresh.
2. A claim whose refresh the connector does not offer fails the
   prepare, and the error names the refreshes that exist for that
   name.
3. A claim with no refresh keeps plan 05's behavior: it matches
   whatever refresh the screen already runs, with no restart.
4. After the operator's pod restarts with a claim in place, the
   slice's mode list matches the kernel's own file for that
   connector, so the republish on prepare holds against the case
   the folded problem measured.

## What the lab measured

The drill ran on liken-1 on 2026-08-19, on release 2026.08.19-007.
The LG widescreen offers `3840x1600` at 60, 75, and 30, and the
claim `3840x1600@30` restarted the compositor exactly once and read
back `3840x1600@30`. The refusals named what exists:
`HDMI-A-2 does not offer the mode "1280x720@100"; it offers
1280x720 at 60`. A bare `3840x1600` claim against the screen
running 30 Hz delivered with no restart. After a delete of the
operator's pod with that claim in place, the consumer pod stayed
`Running`, the prepare republished the slice, and both connectors'
mode lists matched the kernel's own files exactly up to the
64-character cut: `HDMI-A-1`'s seventh name would need 68
characters and `HDMI-A-2`'s eighth would need 69.
