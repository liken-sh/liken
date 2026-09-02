// One press as the standing remote pod publishes it, on a controller's
// events topic. The name is the kernel's, so a consumer holds no table of
// numbers, and this crate passes the name through to its client unchanged.

use serde::Deserialize;

/// One key event. `value` is the kernel's: 0 release, 1 press, 2 autorepeat.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct Press {
    pub key: String,
    pub value: i64,
}

impl Press {
    /// Whether this event is a control held down, the press or the repeat.
    /// The release is excluded because only a down edge is a person's act,
    /// and a release that counted would wake the screen its own press just
    /// put to sleep.
    pub fn edge(&self) -> bool {
        self.value == 1 || self.value == 2
    }

    /// Whether this event is the control going down, which is the edge mute
    /// acts on and the cycle key asks on.
    pub fn down(&self) -> bool {
        self.value == 1
    }
}

/// Read one event off a controller's events topic. A payload that is not an
/// object, and one that names no key or no value, are no press at all. Both
/// fields are required, so a message from another writer on this topic
/// changes nothing rather than reading as a release of an unnamed control.
pub fn parse(payload: &[u8]) -> Option<Press> {
    crate::object(payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_press_carries_the_kernels_name_and_the_kernels_value() {
        assert_eq!(
            parse(br#"{"key":"KEY_UP","value":1}"#),
            Some(Press {
                key: "KEY_UP".into(),
                value: 1
            })
        );
    }

    #[test]
    fn the_press_and_the_repeat_are_the_control_held_down() {
        assert!(
            parse(br#"{"key":"KEY_UP","value":1}"#)
                .expect("it decodes")
                .edge()
        );
        assert!(
            parse(br#"{"key":"KEY_UP","value":2}"#)
                .expect("it decodes")
                .edge()
        );
        assert!(
            !parse(br#"{"key":"KEY_UP","value":0}"#)
                .expect("it decodes")
                .edge()
        );
    }

    #[test]
    fn only_value_one_is_the_control_going_down() {
        assert!(
            parse(br#"{"key":"KEY_MUTE","value":1}"#)
                .expect("it decodes")
                .down()
        );
        assert!(
            !parse(br#"{"key":"KEY_MUTE","value":2}"#)
                .expect("it decodes")
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
        assert_eq!(parse(br#"{"action":"re-present"}"#), None);
    }
}
