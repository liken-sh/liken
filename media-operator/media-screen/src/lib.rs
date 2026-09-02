//! Everything a screen client for one `Player` does with the bus, short
//! of drawing.
//!
//! The crate has two halves. [`Screen`] is pure and holds every rule:
//! the focus gate, the play gate, the quiet window and the shade, the
//! off window and the panel desire, the volume step, the cycle request,
//! and the re-present. [`Reader`] is the thread over the broker: it
//! subscribes, folds each message through the rules, runs the
//! deadlines, performs the publishes, and hands the client what it
//! draws.
//!
//! Two clients read it: this repository's idle screen, and the library
//! layer's media browser, which takes it as a git dependency pinned to
//! a release tag. The crate opens no window, holds no keymap of its
//! own, and names no toolkit.

pub mod panel;
pub mod reader;
pub mod screen;
pub mod status;
pub mod volume;
pub mod wiring;

pub use reader::{Bus, Reader, Waker};
pub use screen::{Effect, Moment, Publish, Screen};
pub use wiring::Wiring;

/// Read one JSON object off a topic. Every payload on these topics is an
/// object, so a payload that is not one is not this operator's and it changes
/// nothing. The check is here because a derived reader also takes a JSON array
/// as the same fields in order, and a two-element array is not a status.
pub(crate) fn object<T: serde::de::DeserializeOwned>(payload: &[u8]) -> Option<T> {
    let value: serde_json::Value = serde_json::from_slice(payload).ok()?;
    if !value.is_object() {
        return None;
    }
    serde_json::from_value(value).ok()
}
