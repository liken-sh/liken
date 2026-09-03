// A page's canvases in the order the renderer draws them. The order is a
// type because of a renderer rule: inside one layer it draws every mesh,
// then every image, then every text, whatever order the canvas drew them
// in, so a fill drawn over a backdrop is painted under it. Each canvas of
// a stack is a layer of its own, and the stack gives the page its depth.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::{Stack, canvas};
use iced_winit::core::{Element, Length, Point, Rectangle, Theme, mouse};

use super::{Tone, area, curtain, extent, paint};
use crate::look;
use crate::posters::Posters;

// The share of the width at which the scrim gives the art back, and the
// share of that run it holds the full shade for. The shade holds across
// the whole text column and falls off over the rest, because a line that
// ends in a half-cleared scrim is a line over the art itself.
const CLEARS_AT: f32 = 0.68;
const HOLDS_TO: f32 = 0.62;

/// The ground under the part of a page that holds art of its own. A
/// movie's page has none, because nothing on it sits on the backdrop but
/// text and one strip. A series' page lays a near-black ground under its
/// episode wall, so the stills never fight the backdrop and the backdrop
/// still shows whole above them, heads included.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Ground {
    /// No ground. The backdrop shows through the scrim alone.
    None,
    /// A ground from this many pixels down to the foot of the frame, with
    /// a short fade at its top edge.
    Below(f32),
}

// The height of the fade at the top edge of a ground, so the backdrop
// does not end on a hard line under the header.
const FADE: f32 = 48.0;

impl Ground {
    /// The part of these bounds the ground covers at full strength.
    pub fn of(self, bounds: Rectangle) -> Option<Rectangle> {
        match self {
            Self::None => None,
            Self::Below(top) => {
                let top = top.min(bounds.height);
                Some(area(
                    bounds.x,
                    bounds.y + top,
                    bounds.width,
                    bounds.height - top,
                ))
            }
        }
    }

    /// The band above the ground where it fades in.
    pub fn fade(self, bounds: Rectangle) -> Option<Rectangle> {
        let ground = self.of(bounds)?;
        let top = (ground.y - FADE).max(bounds.y);
        Some(area(bounds.x, top, bounds.width, ground.y - top))
    }
}

/// One page as its three layers: the backdrop, the scrim over it, and
/// everything the screen draws over both.
pub struct Page<'a, P, F> {
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The path of the backdrop file, empty where the item has none.
    pub art: &'a str,
    /// The store the backdrop comes from.
    pub posters: &'a RefCell<P>,
    /// The ground under the page's own art, where it has any.
    pub ground: Ground,
    /// The page itself: its fills, its art, and its text, in that draw
    /// order inside its own layer.
    pub front: F,
    /// The loading state's layers over the page, where a press has put the
    /// page into that state.
    pub over: Option<curtain::Layer<'a, P>>,
}

impl<'a, P, F> Page<'a, P, F>
where
    P: Posters + 'a,
    F: canvas::Program<Infallible, Theme, Renderer> + 'a,
{
    /// The page as one element, its layers in depth order.
    pub fn view(self) -> Element<'a, Infallible, Theme, Renderer> {
        let mut layers = vec![
            whole(Backdrop {
                library: self.library,
                art: self.art,
                posters: self.posters,
            }),
            whole(Scrim {
                ground: self.ground,
            }),
            whole(self.front),
        ];
        if let Some(over) = self.over {
            layers.push(whole(over));
            layers.push(whole(curtain::Front(over)));
        }
        Stack::with_children(layers)
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }
}

// One layer over the whole frame.
fn whole<'a, Q>(program: Q) -> Element<'a, Infallible, Theme, Renderer>
where
    Q: canvas::Program<Infallible, Theme, Renderer> + 'a,
{
    canvas(program)
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
}

// The lowest layer: the item's backdrop over the whole frame. The store
// is asked at the size of the frame, which is the size the prefetch asked
// for while the wall held focus, so the page finds the decode in the
// cache.
struct Backdrop<'a, P> {
    library: &'a str,
    art: &'a str,
    posters: &'a RefCell<P>,
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Backdrop<'_, P> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let art = self.posters.borrow_mut().poster(
            self.library,
            self.art,
            bounds.width as u32,
            bounds.height as u32,
        );
        match art {
            Some(image) => paint(&mut frame, &image, bounds, Tone::Full),
            None => frame.fill_rectangle(bounds.position(), extent(bounds), look::BACKGROUND),
        }
        vec![frame.into_geometry()]
    }
}

// The middle layer: one panel of shade that clears toward the right, so
// every line of the page reads over the art whatever the art holds, and
// the ground under the page's own art where the page has one.
struct Scrim {
    ground: Ground,
}

impl canvas::Program<Infallible, Theme, Renderer> for Scrim {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        frame.fill_rectangle(
            bounds.position(),
            extent(bounds),
            canvas::gradient::Linear::new(
                Point::new(bounds.x, bounds.y),
                Point::new(bounds.x + bounds.width * CLEARS_AT, bounds.y),
            )
            .add_stop(0.0, look::shade())
            .add_stop(HOLDS_TO, look::shade())
            .add_stop(1.0, look::CLEAR),
        );
        if let (Some(fade), Some(ground)) = (self.ground.fade(bounds), self.ground.of(bounds)) {
            frame.fill_rectangle(
                fade.position(),
                extent(fade),
                canvas::gradient::Linear::new(
                    Point::new(fade.x, fade.y),
                    Point::new(fade.x, fade.y + fade.height),
                )
                .add_stop(0.0, look::CLEAR)
                .add_stop(1.0, look::ground()),
            );
            frame.fill_rectangle(ground.position(), extent(ground), look::ground());
        }
        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn bounds() -> Rectangle {
        area(0.0, 0.0, 1920.0, 1080.0)
    }

    #[test]
    fn a_page_with_no_ground_lays_none() {
        assert_eq!(Ground::None.of(bounds()), None);
        assert_eq!(Ground::None.fade(bounds()), None);
    }

    #[test]
    fn a_ground_runs_from_its_top_to_the_foot_of_the_frame() {
        let ground = Ground::Below(378.0).of(bounds()).unwrap();
        assert_eq!(ground.y, 378.0);
        assert_eq!(ground.width, 1920.0);
        assert_eq!(ground.y + ground.height, 1080.0);
    }

    #[test]
    fn a_ground_fades_in_just_above_its_top() {
        let fade = Ground::Below(378.0).fade(bounds()).unwrap();
        assert_eq!(fade.y + fade.height, 378.0);
        assert_eq!(fade.height, FADE);
    }

    #[test]
    fn a_ground_below_the_frame_covers_nothing() {
        let ground = Ground::Below(4000.0).of(bounds()).unwrap();
        assert_eq!(ground.height, 0.0);
    }
}
