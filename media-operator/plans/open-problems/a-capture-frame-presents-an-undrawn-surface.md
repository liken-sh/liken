# A capture frame presents an undrawn surface

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

In `idle/src/harness/frame.rs`, `frame` acquires the surface texture
and then, on a frame that `capture` writes to a PNG, skips
`Renderer::present`, which draws into that texture. It still calls
`frame.present()`, so the window shows a texture that nothing drew
into. Under the `measure` feature, each `--capture-at` second can show
a blank or a stale frame on the screen. The image is built with
`--no-default-features` (`Dockerfile.screen`), so the idle screen in a
pod does not capture.

## Evidence

`frame.rs`, `frame`: `if !captured { ... renderer.present(...) }`
and then `frame.present()` with no condition.

## What would confirm it

A run with `--capture-at` under a compositor, recorded with a screen
capture, shows the frame at that second.

## What a fix has to decide

Whether a capture frame also draws into the surface texture, which
puts the screenshot's GPU work into the same frame, or does not
present that frame.
