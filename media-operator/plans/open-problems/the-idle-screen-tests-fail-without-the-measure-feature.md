# The idle-screen tests fail without the `measure` feature

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Two tests in `idle/src/harness/options.rs` use flags and fields of the
`measure` feature without `#[cfg(feature = "measure")]`.
`each_flag_sets_its_option` passes `--capture`, `--capture-at`, and
`--stats` and reads `options.capture_dir`, `capture_at`, and `stats`,
which exist only under the feature, so `cargo test -p idle-screen
--no-default-features` does not compile. `the_help_lists_every_flag`
expects the same three flags in the help, which lists them only under
the feature.

The image builds with `--no-default-features`, and the `Makefile` runs
clippy that way only for `--lib --bins`, so no check builds the tests
without the feature.

## Evidence

- `options.rs`: the fields are under `#[cfg(feature = "measure")]`,
  and the help text for them is the `MEASURE` constant under the same
  `cfg`.
- `Makefile`: `cargo clippy -p idle-screen --no-default-features --lib
  --bins`.

## What would confirm it

`cargo test -p idle-screen --no-default-features` in `media-operator/`.

## What a fix has to decide

Whether the two tests split into a part that runs in every build and a
part under the `cfg`, and whether `make test-rust` runs the tests
without the feature too, so the shipped build is the one under test.
