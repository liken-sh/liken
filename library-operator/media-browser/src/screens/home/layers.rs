// The two canvas layers the home page draws its rows on: the under layer
// with the banner's backdrop, and the middle layer with the rows. Each
// layer reads the layout and the scroll from `Home::drawn`, so both agree
// on where every row stands while the page scrolls. Neither draws the
// focus mark: the browser's outline goes around the box `Home::marked`
// answers.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Rectangle, Theme, mouse};

use super::{Block, Home, Last, Strip};
use crate::art::Art;
use crate::audience::faces::Faces;
use crate::screens::Item;
use crate::views::{self, band, strip};

// The under layer: the banner's backdrop and its scrim, clipped under
// the band.
pub(super) struct Ground<'a, A> {
    pub(super) home: &'a Home,
    pub(super) store: &'a RefCell<A>,
    // The second this frame draws, which the scroll eases on.
    pub(super) at: f64,
}

impl<A: Art> canvas::Program<Infallible, Theme, Renderer> for Ground<'_, A> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let home = self.home;
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let (layout, offset, clip) = home.drawn(bounds, self.at);
        frame.with_clip(clip, |frame| {
            let store = &mut *self.store.borrow_mut();
            for (index, block) in home.blocks.iter().enumerate() {
                let Block::Banner(banner) = block else {
                    continue;
                };
                let Some(title) = banner.focused() else {
                    continue;
                };
                let Some(region) = layout.region(home, index, offset, bounds) else {
                    continue;
                };
                views::banner::backdrop(frame, store, &title.item.library, &title.backdrop, region);
            }
        });
        vec![frame.into_geometry()]
    }
}

// The middle layer: the rows, on one frame.
pub(super) struct Program<'a, A> {
    pub(super) home: &'a Home,
    pub(super) store: &'a RefCell<A>,
    // The faces the continue-watching row's heading draws.
    pub(super) faces: &'a Faces,
    // Whether the page holds focus, or the browser's strip over it does.
    pub(super) held: bool,
    // The second this frame draws, which the scrolls ease on.
    pub(super) at: f64,
}

impl<A: Art> canvas::Program<Infallible, Theme, Renderer> for Program<'_, A> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let home = self.home;
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let (layout, offset, clip) = home.drawn(bounds, self.at);
        frame.with_clip(clip, |frame| {
            let store = &mut *self.store.borrow_mut();
            for (index, block) in home.blocks.iter().enumerate() {
                let Some(region) = layout.region(home, index, offset, bounds) else {
                    continue;
                };
                if region.y + region.height < band::HEIGHT || region.y > bounds.height {
                    continue;
                }
                let focused = self.held && home.focus == index;
                match block {
                    Block::Banner(banner) => {
                        let Some(title) = banner.focused() else {
                            continue;
                        };
                        views::banner::draw(
                            frame,
                            store,
                            &views::banner::Banner {
                                library: &title.item.library,
                                logo: &title.logo,
                                name: &title.name,
                                facts: &title.facts,
                                genres: &title.genres,
                                ratings: &title.ratings,
                                tagline: &title.tagline,
                                count: banner.titles.len(),
                                current: banner.focus,
                                region,
                            },
                        );
                    }
                    Block::Strip(row) => {
                        let mut view = viewed(row, region, focused, self.faces);
                        view.scroll = Some(home.slid(index, strip::scrolled(&view), self.at));
                        strip::draw(frame, store, &view);
                    }
                }
            }
        });
        vec![frame.into_geometry()]
    }
}

/// One strip of the page as the strip primitive draws it in this region,
/// with focus on it where `focused`. The faces are the room's, which the
/// continue-watching row's heading draws.
pub(super) fn viewed<'a>(
    row: &'a Strip,
    region: Rectangle,
    focused: bool,
    faces: &'a Faces,
) -> strip::Strip<'a, Item> {
    strip::Strip {
        headed: false,
        room: Some(strip::Room {
            viewers: &row.viewers,
            faces,
            circled: focused && row.rung,
        }),
        members: &row.items,
        current: None,
        focus: (focused && !row.rung).then_some(row.focus),
        kept: Some(row.focus),
        heading: &row.heading,
        library: "",
        last: row.last.as_ref().map(Last::view),
        lines: row.lines,
        region,
        scroll: None,
    }
}
