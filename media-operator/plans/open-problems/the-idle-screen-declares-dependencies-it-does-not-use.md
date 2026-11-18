# The idle screen declares dependencies it does not use

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`idle/Cargo.toml` lists `serde` and `metrics` as dependencies, and no
file in `idle/src` uses either crate. `src/metrics.rs` calls
`metrics_exporter_prometheus`, `metrics_process`, and
`media_screen::metrics`, and `main.rs` reads `idle_screen::metrics`,
the crate's own module. `serde_json` is used only by
`harness/stats.rs`, which builds only under the `measure` feature, and
the dependency is not optional, so the image's build compiles it too.

## Evidence

A search of `idle/src` for `serde::`, `Serialize`, `Deserialize`, and
the `metrics` crate's macros finds nothing. `harness.rs` declares
`pub mod stats` under `#[cfg(feature = "measure")]`.

## What would confirm it

Remove `serde` and `metrics` from `idle/Cargo.toml`, and make
`serde_json` part of `measure`. Then build and test the crate with and
without the feature.

## What a fix has to decide

Whether a dependency stays to fix a version that another crate pulls
in, which would need a comment that says so, or goes.
