//! Moves mpv's subtitles up while the display is showing something at
//! the foot of the frame.
//!
//! mpv draws subtitles in its own surface under the display's. At their
//! normal place at the foot of the frame, a cue would sit under the
//! scrubber and the control strip, and a person who brings up the
//! display to check the time could not read it. So while the display
//! draws anything in the band at the foot of the frame, it sets mpv's
//! `sub-pos` so that the lowest line of a cue ends `GAP` above the
//! highest thing the display draws there. When the display hides,
//! `sub-pos` goes back to mpv's default.
//!
//! The display uses `sub-pos` and not `sub-margin-y`. `sub-margin-y`
//! moves only text tracks that have no style of their own, such as
//! SubRip. It leaves ASS tracks and image tracks (PGS, VobSub) where
//! they were. `sub-pos` moves all three. libass moves each ASS event
//! that the script does not place itself, so a sign placed with `\pos`
//! stays on the thing it labels. mpv moves each bitmap of an image
//! track that starts in the lower half of the picture.
//!
//! mpv does not animate `sub-pos`. So on each tick of the display's
//! fade, the display sends the position for the fade's current value,
//! and the subtitles move with the fade. When the fade is at rest, the
//! display sends nothing.

use serde_json::json;

use crate::ipc::Command;
use crate::theme;

pub const PROPERTY: &str = "sub-pos";

/// mpv's default `sub-pos`, which leaves a subtitle at its normal
/// place.
pub const REST: f64 = 100.0;

/// The space, in canvas rows, between the lowest line of a cue and the
/// highest thing the display draws in the band. It equals the padding
/// that a chooser keeps around its rows, so a raised cue is as far from
/// the band as a row is from the edge of its panel.
pub const GAP: f32 = 24.0;

/// The canvas row where the lowest line of a text cue ends at its
/// normal place. mpv puts a text cue `sub-margin-y` above the foot of
/// the frame, which defaults to 34 scaled pixels, and a scaled pixel is
/// one row of a frame 720 rows tall.
const FOOT: f32 = theme::CANVAS_HEIGHT * (1.0 - 34.0 / 720.0);

/// Returns the `sub-pos` that ends the lowest line of a text cue `GAP`
/// above `top`, the highest canvas row that the display draws on in the
/// band. libass ends a text cue at `sub-pos` percent of the way from
/// the top of the frame to `FOOT`, so the result is `top - GAP` as a
/// percentage of `FOOT`, clamped to 0 through `REST`. A band below the
/// cue's normal place needs no lift, so it returns `REST`.
///
/// Other tracks move by the same percentage of a different height, so
/// they end near that row and not exactly on it. An ASS cue moves
/// toward the margin that its script sets, and lands a little lower
/// than a text cue when that margin is smaller than mpv's. An image
/// track moves by a share of its own canvas. A Blu-ray track that draws
/// inside the picture of a letterboxed film ends higher than a text
/// cue.
pub fn lifted(top: f32) -> f64 {
    (f64::from((top - GAP) / FOOT) * REST).clamp(0.0, REST)
}

/// Returns the `sub-pos` for a fade value: `REST` when `top` is `None`,
/// and otherwise a value that moves from `REST` at a fade of 0 to
/// `lifted(top)` at a fade of 1. It is rounded to a tenth of a percent,
/// about one canvas row, so the fade sends no change too small to move
/// a subtitle on screen.
pub fn position(top: Option<f32>, fade: f32) -> f64 {
    let Some(top) = top else {
        return REST;
    };
    let at = REST - (REST - lifted(top)) * f64::from(fade.clamp(0.0, 1.0));
    (at * 10.0).round() / 10.0
}

/// The `sub-pos` last sent to mpv, and the band that it was sent for.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Subtitles {
    sent: f64,
    band: Option<f32>,
}

impl Default for Subtitles {
    /// mpv starts with every subtitle at its normal place, and nothing
    /// else in the pod sets `sub-pos`.
    fn default() -> Self {
        Self {
            sent: REST,
            band: None,
        }
    }
}

/// What one call to `Subtitles::follow` returns.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Follow {
    /// The command that sets `sub-pos`, when the new position differs
    /// from the one last sent.
    pub command: Option<Command>,
    /// A log line when the band changed, so a fade writes one line and
    /// not one for each tick.
    pub logged: Option<String>,
}

