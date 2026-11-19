# Two caches keep entries that nothing reads again

Open bug, recorded on 2026-11-18. Unit tests reproduced both items on
2026-11-19.

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

## The reproduction

The tests are not in the tree, because a failing test cannot land on
`main`.

- For `verdictCache`, a test stores a subject and a grant that expire
  after a minute, reads each of them two minutes later, and asserts
  that both maps are empty. It failed with:

  ```
  after the reads past expiry the cache holds 1 subjects and 1 grants, want none
  ```

- For `activities`, a test in a `synctest` bubble with a fake broker
  publishes a playing status for a Player, clears the Player as
  deleted, publishes an idle status on the same topic, and asserts
  that `publishDerived` reports no change of activity. It failed with:

  ```
  the first status of the Player created again reports a change from "playing", want no change because the Player is new
  ```

## What a fix has to decide

`media-api` keeps an entry for every token it ever accepted, so its
memory grows with each short-lived token. A Player that you delete and
create again with the same name logs a change of activity from the
deleted Player.

When an expired verdict leaves `verdictCache`: on the read that finds
it expired, or on a sweep. A sweep on a timer is allowed only as a
clock, as the root `AGENTS.md` states. For `activities`, whether
`clear` deletes the activity with the payload, and whether a Player
created again with the same name should log a change from the old
Player's activity.
