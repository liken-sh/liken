# 17, A volume checks out only its paths

Proposed and built on 2026-11-21. Three reviews of the first draft on
2026-11-21 ran git 2.53.0 and the image's git 2.55.0 against small
repositories and a clone of `liken`. Their findings and measurements
are in this version. The drill on `liken-1` has not run. "What the
build changed" at the end lists where the code differs from the
design below.

## The problem

A volume checks out the whole tree of its commit, even when its pod
reads one directory. A read-only volume of the `liken` repository
writes 56.2 MB in 6,665 files on each node, to serve a few kilobytes
of YAML in four directories. A writeable volume of a configuration
repository that holds many applications, as in the guide "Give many
applications one repository", checks out every application's files
on the node, and `subPath` only hides them from the container.

A read-only volume also writes its whole tree again on each commit.
`replaceTree` in `store.go` renames every file of the new checkout
into the published tree, including the files that did not change. So
each push writes the whole tree once per volume, and every file gets
a new inode. A pod that watches its files, such as `transect` with
`--resources`, reloads on every commit to the repository. Across the
last 100 first-parent commits of `liken`, the median commit changed 4
files.

## The attribute

Every kind of volume takes a new attribute, `paths`: the directories
and files of the repository that the driver checks out. The volume's
tree contains only those paths, each at the same place it has in the
repository. A volume with no `paths` checks out the whole tree, as
before. `paths` does not move a directory to the root of the mount.
`subPath` on the pod's `volumeMount` still does that.

A volume attribute is a string, because Kubernetes defines
`volumeAttributes` as `map[string]string` and CSI defines the volume
context as `map<string, string>`. The driver splits the value on
commas and on newlines, trims the spaces around each entry, and drops
empty entries. The `ignore` parameter of a class uses the same
splitter, so it accepts newlines too. Both forms work:

```yaml
paths: media-operator/transect,audio-operator/transect
```

```yaml
paths: |
  media-operator/transect
  audio-operator/transect
```

So an entry cannot contain a comma, and it cannot start or end with a
space. The driver runs git with `--literal-pathspecs` for every
command that takes the entries, so `pages/[id]` and `odd:name` are
paths and not patterns. It strips a trailing `/`, because git treats
`a/file/` as missing. It sorts the entries and removes duplicates, so
two volumes with the same entries in another order record the same
value. The driver refuses the value, with `InvalidArgument` and the
entry, before git runs, when an entry:

- starts with `/`;
- has a `.`, `..`, or empty component, such as `a//b`;
- has `.git` as a component.

It also refuses a value that has no entry after the split, such as
`" , "`, because that would quietly mean the whole tree.

`paths` is not a class parameter. `parsePolicy` refuses an unknown
parameter, so a class that sets it is refused. A `PersistentVolume`'s
attributes are immutable, `ControllerModifyVolume` cannot change a
volume's context, and an inline volume's attributes are fixed for its
pod. So a staged volume never changes its `paths`. A work tree kept
under `Retain` can return under a new `PersistentVolume` with other
`paths`, and "A kept work tree" says what happens then.

The bare repository of the URL stays complete. Volumes of one URL with
different `paths` share it, and the fetch does not change.

A symlink entry checks out only the link. A symlink inside an entry
that points outside the entries does not resolve.

## A read-only volume

### The checkout

`repository.checkout` runs `git read-tree <commit>` into its temporary
index and then `git checkout --force -- <entries>` from that index.
Reading the whole tree into the index first applies a
`.gitattributes` above an entry, such as a root `* text eol=crlf`, so
a file with `paths` has the same bytes as the same file without. On
`liken`, this adds 0.03 s and a temporary index of 750 KB.

Before the checkout, one `git rev-parse <commit>:<entry>...` checks
that every entry exists. One process checks every entry, its error
names the missing entry, and it accepts a submodule entry, which `git
cat-file -e` refuses because a gitlink's object is in another
repository.

### Writing only what changed

This part applies to every read-only volume, with or without `paths`.
When a refresh finds a new commit, the driver runs `git diff-tree -r
-z --no-renames --raw <old> <new> -- <entries>`, with the whole tree
as the pathspec when the volume has no `paths`.

- **No change.** The driver records the new commit and writes no file.
- **Some changes.** The driver checks out only the changed files into
  `next/`, renames each one into the published tree, deletes each
  removed file, and then removes each directory that the deletes left
  empty. A pod sees each file change with one rename, as before.
- **A full checkout.** The driver checks out every entry and runs
  `replaceTree`, as now, when the old commit is not in the repository
  (`diff-tree` exits 128, for example after a forced push and a sweep),
  when the volume has no recorded commit, or when a `.gitattributes`
  file on the path from the root to an entry changed.

`diff-tree` compares modes as well as object ids, so a `chmod +x` or a
file that becomes a symlink counts as a change. `git rev-parse
<old>:<file>` would miss both.

