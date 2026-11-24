//! The `sub-pos` that raises a cue above the controls at the foot of the
//! frame. The lift starts from the selected track's resting `sub-pos`,
//! and the track's `Lift` says how `sub-pos` moves its cues.

use super::placement::Lift;
use crate::theme;

/// The space, in canvas rows, between the lowest line of a cue and the
/// highest thing the display draws in the band. It equals the padding
/// that the track panel keeps around its rows, so a raised cue is as far
/// from the band as a row is from the edge of the panel.
pub const GAP: f32 = 24.0;

/// The canvas row where the lowest line of a cue ends at its resting
/// place. mpv puts a text cue `sub-margin-y` above the foot of the frame,
/// which is 34 scaled pixels, and a scaled pixel is one row of a frame
/// 720 rows tall. The resting values of the other kinds end their cues on
/// the same row.
pub const FOOT: f32 = theme::CANVAS_HEIGHT * (1.0 - 34.0 / 720.0);

/// Returns the `sub-pos` that ends the lowest line of a cue `GAP` above
/// `top`, the highest canvas row that the display draws on in the band.
/// A band below the cue's resting place needs no lift, so it returns
/// `rest`.
///
/// For a text cue, libass ends the cue at `sub-pos` percent of the way
/// down to the row where it ends at 100, so the lifted value is `rest`
/// times `top − GAP` over `FOOT`. An image cue moves by a share of its
/// canvas, which the stretch maps onto the screen, so the lifted value is
/// `rest` less the rows to move as a percentage of the screen's height.
pub fn lifted(rest: f64, lift: Lift, top: f32) -> f64 {
    let end = f64::from(top - GAP);
    let at = match lift {
        Lift::Share => rest * end / f64::from(FOOT),
        Lift::Rows => rest - 100.0 * (f64::from(FOOT) - end) / f64::from(theme::CANVAS_HEIGHT),
    };
    at.clamp(0.0, rest)
}

/// Returns the `sub-pos` for a fade value: `rest` when `top` is `None`,
/// and otherwise a value that moves from `rest` at a fade of 0 to
/// `lifted` at a fade of 1. A step is rounded to a tenth of a percent,
/// about one canvas row, so the fade sends no change too small to move a
/// subtitle on screen. `rest` is returned as it is, because the resting
/// value of an image track is exact to a fraction of a canvas row.
pub fn position(rest: f64, lift: Lift, top: Option<f32>, fade: f32) -> f64 {
    let Some(top) = top else {
        return rest;
    };
    let at = rest - (rest - lifted(rest, lift, top)) * f64::from(fade.clamp(0.0, 1.0));
    match at == rest {
        true => rest,
        false => (at * 10.0).round() / 10.0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const REST: f64 = 100.0;

    /// The rows are band tops across the range that the display draws at,
    /// and bands at or below the cue's resting place.
    #[test]
    fn a_plain_cue_ends_one_gap_above_the_band() {
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
            let at = lifted(REST, Lift::Share, top);
            assert!(
                (at - expected).abs() < 0.01,
                "the position for a band at {top} is {at}, want {expected}"
            );
        }
    }

    /// libass ends a text cue at `sub-pos` percent of the row where it
    /// ends at 100. At the resting value that row is `FOOT`, so it is
    /// `FOOT` times 100 over the resting value.
    #[test]
    fn libass_puts_the_lowest_line_one_gap_above_the_band() {
        for rest in [REST, 97.358, 96.449] {
            let at_100 = f64::from(FOOT) * REST / rest;
            let bottom = at_100 * lifted(rest, Lift::Share, 846.0) / REST;
            assert!(
                (bottom - f64::from(846.0 - GAP)).abs() < 0.01,
                "with a resting sub-pos of {rest}, the lowest line ends at {bottom}, want {}",
                846.0 - GAP
            );
        }
    }

    /// mpv moves a bitmap by `sub-pos` minus 100 percent of the canvas,
    /// and the stretch maps the canvas onto the screen, so the cue rises
    /// by that share of the screen from `FOOT`.
    #[test]
    fn an_image_cue_rises_by_the_rows_between_its_resting_row_and_the_band() {
        let cases = [(111.157, 846.0), (106.146, 770.0), (101.157, 652.0)];
        for (rest, top) in cases {
            let moved =
                (rest - lifted(rest, Lift::Rows, top)) / REST * f64::from(theme::CANVAS_HEIGHT);
            let bottom = f64::from(FOOT) - moved;
            assert!(
                (bottom - f64::from(top - GAP)).abs() < 0.01,
                "with a resting sub-pos of {rest}, the lowest line ends at {bottom}, want {}",
                top - GAP
            );
        }
        assert_eq!(lifted(111.157, Lift::Rows, FOOT + GAP), 111.157);
    }

    /// The fade's steps are rounded to a tenth, about one canvas row. The
    /// resting value is sent as the placement computed it, because a tenth
    /// of a percent of an image canvas can move a cue one row.
    #[test]
    fn the_position_follows_the_fade_from_the_resting_value() {
        let cases = [
            (REST, Lift::Share, Some(846.0), 0.0, REST),
            (REST, Lift::Share, Some(846.0), 0.5, 89.9),
            (REST, Lift::Share, Some(846.0), 1.0, 79.9),
            (REST, Lift::Share, None, 1.0, REST),
            (97.358, Lift::Share, None, 0.5, 97.358),
            (97.358, Lift::Share, Some(846.0), 0.0, 97.358),
            (97.358, Lift::Share, Some(846.0), 1.0, 77.8),
            (111.157, Lift::Rows, Some(846.0), 1.0, 92.0),
            (111.157, Lift::Rows, Some(FOOT + GAP), 1.0, 111.157),
        ];
        for (rest, lift, top, fade, expected) in cases {
            assert_eq!(
                position(rest, lift, top, fade),
                expected,
                "the position from {rest} by {lift:?} for a band at {top:?} and fade {fade}"
            );
        }
    }
}
