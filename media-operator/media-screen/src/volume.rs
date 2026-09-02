// The listening level as `Player` state. One retained topic carries it, every
// pod for the unit follows that topic, and `media-operator` writes it in
// `volume.go`.

use serde::{Deserialize, Serialize};

/// The bottom of the range, and unity. The level runs 0 to 100, and 100 is
/// unity. The cap is unity because the sinks play at unity on the audio side,
/// and a software gain above unity only distorts.
pub const MIN_LEVEL: i64 = 0;
pub const UNITY_LEVEL: i64 = 100;

/// The whole payload on the volume topic. The operator writes both fields on
/// every message, and each one still defaults here, so a partial payload from
/// another writer reads as the zero value instead of failing to decode.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
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

/// The amount one press moves the level by. It is this consumer's default,
/// the same five the playback pod steps by, so a level moves the same amount
/// whether or not a film plays.
pub const STEP: i64 = 5;

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

    /// The state one level press leaves. It steps from the last message the
    /// topic delivered, so two pods that press against the same message
    /// compute the same absolute value and the step lands once.
    pub fn stepped(self, by: i64) -> Self {
        Self {
            level: self.level + by,
            ..self
        }
        .clamped()
    }

    /// The state one mute press leaves. Mute is a plain toggle, so the flag
    /// survives into the next `Play` and the indicator's glyph is what says
    /// so.
    pub fn toggled(self) -> Self {
        Self {
            muted: !self.muted,
            ..self
        }
        .clamped()
    }

    /// The state as it travels on the topic. The clamp runs here too, so
    /// nothing this crate publishes is out of range.
    pub fn payload(self) -> Vec<u8> {
        // A level and a flag always encode, so the error is the interface's
        // and not a state this code reaches.
        serde_json::to_vec(&self.clamped()).unwrap_or_default()
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
    #[test]
    fn a_level_press_steps_from_the_last_message() {
        let held = Volume {
            level: 40,
            muted: false,
        };
        assert_eq!(held.stepped(STEP).level, 45);
        assert_eq!(held.stepped(-STEP).level, 35);
    }

    #[test]
    fn a_step_never_leaves_the_range() {
        assert_eq!(
            Volume {
                level: 98,
                muted: false
            }
            .stepped(STEP)
            .level,
            UNITY_LEVEL
        );
        assert_eq!(
            Volume {
                level: 2,
                muted: false
            }
            .stepped(-STEP)
            .level,
            MIN_LEVEL
        );
    }

    #[test]
    fn a_mute_press_toggles_the_flag_and_holds_the_level() {
        let muted = Volume {
            level: 40,
            muted: false,
        }
        .toggled();
        assert_eq!(
            muted,
            Volume {
                level: 40,
                muted: true
            }
        );
        assert!(!muted.toggled().muted);
    }

    #[test]
    fn the_payload_is_the_whole_state() {
        assert_eq!(
            Volume {
                level: 45,
                muted: false
            }
            .payload(),
            br#"{"level":45,"muted":false}"#
        );
        assert_eq!(
            Volume {
                level: 400,
                muted: true
            }
            .payload(),
            br#"{"level":100,"muted":true}"#
        );
    }
}
