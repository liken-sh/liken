// The navigation actions the idle command pod forwards on the commands
// topic, and the browser key each one is. The vocabulary is the one a
// translator publishes on a `Play`'s commands topic, so one press reads the
// same on both trees.

/// Read one press off the commands topic. A payload that is not an object,
/// and an action this browser does not name, are no press at all.
pub fn parse(payload: &[u8]) -> Option<&'static str> {
    let value: serde_json::Value = serde_json::from_slice(payload).ok()?;
    key_of(value.as_object()?.get("action")?.as_str()?)
}

/// One action as the browser key it is. Select is enter and back is escape,
/// so a press from a remote takes the path the keyboard and the script take.
pub fn key_of(action: &str) -> Option<&'static str> {
    match action {
        "up" => Some("up"),
        "down" => Some("down"),
        "left" => Some("left"),
        "right" => Some("right"),
        "select" => Some("enter"),
        "back" => Some("escape"),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_arrows_are_themselves() {
        assert_eq!(parse(br#"{"action":"up"}"#), Some("up"));
        assert_eq!(parse(br#"{"action":"down"}"#), Some("down"));
        assert_eq!(parse(br#"{"action":"left"}"#), Some("left"));
        assert_eq!(parse(br#"{"action":"right"}"#), Some("right"));
    }

    #[test]
    fn select_is_enter_and_back_is_escape() {
        assert_eq!(parse(br#"{"action":"select"}"#), Some("enter"));
        assert_eq!(parse(br#"{"action":"back"}"#), Some("escape"));
    }

    #[test]
    fn an_action_this_browser_does_not_name_is_no_press() {
        assert_eq!(parse(br#"{"action":"mute"}"#), None);
        assert_eq!(parse(br#"{"action":""}"#), None);
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
        assert_eq!(parse(b"up"), None);
        assert_eq!(parse(br#"["up"]"#), None);
        assert_eq!(parse(br#"{"remote":0}"#), None);
        assert_eq!(parse(br#"{"action":3}"#), None);
    }
}