The driver deletes `placedFile` before it changes the published tree,
and writes it again after. A driver that stops during an update leaves
a tree with no recorded commit, so the next refresh does a full
checkout.

### A missing entry

A missing entry at the first stage of a read-only volume refuses the
stage with `FailedPrecondition`. The message quotes the entry and
names the commit. A read-only claim posts it on the claim, and an
inline volume posts it on the pod.

A missing entry at a later commit leaves the volume on its current
commit. The fetch succeeded, so the driver records a fault of its own,
not a failed fetch: the reason `GitVolumePathMissing`, posted once per
transition, with the entry and the commit in the message and in the
volume's health. The driver does not retry until the ref moves, and a
demand counts as satisfied. A later commit that has every entry again
clears the fault with a Normal Event. With `offline: allowStale`, a
stale copy can lack an entry that is new upstream, and the message
names the failed fetch as well as the entry.

## A writeable volume

### The sparse checkout

A writeable volume has a git directory and an index, so it uses git's
sparse checkout in pattern mode. `workTree.create` writes this before
the first `git reset --hard`:

```
core.sparseCheckout = true
core.sparseCheckoutCone = false
sparse.expectFilesOutsideOfPatterns = true
info/sparse-checkout: one line per entry, /<dir>/ or /<file>
```

The driver writes the configuration and the file itself. It does not
run `git sparse-checkout set`, because that command turns on
`extensions.worktreeConfig`. `git worktree add` copies
`info/sparse-checkout` into the scratch tree on git 2.53 and 2.55, so
the rebase checks out only the entries, with no change to
`addScratchTree`.

Pattern mode was measured against cone mode on git 2.55, against
`liken` at `bdee27de0`, with four `deploy/` directories as the
entries. Each number is the median of 3 runs:

| mode | files | tree | index | checkout | status |
|---|---|---|---|---|---|
| full | 6,645 | 56.2 MB | 750 KB | 657 ms | 140 ms |
| pattern | 48 | 0.40 MB | 763 KB | 45 ms | 15 ms |
| cone | 698 | 6.33 MB | 762 KB | 121 ms | 42 ms |
| cone + `index.sparse` | 698 | 6.33 MB | 75 KB | 102 ms | 34 ms |

Cone mode checks out every file at the root and every file directly
inside a parent directory of an entry. In `liken`, that adds 650 Go
files and 5.9 MB for these four entries. Cone mode also cannot name a
file entry. The 2.55 manual of `git sparse-checkout` calls non-cone
mode deprecated, for its O(N·M) matching, glob mistakes, and the lack
of a sparse index. The description of `core.sparseCheckoutCone` still
offers it, and git printed no warning. A test covers the pattern
mode's tree, so a git bump that removes it fails CI.

### Writes outside the paths

A pod that mounts the volume's root can write outside the paths.
Without `sparse.expectFilesOutsideOfPatterns`, git clears the
skip-worktree bit of a tracked file that the pod writes outside the
patterns. `git status` then reports it, the volume never shows a clean
tree, and a later `read-tree -m -u` fails with "Entry not uptodate",
which diverges the volume. With the setting, git ignores those writes.

The driver never commits a file outside the paths:

- `pending` runs `git status ... -- <entries>`, so untracked files
  outside the paths are not pending changes and do not count in
  `git_csi_pending_paths`.
- The commit keeps its `git add` per path, fed by that scoped status,
  so `commit.maxFileSize` still skips a large file.
- The inotify watch resets the quiesce timer only for an event inside
  an entry, so a pod that writes a log outside its paths still
  commits.
- The first write to each path outside the entries posts one
  `GitVolumeFileSkipped` Event on the pod and the claim, the same
  reason as for a file that `ignore` or `maxFileSize` skips, and
  counts in `git_csi_skipped_files`.

The kernel could refuse those writes instead: a read-only bind of the
tree with a read-write bind of each entry on top. That makes publish
and unpublish manage a mount per entry, and a file entry cannot be
replaced by a rename. It is not part of this plan.

### The metadata record

`refs/git-csi/metadata` is one ref per URL, and every volume of the
URL writes it. Today each record lists the whole tree. A sparse
volume's record would replace the other volumes' modes and empty
directories. So a volume with `paths`:

- walks only its entries;
- writes the remote record's lines outside its entries, followed by
  its own lines inside them;
- replays only the lines inside its entries.

### A missing entry

A writeable volume accepts a missing entry and checks out nothing for
it. A new application in the many-apps layout has no directory until
its first commit, and an application that deletes the last file of its
directory commits that deletion. Both must still stage. The pod
creates the directory when it writes, or the kubelet creates it for a
`subPath` mount.

### A kept work tree

