// The key events that the remote pod publishes on a controller's events
// topic. The key is the kernel's input name, such as `KEY_UP`, and this crate
// passes it to its client unchanged.

use serde::Deserialize;

/// One key event. `value` is the kernel's: 0 release, 1 press, 2 autorepeat.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct Press {
    pub key: String,
    pub value: i64,
}

impl Press {
    /// Whether the key is held down: a press or an autorepeat. A release does
    /// not count, because the release of a press that put the screen to sleep
    /// would otherwise wake it again.
    pub fn held(&self) -> bool {
        self.value == 1 || self.value == 2
    }

    /// Whether this event is the press and not a repeat.
    pub fn down(&self) -> bool {
        self.value == 1
    }
}

/// Decodes one event. Both fields are required, so a message from another
/// writer on the topic returns `None` instead of a release of an unnamed key.
pub fn parse(payload: &[u8]) -> Option<Press> {
    crate::object(payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_press_decodes_the_kernels_key_name_and_value() {
        assert_eq!(
            parse(br#"{"key":"KEY_UP","value":1}"#),
            Some(Press {
                key: "KEY_UP".into(),
                value: 1
            })
        );
    }

    #[test]
    fn a_press_and_a_repeat_are_held_and_a_release_is_not() {
        assert!(
            parse(br#"{"key":"KEY_UP","value":1}"#)
                .expect("the press payload decodes")
                .held()
        );
        assert!(
            parse(br#"{"key":"KEY_UP","value":2}"#)
                .expect("the press payload decodes")
                .held()
        );
        assert!(
            !parse(br#"{"key":"KEY_UP","value":0}"#)
                .expect("the press payload decodes")
                .held()
        );
    }

    #[test]
    fn only_value_one_is_the_key_going_down() {
        assert!(
            parse(br#"{"key":"KEY_MUTE","value":1}"#)
                .expect("the press payload decodes")
                .down()
        );
        assert!(
            !parse(br#"{"key":"KEY_MUTE","value":2}"#)
                .expect("the press payload decodes")
                .down()
        );
    }

    #[test]
    fn text_that_does_not_parse_is_no_press() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"not json"), None);
        assert_eq!(parse(b"KEY_UP"), None);
        assert_eq!(parse(br#"["KEY_UP",1]"#), None);
    }

    #[test]
    fn a_message_that_names_no_key_or_no_value_is_no_press() {
        assert_eq!(parse(br#"{"key":"KEY_UP"}"#), None);
        assert_eq!(parse(br#"{"value":1}"#), None);
        assert_eq!(parse(br#"{"key":3,"value":1}"#), None);
        assert_eq!(parse(br#"{"key":"KEY_UP","value":"1"}"#), None);
        assert_eq!(parse(br#"{"action":"play-next"}"#), None);
    }
}
