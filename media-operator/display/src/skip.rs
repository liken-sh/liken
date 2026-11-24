//! The skip button. While the playhead is inside an intro or a recap
//! span it reads "Skip intro" or "Skip recap", and selecting it seeks
//! to the end of the span. Inside the end credits of a film that has a
//! scene after them, it reads "Skip to post-credits scene" and seeks to
//! that scene. It never skips by itself, because a viewer who has not
//! seen the show may want the recap or the intro, and a viewer may want
//! to read the credits.
//!
//! The button is on the up-next chip's row, with the chip's metrics and
//! the card's fill, so it looks like part of the same set of controls.
//! It is at the left margin, and the chip and the card are at the right
//! margin, so they do not overlap. While the OSD is up, it is one of the
//! OSD's focus stops for as long as the playhead is inside the span.
//!
//! Over the bare video, with its own fade, the intro and recap button
//! shows only while the playhead is in the first [`ALONE`] seconds of
//! its span, so it does not sit over a long recap. It is drawn focused
//! there, and a select activates it, so a viewer can skip with one
//! press of the remote. The window follows the playhead, not a timer,
//! so a pause does not use it up and a seek back to the start of the
//! span shows the button again. The post-credits button shows over the
//! bare video for its whole span. It is drawn unfocused there, and a
//! select pauses the film, because the up-next card is also up during
//! the credits and a viewer may want to pause and read them.

use iced::{Point, Rectangle, Size};
use serde_json::json;

use crate::canvas::{Anchor, Brush, Line, measure};
use crate::fade::{Clock, Fade, Hide};
use crate::ipc::Command;
use crate::marks::{Jump, Kind};
use crate::theme;
use crate::upnext::{CARD_ALPHA, CHIP_H, CHIP_PAD_X, CHIP_PAD_Y, CHIP_R, CHIP_Y};

/// The seconds from the start of an intro or a recap during which its
/// button shows over the bare video.
pub const ALONE: f64 = 10.0;

#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Skip {
    /// The span that the playhead is inside, and the time that a select
    /// seeks to. It is `None` while the button is not offered.
    jump: Option<Jump>,
    /// The kind of span last offered. The button keeps this label while
    /// it fades out after the playhead has left the span.
    kind: Option<Kind>,
    /// The span that a select skipped. mpv can report a position inside
    /// the span before the seek completes, and the button stays hidden
    /// for those reports. A report of any other span, or of none, clears
    /// this field, so a later seek back into the span offers it again.
    passed: Option<Jump>,
    /// Whether the button shows over the bare video at the playhead.
    alone: bool,
    clock: Clock,
}

impl Skip {
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    /// Sets the playhead and the jump that the marks offer there, or
    /// `None`. Returns whether the offered jump or its place over the bare
    /// video changed, so the frame loop redraws only on a change.
    pub fn on_position(&mut self, at: Option<f64>, inside: Option<Jump>) -> bool {
        if self.passed != inside {
            self.passed = None;
        }
        let offered = inside.filter(|jump| Some(*jump) != self.passed);
        let alone = offered.is_some_and(|jump| shows_alone(jump, at));
        if offered == self.jump && alone == self.alone {
            return false;
        }
        self.jump = offered;
        self.alone = alone;
        if let Some(jump) = offered {
            self.kind = Some(jump.span.kind);
        }
        // The playhead ends the button's time over the bare video, so it
        // requests no hide timer.
        match alone {
            true => self.clock.show(Hide::Keep),
            false => self.clock.hide(),
        }
        true
    }

    /// Returns whether the button offers a span, which makes it a focus
    /// stop.
    pub fn available(&self) -> bool {
        self.jump.is_some()
    }

    /// Returns whether a select over the bare video activates the
    /// button, which it does while an intro or a recap button shows
    /// there.
    pub fn takes_select_outside(&self) -> bool {
        self.alone && self.jump.is_some_and(|jump| takes_select(jump.span.kind))
    }

    /// Returns the command that seeks to the jump's target. The seek is
    /// exact, so the first frame after the intro, or the first frame of
    /// the scene, is the one shown. The button hides at once and stays
    /// hidden until the playhead leaves the span it skipped.
    pub fn take(&mut self) -> Vec<Command> {
        let Some(jump) = self.jump.take() else {
            return Vec::new();
        };
        self.passed = Some(jump);
        self.alone = false;
        self.clock.hide();
        vec![vec![json!("seek"), json!(jump.to), json!("absolute+exact")]]
    }

    /// Draws the button as part of the OSD, on the focus panel while it
    /// has focus.
    pub fn draw(&self, brush: &mut Brush<'_>, focused: bool) {
        if let Some(jump) = self.jump {
            pill(brush, jump.span.kind, focused);
        }
    }

