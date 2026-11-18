# A one-band bitmap keeps extra bytes

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`Bitmap::from_rgba` in `display/src/art/bitmap.rs` accepts `pixels`
longer than `width * height * 4`. A picture that needs more than one
band takes an exact slice for each band. A picture that fits in one
band passes the whole `pixels` buffer to `Handle::from_rgba`, extra
bytes included. What iced does with a buffer longer than its
dimensions is not checked.

## Evidence

`bitmap.rs`, `from_rgba`: the one-band branch builds
`Handle::from_rgba(width, height, pixels)`, and the banded branch
builds each band from `pixels[top * row..(top + height) * row]`.

## What would confirm it

Find whether any decoder in the crate returns a buffer longer than its
dimensions. Then a test that builds a one-band bitmap from a longer
buffer and draws it, or a reading of how `iced_wgpu` uploads an RGBA
handle.

## What a fix has to decide

Whether `from_rgba` truncates `pixels` to the exact length, or refuses
a buffer that is not exact, as it refuses a short one.
