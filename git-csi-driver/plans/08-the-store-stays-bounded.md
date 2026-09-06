# 08, The store stays bounded

Built on 2026-09-06 and drilled in the lab with development build
`<!-- DEV -->`: drills 03 and 07 passed with the hourly sweep in place,
and the unit tests prove the ref deletion and the collection. Two
things were measured while building it and changed the design below:
`git gc --auto` detaches into the background unless told not to, and
git reads a bare duration as a date it does not mean.

## The problem

The sweep removes a work tree nothing stages and a bare repository
nothing names. A bare repository that stays is never tidied. Every ref
a volume ever followed stays under `refs/git-csi/`, so a repository
followed at `main`, then at a tag per release, keeps every tag's
history alive. Nothing runs `git gc`, so the objects of a moved ref
are never packed and never pruned. A node that serves one repository
for a year grows without bound, slowly.

## The design

### Stale refs go at the sweep

On every sweep pass, for every bare repository that stays, the driver
lists the refs under `refs/git-csi/` and deletes each one that no volume
follows. A volume follows a ref when it is staged or published on this
node with that repository and that ref, or when its record or work tree
in the store names that repository and that ref. The refs the work
trees keep for themselves, `refs/git-csi/pushed` and
`refs/git-csi/metadata`, live in the work tree's own git directory and
not in the bare repository, so this list never touches them.

The deletion happens under the repository's lock, the same lock a stage
and a fetch take, so a ref is never deleted between the fetch that moved
it and the checkout that reads it.

### The repository is collected

After the stale refs go, the driver runs `git -c gc.autoDetach=false
gc --quiet --auto --prune=<timestamp>` in the repository, under the same
lock. `--auto` makes the call cheap on a pass where nothing changed.
`gc.autoDetach=false` keeps the work in the foreground, because `git gc
--auto` otherwise returns at once and does its work in a detached
process, outside the lock and with its failures written to a file in
the repository instead of the driver's log. The prune argument is the
sweep age before now, written as an RFC 3339 timestamp, because git
reads a bare Go duration such as `720h` as this moment and a count of
seconds above 99999999 as a second of the epoch. So an object that a
ref stopped naming stays as long as an unstaged work tree does. A work tree
holds its own commits in its own object directory and reads the
repository's objects through alternates, so an object a work tree's
history needs is one an upstream ref named at the time, and it stays
reachable from the followed ref's history until upstream rewrites it.
An upstream rewrite older than the sweep age is the one case this
design does not protect, and it is written in the design as such.

A `git gc` that fails is logged and does not fail the pass.

### What the log says

A pass that deletes a ref logs the repository and the ref. A pass that
collects logs nothing unless `git gc` fails, because `--auto` runs on
most passes and does nothing.

## Considered and set aside

- **One ref per volume.** Fetching into `refs/git-csi/<volume id>/<ref>`
  would make the refs die with the volume's directory. It would also
  fetch the same ref once per volume, and the shared ref is what lets
  ten pods on one node cost one fetch.
- **`git gc` at the fetch.** Running it after every fetch would tidy
  sooner and cost a lock hold on every pull. The sweep runs hourly and
  is already the store's housekeeping.

## Proof

Unit tests drive a sweep over a repository with two followed refs and a
third that nothing follows, and see the third deleted and the two kept;
a sweep with a work tree in the store that names a ref keeps that ref;
a sweep collects a repository and prunes an object aged past the sweep
age while a fresh one stays; a failing `git gc` and a ref that cannot be
deleted leave the pass to finish. `make test` holds at 100. In the lab,
drill 03 and drill 07 pass on the build, which covers a fetch under the
hourly sweep. The sweep itself runs once an hour and prunes at 30 days,
so the lab does not watch a real pass.
