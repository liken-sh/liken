// One row a title's page scrolls across: a strip of posters, such as the
// set strip or a franchise strip, or a stripe of headshots. A movie's page
// and a series' page stack rows of both kinds under their other blocks,
// and each page eases every row's scroll on its own, so the two kinds
// answer the same three questions: where the row rests, where focus is on
// it, and how to draw it at a scroll.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::Rectangle;

use super::{Card, people, strip};
use crate::art::Art;

/// One row of a title's page.
pub enum Across<'a, T, P> {
    /// A strip of posters.
    Strip(strip::Strip<'a, T>),
    /// A stripe of headshots.
    Stripe(people::Stripe<'a, P>),
}

impl<T: Card, P: Card> Across<'_, T, P> {
    /// How far the row scrolls to rest with its anchor in view.
    pub fn scrolled(&self) -> f32 {
        match self {
            Self::Strip(row) => strip::scrolled(row),
            Self::Stripe(row) => people::scrolled(row),
        }
    }

    /// The box the focus outline goes around on this row, where its scroll
    /// comes to rest, or nothing while another row of the page holds focus.
    pub fn focused(&self) -> Option<Rectangle> {
        match self {
            Self::Strip(row) => strip::focused(row),
            Self::Stripe(row) => people::focused(row),
        }
    }

    /// The row drawn at this scroll, as a frame part way through an ease
    /// draws it.
    pub fn at(self, scroll: f32) -> Self {
        match self {
            Self::Strip(row) => Self::Strip(strip::Strip {
                scroll: Some(scroll),
                ..row
            }),
            Self::Stripe(row) => Self::Stripe(people::Stripe {
                scroll: Some(scroll),
                ..row
            }),
        }
    }

    /// Draw the row.
    pub fn draw<A: Art>(&self, frame: &mut canvas::Frame<Renderer>, store: &mut A) {
        match self {
            Self::Strip(row) => strip::draw(frame, store, row),
            Self::Stripe(row) => people::draw(frame, store, row),
        }
    }
}
