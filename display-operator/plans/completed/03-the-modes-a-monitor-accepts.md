# 03, The modes a monitor accepts

Built and adopted on liken-1 on 2026-08-19. Inventory only: this plan publishes what
each monitor accepts and changes nothing about what the compositor
drives.

## The problem

The inventory states each monitor's preferred mode, as
`widthPixels`, `heightPixels`, and `refreshMillihertz`, and
nothing else. A monitor accepts more modes than its preferred one,
and a reader of the slice cannot see them. The audio operator's
plan 05 gives Bluetooth speakers a `codecs` attribute for the same
reason: a device's alternatives belong in the inventory, so that a
choice is visible before anyone builds a way to make it.

## The source

The kernel already holds the answer. DRM parses every EDID timing,
prunes the modes the connector cannot drive, and lists the
survivors in `/sys/class/drm/<connector>/modes`, one name per
line, the preferred mode first. This operator already walks that
directory for `status` and `edid`, so the read is one more file
per connector.

Reading the list beats parsing the rest of the EDID ourselves. The
EDID states modes in four different encodings across its base
block and extensions, the kernel decodes all four, and it also
drops the modes the hardware cannot carry, which no EDID parse
would know.

Two facts about the file, read on the lab machine:

* A name repeats once per refresh variant. The ultrawide lists
  `3840x2160` seven times. The names carry no refresh, so the
  variants collapse under deduplication, and the published list
  speaks resolutions only. The preferred mode's refresh is already
  its own attribute.
* The list runs long. The ultrawide names sixteen distinct
  resolutions, about 160 characters joined, and the API caps a
  string attribute at 64.

## The design

Each connected output publishes `modes`: the connector's mode
names, deduplicated in the order the kernel listed them, space
joined. When the next name would push the string past 64
characters, the list ends there. The kernel's order puts the
preferred mode first and descends, so the cut drops the smallest
tail, `832x624` and below on the ultrawide, which is the end no
claim asks for. The lab ultrawide publishes six of sixteen; the
portable panel publishes seven of eight and drops only `640x480`.

The attribute joins the space-separated-string convention that
`lpcmBitDepths` and plan 05's `codecs` follow: the attribute
language has no array type, so a list is one string and a selector
asks with `.contains()`.

An output with no monitor publishes no `modes`, the same rule
every EDID attribute follows: an absent attribute is a fact a
`has()` guard can ask about, and an empty string is a lie with a
type.

## What this defers

Selecting a mode. A codec switch renegotiates one stream in a
second; a mode switch today means a compositor restart, and the
compositor serves every output on the machine, so one claim's
choice would blank its neighbors. Until the compositor can change
one output's mode in place, the inventory shows the choice and no
API makes it.
