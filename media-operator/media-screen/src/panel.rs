// The retained panel topic carries a desire and not a report. The unit it
// belongs to is named by the topic, not by the body.
//
// A client states a desire instead of writing the panel, because a
// screen client holds no API credentials and no wire. The operator
// reads the desire off this topic and overrides the screen's `Display`,
// and the display-operator writes the hardware.

use serde::{Deserialize, Serialize};

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

/// The payload as a reader takes it off the topic. A client reads the
/// retained desire back before it states one of its own.
#[derive(Debug, Deserialize)]
pub struct Stated {
    #[serde(default)]
    desire: String,
}

impl Stated {
    /// The desire as one of the two words this crate states. A word it does
    /// not state is no desire, so a newer writer's word changes nothing here.
    pub fn desire(&self) -> Option<&'static str> {
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
    fn a_reader_takes_each_desire_back_and_no_other_word() {
        let read =
            |payload: &[u8]| crate::object::<Stated>(payload).and_then(|stated| stated.desire());
        assert_eq!(read(br#"{"desire":"on"}"#), Some(ON));
        assert_eq!(read(br#"{"desire":"off"}"#), Some(OFF));
        assert_eq!(read(br#"{"desire":"dim"}"#), None);
        assert_eq!(read(b""), None);
    }

    #[test]
    fn each_desire_is_one_word_on_the_topic() {
        assert_eq!(Desire { desire: ON }.payload(), br#"{"desire":"on"}"#);
        assert_eq!(Desire { desire: OFF }.payload(), br#"{"desire":"off"}"#);
    }
}
