//! The skip control: "Skip intro" or "Skip recap" while the playhead is inside
//! one of those spans, and a seek to the span's end when a person selects it.
//! Inside the end credits of a film with a scene after them, it reads "Skip to
//! post-credits scene" and seeks to the scene. It never skips on its own,
//! because a viewer who has not seen the show may want the recap or the
//! intro, and a viewer may want to read the credits.
//!
//! It draws on the up-next chip's row, at the left margin, with the chip's
//! metrics and the card's fill, so it reads as one of the offer's family
//! and never collides with the chip or the card at the right margin. It shows
//! over the bare video for as long as the playhead is inside the span, on a
//! fade of its own, and inside the OSD it is a focus stop.

use iced::{Point, Rectangle, Size};
use serde_json::json;

use crate::canvas::{Anchor, Brush, Line, measure};
use crate::fade::{Clock, Fade, Hide};
use crate::ipc::Command;
use crate::marks::{Jump, Kind};
use crate::theme;
use crate::upnext::{CARD_ALPHA, CARD_R, CHIP_H, CHIP_PAD_X, CHIP_PAD_Y, CHIP_Y};

/// The span the control offers to leave, and its own fade.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Skip {
    /// The span the playhead is inside and the second a select seeks to,
    /// while the control offers them.
    jump: Option<Jump>,
    /// The kind the control offered last. The control keeps its label while
    /// it fades out, after the span has gone.
    kind: Option<Kind>,
    /// The span a select skipped. mpv reports a position or two from inside
    /// it before the seek lands, and the control stays down for those. A
    /// position outside the span clears this, so a seek back into the intro
    /// offers the skip again.
    passed: Option<Jump>,
    clock: Clock,
}

impl Skip {
    /// The control's own fade, which the frame loop steps while it is moving.
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    /// Take the jump the marks offer at the playhead, or nothing. It answers
    /// whether the control came or went, so the frame loop redraws only for a
    /// change.
    pub fn on_position(&mut self, inside: Option<Jump>) -> bool {
        if self.passed != inside {
            self.passed = None;
        }
        let offered = inside.filter(|jump| Some(*jump) != self.passed);
        if offered == self.jump {
            return false;
        }
        self.jump = offered;
        match offered {
            Some(jump) => {
                self.kind = Some(jump.span.kind);
                // The control stays for the whole span, so it arms no hide
                // window.
                self.clock.show(Hide::Keep);
            }
            None => self.clock.hide(),
        }
        true
    }

    /// The stop is present only while the playhead is inside a span.
    pub fn available(&self) -> bool {
        self.jump.is_some()
    }

    /// Seek to the target, exactly, so the first frame after the intro, or
    /// the first frame of the scene, is the frame that shows. The control
    /// leaves at once, and stays down until the playhead leaves the span it
    /// skipped.
    pub fn take(&mut self) -> Vec<Command> {
        let Some(jump) = self.jump.take() else {
            return Vec::new();
        };
        self.passed = Some(jump);
        self.clock.hide();
        vec![vec![json!("seek"), json!(jump.to), json!("absolute+exact")]]
    }

    /// Draw the control as part of the OSD, bright on the panel while the
    /// focus is on it.
    pub fn draw(&self, brush: &mut Brush<'_>, focused: bool) {
        if let Some(jump) = self.jump {
            pill(brush, jump.span.kind, focused);
        }
    }

    /// Draw the control over the bare video, at its own fade. While the OSD is
    /// up the OSD draws it, so this draws nothing then.
    pub fn draw_outside(&self, brush: &mut Brush<'_>, osd_visible: bool) {
        if !self.draws_outside(osd_visible) {
            return;
        }
        let Some(kind) = self.kind else {
            return;
        };
        brush.at_fade(self.fade().value(), |brush| pill(brush, kind, false));
    }

    /// Whether the control has anything to draw over the bare video, so the
    /// frame loop draws no layer for a control that is not there.
    pub fn draws_outside(&self, osd_visible: bool) -> bool {
        !osd_visible && self.kind.is_some() && self.fade().value() > 0.0
    }
}

/// The words the control reads. The span's kind picks them: the marks offer a
/// jump out of an intro, a recap, or the end credits alone.
fn label(kind: Kind) -> &'static str {
    match kind {
        Kind::Recap => "Skip recap",
        Kind::Credits => "Skip to post-credits scene",
        _ => "Skip intro",
    }
}