    /// Draws the button over the bare video, with its own fade. While
    /// the OSD is up, the OSD draws the button, so this draws nothing.
    pub fn draw_outside(&self, brush: &mut Brush<'_>, osd_visible: bool) {
        if !self.draws_outside(osd_visible) {
            return;
        }
        let Some(kind) = self.kind else {
            return;
        };
        brush.at_fade(self.fade().value(), |brush| {
            pill(brush, kind, takes_select(kind))
        });
    }

    /// Returns whether `draw_outside` would draw anything, so the frame
    /// loop adds no layer when the button is not visible.
    pub fn draws_outside(&self, osd_visible: bool) -> bool {
        !osd_visible && self.kind.is_some() && self.fade().value() > 0.0
    }
}

/// Returns whether the button for a jump shows over the bare video at
/// the playhead: for the whole credits span, and for the first [`ALONE`]
/// seconds of an intro or a recap.
fn shows_alone(jump: Jump, at: Option<f64>) -> bool {
    match jump.span.kind {
        Kind::Intro | Kind::Recap => at.is_some_and(|at| at < jump.span.start + ALONE),
        _ => true,
    }
}

fn takes_select(kind: Kind) -> bool {
    matches!(kind, Kind::Intro | Kind::Recap)
}

/// Returns the button's label for a kind of span. The marks only offer
/// jumps out of an intro, a recap, or the end credits.
fn label(kind: Kind) -> &'static str {
    match kind {
        Kind::Recap => "Skip recap",
        Kind::Credits => "Skip to post-credits scene",
        _ => "Skip intro",
    }
}

/// Returns the box behind the label. It extends one padding past the
/// left margin, as the chip's panel extends one padding past the right
/// margin. The left margin is the same at every canvas width, so the
/// box does not depend on the canvas.
fn pill_box(kind: Kind) -> Rectangle {
    let width = measure(label(kind), theme::type_scale::TINY) + 2.0 * CHIP_PAD_X;
    Rectangle::new(
        Point::new(theme::MARGIN_X - CHIP_PAD_X, CHIP_Y - CHIP_PAD_Y),
        Size::new(width, CHIP_H + 2.0 * CHIP_PAD_Y),
    )
}

/// Returns the label, centred vertically in the box, so the padding
/// above and below the capitals is equal.
fn pill_line(kind: Kind) -> Line {
    Line::new(
        label(kind),
        Point::new(theme::MARGIN_X, pill_box(kind).center().y),
        Anchor::Left,
        theme::type_scale::TINY,
        theme::color::text(),
    )
}

