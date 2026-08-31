// The listening level as `Player` state. One retained topic carries it, every
// pod for the unit follows that topic, and `media-operator` writes it in
// `volume.go`.

use serde::Deserialize;

/// The bottom of the range, and unity. The level runs 0 to 100, and 100 is
/// unity. The cap is unity because the sinks play at unity on the audio side,
/// and a software gain above unity only distorts.
pub const MIN_LEVEL: i64 = 0;
pub const UNITY_LEVEL: i64 = 100;

/// The whole payload on the volume topic. Both fields are always written, so a
/// message that omits one reads as the zero value the writer would have read,
/// and no reader needs a default of its own.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
pub struct Volume {
    #[serde(default)]
    pub level: i64,
    #[serde(default)]
    pub muted: bool,
}

impl Default for Volume {
    /// The state the client holds before any message reaches it, and the value
    /// the operator seeds a unit at.
    fn default() -> Self {
        Self {
            level: UNITY_LEVEL,
            muted: false,
        }
    }
}

impl Volume {
    /// The level held inside 0 to 100. A level outside the range is clamped
    /// rather than refused, so another program's message cannot draw a row past
    /// unity.
    pub fn clamped(self) -> Self {
        Self {
            level: self.level.clamp(MIN_LEVEL, UNITY_LEVEL),
            ..self
        }
    }
}

/// Read one message off the volume topic. A payload that does not decode is no
/// state at all and changes nothing.
pub fn parse(payload: &[u8]) -> Option<Volume> {
    super::object::<Volume>(payload).map(Volume::clamped)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_state_decodes() {
        assert_eq!(
            parse(br#"{"level":40,"muted":true}"#),
            Some(Volume {
                level: 40,
                muted: true
            })
        );
    }

    #[test]
    fn a_level_outside_the_range_is_clamped() {
        assert_eq!(
            parse(br#"{"level":400,"muted":false}"#).map(|v| v.level),
            Some(100)
        );
        assert_eq!(
            parse(br#"{"level":-9,"muted":false}"#).map(|v| v.level),
            Some(0)
        );
    }

    #[test]
    fn a_field_the_message_omits_reads_as_its_zero_value() {
        assert_eq!(
            parse(br#"{"level":40}"#),
            Some(Volume {
                level: 40,
                muted: false
            })
        );
        assert_eq!(parse(br#"{}"#).map(|state| state.level), Some(0));
    }

    #[test]
    fn text_that_does_not_parse_is_no_state() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"40"), None);
        assert_eq!(parse(b"[40,true]"), None);
        assert_eq!(parse(br#"{"level":"loud"}"#), None);
    }

    #[test]
    fn a_client_with_no_message_holds_unity_unmuted() {
        assert_eq!(Volume::default().level, UNITY_LEVEL);
        assert!(!Volume::default().muted);
    }
}
