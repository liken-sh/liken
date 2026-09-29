// The two canvas layers the home page draws its rows on: the under layer
// with the banner's backdrop, and the middle layer with the rows. Each
// layer reads the layout and the scroll from `Home::placed`, so both agree
// on where every row stands.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Rectangle, Theme, mouse};

use super::{Block, Home, Last};
use crate::art::Art;
use crate::views::{self, band, strip};

// The under layer: the banner's backdrop and its scrim, clipped under
// the band.
pub(super) struct Ground<'a, A> {
    pub(super) home: &'a Home,
    pub(super) store: &'a RefCell<A>,
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
        let (layout, offset, clip) = home.placed(bounds);
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
    // Whether the page holds focus, or the browser's strip over it does.
    pub(super) held: bool,
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
        let (layout, offset, clip) = home.placed(bounds);
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
                                focused,
                                region,
                            },
                        );
                    }
                    Block::Strip(strip) => strip::draw(
                        frame,
                        store,
                        &strip::Strip {
                            headed: false,
                            letters: &strip.letters,
                            circled: focused && strip.rung,
                            members: &strip.items,
                            current: None,
                            focus: (focused && !strip.rung).then_some(strip.focus),
                            kept: Some(strip.focus),
                            heading: &strip.heading,
                            library: "",
                            last: strip.last.as_ref().map(Last::view),
                            lines: strip.lines,
                            region,
                        },
                    ),
                }
            }
        });
        vec![frame.into_geometry()]
    }
}
