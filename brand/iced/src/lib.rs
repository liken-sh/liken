//! The `liken` look, for a program that draws with `iced`.
//!
//! The mark and the palette come out of `liken.svg` and `liken.css`, the two
//! originals in this repository, and this crate embeds both files and parses
//! them. Nothing here is a copy of a number in those files, so the mark on a
//! screen and the mark on a page cannot drift.
//!
//! The crate carries the mark, the palette, the pulse the mark runs on, and
//! the family's two faces. It carries no type scale, no margins, and no
//! layout. Those belong to the display that draws the mark, and a ten-foot
//! screen and a web page do not share them.

pub mod font;
pub mod mark;
pub mod palette;
pub mod pulse;
