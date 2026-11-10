//! Where mpv draws the subtitles while the display stands over the film.
//!
//! mpv draws the subtitles into its own surface, under the display's, so
//! a cue at its place at the foot of the frame draws under the scrubber
//! and the control strip. A person who summons the display to read the
//! time must still read the cue, so while the display shows anything in
//! the band at the foot of the frame, the display asks mpv to move the
//! subtitles up until their lowest line ends one gap above the highest
//! thing it draws there. They return when the display hides.
//!
//! The display moves them with mpv's `sub-pos`, not `sub-margin-y`.
//! `sub-margin-y` moves only a text track that carries no style of its own,
//! such as SubRip, and leaves an ASS track and an image track (PGS, VobSub)
//! where they were. `sub-pos` moves all three: libass moves each ASS event
//! that the script does not place by itself, so a sign placed with `\pos`
//! stays on the thing it labels, and mpv moves each bitmap of an image
//! track that starts in the lower half of the picture.
//!
//! mpv has no animation of its own for the property. The display already
//! steps its fade on a tick while the fade moves, so it sends the position
//! the fade has reached on each of those ticks, and the subtitles move with
//! the fade in and out of the display. With the fade at rest it sends
//! nothing.

use serde_json::json;

use crate::ipc::Command;
use crate::theme;

/// The property the display sets.
pub const PROPERTY: &str = "sub-pos";

/// The `sub-pos` of a subtitle at its own place, which is mpv's default.
pub const REST: f64 = 100.0;

/// The space between the lowest line of a cue and the highest thing the
/// display draws in the band. It is the pad a chooser keeps around its rows,
/// so a lifted cue stands as far from the band as a row stands from the edge
/// of its panel.
pub const GAP: f32 = 24.0;

/// The canvas row where the lowest line of a text cue ends at its place. mpv
/// keeps a text cue `sub-margin-y` above the foot of the frame, 34 scaled
/// pixels by default, and a scaled pixel is one row of a frame 720 rows tall.
const FOOT: f32 = theme::CANVAS_HEIGHT * (1.0 - 34.0 / 720.0);

/// The `sub-pos` that puts the lowest line of a text cue one gap above
/// `top`, the highest canvas row the display draws on in the band.
///
/// libass ends a text cue at `sub-pos` percent of the way from the top of
/// the frame to the cue's own place, so the share of `FOOT` that clears the
/// band is the position. The rule is exact for a text track, the kind most
/// files carry.
///
/// The other kinds move by the same share of another height. libass ends an
/// ASS cue at the same share of the way from the top of the picture to the
/// margin the script states, so an ASS cue lands where a text cue does when
/// the script keeps mpv's margin, and a little lower when it keeps less. mpv
/// moves a bitmap of an image track up by the share of the track's own
/// canvas, scaled into the picture. A Blu-ray track draws inside the picture
/// of a letterboxed film, above the foot of the frame, so its cue starts
/// higher than a text cue and ends higher too.
///
/// A row in the lower band is never above the top of the frame, and a band
/// below the cue's place needs no lift, so the result stays between 0 and
/// `REST`.
pub fn lifted(top: f32) -> f64 {
    (f64::from((top - GAP) / FOOT) * REST).clamp(0.0, REST)
}

/// The `sub-pos` for one moment of the fade: at its place while nothing is
/// in the band, at `lifted` with the display full, and the share of the way
/// between them that the fade has reached while it moves. It is rounded to
/// a tenth of a percent, about one row of the canvas, so a fade sends no
/// change that moves nothing on screen.
pub fn position(top: Option<f32>, fade: f32) -> f64 {
    let Some(top) = top else {
        return REST;
    };
    let at = REST - (REST - lifted(top)) * f64::from(fade.clamp(0.0, 1.0));
    (at * 10.0).round() / 10.0
}

/// The position the display last asked mpv for, and the band it was asked
/// to clear.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Subtitles {
    sent: f64,
    band: Option<f32>,
}

impl Default for Subtitles {
    /// mpv starts every subtitle at its place, and nothing else in the pod
    /// sets `sub-pos`.
    fn default() -> Self {
        Self {
            sent: REST,
            band: None,
        }
    }
}

/// What one change asks of mpv and of the display's record.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Follow {
    /// The command that sets the new position, when it differs from the one
    /// mpv holds.
    pub command: Option<Command>,
    /// One line for the record when the band the subtitles clear changes,
    /// so a fade writes one line and not one for each of its ticks.
    pub said: Option<String>,
}

impl Subtitles {
    /// Follow the band and the fade. `top` is the highest canvas row the
    /// display draws on in the band, or nothing while it draws nothing there
    /// or no subtitle track plays.
    pub fn follow(&mut self, top: Option<f32>, fade: f32) -> Follow {
        let at = position(top, fade);
        let command = (at != self.sent).then(|| {
            self.sent = at;
            vec![json!("set_property"), json!(PROPERTY), json!(at)]
        });
        let said = (top != self.band).then(|| {
            self.band = top;
            match top {
                Some(top) => format!(
                    "media-display: subtitles move above row {top} of the display, to {PROPERTY} {}",
                    position(Some(top), 1.0)
                ),
                None => format!("media-display: subtitles return to their place, {PROPERTY} {REST}"),
            }
        });
        Follow { command, said }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Each case is the band's top row and the position that ends a text
    /// cue one gap above it: the time over the scrubber at 846, the up-next
    /// chip at 800, a one-row chooser at 770, the cast row at 652, and a
    /// band at or under the cue's own place, which needs no lift.
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
                "{top}: {} is not {expected}",
                lifted(top)
            );
        }
    }

    /// The lowest line of a text cue at the lifted position ends one gap
    /// above the band, by the rule libass places it by.
    #[test]
    fn libass_puts_the_lowest_line_one_gap_above_the_band() {
        let bottom = FOOT * (lifted(846.0) / REST) as f32;
        assert!((bottom - (846.0 - GAP)).abs() < 0.01, "{bottom}");
    }

    /// The position follows the fade from the cue's place to the lift, and
    /// with nothing in the band it stays at the place whatever the fade.
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
            assert_eq!(position(top, fade), expected, "{top:?} at {fade}");
        }
    }

    fn set(at: f64) -> Option<Command> {
        Some(vec![json!("set_property"), json!("sub-pos"), json!(at)])
    }

    /// A change that leaves the position where mpv holds it sends nothing,
    /// so the display sends no command while its fade stands still.
    #[test]
    fn a_position_mpv_holds_is_not_sent_again() {
        let mut subtitles = Subtitles::default();
        assert_eq!(subtitles.follow(None, 0.0).command, None);
        assert_eq!(subtitles.follow(Some(846.0), 0.0).command, None);
        assert_eq!(subtitles.follow(Some(846.0), 1.0).command, set(79.9));
        assert_eq!(subtitles.follow(Some(846.0), 1.0).command, None);
        assert_eq!(subtitles.follow(None, 0.0).command, set(REST));
    }

    /// The record takes one line when the band changes and none for a step
    /// of the fade.
    #[test]
    fn the_record_takes_one_line_for_each_band() {
        let mut subtitles = Subtitles::default();
        let said: Vec<Option<String>> = [
            (Some(846.0), 0.0),
            (Some(846.0), 0.5),
            (Some(846.0), 1.0),
            (Some(770.0), 1.0),
            (None, 0.0),
            (None, 0.0),
        ]
        .into_iter()
        .map(|(top, fade)| subtitles.follow(top, fade).said)
        .collect();
        assert_eq!(
            said,
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
