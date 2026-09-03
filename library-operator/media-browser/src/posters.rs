// Posters live on the volume, not in the catalog, so the views ask for
// them through this seam and draw a placeholder until one arrives.

use std::path::PathBuf;

use crate::harness::Waker;

// Below the seam: a bounded cache that decodes art files into RGBA
// buffers at the size they are drawn, and the adapter that wraps those
// buffers in the handles the views draw.
mod art;
mod cache;
mod decode;
mod queue;
pub mod store;
pub mod volumes;

#[cfg(test)]
mod tests;

pub use art::Art;

/// How a decode fills the box it is asked for.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Fit {
    /// Scale to cover the box and crop the overflow at its center. A poster
    /// slot is drawn at the poster's own ratio, so the crop takes nothing.
    Cover,
    /// Scale to fit inside the box at the art's own ratio, so the whole
    /// image survives. The answer is no larger than the box.
    Contain,
}

/// The poster store the views draw from.
///
/// `art` is the catalog's art path, relative to the library root, and
/// `width` and `height` are the pixels the poster is drawn at, so the
/// store decodes and scales once per drawn size and holds the results
/// under a bound. `None` says the poster is not decoded yet, or the item
/// has no art; the views draw a placeholder and ask again on a later
/// frame. A store that decodes in the background wakes the loop when a
/// poster lands, so the next ask finds it.
pub trait Posters {
    /// The poster for one item at the size it is drawn.
    fn poster(&mut self, library: &str, art: &str, width: u32, height: u32) -> Option<Art>;

    /// The art fitted inside the box at its own ratio. A logo is wide and
    /// would lose its ends to a cover crop. A store with no fit answers
    /// nothing.
    fn fitted(&mut self, _library: &str, _art: &str, _width: u32, _height: u32) -> Option<Art> {
        None
    }

    /// True once when a decode landed since the last call, so the
    /// harness redraws the frame that asked for the poster.
    /// The answer says nothing about the catalog and never asks the
    /// source to read the rows again.
    fn delivered(&mut self) -> bool {
        false
    }

    /// The path of one file of a library's volume on this machine, or nothing where the store holds no root for that library
    /// or the path leaves its root. A page reads a file the catalog names
    /// but does not hold, such as a person's biography, through the same
    /// roots the art resolves against. A store over no volume answers
    /// nothing.
    fn file(&self, _library: &str, _path: &str) -> Option<PathBuf> {
        None
    }

    /// Take the handle that wakes the loop, for a store that decodes in
    /// the background. A store that answers on the calling thread takes
    /// it and does nothing.
    fn wake_by(&mut self, _wake: Waker) {}
}
