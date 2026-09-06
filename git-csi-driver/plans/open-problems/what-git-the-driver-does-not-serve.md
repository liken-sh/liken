# What git the driver does not serve

## The problem

The driver serves a checkout of one ref of one repository. Three things
a person may expect of a git checkout are not there, and no page says
so, so a person learns each one by trial:

- **Submodules.** The checkout writes the superproject's tree. A
  submodule's directory is empty.
- **Git LFS.** An LFS pointer file is checked out as the pointer, a few
  lines of text, and not the object it names.
- **`depth` on a writeable volume.** A writeable volume refuses
  `depth`, so a large repository is cloned in full onto the node before
  the first pod starts.

## What is known

- Submodules would need a second fetch per submodule, each with its own
  URL and possibly its own credential, and a tree replaced across
  repository boundaries. LFS would need the `git-lfs` binary in the image
  and a smudge pass over the tree at every replace.
- A shallow writeable volume can push, but a rebase at stage across a
  shallow boundary fails in ways the divergence path does not report
  today.

## What would settle it

For now, a non-goals list in the design and in both guides, so the
three limits are stated. Each one becomes a plan when a use asks for it,
and none does today.
