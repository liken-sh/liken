# `ManyComponents` warnings repeat when a status write fails

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`applyScan` in `discovered.go` posts each group's `ManyComponents`
`Warning` after it calls `settleEntries`, and it posts the warning
whether `settleEntries` returned an error or not. The comment on
`post` in `events.go` states the opposite rule: after a status write,
the caller posts only when the write succeeded, so that a refused
write posts nothing and the next pass computes the same change again.

When any write of `applyScan` fails, the caller in `scans.go` keeps
the scan pod, and the next pass calls `applyScan` again with the same
result. Each such pass posts every group's `ManyComponents` warning
again, for the groups whose writes succeeded too. The root
`AGENTS.md` says that a component posts no `Event` for each retry of
a retry loop.

## Evidence

- `applyScan` in `discovered.go`: both loops call `o.post(...,
  s.warnings[key], false)` on the line after `settleEntries`, with no
  check of its error.
- The comment on `post` in `events.go`.
- The loop after `applyScan` in `scans.go`, which keeps the pod when
  `applyScan` returns an error.
- `two-component-ids-that-differ-in-case-get-one-name.md` in this
  directory describes an error of `applyScan` that repeats on every
  pass, which would post the warnings on every pass.

## What would confirm it

A test of the discovery world in a `synctest` bubble with a scan that
reports a device with releases from two components, and a fake API
server that refuses the status write of the discovered `Firmware`
once. The test reads `eventstest` and counts the `ManyComponents`
`Event`s after the second pass. One is the expected count.

## What a fix has to decide

- Whether the warning is posted only when `settleEntries` writes the
  entry, as `post` documents, or only when the entry of the device
  changes.
- Whether a scan pod that is read again posts its warnings again at
  all, because the same scan result produced them the first time.
