// The retained volume topic, which only `media-operator` writes. The
// operator divides the level that the `Player`'s `Receiver` or `Sink`
// reports by the device's maximum, so every screen draws a 0.0 to 1.0
// scale whatever the device. The maximum is `spec.volume.max`, or for a
// `Receiver` that sets none, the top of its driver's own scale. When a `Receiver` sets
// `spec.volume.indicator` to `Receiver`, it draws its own overlay on the TV,
// and the operator adds `"indicator": "receiver"` so the screen draws no
// second bar.

use serde::Deserialize;

/// The ends of the range. `MAX_LEVEL` is the device's maximum.
pub const MIN_LEVEL: f64 = 0.0;
pub const MAX_LEVEL: f64 = 1.0;

/// Which side draws the volume bar. A missing field and an unknown value both
/// decode as `Player`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Indicator {
    Receiver,
    #[default]
    #[serde(other)]
    Player,
}

/// The payload of the volume topic. A payload that omits a field decodes
/// with that field's zero value instead of failing.
#[derive(Debug, Clone, Copy, PartialEq, Deserialize)]
pub struct Volume {
    #[serde(default)]
    pub level: f64,
    #[serde(default)]
    pub muted: bool,
    #[serde(default)]
    pub indicator: Indicator,
}

impl Default for Volume {
    /// The volume before any message arrives.
    fn default() -> Self {
        Self {
            level: MAX_LEVEL,
            muted: false,
            indicator: Indicator::Player,
        }
    }
}

impl Volume {
    /// Clamps `level` to 0.0 to 1.0, so a bad message cannot draw a bar past
    /// either end of the scale.
    pub fn clamped(self) -> Self {
        Self {
            level: self.level.clamp(MIN_LEVEL, MAX_LEVEL),
            ..self
        }
    }

    /// Whether a live message with this volume draws the bar, after the
    /// screen had `before`. A message that changes only `indicator` draws
    /// nothing, because the operator republished the level after a change of
    /// `spec.volume.indicator` or of device, and nobody pressed a key. A
    /// message equal to `before` does draw, because a press at either end of
    /// the scale republishes the same payload.
    pub fn draws_after(self, before: Volume) -> bool {
        let only_the_indicator = self.indicator != before.indicator
            && self.level == before.level
            && self.muted == before.muted;
        self.indicator == Indicator::Player && !only_the_indicator
    }

    /// The level as a whole percent, for the label beside the bar.
    pub fn percent(self) -> i64 {
        // The level is clamped to 0.0 to 1.0, so the product fits in an i64.
        #[allow(clippy::cast_possible_truncation)]
        let percent = (self.clamped().level * 100.0).round() as i64;
        percent
    }
}

/// Decodes one message from the volume topic, with the level clamped.
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
                muted: true,
                ..Volume::default()
            })
        );
    }

    #[test]
    fn the_indicator_decodes() {
        for (payload, indicator) in [
            (r#"{"level":0.63,"muted":false}"#, Indicator::Player),
            (
                r#"{"level":0.63,"muted":false,"indicator":"receiver"}"#,
                Indicator::Receiver,
            ),
            (
                r#"{"level":0.63,"muted":false,"indicator":"hologram"}"#,
                Indicator::Player,
            ),
        ] {
            assert_eq!(
                parse(payload.as_bytes()).map(|volume| volume.indicator),
                Some(indicator),
                "{payload}"
            );
        }
    }

    fn at(level: f64, muted: bool, indicator: Indicator) -> Volume {
        Volume {
            level,
            muted,
            indicator,
        }
    }

    #[test]
    fn a_live_level_draws_unless_the_receiver_draws_it() {
        use Indicator::{Player, Receiver};
        for (before, after, draws) in [
            (at(0.4, false, Player), at(0.45, false, Player), true),
            (at(0.4, false, Player), at(0.4, false, Player), true),
            (at(0.4, false, Player), at(0.4, true, Player), true),
            (at(0.4, false, Receiver), at(0.45, false, Receiver), false),
            (at(0.4, false, Player), at(0.4, false, Receiver), false),
            (at(0.4, false, Receiver), at(0.4, false, Player), false),
            (at(0.4, false, Receiver), at(0.45, false, Player), true),
        ] {
            assert_eq!(
                after.draws_after(before),
                draws,
                "{before:?} then {after:?}"
            );
        }
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
                muted: false,
                ..Volume::default()
            })
        );
        assert_eq!(parse(br#"{}"#).map(|state| state.level), Some(0.0));
    }

    #[test]
    fn a_payload_that_does_not_decode_returns_none() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"0.4"), None);
        assert_eq!(parse(b"[0.4,true]"), None);
        assert_eq!(parse(br#"{"level":"loud"}"#), None);
    }

    #[test]
    fn the_default_volume_is_the_maximum_level_unmuted() {
        assert_eq!(Volume::default().level, MAX_LEVEL);
        assert!(!Volume::default().muted);
    }

    #[test]
    fn the_percent_rounds_the_level() {
        for (level, percent) in [(0.0, 0), (0.625, 63), (0.632, 63), (1.0, 100), (1.5, 100)] {
            assert_eq!(
                Volume {
                    level,
                    muted: false,
                    ..Volume::default()
                }
                .percent(),
                percent
            );
        }
    }
}
