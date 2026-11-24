//! The MQTT side of a screen client for one `Player`: everything except
//! drawing. `media-operator`'s idle screen and `library-operator`'s media
//! browser both link it by path, so a change here reaches both clients.
//!
//! [`Screen`] holds the rules and does no I/O: which controller's presses
//! count, when the screen sleeps and when the panel turns off, the volume
//! level that `media-operator` relays, and the request to move focus to the
//! next `Player`. [`Reader`] runs a thread on the broker connection that
//! subscribes, passes each message to [`Screen`], runs its deadlines, and
//! sends its publishes. A client can add topics of its own, so that it reads
//! back retained state that it publishes. The crate has no window and no
//! drawing toolkit, so each client draws a sleeping screen in its own way.
//!
//! [`language`] and [`tracks`] name the audio and subtitle tracks of a file:
//! the language, the role of a subtitle track, and the format. The media
//! browser and the film's display both draw these names, so a track has the
//! same name before a play and during it.

pub mod language;
pub mod metrics;
pub mod panel;
pub mod reader;
pub mod screen;
pub mod status;
pub mod tracks;
pub mod volume;
pub mod wiring;

pub use reader::{Bus, Reader, Waker};
pub use screen::{Effect, Moment, Publish, Screen};
pub use wiring::Wiring;

/// Returns `None` for any payload that is not a JSON object, because serde
/// also decodes a JSON array into a struct as its fields in order.
pub(crate) fn object<T: serde::de::DeserializeOwned>(payload: &[u8]) -> Option<T> {
    let value: serde_json::Value = serde_json::from_slice(payload).ok()?;
    if !value.is_object() {
        return None;
    }
    serde_json::from_value(value).ok()
}