/// The box behind the label, which measures the label. It hangs one padding
/// outside the left margin, the way the chip's panel hangs one padding
/// outside the right margin. The left margin is the same on every canvas
/// width, so the box reads no canvas.
fn pill_box(kind: Kind) -> Rectangle {
    let width = measure(label(kind), theme::type_scale::TINY) + 2.0 * CHIP_PAD_X;
    Rectangle::new(
        Point::new(theme::MARGIN_X - CHIP_PAD_X, CHIP_Y - CHIP_PAD_Y),
        Size::new(width, CHIP_H + 2.0 * CHIP_PAD_Y),
    )
}

fn pill_line(kind: Kind) -> Line {
    Line::new(
        label(kind),
        Point::new(theme::MARGIN_X, CHIP_Y),
        Anchor::TopLeft,
        theme::type_scale::TINY,
        theme::color::text(),
    )
}

/// The control is a button over the film, so it always draws on a fill. The
/// unfocused fill is the unfocused card's, and the focused one is the panel
/// every focused control takes.
fn pill(brush: &mut Brush<'_>, kind: Kind, focused: bool) {
    let shape = pill_box(kind);
    if focused {
        brush.panel(shape);
    } else {
        brush.rounded(shape, CARD_R, theme::at(theme::color::SHADOW, CARD_ALPHA));
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
    fn outside_every_span_the_control_offers_nothing() {
        let mut skip = Skip::default();
        assert!(!skip.on_position(None));
        assert!(!skip.available());
        assert!(!skip.draws_outside(false));
        assert!(skip.take().is_empty());
    }

    /// The control comes when the playhead enters the span, draws over the
    /// bare video for the whole of it, and fades when the playhead leaves.
    #[test]
    fn the_control_follows_the_playhead_through_the_span() {
        let mut skip = Skip::default();
        assert!(skip.on_position(Some(intro())));
        assert!(skip.available());
        settle(&mut skip);
        assert!(skip.draws_outside(false));
        assert!(!skip.draws_outside(true));
        // A second report from inside the same span changes nothing.
        assert!(!skip.on_position(Some(intro())));

        assert!(skip.on_position(None));
        assert!(!skip.available());
        // The label stays while the fade runs out.
        assert!(skip.draws_outside(false));
        settle(&mut skip);
        assert!(!skip.draws_outside(false));
    }

    /// A select seeks to the span's end and takes the control down, and a
    /// report from inside the span before the seek lands leaves it down.
    #[test]
    fn a_take_seeks_to_the_end_and_the_control_stays_down() {
        let mut skip = Skip::default();
        skip.on_position(Some(intro()));
        assert_eq!(
            skip.take(),
            vec![vec![json!("seek"), json!(107.0), json!("absolute+exact")]]
        );
        assert!(!skip.available());
        assert!(!skip.on_position(Some(intro())));
        assert!(!skip.available());
    }

    /// Inside the end credits the control seeks to the scene after them,
    /// which need not be where the credits span ends.
    #[test]
    fn a_take_in_the_credits_seeks_to_the_scene() {
        let mut skip = Skip::default();
        skip.on_position(Some(Jump {
            span: Span {
                kind: Kind::Credits,
                start: 5801.777,
                end: 6400.0,
            },
            to: 6371.111,
        }));
        assert_eq!(
            skip.take(),
            vec![vec![
                json!("seek"),
                json!(6371.111),
                json!("absolute+exact")
            ]]
        );
    }

    /// Once the playhead has left the span it skipped, a seek back into it
    /// offers the skip again.
    #[test]
    fn a_seek_back_into_a_skipped_span_offers_it_again() {
        let mut skip = Skip::default();
        skip.on_position(Some(intro()));
        skip.take();
        skip.on_position(None);
        assert!(skip.on_position(Some(intro())));
        assert!(skip.available());
    }

    /// The control is on the chip's row at the left margin and clears
    /// the row the scrubber's time label draws on.
    #[test]
    fn the_control_is_on_the_chips_row_at_the_left_margin() {
        let cases = [
            (Kind::Intro, "Skip intro"),
            (Kind::Recap, "Skip recap"),
            (Kind::Credits, "Skip to post-credits scene"),
        ];
        for (kind, words) in cases {
            let line = pill_line(kind);
            assert_eq!(line.content, words);
            assert_eq!(line.at, Point::new(96.0, 806.0));
            assert_eq!(line.anchor, Anchor::TopLeft);
            assert_eq!(line.size, theme::type_scale::TINY);

            let shape = pill_box(kind);
            assert_eq!(shape.x, 74.0);
            assert_eq!(shape.y, 800.0);
            assert!(shape.y + shape.height <= 846.0);
            let room = measure(words, theme::type_scale::TINY) + 44.0;
            assert!((shape.width - room).abs() < 0.01);
        }
    }
}
