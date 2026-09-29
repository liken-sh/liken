# Working on media-screen

This directory is the Rust crate `media-screen`, the bus half of a
screen client. media-operator's idle screen links it from
`media-operator/idle/`, and library-operator's media browser links it
from `library-operator/media-browser/`, both through a path dependency.
A change here reaches both clients in the same commit. Run `make test`
here, and `make test-rust` in `media-operator/` and `make test` in
`library-operator/media-browser/`, because both clients build against
the change.

@../brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included.

## What belongs here

A rule belongs here when both clients hold it: the timers, the focus
gate, the shade, the volume step and the owner mark, the cycle request,
and the panel desire. A rule that only one client holds stays in that
client. The crate names no window library, because each client draws
with a toolkit of its own.

## Dependency versions

The crate is a cargo workspace of its own, and its `Cargo.toml` pins
each dependency to an exact version. media-operator's workspace pins
the same versions. A bump changes both files in one commit, or cargo
refuses the build of the idle screen.
