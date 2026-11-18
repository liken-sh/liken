// The panel topic. A screen client has no Kubernetes credentials and no
// connection to the monitor, so it publishes the monitor state it requests,
// `on` or `off`, in the `desire` field of a retained message.
// `media-operator` reads it and sets an override on the screen's `Display`,
// and `display-operator` switches the monitor. The topic name identifies the
// `Player`, so the payload does not.

use serde::{Deserialize, Serialize};

/// The values of the `desire` field. These are values of the panel topic, not
/// of the `Player` status.
pub const ON: &str = "on";
pub const OFF: &str = "off";

/// The panel request that a client publishes.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub struct Request<'a> {
    pub desire: &'a str,
}

impl Request<'_> {
    /// The JSON bytes to publish.
    pub fn payload(self) -> Vec<u8> {
        // Serializing a struct with one string field cannot fail.
        serde_json::to_vec(&self).unwrap_or_default()
    }
}

/// A panel request as the broker sends it back.
#[derive(Debug, Deserialize)]
pub struct Received {
    #[serde(default)]
    desire: String,
}

impl Received {
    /// Returns `None` for any value other than `ON` or `OFF`, so a value that
    /// a newer writer adds changes nothing in this client.
    pub fn request(&self) -> Option<&'static str> {
        match self.desire.as_str() {
            ON => Some(ON),
            OFF => Some(OFF),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_received_request_is_on_or_off_and_nothing_else() {
        let read = |payload: &[u8]| {
            crate::object::<Received>(payload).and_then(|received| received.request())
        };
        assert_eq!(read(br#"{"desire":"on"}"#), Some(ON));
        assert_eq!(read(br#"{"desire":"off"}"#), Some(OFF));
        assert_eq!(read(br#"{"desire":"dim"}"#), None);
        assert_eq!(read(b""), None);
    }

    #[test]
    fn a_request_publishes_one_desire_field() {
        assert_eq!(Request { desire: ON }.payload(), br#"{"desire":"on"}"#);
        assert_eq!(Request { desire: OFF }.payload(), br#"{"desire":"off"}"#);
    }
}
