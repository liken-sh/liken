// The keys the idle command pod forwards on the commands topic, and the
// browser key each one is. The names are the kernel's, as the remote pod
// publishes them, so one press reads the same on every consumer.

/// Read one press off the commands topic. `value` is the kernel's: 0 is a
/// release, 1 a press, and 2 an autorepeat, and a repeat is another press.
/// A payload that is not an object, a release, and a key this browser does
/// not name are no press at all.
pub fn parse(payload: &[u8]) -> Option<&'static str> {
    let value: serde_json::Value = serde_json::from_slice(payload).ok()?;
    let event = value.as_object()?;
    if !matches!(event.get("value")?.as_i64()?, 1 | 2) {
        return None;
    }
    key_of(event.get("key")?.as_str()?)
}

/// One kernel key name as the browser key it is. Several names reach one
/// key, because remotes differ in the name they send for OK and for back.
/// Select is enter and back is escape, so a press from a remote takes the
/// path the keyboard and the script take.
pub fn key_of(name: &str) -> Option<&'static str> {
    match name {
        "KEY_UP" => Some("up"),
        "KEY_DOWN" => Some("down"),
        "KEY_LEFT" => Some("left"),
        "KEY_RIGHT" => Some("right"),
        "KEY_ENTER" | "KEY_OK" | "KEY_SELECT" | "KEY_KPENTER" => Some("enter"),
        "KEY_BACK" | "KEY_ESC" | "KEY_EXIT" => Some("escape"),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_arrows_are_themselves() {
        assert_eq!(parse(br#"{"key":"KEY_UP","value":1}"#), Some("up"));
        assert_eq!(parse(br#"{"key":"KEY_DOWN","value":1}"#), Some("down"));
        assert_eq!(parse(br#"{"key":"KEY_LEFT","value":1}"#), Some("left"));
        assert_eq!(parse(br#"{"key":"KEY_RIGHT","value":1}"#), Some("right"));
    }

    #[test]
    fn every_name_a_remote_gives_select_is_enter() {
        assert_eq!(parse(br#"{"key":"KEY_ENTER","value":1}"#), Some("enter"));
        assert_eq!(parse(br#"{"key":"KEY_OK","value":1}"#), Some("enter"));
        assert_eq!(parse(br#"{"key":"KEY_SELECT","value":1}"#), Some("enter"));
        assert_eq!(parse(br#"{"key":"KEY_KPENTER","value":1}"#), Some("enter"));
    }

    #[test]
    fn every_name_a_remote_gives_back_is_escape() {
        assert_eq!(parse(br#"{"key":"KEY_BACK","value":1}"#), Some("escape"));
        assert_eq!(parse(br#"{"key":"KEY_ESC","value":1}"#), Some("escape"));
        assert_eq!(parse(br#"{"key":"KEY_EXIT","value":1}"#), Some("escape"));
    }

    #[test]
    fn an_autorepeat_is_another_press() {
        assert_eq!(parse(br#"{"key":"KEY_DOWN","value":2}"#), Some("down"));
        assert_eq!(parse(br#"{"key":"KEY_ENTER","value":2}"#), Some("enter"));
    }

    #[test]
    fn a_release_is_no_press() {
        assert_eq!(parse(br#"{"key":"KEY_DOWN","value":0}"#), None);
        assert_eq!(parse(br#"{"key":"KEY_ENTER","value":0}"#), None);
    }

    #[test]
    fn a_key_this_browser_does_not_name_is_no_press() {
        assert_eq!(parse(br#"{"key":"KEY_MUTE","value":1}"#), None);
        assert_eq!(parse(br#"{"key":"","value":1}"#), None);
    }

    #[test]
    fn the_browsers_own_sleep_request_is_no_press() {
        assert_eq!(parse(br#"{"action":"sleep"}"#), None);
    }

    #[test]
    fn the_operators_re_present_is_no_press() {
        assert_eq!(parse(br#"{"action":"re-present"}"#), None);
    }

    #[test]
    fn text_that_does_not_parse_is_no_press() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"KEY_UP"), None);
        assert_eq!(parse(br#"["KEY_UP"]"#), None);
        assert_eq!(parse(br#"{"remote":0}"#), None);
        assert_eq!(parse(br#"{"key":3,"value":1}"#), None);
        assert_eq!(parse(br#"{"key":"KEY_UP"}"#), None);
        assert_eq!(parse(br#"{"key":"KEY_UP","value":"1"}"#), None);
    }
}