impl Subtitles {
    /// `top` is the highest canvas row that the display draws on in the
    /// band. It is `None` while the display is hidden or no subtitle
    /// track is selected.
    pub fn follow(&mut self, top: Option<f32>, fade: f32) -> Follow {
        let at = position(top, fade);
        let command = (at != self.sent).then(|| {
            self.sent = at;
            vec![json!("set_property"), json!(PROPERTY), json!(at)]
        });
        let logged = (top != self.band).then(|| {
            self.band = top;
            match top {
                Some(top) => format!(
                    "media-display: subtitles move above row {top} of the display, to {PROPERTY} {}",
                    position(Some(top), 1.0)
                ),
                None => format!("media-display: subtitles return to their place, {PROPERTY} {REST}"),
            }
        });
        Follow { command, logged }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The rows are the tops of the time above the scrubber at 846, the
    /// up-next chip at 800, a one-row chooser at 770, the cast row at
    /// 652, and bands at or below the cue's normal place.
    #[test]
    fn a_cue_ends_one_gap_above_the_band() {
        let cases = [
            (846.0, 79.88),
            (800.0, 75.41),
            (770.0, 72.50),
            (652.0, 61.03),
            (FOOT + GAP, REST),
            (theme::CANVAS_HEIGHT, REST),
            (0.0, 0.0),
        ];
        for (top, expected) in cases {
            assert!(
                (lifted(top) - expected).abs() < 0.01,
                "the position for a band at {top} is {}, want {expected}",
                lifted(top)
            );
        }
    }

    /// libass ends a text cue at `sub-pos` percent of `FOOT`.
    #[test]
    fn libass_puts_the_lowest_line_one_gap_above_the_band() {
        let bottom = FOOT * (lifted(846.0) / REST) as f32;
        assert!(
            (bottom - (846.0 - GAP)).abs() < 0.01,
            "the lowest line ends at {bottom}, want {}",
            846.0 - GAP
        );
    }

    /// With nothing in the band, the position stays at `REST` at any
    /// fade value.
    #[test]
    fn the_position_follows_the_fade() {
        let cases = [
            (Some(846.0), 0.0, REST),
            (Some(846.0), 0.5, 89.9),
            (Some(846.0), 1.0, 79.9),
            (None, 1.0, REST),
            (None, 0.5, REST),
        ];
        for (top, fade, expected) in cases {
            assert_eq!(
                position(top, fade),
                expected,
                "the position for a band at {top:?} and fade {fade}"
            );
        }
    }

    fn set(at: f64) -> Option<Command> {
        Some(vec![json!("set_property"), json!("sub-pos"), json!(at)])
    }

    /// A call that computes the position last sent returns no command,
    /// so the display sends nothing while its fade is at rest.
    #[test]
    fn a_position_already_sent_is_not_sent_again() {
        let mut subtitles = Subtitles::default();
        assert_eq!(subtitles.follow(None, 0.0).command, None);
        assert_eq!(subtitles.follow(Some(846.0), 0.0).command, None);
        assert_eq!(subtitles.follow(Some(846.0), 1.0).command, set(79.9));
        assert_eq!(subtitles.follow(Some(846.0), 1.0).command, None);
        assert_eq!(subtitles.follow(None, 0.0).command, set(REST));
    }

    /// A step of the fade returns no log line.
    #[test]
    fn a_change_of_band_returns_one_log_line() {
        let mut subtitles = Subtitles::default();
        let logged: Vec<Option<String>> = [
            (Some(846.0), 0.0),
            (Some(846.0), 0.5),
            (Some(846.0), 1.0),
            (Some(770.0), 1.0),
            (None, 0.0),
            (None, 0.0),
        ]
        .into_iter()
        .map(|(top, fade)| subtitles.follow(top, fade).logged)
        .collect();
        assert_eq!(
            logged,
            [
                Some(
                    "media-display: subtitles move above row 846 of the display, to sub-pos 79.9"
                        .to_string()
                ),
                None,
                None,
                Some(
                    "media-display: subtitles move above row 770 of the display, to sub-pos 72.5"
                        .to_string()
                ),
                Some("media-display: subtitles return to their place, sub-pos 100".to_string()),
                None,
            ]
        );
    }
}
