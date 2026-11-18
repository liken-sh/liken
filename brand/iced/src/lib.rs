//! The `liken` look, for a program that draws with `iced`.
//!
//! This crate embeds `liken.svg` and `liken.css` from this repository and
//! parses them on first use to get the mark and the palette. It copies no
//! color or coordinate from those files, so the mark on a screen and the mark on a web
//! page stay the same.
//!
//! The crate has no type scale, margins, or layout. Each program that uses
//! it sets its own, because a screen read from across a room needs
//! different ones from a web page.

pub mod font;
pub mod mark;
pub mod palette;
pub mod pulse;
