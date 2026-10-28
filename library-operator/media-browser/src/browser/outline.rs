// The focus outline: one stroke over the screen, the strip, or the picker,
// whichever holds focus. When a press moves focus, the outline travels
// from where it is drawn to the box the new focus answers, and the rows
// under it scroll to rest on the same press. While focus rests, the
// outline breathes. The browser holds the outline, because only the
// browser knows which layer holds focus and when the page under it
// changed; a screen answers where its focus is and draws no mark of its
// own.
//
// A press that changes the page, such as opening a title or going back,
// is not a move across one page, so the outline lands on the new page's
// focus at once.

use iced_winit::core::{Color, Rectangle};

use super::Browser;
use crate::art::Art;
use crate::catalog::Source;
use crate::look;
use crate::motion::Outline;
use crate::views;

/// The layer of the frame that holds focus, which the outline draws over.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum Layer {
    Screen,
    Strip,
    Picker,
}

/// Where focus is: the rectangle the stroke follows, the layer that holds
/// focus, and the page under it, counted by the browser's moves across the
/// stack.
#[derive(Debug, Clone, Copy, PartialEq)]
pub(super) struct Target {
    pub(super) around: Rectangle,
    pub(super) layer: Layer,
    page: u64,
}

impl Target {
    // Whether the outline travels from this focus to that one: both on one
    // page, and both on the picker or both off it. The strip stands over
    // every page, so a move between the strip and the page under it is a
    // move on one page. The picker is a question over the page, so the
    // outline lands on it, and back off it, at once.
    fn travels_to(&self, next: &Target) -> bool {
        self.page == next.page && (self.layer == Layer::Picker) == (next.layer == Layer::Picker)
    }
}

/// The outline as one frame draws it.
#[derive(Debug, Clone, Copy, PartialEq)]
pub(super) struct Drawn {
    pub(super) around: Rectangle,
    pub(super) color: Color,
    pub(super) layer: Layer,
}

impl<S: Source, A: Art> Browser<S, A> {
    // The frame every screen draws in: the window, at the logical size.
    fn bounds(&self) -> Rectangle {
        views::area(0.0, 0.0, self.page.0 as f32, self.page.1 as f32)
    }

    // Where focus is now, at rest: the focused tile or link of the picker
    // while it is up, the strip's target while the strip holds focus, and
    // the screen's own answer otherwise. Nothing under the shade, and
    // nothing on a screen that strokes its own mark.
    pub(super) fn target(&self) -> Option<Target> {
        if self.asleep() {
            return None;
        }
        let bounds = self.bounds();
        let (around, layer) = match self.picker_layer() {
            Some(picker) => (Some(picker.focused(bounds)), Layer::Picker),
            None if self.on_strip => (
                self.strip().and_then(|strip| strip.focused(bounds.width)),
                Layer::Strip,
            ),
            None => (self.top().marked(bounds), Layer::Screen),
        };
        around.map(|around| Target {
            around: match layer {
                Layer::Screen => around,
                Layer::Strip | Layer::Picker => views::marked(around),
            },
            layer,
            page: self.pages,
        })
    }

    // Follow where focus and the scrolls of the screen on top are now. A
    // press passes its pace, and the outline and the scrolls travel there;
    // with no press they snap, because a re-read, a resize, or a page that
    // changed is not a move a person made. The answer is the second the
    // last motion ends.
    pub(super) fn follow(&mut self, pace: Option<f64>) -> f64 {
        let at = self.clock;
        let bounds = self.bounds();
        let top = self.stack.last_mut().unwrap_or(&mut self.home);
        let mut rests = top.follow(bounds, at, pace);
        let target = self.target();
        self.outline = match (self.outline.take(), target) {
            (_, None) => None,
            (Some((mut outline, from)), Some(to)) => {
                match pace {
                    Some(pace) if from.travels_to(&to) => outline.travel(to.around, at, pace),
                    _ if outline.to() != to.around => outline = Outline::still(to.around),
                    _ => {}
                }
                rests = rests.max(outline.ends());
                Some((outline, to))
            }
            (None, Some(to)) => Some((Outline::still(to.around), to)),
        };
        rests
    }

    // The outline this second draws: where it travels, in the color its
    // breath gives it. A focus that moved since the last follow, such as on
    // a resize between a tick and its frame, draws where it is now.
    pub(super) fn drawn(&self) -> Option<Drawn> {
        let target = self.target()?;
        let around = match &self.outline {
            Some((outline, _)) if outline.to() == target.around => outline.at(self.clock),
            _ => target.around,
        };
        Some(Drawn {
            around,
            color: look::breathed(self.breath.level(self.clock)),
            layer: target.layer,
        })
    }

    // The second the outline or a scroll next changes, or nothing under
    // the shade or a film.
    pub(super) fn outline_frame(&self, at: f64) -> Option<f64> {
        if self.asleep() || self.refresh.covered() {
            return None;
        }
        self.breath.next_frame(at, self.target().is_some())
    }
}
