// The slots one query answers, as a wall of art under a band. The band
// carries the query's heading, and the three controls for sort, filter,
// and search reserve their place at its right. Up from the first row of
// posters reaches the band, and down from the band returns focus to the
// slot the wall remembers.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Rectangle, Theme, mouse};

use super::Step;
use super::slots::Slots;
use crate::catalog::{Query, Source};
use crate::focus;
use crate::posters::Posters;
use crate::views::{area, band, wall};

/// The wall screen: the slots the query answered, the heading as the
/// band draws it, and the control that holds focus, or nothing while the
/// slots hold it. The heading is built at every read and not on every
/// frame.
#[derive(Debug)]
pub struct Wall {
    pub slots: Slots,
    pub heading: String,
    pub control: Option<usize>,
}

impl Wall {
    /// Read the query's slots, with focus on the first of them.
    pub fn open(query: Query, source: &mut dyn Source) -> Self {
        let slots = Slots::open(query, source);
        Self {
            heading: slots.heading(),
            slots,
            control: None,
        }
    }

    /// Read the titles again and keep focus in range, because a change
    /// can remove the focused title.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.slots.reread(source);
        self.heading = self.slots.heading();
    }

    /// Fold one press in. In the band, left and right move across the
    /// controls, select does nothing because none of the three exists
    /// yet, and down returns focus to the title the wall remembers. On
    /// the wall, up from the first row reaches the band.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if let Some(control) = self.control {
            match key {
                "down" => self.control = None,
                "enter" => {}
                _ => self.control = Some(focus::row(control, band::CONTROLS.len(), key)),
            }
            return Step::Stay;
        }
        if key == "up" && self.slots.focus < wall::COLUMNS {
            self.control = Some(0);
            return Step::Stay;
        }
        self.slots.key(key, source)
    }

    /// Whether a rest of focus on this wall is worth a prefetch. It is
    /// while the posters hold focus, because a select on either kind
    /// opens a page over a backdrop.
    pub fn prefetches(&self) -> bool {
        self.control.is_none()
    }

    /// The library and the backdrop the focused title's page draws over.
    /// The browser asks the store for it once focus rests. A wall whose
    /// select opens no page over art answers nothing.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        if !self.prefetches() {
            return None;
        }
        self.slots.resting(source)
    }

    /// The view: the band, and the wall of posters under it.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        canvas(Program {
            wall: self,
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
    }
}

// The wall's drawing: the band over the grid, on one frame.
struct Program<'a, P> {
    wall: &'a Wall,
    posters: &'a RefCell<P>,
}

impl<P: Posters> canvas::Program<Infallible, Theme, Renderer> for Program<'_, P> {
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
        band::draw(
            &mut frame,
            bounds.width,
            &self.wall.heading,
            self.wall.control,
        );
        // The head keeps the mark of a focused slot in the first row off
        // the band above it.
        let region = area(
            0.0,
            band::HEIGHT + wall::HEAD,
            bounds.width,
            bounds.height - band::HEIGHT - wall::HEAD,
        );
        self.wall.slots.draw(
            &mut frame,
            &mut *self.posters.borrow_mut(),
            region,
            self.wall.control.is_none(),
            1,
        );
        vec![frame.into_geometry()]
    }
}
