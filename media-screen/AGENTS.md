# Working on media-screen

This directory contains `media-screen`, a Rust crate. `README.md` says
what it is for and describes its modules.

## How it runs

The crate runs inside the process of each screen client that links it
through a path dependency. `media-operator`'s idle screen in
`media-operator/idle/` and `library-operator`'s media browser in
`library-operator/media-browser/` are examples. To list every client,
run `grep -rl --include=Cargo.toml media-screen .` at the top of the
repository. A change here reaches each client in the same commit, so
run the tests of the crate and of each client. For the clients above,
run `make test` here, `make test-rust` in `media-operator/`, and
`make test` in `library-operator/media-browser/`.

## Design

The crate has each screen rule that every client follows, such as the
fade and off timers, the sleep and wake of the screen, and the focus
check that drops a press from a controller pointed at another
`Player`. A rule that only one client follows stays in that client.
For example, only `media-operator` turns a volume press into a level,
so the crate handles no volume key and publishes no volume. The crate
depends on no window library, because each client draws with its own
toolkit.

## Finding things

- `src/wiring.rs` and `media-operator/wire.go` declare the same
  environment variables, so a change to one needs the same change in
  the other.
