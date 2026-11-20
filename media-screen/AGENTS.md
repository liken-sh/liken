# Working on media-screen

This directory contains `media-screen`, a Rust crate. `README.md` says
what it is for and describes its modules.

## How it runs

The crate runs inside each screen client's process. `media-operator`'s
idle screen in `media-operator/idle/` and `library-operator`'s media
browser in `library-operator/media-browser/` both link it through a
path dependency. So a change here reaches both clients in the same
commit. Both clients build against the change, so run the tests of
the crate and of both clients: `make test` here, `make test-rust` in
`media-operator/`, and `make test` in
`library-operator/media-browser/`.

## Design

The crate has each screen rule that both clients follow, such as the
fade and off timers, the sleep and wake of the screen, and the focus
check that drops a press from a controller pointed at another
`Player`. A rule that only one client follows stays in that client.
For example, only `media-operator` turns a volume press into a level,
so the crate handles no volume key and publishes no volume. The crate
depends on no window library, because each client draws with its own
toolkit.

The crate's `Cargo.toml` pins each dependency to an exact version.
Each client pins the same version of each dependency that it shares
with the crate. When you bump a dependency, change every `Cargo.toml`
that pins it in the same commit, because cargo fails to build a
client that still pins the old version.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

- `src/wiring.rs` and `media-operator/wire.go` declare the same
  environment variables, so a change to one needs the same change in
  the other.
