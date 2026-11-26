//! The header of the copy of a text track that is not ASS, such as
//! SubRip. mpv draws such a track through FFmpeg's converter, and then
//! changes the converted script's default style for each size of the
//! screen, in `configure_ass` of mpv 0.41.0's `sub/sd_ass.c`. The copy is
//! an ASS track, which mpv does not change. So the display writes those
//! changed values into the copy's header itself, for the screen as it is
//! now, and a cue of one line of the copy is drawn on the same rows as the
//! same cue of the original.
//!
//! mpv's steps for a converted track, with the house style of `pod.go`:
//!
//! - `mp_ass_set_style` sets the default style from the `sub-*` options in
//!   a `PlayResY` of 288, which scales a scaled pixel by 288 ÷ 720. libass
//!   stores a margin as an integer, so `sub-margin-y=34` becomes 13 and
//!   `sub-margin-x=19` becomes 7.
//! - `PlayResX` becomes `PlayResY` times the shape of the picture, as an
//!   integer.
//! - `MarginL` and `MarginR` are scaled by `PlayResX ÷ 384`, rounded.
//! - `MarginV` is scaled by the font scale that `sub-scale-with-window`
//!   gives, the height of the screen over the height of the picture,
//!   rounded. This step moves a converted cue by a row or two from one
//!   picture height to the next.
//!
//! mpv draws the copy under `sub-ass-override=force` at `sub-pos` 100, as
//! it draws the original.

use super::super::cue::Rest;
use crate::film::Screen;

/// libass's `PlayResY` for a converted track, and FFmpeg's `PlayResX`.
const PLAY_RES_Y: u32 = 288;
const PLAY_RES_X: u32 = 384;

/// The house style's margins, `sub-margin-y=34` and `sub-margin-x=19`,
/// in a `PlayResY` of 288, as `mp_ass_set_style` stores them.
const MARGIN_V: f64 = 13.0;
const MARGIN_X: f64 = 7.0;

/// The `sub-pos` at which mpv draws a converted track, which the copy
/// keeps.
pub const SUB_POS: f64 = 100.0;

/// The values that mpv gives the default style of a converted track on
/// `screen`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Style {
    pub play_res_x: u32,
    pub margin_x: i32,
    pub margin_v: i32,
}

/// The style on `screen`, or mpv's values before it knows a screen.
pub fn style(screen: Option<&Screen>) -> Style {
    let Some(screen) = screen.filter(|screen| screen.width > 0 && screen.height > 0) else {
        return Style {
            play_res_x: PLAY_RES_X,
            margin_x: MARGIN_X as i32,
            margin_v: MARGIN_V as i32,
        };
    };
    let width = (screen.width - screen.left - screen.right).max(1) as f64;
    let height = (screen.height - screen.top - screen.bottom).max(1) as f64;
    let play_res_x = (f64::from(PLAY_RES_Y) * width / height) as u32;
    let scaled_height = (screen.width as f64 / width * height).min(screen.height as f64);
    let font_scale = screen.height as f64 / scaled_height.max(1.0);
    Style {
        play_res_x,
        margin_x: (MARGIN_X * f64::from(play_res_x) / f64::from(PLAY_RES_X)).round_ties_even()
            as i32,
        margin_v: (MARGIN_V * font_scale).round_ties_even() as i32,
    }
}

/// How the copy rests on `screen`, for the margin of a cue of more lines.
pub fn rest(screen: &Screen) -> Rest {
    Rest {
        play_res_y: PLAY_RES_Y,
        margin_v: style(Some(screen)).margin_v,
        sub_pos: SUB_POS,
    }
}

/// The copy's header on `screen`: FFmpeg's header for a converted track,
/// with the style that mpv gives it there.
pub fn header(screen: Option<&Screen>) -> String {
    let style = style(screen);
    format!(
        "[Script Info]\n\
         ScriptType: v4.00+\n\
         PlayResX: {}\n\
         PlayResY: {PLAY_RES_Y}\n\
         ScaledBorderAndShadow: yes\n\
         YCbCr Matrix: None\n\
         \n\
         [V4+ Styles]\n\
         Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, \
         BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, \
         BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n\
         Style: Default,Arial,16,&Hffffff,&Hffffff,&H0,&H0,0,0,0,0,100,100,0,0,1,1,0,2,{},{},{},0\n",
        style.play_res_x, style.margin_x, style.margin_x, style.margin_v
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn letterboxed(picture: i64) -> Screen {
        let bar = (1080 - picture) / 2;
        Screen {
            width: 1920,
            height: 1080,
            top: bar,
            bottom: 1080 - picture - bar,
            left: 0,
            right: 0,
        }
    }

    /// The calibration of plan 49. A header with each row's values, drawn
    /// under `force` at `sub-pos` 100 by mpv 0.41.0 on a real picture of
    /// that height, drew each one-line and two-line cue on the same rows as
    /// the SubRip track that it copies.
    #[test]
    fn the_style_is_the_one_mpv_gives_a_converted_track() {
        let cases = [
            (800, 691, 13, 18),
            (804, 687, 13, 17),
            (872, 634, 12, 16),
            (1038, 532, 10, 14),
            (1080, 512, 9, 13),
        ];
        for (picture, play_res_x, margin_x, margin_v) in cases {
            assert_eq!(
                style(Some(&letterboxed(picture))),
                Style {
                    play_res_x,
                    margin_x,
                    margin_v
                },
                "a 1920x{picture} picture"
            );
        }
    }

    /// A 4:3 title fills the height of the screen, so the font scale is 1.
    #[test]
    fn a_picture_with_side_bars_keeps_the_margin() {
        let pillarboxed = Screen {
            width: 1920,
            height: 1080,
            top: 0,
            bottom: 0,
            left: 240,
            right: 240,
        };
        assert_eq!(style(Some(&pillarboxed)).margin_v, 13);
        assert_eq!(style(None).margin_v, 13);
    }

    #[test]
    fn the_header_carries_the_style() {
        let header = header(Some(&letterboxed(800)));
        assert!(header.contains("\nPlayResX: 691\nPlayResY: 288\n"));
        assert!(header.ends_with(",2,13,13,18,0\n"));
    }
}
