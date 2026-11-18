# Fields and constants that nothing reads

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Three names in the component are written or declared, and no code
reads them:

- `equipment.ZoneState.VolumeMaxStable` (`equipment/state.go`).
  `wiim/state.go` sets it to `true` in `Status.zone`. No file in the
  repository reads it, and its comment does not say who would.
- `ReceiverStoredStatus.PowerGeneration` (`api.go`). The status
  decodes it, and only `receiver_power_test.go` and `watch_test.go`
  set it, to build a stored status of an earlier version. The comment
  says an earlier operator wrote it and the first status apply removes
  it. `SettingsGeneration`, beside it, is read by
  `newSettledRecord`, and `PowerGeneration` is not.
- The test constant `secondSpell` (`discovery_test.go`). Its comment
  says it is `secondUUID` as mDNS spells it, and no test uses it.

## Evidence

A search of the repository for each name finds only the places
listed above.

## What would confirm it

Delete each name and run `make test-go`. A build or test that fails
shows a reader that the search missed.

## What a fix has to decide

- Whether `VolumeMaxStable` has a reader that should exist, such as a
  session that scales the volume to a fixed maximum, or goes.
- Whether the decode of `PowerGeneration` is still needed for a
  `Receiver` that an earlier version wrote.
- Whether `secondSpell` belongs in a test of the mDNS form of a UUID,
  or goes.