/// Draws the button on a fill, because it appears over the film. The
/// unfocused fill is the unfocused up-next card's, and the focused fill
/// is the panel that every focused control uses.
fn pill(brush: &mut Brush<'_>, kind: Kind, focused: bool) {
    let shape = pill_box(kind);
    if focused {
        brush.panel_rounded(shape, CHIP_R);
    } else {
        brush.rounded(shape, CHIP_R, theme::at(theme::color::SHADOW, CARD_ALPHA));
    }
    brush.text(pill_line(kind));
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::marks::Span;

    fn intro() -> Jump {
        Jump {
            span: Span {
                kind: Kind::Intro,
                start: 7.0,
                end: 107.0,
            },
            to: 107.0,
        }
    }

    fn settle(skip: &mut Skip) {
        while skip.fade().running() {
            skip.fade_mut().step();
        }
    }

    #[test]
    fn outside_every_span_the_button_offers_nothing() {
        let mut skip = Skip::default();
        assert!(!skip.on_position(Some(200.0), None));
        assert!(!skip.available());
        assert!(!skip.draws_outside(false));
        assert!(skip.take().is_empty());
    }

    #[test]
    fn the_button_follows_the_playhead_through_the_span() {
        let mut skip = Skip::default();
        assert!(skip.on_position(Some(7.0), Some(intro())));
        assert!(skip.available());
        settle(&mut skip);
        assert!(skip.draws_outside(false));
        assert!(!skip.draws_outside(true));
        // A second report from inside the same span changes nothing.
        assert!(!skip.on_position(Some(7.0), Some(intro())));

        assert!(skip.on_position(Some(200.0), None));
        assert!(!skip.available());
        // The label stays while the fade runs out.
        assert!(skip.draws_outside(false));
        settle(&mut skip);
        assert!(!skip.draws_outside(false));
    }

    /// A position report from inside the span before the seek completes
    /// leaves the button hidden.
    #[test]
    fn a_select_seeks_to_the_end_and_the_button_stays_hidden() {
        let mut skip = Skip::default();
        skip.on_position(Some(7.0), Some(intro()));
        assert_eq!(
            skip.take(),
            vec![vec![json!("seek"), json!(107.0), json!("absolute+exact")]]
        );
        assert!(!skip.available());
        assert!(!skip.on_position(Some(7.0), Some(intro())));
        assert!(!skip.available());
    }

    /// The scene after the credits can start before the credits span
    /// ends.
    #[test]
    fn a_select_in_the_credits_seeks_to_the_scene() {
        let mut skip = Skip::default();
        skip.on_position(
            Some(5900.0),
            Some(Jump {
                span: Span {
                    kind: Kind::Credits,
                    start: 5801.777,
                    end: 6400.0,
                },
                to: 6371.111,
            }),
        );
        assert_eq!(
            skip.take(),
            vec![vec![
                json!("seek"),
                json!(6371.111),
                json!("absolute+exact")
            ]]
        );
    }

    #[test]
    fn a_select_outside_the_osd_activates_an_intro_or_a_recap_button() {
        let cases = [
            (Kind::Intro, true),
            (Kind::Recap, true),
            (Kind::Credits, false),
        ];
        for (kind, takes) in cases {
            let mut skip = Skip::default();
            assert!(
                !skip.takes_select_outside(),
                "a new Skip takes Select outside for {kind:?}, want false"
            );
            skip.on_position(
                Some(7.0),
                Some(Jump {
                    span: Span {
                        kind,
                        ..intro().span
                    },
                    ..intro()
                }),
            );
            assert_eq!(
                skip.takes_select_outside(),
                takes,
                "takes_select_outside for {kind:?}"
            );
            skip.take();
            assert!(
                !skip.takes_select_outside(),
                "a taken Skip takes Select outside for {kind:?}, want false"
            );
        }
    }

    #[test]
    fn a_seek_back_into_a_skipped_span_offers_it_again() {
        let mut skip = Skip::default();
        skip.on_position(Some(7.0), Some(intro()));
        skip.take();
        skip.on_position(Some(200.0), None);
        assert!(skip.on_position(Some(7.0), Some(intro())));
        assert!(skip.available());
    }

    /// Over the bare video, the intro button shows for the first 10
    /// seconds of the span, from 7 to 17 seconds. After that it is still
    /// a stop of the OSD, and a select at the bare video no longer takes
    /// it.
    #[test]
    fn the_intro_button_shows_alone_for_the_first_ten_seconds() {
        let mut skip = Skip::default();
        skip.on_position(Some(7.0), Some(intro()));
        settle(&mut skip);
        assert!(!skip.on_position(Some(16.9), Some(intro())));
        assert!(skip.draws_outside(false));
        assert!(skip.takes_select_outside());

        assert!(skip.on_position(Some(17.0), Some(intro())));
        settle(&mut skip);
        assert!(!skip.draws_outside(false));
        assert!(!skip.takes_select_outside());
        assert!(skip.available());
        // A later report from the same span changes nothing.
        assert!(!skip.on_position(Some(60.0), Some(intro())));
    }

    /// A seek into the middle of an intro offers the button on the OSD
    /// only.
    #[test]
    fn a_seek_past_the_first_ten_seconds_shows_nothing_alone() {
        let mut skip = Skip::default();
        assert!(skip.on_position(Some(50.0), Some(intro())));
        settle(&mut skip);
        assert!(skip.available());
        assert!(!skip.draws_outside(false));
        assert!(!skip.takes_select_outside());
    }

    /// A seek back to the start of the span shows the button alone
    /// again.
    #[test]
    fn a_seek_back_to_the_start_shows_the_button_alone_again() {
        let mut skip = Skip::default();
        skip.on_position(Some(50.0), Some(intro()));
        assert!(skip.on_position(Some(8.0), Some(intro())));
        settle(&mut skip);
        assert!(skip.draws_outside(false));
    }

    /// The post-credits button stays over the bare video for the whole
    /// span, because the credits run for minutes and the up-next card is
    /// up beside it.
    #[test]
    fn the_post_credits_button_stays_for_the_whole_span() {
        let credits = Jump {
            span: Span {
                kind: Kind::Credits,
                start: 5801.777,
                end: 6400.0,
            },
            to: 6371.111,
        };
        let mut skip = Skip::default();
        skip.on_position(Some(5900.0), Some(credits));
        settle(&mut skip);
        assert!(skip.draws_outside(false));
    }

    /// The button ends above row 870, where the scrubber's time label
    /// starts.
    #[test]
    fn the_button_is_on_the_chips_row_at_the_left_margin() {
        let cases = [
            (Kind::Intro, "Skip intro"),
            (Kind::Recap, "Skip recap"),
            (Kind::Credits, "Skip to post-credits scene"),
        ];
        for (kind, words) in cases {
            let line = pill_line(kind);
            assert_eq!(line.content, words);
            assert_eq!(line.at, Point::new(96.0, 847.0));
            assert_eq!(line.anchor, Anchor::Left);
            assert_eq!(line.size, theme::type_scale::TINY);

            let shape = pill_box(kind);
            assert_eq!(shape.x, 74.0);
            assert_eq!(shape.y, 824.0);
            assert_eq!(shape.height, 46.0);
            assert_eq!(shape.center().y, line.at.y, "the label centres in the box");
            assert!(shape.y + shape.height <= 870.0);
            let room = measure(words, theme::type_scale::TINY) + 44.0;
            assert!((shape.width - room).abs() < 0.01);
        }
    }
}
