// The four events on the `Player`'s screen topic, which are the idle command
// pod's own decisions. The shade travels retained, and the moments do not.

/// One event, as the idle command pod states it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Event {
    /// The shade came down. The browser draws a black frame and holds the
    /// level it was on.
    Sleep,
    /// The shade went up. The browser draws the level it left, at the same
    /// focus.
    Wake,
    /// A live mark named this `Player`. The browser draws no identity block,
    /// so nothing pulses.
    Focus { remote: usize },
    /// A `Play` ended and the screen is the browser's again. A fresh Wayland
    /// surface is what reveals it.
    Present,
}

/// Read one moment off the screen topic. A payload that does not decode, an
/// event word this browser does not name, and a focus that names no controller
/// are all no moment at all.
pub fn parse(payload: &[u8]) -> Option<Event> {
    let value: serde_json::Value = serde_json::from_slice(payload).ok()?;
    let message = value.as_object()?;

    match message.get("event")?.as_str()? {
        "sleep" => Some(Event::Sleep),
        "wake" => Some(Event::Wake),
        "focus" => Some(Event::Focus {
            remote: usize::try_from(message.get("remote")?.as_i64()?).ok()?,
        }),
        "present" => Some(Event::Present),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_shade_moments_decode() {
        assert_eq!(parse(br#"{"event":"sleep"}"#), Some(Event::Sleep));
        assert_eq!(parse(br#"{"event":"wake"}"#), Some(Event::Wake));
    }

    #[test]
    fn a_present_decodes() {
        assert_eq!(parse(br#"{"event":"present"}"#), Some(Event::Present));
    }

    #[test]
    fn a_focus_carries_the_controller_it_landed_on() {
        assert_eq!(
            parse(br#"{"event":"focus","remote":0}"#),
            Some(Event::Focus { remote: 0 })
        );
        assert_eq!(
            parse(br#"{"event":"focus","remote":2}"#),
            Some(Event::Focus { remote: 2 })
        );
    }

    #[test]
    fn a_focus_that_names_no_controller_is_no_moment() {
        assert_eq!(parse(br#"{"event":"focus"}"#), None);
        assert_eq!(parse(br#"{"event":"focus","remote":-1}"#), None);
    }

    #[test]
    fn an_event_word_this_browser_does_not_name_is_no_moment() {
        assert_eq!(parse(br#"{"event":"revealed"}"#), None);
        assert_eq!(parse(br#"{"event":""}"#), None);
    }

    #[test]
    fn text_that_does_not_parse_is_no_moment() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"sleep"), None);
        assert_eq!(parse(br#"["sleep",null]"#), None);
        assert_eq!(parse(br#"{"remote":0}"#), None);
    }
}
