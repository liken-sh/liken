// The retained panel topic carries a desire and not a report. The unit it
// belongs to is named by the topic, not by the body.
//
// A client states a desire instead of writing the panel, because a
// screen client holds no API credentials and no wire. The operator
// reads the desire off this topic and overrides the screen's `Display`,
// and the display-operator writes the hardware.

use serde::Serialize;

/// The two desires a client states. They are the values on the panel topic,
/// not the states the `Player` status carries.
pub const ON: &str = "on";
pub const OFF: &str = "off";

/// The whole payload on the panel topic.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub struct Desire<'a> {
    pub desire: &'a str,
}

impl Desire<'_> {
    /// The desire as it travels on the topic.
    pub fn payload(self) -> Vec<u8> {
        // A word always encodes, so the error is the interface's and not a
        // state this code reaches.
        serde_json::to_vec(&self).unwrap_or_default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn each_desire_is_one_word_on_the_topic() {
        assert_eq!(Desire { desire: ON }.payload(), br#"{"desire":"on"}"#);
        assert_eq!(Desire { desire: OFF }.payload(), br#"{"desire":"off"}"#);
    }
}
