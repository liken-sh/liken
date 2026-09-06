// The search half of a wall: the field a person types in, which the
// browser's strip draws, and the grid a remote with no keyboard picks
// from, which this module draws as a layer over the band. A wall over
// any other query holds none of it.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::{Step, Wall};
use crate::catalog::{Query, Source};
use crate::screens::Screen;
use crate::views::band;
use crate::views::field::{self, TextField};
use crate::views::keyboard::{self, Keyboard};

/// What a wall over a `Search` holds. A wall that types always has a
/// field and has a grid only while the grid is shown, so the type says
/// which of the two a press can reach.
#[derive(Debug, Default)]
pub struct Search {
    pub field: TextField,
    pub keyboard: Option<Keyboard>,
}

/// The search wall over this text, with the grid shown or hidden, as the
/// screen the browser pushes. Every way in lands here: a letter on
/// another screen opens it seeded with the grid hidden, and the search
/// key and the strip's icon open it empty with the grid shown.
pub fn searched(text: &str, grid: bool, source: &mut dyn Source) -> Screen {
    let query = Query::Search {
        text: text.to_string(),
    };
    let mut wall = Wall::open(query, source);
    wall.search = Some(Search {
        field: TextField::of(text),
        keyboard: grid.then(Keyboard::default),
    });
    Screen::Wall(Box::new(wall))
}

impl Wall {
    // The presses a search wall takes for itself: a letter, a digit, or
    // the space types and hides the grid, and while the grid is shown the
    // arrows move it and enter presses its cell. The answer is nothing
    // where the press was none of these, so it reaches the band and the
    // slots the way it does on every other wall.
    pub(super) fn typed(&mut self, key: &str, source: &mut dyn Source) -> Option<Step> {
        let search = self.search.as_mut()?;
        let changed = if field::edits(key) {
            search.keyboard = None;
            search.field.press(key)
        } else {
            match &mut search.keyboard {
                None => return None,
                Some(grid) if key == "enter" => {
                    let word = grid.pick();
                    search.field.press(word)
                }
                // An arrow at the edge of the grid moves nothing, and
                // the frame on the glass still draws it.
                Some(grid) => return Some(step(grid.key(key))),
            }
        };
        if changed {
            self.retyped(source);
        }
        Some(step(changed))
    }

    // The press that leaves a screen, as a search wall reads it:
    // backspace removes a character and escape clears the field, and
    // both read the wall again. A wall that types nothing answers
    // nothing, and the browser then goes back.
    pub(super) fn cleared(&mut self, key: &str, source: &mut dyn Source) -> Option<Step> {
        let search = self.search.as_mut()?;
        let changed = match key {
            "backspace" => search.field.press(key),
            _ => {
                let held = !search.field.text().is_empty();
                search.field = TextField::default();
                held
            }
        };
        if !changed {
            return None;
        }
        self.retyped(source);
        Some(Step::Stay)
    }

    // Read the wall again over the text the field now holds. The query
    // carries the text, so the read, the heading, and the count follow
    // from it, and focus starts at the best hit.
    fn retyped(&mut self, source: &mut dyn Source) {
        let Some(search) = &self.search else {
            return;
        };
        self.slots.query = Query::Search {
            text: search.field.text().to_string(),
        };
        self.slots.focus = 0;
        self.reread(source);
    }

    /// Show the grid. The browser drops the strip's focus with it, so
    /// one place on the screen holds focus.
    pub fn show_grid(&mut self) {
        if let Some(search) = &mut self.search {
            search.keyboard = Some(Keyboard::default());
        }
    }

    // Whether the grid a remote picks letters from is shown.
    pub(super) fn grid(&self) -> Option<&Keyboard> {
        self.search.as_ref()?.keyboard.as_ref()
    }
}

// The search wall's own layer over the band: the grid, while it is
// shown. The field draws in the browser's strip.
pub(super) struct Typing<'a> {
    pub(super) keyboard: &'a Keyboard,
}

impl canvas::Program<Infallible, Theme, Renderer> for Typing<'_> {
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
        keyboard::draw(&mut frame, grid_at(bounds.width), self.keyboard);
        vec![frame.into_geometry()]
    }
}

// The step a press answers: a change the screen drew, or nothing at all.
fn step(changed: bool) -> Step {
    match changed {
        true => Step::Stay,
        false => Step::Still,
    }
}

// Where the grid's first cell goes: centered across the frame, a space
// under the band.
pub(super) fn grid_at(width: f32) -> Point {
    Point::new((width - keyboard::width()) / 2.0, band::HEIGHT + GRID_TOP)
}

// The space over the grid and the space under it, before the slots.
const GRID_TOP: f32 = 24.0;
const GRID_FOOT: f32 = 24.0;

// The height the grid takes off the top of the slots' region, and none
// where no grid is shown.
pub(super) fn grid_height(shown: bool) -> f32 {
    match shown {
        true => GRID_TOP + keyboard::height() + GRID_FOOT,
        false => 0.0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::screens::wall::region;
    use crate::views::area;

    const WIDTH: f32 = 1920.0;
    const HEIGHT: f32 = 1080.0;

    #[test]
    fn the_shown_grid_stands_between_the_band_and_the_slots() {
        let at = grid_at(WIDTH);
        assert_eq!(at.x + keyboard::width(), WIDTH - at.x);
        assert!(at.y > band::HEIGHT);

        let under = region(area(0.0, 0.0, WIDTH, HEIGHT), grid_height(true));
        assert!(under.y >= at.y + keyboard::height(), "{under:?}");
        assert!(under.height > 0.0, "{under:?}");
        assert_eq!(under.y + under.height, HEIGHT);
    }
}
