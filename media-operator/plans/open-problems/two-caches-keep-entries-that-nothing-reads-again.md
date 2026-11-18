# Two caches keep entries that nothing reads again

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Two maps in `media-operator` add an entry for each new key and delete
none, so they grow for the life of the process.

- `verdictCache` in `apiauth.go` stores each positive `TokenReview`
  in `subjects` and each positive `SubjectAccessReview` in `grants`,
  with an expiry. `subject` and `granted` ignore an expired entry, and
  no code deletes one. Each distinct token or certificate adds an
  entry, and a client that mints a short-lived token for each capture
  adds one for each capture.
- `publishedStatuses.activities` in `playerstatus.go` records the
  activity of each Player's status topic. `clear` deletes a deleted
  Player's topic from `payloads` and not from `activities`, and `reset`
  leaves `activities` in place on purpose. A Player that is deleted
  and then created again with the same name has its old activity
  read back by `publishDerived` as the previous activity.

## Evidence

- `apiauth.go`: `storeSubject` and `storeGrant` are the only writers
  of the two maps, and the package has no `delete` on them.
- `playerstatus.go`: the only `delete` in the type is
  `delete(p.payloads, topic)` in `clear`.

## What would confirm it

For `verdictCache`, a test that authenticates many tokens that expire,
moves the bubble's clock past their expiry, and reads the map's
length. For `activities`, a test that publishes a Player's status,
clears it as deleted, and reads the map.

## What a fix has to decide

When an expired verdict leaves `verdictCache`: on the read that finds
it expired, or on a sweep. A sweep on a timer is allowed only as a
clock, as the root `AGENTS.md` states. For `activities`, whether
`clear` deletes the activity with the payload, and whether a Player
created again with the same name should log a change from the old
Player's activity.