A work tree kept under `Retain` can be staged again by a new
`PersistentVolume` with the same `volumeHandle` and other `paths`. At
each stage of an existing tree, the driver compares the volume's
entries with `info/sparse-checkout`. When they differ and the tree has
no pending change inside the old entries, the driver writes the new
patterns and runs `git read-tree -m -u HEAD`, which checks out the new
entries and removes the clean old ones. When a pending change is
inside an old entry, the stage fails with `FailedPrecondition` and a
message that names the old and the new entries, because applying the
new patterns would leave that change where the driver never commits
it.

## Events and messages

- `GitVolumePathMissing` is new: a read-only volume whose commit lacks
  an entry.
- The message for an unknown attribute lists `paths`.
- A refusal at stage keeps today's routes: the claim of a read-only
  claim, the pod of an inline volume, and the kubelet's `FailedMount`
  on the pod for a writeable volume.

Records keep the raw volume context, so a restarted driver reads
`paths` again with no change to `records.go`. An older driver that
reads a record with `paths` refuses the unknown attribute and skips
that volume.

## Alternatives set aside

- **Partial clone** (`--filter=blob:none`) would make the first fetch
  download only the blobs under the paths. A writeable volume's
  alternates point into the bare repository, so `git status` or a
  rebase that needs a missing blob fails, and the bare repository is
  shared by every volume of the URL. With `depth: 1`, the `liken`
  repository is 17.2 MB on the node, and the checkout of the entries
  is a few hundred kilobytes.
- **Cone mode** for a writeable volume, for the reasons in the table.
- **Re-rooting one directory as the volume's root.** `subPath` already
  does this for every kind of volume.

Two costs that the reviews found are not part of this plan. Each
volume of a URL fetches on its own in each pass of the fetch loop, and
a refresh fetches with no depth, so a forced push to a `depth: 1`
repository fetches the new commit's whole history.

## The proof

Unit tests use real repositories on disk, as the tests of `store.go`
and `worktree.go` do now. They cover:

- the parsing, each refusal, and the splitter of `ignore`;
- the tree of each kind of volume with `paths`, including a nested
  pair, a file entry, a submodule entry, and a root `.gitattributes`;
- a commit that changes no entry and writes no file, a commit that
  changes some files, a mode change, a forced push, and a stop during
  an update;
- a missing entry at stage and at a refresh, for each kind;
- a write outside the paths, tracked and untracked, its Event, and the
  quiesce timer;
- the merged metadata record of two volumes with different entries;
- a kept work tree staged again with other entries, clean and with a
  pending change.

A drill on `liken-1` runs each mode from a development build, with a
fixture repository that has root files, nested directories, and a
directory per application:

| Case | Proves |
|---|---|
| Inline, timer pull, a directory and a file entry | The tree is exactly the entries. A push outside the entries moves the commit and leaves each file's inode. A push inside reaches the pod. `../x` and a missing entry are refused on the pod. |
| Read-only claim, `on-demand`, `depth: 1`, two pods | The demanded pull. Renaming an entry upstream keeps the old files and posts `GitVolumePathMissing`, and restoring it posts the Normal Event. |
| The same claim after the node plugin restarts | A resumed volume follows only its entries. |
| Read-only claim without `paths` | A push of one file renames one file. |
| Writeable, unarmed | A write inside is pending. A write outside is skipped. |
| Writeable, armed, with `ignore` | Only changes inside the entries reach the forge. |
| Two writers with disjoint entries | Each rebases with no divergence, neither tree gets the other's files, and both modes survive in the metadata record. |
| Two writers whose entries overlap | Divergence and its repair. |
| A kept tree under a new `PersistentVolume` with other entries | The new entries are checked out. |
| A writeable pod deletes every file of its entry, then stages again | The stage succeeds. |

## What the build changed

- **One pattern form.** Each line of `info/sparse-checkout` is
  `/<entry>`, not `/<dir>/` or `/<file>`. A pattern with no trailing
  slash matches a file or a whole directory, so the driver does not
  look up what an entry is. A missing entry has no kind to look up,
  and an entry that changes from a file to a directory still matches.
- **`.gitattributes` inside an entry.** A change to any
  `.gitattributes` that `diff-tree` reports forces a full checkout,
  including one inside an entry, because it can change the bytes of
  files that did not change.
- **The reason that clears a missing entry** is `GitVolumePathFound`,
  a Normal Event.
- **The missing-entry fault is in memory**, so a restarted driver
  posts `GitVolumePathMissing` again.
- **A write outside the paths** posts `GitVolumeFileSkipped` only while
  the inotify watch works. It counts in `git_csi_skipped_files`, and it
  does not make the volume abnormal.
- **The metadata record at commit** keeps the lines outside the
  entries of the volume's own metadata ref, which holds the newest
  remote lines that the volume has seen. The merge with the record of
  the remote happens in `reparentMetadata`.
- **Literal pathspecs** come from `GIT_LITERAL_PATHSPECS=1` in git's
  environment, which git documents as the same as
  `--literal-pathspecs`.
