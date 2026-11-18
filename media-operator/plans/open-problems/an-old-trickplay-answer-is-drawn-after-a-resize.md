# An old trickplay answer is drawn after a resize

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

In `display/src/art.rs`, `on_resize` clears `inflight` and
`newest_tile` and keeps the running thumbnail decode. The next
`tile_request` sends a decode at the new size, and `inflight` is set
to its key. When the decode at the old size then returns,
`on_answer` takes it in the `Kind::Trickplay` arm, which checks only
that the answer is for the current item:

- it sets `inflight` to `None` while the new-size decode still runs;
- it stores the old-size bitmap in `tile`, so the scrubber draws a
  tile of the old size;
- `newest_tile` differs from the answer's key, so it sends the
  new-size decode again, and two decodes of the same tile run.

The other arms, `Logo`, `Album`, `Next`, and `Portrait`, compare the
answer's key with the requested key.

## Evidence

`art.rs`, `on_answer`, the `Kind::Trickplay` arm, and `on_resize`.

## What would confirm it

A unit test that requests a tile, resizes the canvas, requests the
tile again, and answers the first decode. It sees the old-size bitmap
in `tile` and a second job for the new key.

## What a fix has to decide

Whether the arm drops an answer whose key is not `inflight`, and
whether `on_resize` keeps `inflight` set so that a new request waits
for the running decode, as a request does at the same size.
