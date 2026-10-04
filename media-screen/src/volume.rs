// The listening level as `Player` state. One retained topic carries it, and
// `media-operator` is its only writer: it relays the level that the unit's
// `Receiver` or `Sink` reports, divided by the device's `spec.volume.max`, so
// every screen draws one scale whatever device sets the room's level. A screen
// reads the topic and draws it, and publishes nothing on it.

use serde::Deserialize;

/// The bottom and the top of the normalized range. The top is the device's
/// `spec.volume.max`, the highest level a press can ask for.
pub const MIN_LEVEL: f64 = 0.0;
pub const MAX_LEVEL: f64 = 1.0;

/// The whole payload on the volume topic. The operator writes both fields on
/// every message, and each one still defaults here, so a partial payload reads
/// as the zero value instead of failing to decode.
#[derive(Debug, Clone, Copy, PartialEq, Deserialize)]
pub struct Volume {
    #[serde(default)]
    pub level: f64,
    #[serde(default)]
    pub muted: bool,
}

impl Default for Volume {
    /// The state the client holds before any message reaches it: the top of
    /// the range, unmuted.
    fn default() -> Self {
        Self {
            level: MAX_LEVEL,
            muted: false,
        }
    }
}

impl Volume {
    /// The level held inside 0.0 to 1.0. A level outside the range is clamped
    /// rather than refused, so a message cannot draw a row past the top.
    pub fn clamped(self) -> Self {
        Self {
            level: self.level.clamp(MIN_LEVEL, MAX_LEVEL),
            ..self
        }
    }

    /// The level as the whole percent a screen prints beside the bar.
    pub fn percent(self) -> i64 {
        // The level is clamped to 0.0 to 1.0, so the product fits in an i64.
        #[allow(clippy::cast_possible_truncation)]
        let percent = (self.clamped().level * 100.0).round() as i64;
        percent
    }
}

/// Read one message off the volume topic. A payload that does not decode is no
/// state at all and changes nothing.
pub fn parse(payload: &[u8]) -> Option<Volume> {
    crate::object::<Volume>(payload).map(Volume::clamped)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_state_decodes() {
        assert_eq!(
            parse(br#"{"level":0.63,"muted":true}"#),
            Some(Volume {
                level: 0.63,
                muted: true
            })
        );
    }

    #[test]
    fn a_whole_number_level_decodes_as_a_float() {
        assert_eq!(
            parse(br#"{"level":1,"muted":false}"#),
            Some(Volume::default())
        );
    }

    #[test]
    fn a_level_outside_the_range_is_clamped() {
        assert_eq!(
            parse(br#"{"level":4.0,"muted":false}"#).map(|v| v.level),
            Some(MAX_LEVEL)
        );
        assert_eq!(
            parse(br#"{"level":-0.5,"muted":false}"#).map(|v| v.level),
            Some(MIN_LEVEL)
        );
    }

    #[test]
    fn a_field_the_message_omits_reads_as_its_zero_value() {
        assert_eq!(
            parse(br#"{"level":0.4}"#),
            Some(Volume {
                level: 0.4,
                muted: false
            })
        );
        assert_eq!(parse(br#"{}"#).map(|state| state.level), Some(0.0));
    }

    #[test]
    fn text_that_does_not_parse_is_no_state() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"0.4"), None);
        assert_eq!(parse(b"[0.4,true]"), None);
        assert_eq!(parse(br#"{"level":"loud"}"#), None);
    }

    #[test]
    fn a_client_with_no_message_holds_the_top_unmuted() {
        assert_eq!(Volume::default().level, MAX_LEVEL);
        assert!(!Volume::default().muted);
    }

    #[test]
    fn the_percent_rounds_the_level() {
        for (level, percent) in [(0.0, 0), (0.625, 63), (0.632, 63), (1.0, 100), (1.5, 100)] {
            assert_eq!(
                Volume {
                    level,
                    muted: false
                }
                .percent(),
                percent
            );
        }
    }
}
