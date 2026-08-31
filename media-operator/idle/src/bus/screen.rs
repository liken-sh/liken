// The four events on the `Player`'s screen topic: the idle sidecar's own
// decisions, which this client reads off the bus.
//
// The shade events are state, so the sidecar publishes them retained and
// a client that restarts reads the cover it should draw. The focus and
// present events are moments and travel unretained, because a replayed
// moment is a press that already happened, or a surface nothing asked
// for.

use serde::Deserialize;

/// One event as the sidecar states it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Event {
    /// The quiet window ran out. The shade eases down over 4000 ms.
    Sleep,
    /// A press or a starting `Play` came. The shade eases up over 400 ms.
    Wake,
    /// A live mark named this `Player`. `remote` counts the controllers in the
    /// order the status lists them, which is the `Player`'s `spec.remotes`
    /// order, so the screen beats that part's marker.
    Focus { remote: usize },
    /// A `Play` ended and the screen is this client's again. The client
    /// maps a fresh Wayland surface, and then drops the covered one.
    Present,
}

/// The message as it travels, before the event word is read.
#[derive(Deserialize)]
struct Message {
    event: String,
    /// The controller's index, on a focus and nowhere else.
    #[serde(default)]
    remote: Option<i64>,
}

/// Read one message off the screen topic. A payload that does not decode, an
/// event word this client does not name, and a focus that names no controller
/// are all no moment at all, and they change nothing.
pub fn parse(payload: &[u8]) -> Option<Event> {
    let message: Message = super::object(payload)?;

    match message.event.as_str() {
        "sleep" => Some(Event::Sleep),
        "wake" => Some(Event::Wake),
        "focus" => Some(Event::Focus {
            remote: usize::try_from(message.remote?).ok()?,
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
    fn an_event_word_this_client_does_not_name_is_no_moment() {
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
