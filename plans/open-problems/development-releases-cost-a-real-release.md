# Development releases cost a real release

Open problem. Testing a development build of liken, or of any operator
above it, should be easier than cutting a real release. Today it is
not: the only path onto a cluster is the release path, so every drill
of an unreleased change pays the full price of shipping one.

## The cost today

For liken itself, `make release` with a `VERSION=` builds and serves
a development release for the lab, so the guest and metal loop is
short. The operators have no equivalent. A one-line operator change
that needs a cluster to prove it takes the whole loop: commit, push,
wait for CI, tag, wait for the release workflow to build and push the
images, then bump every pin in the cluster's overlay. The loop costs
minutes per iteration, and each iteration writes a permanent tag and a
release into the registry, so the release history fills with versions
whose only purpose was one drill.

The cost shapes behavior in the wrong direction. A slow loop invites
a person to skip the cluster drill, or to pile several changes into
one release so they share the wait, and then a failure no longer says
which change caused it.

## What an answer must preserve

- A cluster that runs pinned releases keeps running pinned releases.
  The answer must not reintroduce a floating tag: a development build
  needs a name as explicit as a release's, and a cluster must show
  clearly that it runs one.
- A development build must not be mistakable for a release. liken's
  `-9xx` serial convention marks a lab build in the version itself;
  whatever the operators grow should mark theirs as loudly.
- The release path stays the only path to a real version. The short
  loop is for drills, not for shipping.

## Directions worth weighing

- A `lab-release` equivalent for the operators: build the images from
  the working tree, push them under a development version to the
  registry or to a lab registry, and bump the overlay pins the same
  way a release does.
- Skipping the registry entirely where the cluster allows it: load an
  image straight onto the nodes and pin the overlay to the loaded
  name.
- One shared recipe rather than one per repository, since every
  operator already shares the same CI and release pattern.
