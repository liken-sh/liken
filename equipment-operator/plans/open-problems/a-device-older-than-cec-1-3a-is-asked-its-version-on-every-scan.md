# A device older than CEC 1.3a is asked its version on every scan

Open bug, recorded on 2026-11-19. A unit test reproduced it on
2026-11-19.

`Get CEC Version` and `CEC Version` are new messages in CEC 1.3a
(HDMI-CEC 1.3a, CEC 13.6.4). A device that conforms to an earlier
version does not support the opcode, and CEC 12.3 says that a
device answers a directed message whose opcode it does not support
with a `Feature Abort`. The `known` function of
the `Get CEC Version` query in `cec/scan.go` treats the version as
known only when the directory holds a version. A `Feature Abort`
records nothing in the `Directory`, so `Scan` and `Introduce` send
`Get CEC Version` to that device again each time they run, and the
device answers with the same `Feature Abort` each time.

The vendor query already has a rule for an answer that does not
change: a device that answers `Give Device Vendor ID` with no vendor
sets `NoVendor` in `cec/directory.go`, and the query counts the vendor
as known.

## Evidence

- `cec/scan.go`: the `known` function of the `GetCECVersion` query
  returns `p.Version != VersionUnknown`, and the loop does not read
  `result.Aborted`.
- A test with the fake bus of `cec/cectest` adds a TV whose
  `NoVersion` is true, so it answers `Get CEC Version` with a
  `Feature Abort`, and introduces the TV twice. It wants the second
  introduction to ask nothing. It failed with:

  ```
  the second introduction asked 1 questions, want 0, because the TV aborted Get CEC Version the first time
  ```

  The test is not in the tree, because a failing test cannot land on
  `main`.

## What a fix has to decide

- What the status shows in `cecVersion` for a device that aborts
  `Get CEC Version`: no value, as now, or a value that says the device
  is older than 1.3a.
- Whether a device that sends no reply at all, which the
  specification does not describe for this message, is treated as one
  that sent a `Feature Abort`, or is asked again, because a missed
  reply can also come from a busy bus.
